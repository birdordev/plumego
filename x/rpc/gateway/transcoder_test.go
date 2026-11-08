package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
)

func TestRegisterHandlerReturnsOK(t *testing.T) {
	transcoder := New("rpc://backend")
	err := transcoder.Register(t.Context(), func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "hello"})
	}, "GET /v1/hello")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/hello", nil)
	transcoder.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["message"] != "hello" {
		t.Fatalf("message = %q, want hello", body["message"])
	}
}

func TestUnknownPathReturnsNotFound(t *testing.T) {
	transcoder := New("rpc://backend")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)

	transcoder.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestMethodMismatchReturnsMethodNotAllowed(t *testing.T) {
	transcoder := New("rpc://backend")
	if err := transcoder.Register(context.Background(), func(http.ResponseWriter, *http.Request, map[string]string) {}, "POST /v1/hello"); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/hello", nil)
	transcoder.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", rec.Header().Get("Allow"))
	}
}

func TestRegisterNilHandlerReturnsError(t *testing.T) {
	if err := New("").Register(t.Context(), nil, "GET /"); !errors.Is(err, contract.ErrHandlerNil) {
		t.Fatalf("Register() error = %v, want %v", err, contract.ErrHandlerNil)
	}
}
