package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeBridge struct {
	sendErr     error
	downloadErr error
	sent        []SendMessageRequest
}

func (f *fakeBridge) Send(_ context.Context, recipient, message, mediaPath string) error {
	f.sent = append(f.sent, SendMessageRequest{Recipient: recipient, Message: message, MediaPath: mediaPath})
	return f.sendErr
}

func (f *fakeBridge) Download(_ context.Context, _, _ string) (DownloadResult, error) {
	if f.downloadErr != nil {
		return DownloadResult{}, f.downloadErr
	}
	return DownloadResult{MediaType: "image", Filename: "x.jpg", Path: "/abs/x.jpg"}, nil
}

func (f *fakeBridge) Status() BridgeStatus {
	return BridgeStatus{Connected: true, LoggedIn: true}
}

func do(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestSendEndpoint(t *testing.T) {
	fb := &fakeBridge{}
	h := NewAPIHandler(fb, "")

	rec, out := do(t, h, http.MethodPost, "/api/send", `{"recipient":"123456","message":"hi"}`, nil)
	if rec.Code != http.StatusOK || out["success"] != true {
		t.Fatalf("got %d %v", rec.Code, out)
	}
	if len(fb.sent) != 1 || fb.sent[0].Message != "hi" {
		t.Fatalf("bridge not called correctly: %+v", fb.sent)
	}

	cases := []struct {
		method, body string
		want         int
	}{
		{http.MethodGet, "", http.StatusMethodNotAllowed},
		{http.MethodPost, "not json", http.StatusBadRequest},
		{http.MethodPost, `{"message":"hi"}`, http.StatusBadRequest},
		{http.MethodPost, `{"recipient":"123456"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if rec, _ := do(t, h, c.method, "/api/send", c.body, nil); rec.Code != c.want {
			t.Errorf("%s %q: got %d, want %d", c.method, c.body, rec.Code, c.want)
		}
	}

	fb.sendErr = errors.New("boom")
	rec, out = do(t, h, http.MethodPost, "/api/send", `{"recipient":"123456","message":"hi"}`, nil)
	if rec.Code != http.StatusInternalServerError || out["success"] != false || out["message"] != "boom" {
		t.Errorf("error not propagated: %d %v", rec.Code, out)
	}
}

func TestDownloadEndpoint(t *testing.T) {
	fb := &fakeBridge{}
	h := NewAPIHandler(fb, "")

	rec, out := do(t, h, http.MethodPost, "/api/download", `{"message_id":"A","chat_jid":"1@s.whatsapp.net"}`, nil)
	if rec.Code != http.StatusOK || out["path"] != "/abs/x.jpg" {
		t.Fatalf("got %d %v", rec.Code, out)
	}

	if rec, _ := do(t, h, http.MethodPost, "/api/download", `{"message_id":"A"}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("missing chat_jid: got %d", rec.Code)
	}

	fb.downloadErr = ErrMessageNotFound
	if rec, _ := do(t, h, http.MethodPost, "/api/download", `{"message_id":"A","chat_jid":"x"}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("not found: got %d", rec.Code)
	}
}

func TestAPIKeyAuth(t *testing.T) {
	h := NewAPIHandler(&fakeBridge{}, "s3cret")
	body := `{"recipient":"123456","message":"hi"}`

	if rec, _ := do(t, h, http.MethodPost, "/api/send", body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no key: got %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/send", body, map[string]string{"X-API-Key": "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: got %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/send", body, map[string]string{"X-API-Key": "s3cret"}); rec.Code != http.StatusOK {
		t.Errorf("X-API-Key: got %d", rec.Code)
	}
	if rec, _ := do(t, h, http.MethodPost, "/api/send", body, map[string]string{"Authorization": "Bearer s3cret"}); rec.Code != http.StatusOK {
		t.Errorf("bearer: got %d", rec.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	rec, out := do(t, NewAPIHandler(&fakeBridge{}, ""), http.MethodGet, "/api/health", "", nil)
	if rec.Code != http.StatusOK || out["status"] != "ok" || out["connected"] != true || out["logged_in"] != true {
		t.Errorf("got %d %v", rec.Code, out)
	}
}
