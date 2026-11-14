package main

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

// MediaInfo describes the media attached to a message, as needed to download
// and decrypt it later.
type MediaInfo struct {
	Type          string // "image", "video", "audio", "document" or ""
	Filename      string
	URL           string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    uint64
}

// extractTextContent returns the text of a message, including captions of
// media messages.
func extractTextContent(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}

	switch {
	case msg.GetConversation() != "":
		return msg.GetConversation()
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetText()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetCaption()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetCaption()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetCaption()
	}
	return ""
}

// extractMediaInfo returns the media metadata of a message. ts is used to
// build a default filename when the message does not carry one.
func extractMediaInfo(msg *waE2E.Message, ts time.Time) MediaInfo {
	if msg == nil {
		return MediaInfo{}
	}
	stamp := ts.UTC().Format("20060102_150405")

	if img := msg.GetImageMessage(); img != nil {
		return MediaInfo{
			Type: "image", Filename: "image_" + stamp + extensionForMime(img.GetMimetype(), ".jpg"),
			URL: img.GetURL(), MediaKey: img.GetMediaKey(),
			FileSHA256: img.GetFileSHA256(), FileEncSHA256: img.GetFileEncSHA256(), FileLength: img.GetFileLength(),
		}
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return MediaInfo{
			Type: "video", Filename: "video_" + stamp + extensionForMime(vid.GetMimetype(), ".mp4"),
			URL: vid.GetURL(), MediaKey: vid.GetMediaKey(),
			FileSHA256: vid.GetFileSHA256(), FileEncSHA256: vid.GetFileEncSHA256(), FileLength: vid.GetFileLength(),
		}
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return MediaInfo{
			Type: "audio", Filename: "audio_" + stamp + ".ogg",
			URL: aud.GetURL(), MediaKey: aud.GetMediaKey(),
			FileSHA256: aud.GetFileSHA256(), FileEncSHA256: aud.GetFileEncSHA256(), FileLength: aud.GetFileLength(),
		}
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		filename := doc.GetFileName()
		if filename == "" {
			filename = "document_" + stamp
		}
		return MediaInfo{
			Type: "document", Filename: filename,
			URL: doc.GetURL(), MediaKey: doc.GetMediaKey(),
			FileSHA256: doc.GetFileSHA256(), FileEncSHA256: doc.GetFileEncSHA256(), FileLength: doc.GetFileLength(),
		}
	}
	return MediaInfo{}
}

// extensionForMime returns a file extension for a MIME type, or fallback.
func extensionForMime(mimeType, fallback string) string {
	base := strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])
	switch base {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	}
	return fallback
}

// outgoingMedia classifies a file to send by its extension.
type outgoingMedia struct {
	Type     whatsmeow.MediaType
	MimeType string
}

// classifyOutgoingMedia decides how a local file is sent based on its
// extension: as an image, video, voice note (.ogg Opus) or generic document.
func classifyOutgoingMedia(path string) outgoingMedia {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return outgoingMedia{whatsmeow.MediaImage, "image/jpeg"}
	case ".png":
		return outgoingMedia{whatsmeow.MediaImage, "image/png"}
	case ".gif":
		return outgoingMedia{whatsmeow.MediaImage, "image/gif"}
	case ".webp":
		return outgoingMedia{whatsmeow.MediaImage, "image/webp"}
	case ".ogg", ".opus":
		return outgoingMedia{whatsmeow.MediaAudio, "audio/ogg; codecs=opus"}
	case ".mp4":
		return outgoingMedia{whatsmeow.MediaVideo, "video/mp4"}
	case ".avi":
		return outgoingMedia{whatsmeow.MediaVideo, "video/avi"}
	case ".mov":
		return outgoingMedia{whatsmeow.MediaVideo, "video/quicktime"}
	}

	mimeType := "application/octet-stream"
	if ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			mimeType = t
		}
	}
	return outgoingMedia{whatsmeow.MediaDocument, mimeType}
}

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9._ -]`)

// sanitizeFilename turns an untrusted name (for example a document filename
// chosen by the sender) into a safe single path component.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = unsafeFilenameChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, ". ")
	if name == "" {
		return "file"
	}
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = name[:200-len(ext)] + ext
	}
	return name
}

// mediaLocalPath returns where a message's media is saved: one folder per
// chat, and the message ID in the filename so that two messages with the same
// filename never collide.
func mediaLocalPath(storeDir, chatJID, messageID, filename string) string {
	return filepath.Join(storeDir, sanitizeFilename(chatJID), sanitizeFilename(messageID)+"_"+sanitizeFilename(filename))
}

// extractDirectPathFromURL extracts the WhatsApp CDN direct path from a media
// URL, e.g. https://mmg.whatsapp.net/v/t62/abc.enc?x=1 -> /v/t62/abc.enc.
func extractDirectPathFromURL(url string) string {
	parts := strings.SplitN(url, ".net/", 2)
	if len(parts) < 2 {
		return url
	}
	return "/" + strings.SplitN(parts[1], "?", 2)[0]
}

func whatsmeowMediaType(mediaType string) (whatsmeow.MediaType, error) {
	switch mediaType {
	case "image":
		return whatsmeow.MediaImage, nil
	case "video":
		return whatsmeow.MediaVideo, nil
	case "audio":
		return whatsmeow.MediaAudio, nil
	case "document":
		return whatsmeow.MediaDocument, nil
	}
	return "", fmt.Errorf("unsupported media type: %q", mediaType)
}

// mediaDownloader implements whatsmeow.DownloadableMessage for media whose
// metadata was loaded from the database.
type mediaDownloader struct {
	url           string
	directPath    string
	mediaKey      []byte
	fileLength    uint64
	fileSHA256    []byte
	fileEncSHA256 []byte
	mediaType     whatsmeow.MediaType
}

func (d *mediaDownloader) GetDirectPath() string             { return d.directPath }
func (d *mediaDownloader) GetURL() string                    { return d.url }
func (d *mediaDownloader) GetMediaKey() []byte               { return d.mediaKey }
func (d *mediaDownloader) GetFileLength() uint64             { return d.fileLength }
func (d *mediaDownloader) GetFileSHA256() []byte             { return d.fileSHA256 }
func (d *mediaDownloader) GetFileEncSHA256() []byte          { return d.fileEncSHA256 }
func (d *mediaDownloader) GetMediaType() whatsmeow.MediaType { return d.mediaType }

// DownloadResult describes a downloaded media file.
type DownloadResult struct {
	MediaType string
	Filename  string
	Path      string
}

var errNotMedia = errors.New("not a media message")

// downloadMedia downloads (or returns the cached copy of) the media attached
// to a stored message.
func downloadMedia(ctx context.Context, client *whatsmeow.Client, store *MessageStore, storeDir, messageID, chatJID string) (DownloadResult, error) {
	info, err := store.GetMediaInfo(messageID, chatJID)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("find message: %w", err)
	}
	if info.Type == "" {
		return DownloadResult{}, errNotMedia
	}

	localPath := mediaLocalPath(storeDir, chatJID, messageID, info.Filename)
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("resolve path: %w", err)
	}
	result := DownloadResult{MediaType: info.Type, Filename: filepath.Base(localPath), Path: absPath}

	if _, err := os.Stat(localPath); err == nil {
		return result, nil
	}

	if info.URL == "" || len(info.MediaKey) == 0 || len(info.FileSHA256) == 0 || len(info.FileEncSHA256) == 0 || info.FileLength == 0 {
		return DownloadResult{}, errors.New("incomplete media information for download")
	}

	waType, err := whatsmeowMediaType(info.Type)
	if err != nil {
		return DownloadResult{}, err
	}

	data, err := client.Download(ctx, &mediaDownloader{
		url:           info.URL,
		directPath:    extractDirectPathFromURL(info.URL),
		mediaKey:      info.MediaKey,
		fileLength:    info.FileLength,
		fileSHA256:    info.FileSHA256,
		fileEncSHA256: info.FileEncSHA256,
		mediaType:     waType,
	})
	if err != nil {
		return DownloadResult{}, fmt.Errorf("download media: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		return DownloadResult{}, fmt.Errorf("create media directory: %w", err)
	}
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return DownloadResult{}, fmt.Errorf("save media file: %w", err)
	}
	return result, nil
}
