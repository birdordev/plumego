package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/security/authn"
)

type stubSessionStore struct {
	session *Session
	err     error
}

func (s *stubSessionStore) CreateSession(context.Context, *Session) error { return nil }
func (s *stubSessionStore) GetSession(context.Context, string) (*Session, error) {
	return s.session, s.err
}
func (s *stubSessionStore) RotateRefresh(context.Context, string, string, string, time.Time) (*Session, error) {
	return nil, nil
}
func (s *stubSessionStore) RevokeSession(context.Context, string, time.Time, string) error {
	return nil
}
func (s *stubSessionStore) ListSessionsBySubject(context.Context, string, string, int) ([]*Session, error) {
	return nil, nil
}

type staticSessionValidator struct {
	err error
}

func (s staticSessionValidator) ValidateSession(context.Context, *Session, time.Time) error {
	return s.err
}
func (s staticSessionValidator) ValidateRefresh(context.Context, *Session, RefreshClaims, time.Time) error {
	return nil
}

func TestSessionCheckMiddlewareSuccess(t *testing.T) {
	principal := &authn.Principal{
		Subject: "user-1",
		Claims:  map[string]string{"session_id": "sess-1"},
	}
	store := &stubSessionStore{session: &Session{SessionID: "sess-1"}}
	validator := staticSessionValidator{}

	handler := SessionCheck(store, validator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSessionCheckMissingSessionID(t *testing.T) {
	principal := &authn.Principal{Subject: "user-1"}
	store := &stubSessionStore{}
	validator := staticSessionValidator{}

	handler := SessionCheck(store, validator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestSessionCheckRevokedSession(t *testing.T) {
	principal := &authn.Principal{
		Subject: "user-1",
		Claims:  map[string]string{"session_id": "sess-1"},
	}
	store := &stubSessionStore{session: &Session{SessionID: "sess-1"}}
	validator := staticSessionValidator{err: ErrSessionRevoked}

	handler := SessionCheck(store, validator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
