package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// MessageStore persists chats and messages to a local SQLite database that the
// Python MCP server reads from.
type MessageStore struct {
	db *sql.DB
}

// StoredMessage is a single message as written to the messages table.
type StoredMessage struct {
	ID        string
	ChatJID   string
	Sender    string
	Content   string
	Timestamp time.Time
	IsFromMe  bool
	Media     MediaInfo
}

const schema = `
CREATE TABLE IF NOT EXISTS chats (
	jid TEXT PRIMARY KEY,
	name TEXT,
	last_message_time TIMESTAMP
);

CREATE TABLE IF NOT EXISTS messages (
	id TEXT,
	chat_jid TEXT,
	sender TEXT,
	content TEXT,
	timestamp TIMESTAMP,
	is_from_me BOOLEAN,
	media_type TEXT,
	filename TEXT,
	url TEXT,
	media_key BLOB,
	file_sha256 BLOB,
	file_enc_sha256 BLOB,
	file_length INTEGER,
	PRIMARY KEY (id, chat_jid),
	FOREIGN KEY (chat_jid) REFERENCES chats(jid)
);

CREATE INDEX IF NOT EXISTS idx_messages_chat_timestamp ON messages (chat_jid, timestamp);
CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages (timestamp);
CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages (sender);
`

// NewMessageStore opens (and if needed creates) messages.db inside storeDir.
func NewMessageStore(storeDir string) (*MessageStore, error) {
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL",
		filepath.ToSlash(filepath.Join(storeDir, "messages.db")))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open message database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}

	return &MessageStore{db: db}, nil
}

// Close closes the underlying database connection.
func (s *MessageStore) Close() error {
	return s.db.Close()
}

// StoreChat inserts or updates a chat. Timestamps are stored in UTC so that
// lexical ordering in SQLite matches chronological ordering.
func (s *MessageStore) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			name = excluded.name,
			last_message_time = CASE
				WHEN chats.last_message_time IS NULL OR excluded.last_message_time > chats.last_message_time
				THEN excluded.last_message_time
				ELSE chats.last_message_time
			END`,
		jid, name, lastMessageTime.UTC(),
	)
	return err
}

// ChatName returns the stored name for a chat, or "" if unknown.
func (s *MessageStore) ChatName(jid string) (string, error) {
	var name sql.NullString
	err := s.db.QueryRow("SELECT name FROM chats WHERE jid = ?", jid).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return name.String, nil
}

// StoreMessage inserts or replaces a message. Messages without text and
// without media are ignored.
func (s *MessageStore) StoreMessage(m StoredMessage) error {
	if m.Content == "" && m.Media.Type == "" {
		return nil
	}

	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO messages
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ChatJID, m.Sender, m.Content, m.Timestamp.UTC(), m.IsFromMe,
		m.Media.Type, m.Media.Filename, m.Media.URL, m.Media.MediaKey,
		m.Media.FileSHA256, m.Media.FileEncSHA256, m.Media.FileLength,
	)
	return err
}

// ErrMessageNotFound is returned when a message does not exist in the store.
var ErrMessageNotFound = errors.New("message not found")

// GetMediaInfo returns the media metadata stored for a message.
func (s *MessageStore) GetMediaInfo(id, chatJID string) (MediaInfo, error) {
	var (
		info                         MediaInfo
		mediaType, filename, url     sql.NullString
		mediaKey, fileSHA, fileEncSH []byte
		fileLength                   sql.NullInt64
	)

	err := s.db.QueryRow(
		`SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length
		FROM messages WHERE id = ? AND chat_jid = ?`,
		id, chatJID,
	).Scan(&mediaType, &filename, &url, &mediaKey, &fileSHA, &fileEncSH, &fileLength)
	if errors.Is(err, sql.ErrNoRows) {
		return info, ErrMessageNotFound
	}
	if err != nil {
		return info, err
	}

	info.Type = mediaType.String
	info.Filename = filename.String
	info.URL = url.String
	info.MediaKey = mediaKey
	info.FileSHA256 = fileSHA
	info.FileEncSHA256 = fileEncSH
	if fileLength.Valid && fileLength.Int64 > 0 {
		info.FileLength = uint64(fileLength.Int64)
	}
	return info, nil
}
