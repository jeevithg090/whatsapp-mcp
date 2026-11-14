package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// maxMediaBytes caps the size of files the bridge will upload.
const maxMediaBytes = 100 << 20 // 100 MiB

// parseRecipient converts either a JID ("123@s.whatsapp.net", "123@g.us") or a
// phone number with country code ("+44 7700 900123") into a JID.
func parseRecipient(recipient string) (types.JID, error) {
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return types.JID{}, errors.New("recipient is required")
	}

	if strings.Contains(recipient, "@") {
		jid, err := types.ParseJID(recipient)
		if err != nil {
			return types.JID{}, fmt.Errorf("invalid JID %q: %w", recipient, err)
		}
		return jid, nil
	}

	var digits strings.Builder
	for _, r := range recipient {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
			// common phone-number punctuation, ignored
		default:
			return types.JID{}, fmt.Errorf("invalid phone number %q", recipient)
		}
	}
	if digits.Len() < 5 || digits.Len() > 20 {
		return types.JID{}, fmt.Errorf("invalid phone number %q", recipient)
	}
	return types.NewJID(digits.String(), types.DefaultUserServer), nil
}

// buildMediaMessage builds the WhatsApp message for an uploaded file.
func buildMediaMessage(media outgoingMedia, resp whatsmeow.UploadResponse, data []byte, caption, filename string) (*waE2E.Message, error) {
	switch media.Type {
	case whatsmeow.MediaImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String(caption), Mimetype: proto.String(media.MimeType),
			URL: proto.String(resp.URL), DirectPath: proto.String(resp.DirectPath), MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: proto.Uint64(resp.FileLength),
		}}, nil
	case whatsmeow.MediaAudio:
		seconds, waveform, err := analyzeOggOpus(data)
		if err != nil {
			return nil, fmt.Errorf("analyze Ogg Opus file: %w", err)
		}
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			Mimetype: proto.String(media.MimeType),
			URL:      proto.String(resp.URL), DirectPath: proto.String(resp.DirectPath), MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: proto.Uint64(resp.FileLength),
			Seconds: proto.Uint32(seconds), PTT: proto.Bool(true), Waveform: waveform,
		}}, nil
	case whatsmeow.MediaVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Caption: proto.String(caption), Mimetype: proto.String(media.MimeType),
			URL: proto.String(resp.URL), DirectPath: proto.String(resp.DirectPath), MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: proto.Uint64(resp.FileLength),
		}}, nil
	default:
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			Title: proto.String(filename), FileName: proto.String(filename),
			Caption: proto.String(caption), Mimetype: proto.String(media.MimeType),
			URL: proto.String(resp.URL), DirectPath: proto.String(resp.DirectPath), MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: proto.Uint64(resp.FileLength),
		}}, nil
	}
}

// sendWhatsAppMessage sends a text message, or a file with an optional
// caption when mediaPath is set.
func sendWhatsAppMessage(ctx context.Context, client *whatsmeow.Client, recipient, message, mediaPath string) error {
	if !client.IsConnected() {
		return errors.New("the bridge is not connected to WhatsApp right now; check the bridge terminal (it reconnects automatically) and try again")
	}

	jid, err := parseRecipient(recipient)
	if err != nil {
		return err
	}

	msg := &waE2E.Message{Conversation: proto.String(message)}

	if mediaPath != "" {
		info, err := os.Stat(mediaPath)
		if err != nil {
			return fmt.Errorf("read media file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("media path is not a regular file: %s", mediaPath)
		}
		if info.Size() > maxMediaBytes {
			return fmt.Errorf("media file is too large (%d bytes, max %d)", info.Size(), maxMediaBytes)
		}

		data, err := os.ReadFile(mediaPath)
		if err != nil {
			return fmt.Errorf("read media file: %w", err)
		}

		media := classifyOutgoingMedia(mediaPath)
		resp, err := client.Upload(ctx, data, media.Type)
		if err != nil {
			return fmt.Errorf("upload media: %w", err)
		}

		msg, err = buildMediaMessage(media, resp, data, message, filepath.Base(mediaPath))
		if err != nil {
			return err
		}
	}

	if _, err := client.SendMessage(ctx, jid, msg); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	return nil
}
