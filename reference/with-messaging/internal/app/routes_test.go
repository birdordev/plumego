package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"with-messaging/internal/handler"
)

func TestWriteHealthResponseShape(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	handler.WriteHealthResponse(rec, req, "with-messaging", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}

	var env struct {
		Data struct {
			Status    string    `json:"status"`
			Service   string    `json:"service"`
			Timestamp time.Time `json:"timestamp"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if env.Data.Status != "ok" || env.Data.Service != "with-messaging" || env.Data.Timestamp.IsZero() {
		t.Fatalf("unexpected health response: %+v", env.Data)
	}
}
