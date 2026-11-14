package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// maxRequestBodyBytes caps the size of JSON request bodies.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// Bridge is the WhatsApp functionality exposed over the REST API. It is an
// interface so that the HTTP layer can be tested without a WhatsApp session.
type Bridge interface {
	Send(ctx context.Context, recipient, message, mediaPath string) error
	Download(ctx context.Context, messageID, chatJID string) (DownloadResult, error)
	Status() BridgeStatus
}

// BridgeStatus is the WhatsApp connection state reported by GET /api/health.
type BridgeStatus struct {
	// Connected is true while the websocket to WhatsApp is open.
	Connected bool `json:"connected"`
	// LoggedIn is true when the device is paired and authenticated.
	LoggedIn bool `json:"logged_in"`
}

// HealthResponse is the response body of GET /api/health.
type HealthResponse struct {
	Status string `json:"status"`
	BridgeStatus
}

// SendMessageRequest is the request body of POST /api/send.
type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

// SendMessageResponse is the response body of POST /api/send.
type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// DownloadMediaRequest is the request body of POST /api/download.
type DownloadMediaRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

// DownloadMediaResponse is the response body of POST /api/download.
type DownloadMediaResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
}

// NewAPIHandler returns the HTTP handler of the REST API. When apiKey is
// non-empty, every request must authenticate with it.
func NewAPIHandler(b Bridge, apiKey string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok", BridgeStatus: b.Status()})
	})
	mux.HandleFunc("/api/send", handleSend(b))
	mux.HandleFunc("/api/download", handleDownload(b))
	return requireAPIKey(apiKey, mux)
}

func handleSend(b Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req SendMessageRequest
		if !decodePost(w, r, &req) {
			return
		}

		req.Recipient = strings.TrimSpace(req.Recipient)
		if req.Recipient == "" {
			writeJSON(w, http.StatusBadRequest, SendMessageResponse{Message: "recipient is required"})
			return
		}
		if req.Message == "" && req.MediaPath == "" {
			writeJSON(w, http.StatusBadRequest, SendMessageResponse{Message: "message or media_path is required"})
			return
		}

		if err := b.Send(r.Context(), req.Recipient, req.Message, req.MediaPath); err != nil {
			writeJSON(w, http.StatusInternalServerError, SendMessageResponse{Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, SendMessageResponse{Success: true, Message: "Message sent to " + req.Recipient})
	}
}

func handleDownload(b Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req DownloadMediaRequest
		if !decodePost(w, r, &req) {
			return
		}

		if req.MessageID == "" || req.ChatJID == "" {
			writeJSON(w, http.StatusBadRequest, DownloadMediaResponse{Message: "message_id and chat_jid are required"})
			return
		}

		res, err := b.Download(r.Context(), req.MessageID, req.ChatJID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrMessageNotFound) || errors.Is(err, errNotMedia) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, DownloadMediaResponse{Message: "Failed to download media: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, DownloadMediaResponse{
			Success:  true,
			Message:  "Successfully downloaded " + res.MediaType + " media",
			Filename: res.Filename,
			Path:     res.Path,
		})
	}
}

// decodePost enforces POST + a JSON body and decodes it into dst. It writes
// the error response and returns false on failure.
func decodePost(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, SendMessageResponse{Message: "method not allowed"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, SendMessageResponse{Message: "invalid JSON request body"})
		return false
	}
	return true
}

func requireAPIKey(apiKey string, next http.Handler) http.Handler {
	if apiKey == "" {
		return next
	}
	want := []byte(apiKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-API-Key")
		if got == "" {
			got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeJSON(w, http.StatusUnauthorized, SendMessageResponse{Message: "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// newHTTPServer returns an http.Server with sensible timeouts.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Uploading/downloading media can take a while.
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}
}
