package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	plumelog "github.com/birdordev/plumego/log"
)

func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

func TestRequireAdminTokenRejectsMissingHeader(t *testing.T) {
	mw := RequireAdminToken("secret", discardLogger())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/tenants", nil)

	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not run")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireAdminTokenAllowsMatchingHeader(t *testing.T) {
	mw := RequireAdminToken("secret", discardLogger())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/tenants", nil)
	req.Header.Set(HeaderAdminToken, "secret")
	called := false

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler was not called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
