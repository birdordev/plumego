package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	plumelog "github.com/birdordev/plumego/log"
)

func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

func TestChatMissingMessage(t *testing.T) {
	h := NewAIHandler(discardLogger())
	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Chat(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var resp struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Type != "required_field_missing" {
		t.Errorf("error type = %q, want required_field_missing", resp.Error.Type)
	}
}

func TestChatInvalidJSON(t *testing.T) {
	h := NewAIHandler(discardLogger())
	body := bytes.NewBufferString(`{bad json}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Chat(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestChatSuccess(t *testing.T) {
	h := NewAIHandler(discardLogger())
	body := bytes.NewBufferString(`{"message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Chat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data chatResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.SessionID == "" {
		t.Error("session_id should not be empty")
	}
	if resp.Data.Reply == "" {
		t.Error("reply should not be empty")
	}
}

func TestStatusResponseShape(t *testing.T) {
	h := NewAIHandler(discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/api/ai/status", nil)
	rec := httptest.NewRecorder()

	h.Status(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Data struct {
			Provider    string `json:"provider"`
			LiveNetwork bool   `json:"live_network"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Provider != "offline-mock" {
		t.Errorf("provider = %q, want offline-mock", resp.Data.Provider)
	}
	if resp.Data.LiveNetwork {
		t.Error("live_network should be false for mock provider")
	}
}
