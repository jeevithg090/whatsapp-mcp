package main

import (
	"errors"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *MessageStore {
	t.Helper()
	s, err := NewMessageStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewMessageStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreChatKeepsNewestTimestamp(t *testing.T) {
	s := newTestStore(t)
	newer := time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)

	if err := s.StoreChat("1@s.whatsapp.net", "Alice", newer); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreChat("1@s.whatsapp.net", "Alice B", older); err != nil {
		t.Fatal(err)
	}

	var name string
	var last time.Time
	if err := s.db.QueryRow("SELECT name, last_message_time FROM chats WHERE jid = ?", "1@s.whatsapp.net").Scan(&name, &last); err != nil {
		t.Fatal(err)
	}
	if name != "Alice B" {
		t.Errorf("name = %q, want updated name", name)
	}
	if !last.Equal(newer) {
		t.Errorf("last_message_time = %v, want %v", last, newer)
	}

	got, err := s.ChatName("1@s.whatsapp.net")
	if err != nil || got != "Alice B" {
		t.Errorf("ChatName = %q, %v", got, err)
	}
	if got, err := s.ChatName("unknown"); err != nil || got != "" {
		t.Errorf("ChatName(unknown) = %q, %v", got, err)
	}
}

func TestStoreMessageAndMediaInfo(t *testing.T) {
	s := newTestStore(t)
	chat := "1@s.whatsapp.net"
	ts := time.Date(2025, 5, 1, 12, 0, 0, 0, time.FixedZone("IST", 19800))
	if err := s.StoreChat(chat, "Alice", ts); err != nil {
		t.Fatal(err)
	}

	// Empty messages are skipped.
	if err := s.StoreMessage(StoredMessage{ID: "empty", ChatJID: chat, Timestamp: ts}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMediaInfo("empty", chat); !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("empty message should not be stored, got err=%v", err)
	}

	if err := s.StoreMessage(StoredMessage{ID: "txt", ChatJID: chat, Sender: "1", Content: "hello", Timestamp: ts}); err != nil {
		t.Fatal(err)
	}
	info, err := s.GetMediaInfo("txt", chat)
	if err != nil {
		t.Fatalf("GetMediaInfo(text): %v", err)
	}
	if info.Type != "" {
		t.Errorf("text message has media type %q", info.Type)
	}

	media := MediaInfo{Type: "image", Filename: "a.jpg", URL: "https://x.net/a", MediaKey: []byte{1}, FileSHA256: []byte{2}, FileEncSHA256: []byte{3}, FileLength: 99}
	if err := s.StoreMessage(StoredMessage{ID: "img", ChatJID: chat, Sender: "1", Timestamp: ts, Media: media}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMediaInfo("img", chat)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "image" || got.Filename != "a.jpg" || got.FileLength != 99 || string(got.MediaKey) != "\x01" {
		t.Errorf("unexpected media info: %+v", got)
	}

	// Timestamps are normalised to UTC.
	var raw string
	if err := s.db.QueryRow("SELECT CAST(timestamp AS TEXT) FROM messages WHERE id = 'txt'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "2025-05-01 06:30:00+00:00" {
		t.Errorf("stored timestamp = %q, want UTC", raw)
	}
}
