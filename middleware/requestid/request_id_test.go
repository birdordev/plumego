package requestid

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
)

func TestMiddlewareUsesHeader(t *testing.T) {
	handler := Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got != "abc-123" {
			t.Fatalf("expected request id to be propagated, got %q", got)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(contract.RequestIDHeader, "abc-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(contract.RequestIDHeader); got != "abc-123" {
		t.Fatalf("expected response header to match, got %q", got)
	}
}

func TestMiddlewareGeneratesWhenMissing(t *testing.T) {
	handler := Middleware(WithGenerator(func() string { return "gen-1" }))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got != "gen-1" {
			t.Fatalf("expected generated request id, got %q", got)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(contract.RequestIDHeader); got != "gen-1" {
		t.Fatalf("expected generated header, got %q", got)
	}
}

func TestMiddlewareFallsBackWhenGeneratorReturnsEmpty(t *testing.T) {
	handler := Middleware(WithGenerator(func() string { return "" }))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got == "" {
			t.Fatal("expected fallback request id in context")
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(contract.RequestIDHeader); got == "" {
		t.Fatal("expected fallback request id header")
	}
}

func TestMiddlewareTrimsGeneratedRequestID(t *testing.T) {
	handler := Middleware(WithGenerator(func() string { return "  gen-2  " }))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := contract.RequestIDFromContext(r.Context()); got != "gen-2" {
			t.Fatalf("expected trimmed request id, got %q", got)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(contract.RequestIDHeader); got != "gen-2" {
		t.Fatalf("expected trimmed generated header, got %q", got)
	}
}

func TestAttachRequestIDSkipsBlankID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	got := AttachRequestID(rec, req, " \t ", true)
	if got != req {
		t.Fatal("expected blank request id to return original request")
	}
	if rec.Header().Get(contract.RequestIDHeader) != "" {
		t.Fatal("expected blank request id not to be written")
	}
}
