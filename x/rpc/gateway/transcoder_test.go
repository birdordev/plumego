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

func TestRegisterMethodsOnSamePath(t *testing.T) {
	transcoder := New("")
	for _, registration := range []struct {
		pattern string
		status  int
	}{
		{"/demo", http.StatusOK},
		{"POST /demo", http.StatusCreated},
	} {
		err := transcoder.Register(t.Context(), func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
			w.WriteHeader(registration.status)
		}, registration.pattern)
		if err != nil {
			t.Fatal(err)
		}
	}
	handler := func(http.ResponseWriter, *http.Request, map[string]string) {}
	for _, pattern := range []string{"GET /demo", "GET /{", "G@T /demo"} {
		if err := transcoder.Register(t.Context(), handler, pattern); err == nil {
			t.Errorf("Register(%q) should fail", pattern)
		}
	}
	for _, tc := range []struct {
		method string
		status int
		allow  string
	}{
		{http.MethodGet, http.StatusOK, ""},
		{http.MethodPost, http.StatusCreated, ""},
		{http.MethodHead, http.StatusMethodNotAllowed, "GET, POST"},
	} {
		req := httptest.NewRequest(tc.method, "/demo", nil)
		rec := httptest.NewRecorder()
		transcoder.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.status || rec.Header().Get("Allow") != tc.allow {
			t.Errorf("%s: status=%d Allow=%q", tc.method, rec.Code, rec.Header().Get("Allow"))
		}
	}
	err := transcoder.Register(t.Context(), func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
		w.WriteHeader(http.StatusNoContent)
	}, "HEAD /demo")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	transcoder.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/demo", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("registered HEAD: status=%d", rec.Code)
	}
}
