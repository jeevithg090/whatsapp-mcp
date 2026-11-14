package main

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// eventHandler persists incoming WhatsApp events to the message store.
type eventHandler struct {
	client *whatsmeow.Client
	store  *MessageStore
	log    waLog.Logger
}

func (h *eventHandler) handle(evt any) {
	switch v := evt.(type) {
	case *events.Message:
		h.handleMessage(v)
	case *events.HistorySync:
		h.handleHistorySync(v)
	case *events.Connected:
		h.log.Infof("Connected to WhatsApp")
	case *events.LoggedOut:
		h.log.Warnf("Device logged out; restart the bridge and scan the QR code to log in again")
	}
}

func (h *eventHandler) handleMessage(msg *events.Message) {
	chatJID := msg.Info.Chat.String()
	sender := msg.Info.Sender.User

	name := h.chatName(msg.Info.Chat, nil, msg.Info.PushName, sender)
	if err := h.store.StoreChat(chatJID, name, msg.Info.Timestamp); err != nil {
		h.log.Warnf("Failed to store chat %s: %v", chatJID, err)
	}

	content := extractTextContent(msg.Message)
	media := extractMediaInfo(msg.Message, msg.Info.Timestamp)
	if content == "" && media.Type == "" {
		return
	}

	err := h.store.StoreMessage(StoredMessage{
		ID:        msg.Info.ID,
		ChatJID:   chatJID,
		Sender:    sender,
		Content:   content,
		Timestamp: msg.Info.Timestamp,
		IsFromMe:  msg.Info.IsFromMe,
		Media:     media,
	})
	if err != nil {
		h.log.Warnf("Failed to store message %s: %v", msg.Info.ID, err)
		return
	}

	direction := "←"
	if msg.Info.IsFromMe {
		direction = "→"
	}
	kind := "text"
	if media.Type != "" {
		kind = media.Type
	}
	h.log.Infof("%s %s message in %s", direction, kind, name)
}

func (h *eventHandler) handleHistorySync(evt *events.HistorySync) {
	conversations := evt.Data.GetConversations()
	h.log.Infof("Received history sync with %d conversations", len(conversations))

	stored := 0
	for _, conv := range conversations {
		stored += h.storeConversation(conv)
	}
	h.log.Infof("History sync complete: stored %d messages", stored)
}

func (h *eventHandler) storeConversation(conv *waHistorySync.Conversation) int {
	chatJID := conv.GetID()
	if chatJID == "" {
		return 0
	}
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		h.log.Warnf("Failed to parse JID %s: %v", chatJID, err)
		return 0
	}

	messages := conv.GetMessages()
	if len(messages) == 0 {
		return 0
	}

	name := h.chatName(jid, conv, "", "")

	// Messages are ordered newest first; track the newest timestamp seen.
	var latest time.Time
	stored := 0
	for _, hm := range messages {
		info := hm.GetMessage()
		if info == nil || info.GetMessage() == nil {
			continue
		}
		ts := info.GetMessageTimestamp()
		if ts == 0 {
			continue
		}
		timestamp := time.Unix(int64(ts), 0)
		if timestamp.After(latest) {
			latest = timestamp
		}

		content := extractTextContent(info.GetMessage())
		media := extractMediaInfo(info.GetMessage(), timestamp)
		if content == "" && media.Type == "" {
			continue
		}

		key := info.GetKey()
		isFromMe := key.GetFromMe()
		var sender string
		switch {
		case isFromMe && h.client.Store.ID != nil:
			sender = h.client.Store.ID.User
		case !isFromMe && key.GetParticipant() != "":
			sender = key.GetParticipant()
			if p, err := types.ParseJID(sender); err == nil {
				sender = p.User
			}
		case !isFromMe && info.GetParticipant() != "":
			sender = info.GetParticipant()
			if p, err := types.ParseJID(sender); err == nil {
				sender = p.User
			}
		default:
			sender = jid.User
		}

		msgID := key.GetID()
		if msgID == "" {
			continue
		}

		err := h.store.StoreMessage(StoredMessage{
			ID:        msgID,
			ChatJID:   chatJID,
			Sender:    sender,
			Content:   content,
			Timestamp: timestamp,
			IsFromMe:  isFromMe,
			Media:     media,
		})
		if err != nil {
			h.log.Warnf("Failed to store history message %s: %v", msgID, err)
			continue
		}
		stored++
	}

	if !latest.IsZero() {
		if err := h.store.StoreChat(chatJID, name, latest); err != nil {
			h.log.Warnf("Failed to store chat %s: %v", chatJID, err)
		}
	}
	return stored
}

// chatName works out a display name for a chat: the stored name if known,
// otherwise the history-sync conversation name, group subject, contact name,
// push name or finally the JID user part.
func (h *eventHandler) chatName(jid types.JID, conv *waHistorySync.Conversation, pushName, sender string) string {
	chatJID := jid.String()
	if existing, err := h.store.ChatName(chatJID); err == nil && existing != "" {
		return existing
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if jid.Server == types.GroupServer {
		if conv != nil {
			if n := conv.GetDisplayName(); n != "" {
				return n
			}
			if n := conv.GetName(); n != "" {
				return n
			}
		}
		if info, err := h.client.GetGroupInfo(ctx, jid); err == nil && info.Name != "" {
			return info.Name
		}
		return fmt.Sprintf("Group %s", jid.User)
	}

	if conv != nil {
		if n := conv.GetDisplayName(); n != "" {
			return n
		}
		if n := conv.GetName(); n != "" {
			return n
		}
	}
	if contact, err := h.client.Store.Contacts.GetContact(ctx, jid); err == nil {
		if contact.FullName != "" {
			return contact.FullName
		}
		if contact.PushName != "" {
			return contact.PushName
		}
	}
	// For direct chats the push name of an incoming message is the contact's.
	if pushName != "" && sender == jid.User {
		return pushName
	}
	if sender != "" {
		return sender
	}
	return jid.User
}
