package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestSanitizeFilename(t *testing.T) {
	tests := map[string]string{
		"report.pdf":               "report.pdf",
		"../../etc/passwd":         "passwd",
		`..\..\windows\evil.exe`:   "evil.exe",
		"..":                       "file",
		"":                         "file",
		"what?*is<this>.txt":       "what__is_this_.txt",
		"123@s.whatsapp.net":       "123_s.whatsapp.net",
		"   .hidden  ":             "hidden",
		"photo 2024-01-01 (1).jpg": "photo 2024-01-01 _1_.jpg",
	}
	for in, want := range tests {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}

	long := strings.Repeat("a", 500) + ".pdf"
	got := sanitizeFilename(long)
	if len(got) != 200 || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("long name not truncated correctly: len=%d suffix ok=%v", len(got), strings.HasSuffix(got, ".pdf"))
	}
}

func TestMediaLocalPathStaysInsideStore(t *testing.T) {
	store := t.TempDir()
	p := mediaLocalPath(store, "../../x@g.us", "ID/../1", "../../../etc/cron.d/evil")
	rel, err := filepath.Rel(store, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("path %q escapes store %q", p, store)
	}
	if strings.Count(rel, string(filepath.Separator)) != 1 {
		t.Fatalf("expected <chat>/<file>, got %q", rel)
	}
}

func TestMediaLocalPathIsUniquePerMessage(t *testing.T) {
	a := mediaLocalPath("store", "1@s.whatsapp.net", "AAA", "image.jpg")
	b := mediaLocalPath("store", "1@s.whatsapp.net", "BBB", "image.jpg")
	if a == b {
		t.Fatalf("two messages with the same filename map to the same path %q", a)
	}
}

func TestExtractDirectPathFromURL(t *testing.T) {
	got := extractDirectPathFromURL("https://mmg.whatsapp.net/v/t62.7118-24/123_n.enc?ccb=11-4&oh=abc")
	if want := "/v/t62.7118-24/123_n.enc"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := extractDirectPathFromURL("not-a-url"); got != "not-a-url" {
		t.Errorf("unparseable URL should be returned unchanged, got %q", got)
	}
}

func TestClassifyOutgoingMedia(t *testing.T) {
	tests := []struct {
		path     string
		wantType whatsmeow.MediaType
		wantMime string
	}{
		{"/tmp/a.JPG", whatsmeow.MediaImage, "image/jpeg"},
		{"/tmp/a.png", whatsmeow.MediaImage, "image/png"},
		{"/tmp/voice.ogg", whatsmeow.MediaAudio, "audio/ogg; codecs=opus"},
		{"/tmp/clip.mov", whatsmeow.MediaVideo, "video/quicktime"},
		{"/tmp/report.pdf", whatsmeow.MediaDocument, "application/pdf"},
		{"/tmp/noext", whatsmeow.MediaDocument, "application/octet-stream"},
	}
	for _, tt := range tests {
		got := classifyOutgoingMedia(tt.path)
		if got.Type != tt.wantType || got.MimeType != tt.wantMime {
			t.Errorf("classifyOutgoingMedia(%q) = %+v, want %v/%v", tt.path, got, tt.wantType, tt.wantMime)
		}
	}
}

func TestExtractTextContent(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"nil", nil, ""},
		{"conversation", &waE2E.Message{Conversation: proto.String("hi")}, "hi"},
		{"extended", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("link")}}, "link"},
		{"image caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("look")}}, "look"},
		{"video caption", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("watch")}}, "watch"},
		{"empty", &waE2E.Message{}, ""},
	}
	for _, tt := range tests {
		if got := extractTextContent(tt.msg); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestExtractMediaInfo(t *testing.T) {
	ts := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)

	img := extractMediaInfo(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Mimetype: proto.String("image/png"), URL: proto.String("https://x.net/a"), FileLength: proto.Uint64(42),
	}}, ts)
	if img.Type != "image" || img.Filename != "image_20250304_050607.png" || img.FileLength != 42 {
		t.Errorf("unexpected image info: %+v", img)
	}

	doc := extractMediaInfo(&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("cv.pdf")}}, ts)
	if doc.Type != "document" || doc.Filename != "cv.pdf" {
		t.Errorf("unexpected document info: %+v", doc)
	}

	if got := extractMediaInfo(&waE2E.Message{Conversation: proto.String("x")}, ts); got.Type != "" {
		t.Errorf("text message reported as media: %+v", got)
	}
}
