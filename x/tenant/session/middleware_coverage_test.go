package session

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/security/authn"
)

// mapSessionStore is a map-backed SessionStore for coverage tests.
type mapSessionStore struct {
	sessions map[string]*Session
	err      error
}

func newMapSessionStore(sessions map[string]*Session) *mapSessionStore {
	if sessions == nil {
		sessions = map[string]*Session{}
	}
	return &mapSessionStore{sessions: sessions}
}

func (s *mapSessionStore) CreateSession(_ context.Context, sess *Session) error {
	s.sessions[sess.SessionID] = sess
	return nil
}

func (s *mapSessionStore) GetSession(_ context.Context, id string) (*Session, error) {
	if s.err != nil {
		return nil, s.err
	}
	sess, ok := s.sessions[id]
	if !ok {
		return nil, nil
	}
	return sess, nil
}

func (s *mapSessionStore) RotateRefresh(_ context.Context, _, _, _ string, _ time.Time) (*Session, error) {
	return nil, nil
}

func (s *mapSessionStore) RevokeSession(_ context.Context, _ string, _ time.Time, _ string) error {
	return nil
}

func (s *mapSessionStore) ListSessionsBySubject(_ context.Context, _, _ string, _ int) ([]*Session, error) {
	return nil, nil
}

// fixedSessionValidator always returns the configured error (nil = valid).
type fixedSessionValidator struct{ err error }

func (v *fixedSessionValidator) ValidateSession(_ context.Context, _ *Session, _ time.Time) error {
	return v.err
}

func (v *fixedSessionValidator) ValidateRefresh(_ context.Context, _ *Session, _ RefreshClaims, _ time.Time) error {
	return v.err
}

// withSessionClaim returns a request carrying a Principal with session_id = id.
func withSessionClaim(r *http.Request, sessionID string) *http.Request {
	p := &authn.Principal{
		Subject: "user-1",
		Claims:  map[string]string{"session_id": sessionID},
	}
	return r.WithContext(authn.WithPrincipal(r.Context(), p))
}

// withEmptyClaims returns a request with a Principal but no session_id claim.
func withEmptyClaims(r *http.Request) *http.Request {
	p := &authn.Principal{Subject: "user-1", Claims: map[string]string{}}
	return r.WithContext(authn.WithPrincipal(r.Context(), p))
}

func simpleOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// ---- nil validator ----

func TestSessionCheck_NilValidatorReturns500(t *testing.T) {
	mw := SessionCheck(newMapSessionStore(nil), nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "sess-1")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for nil validator, got %d", rec.Code)
	}
}

// ---- no principal ----

func TestSessionCheck_NoPrincipalReturns401(t *testing.T) {
	mw := SessionCheck(newMapSessionStore(nil), &fixedSessionValidator{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing principal, got %d", rec.Code)
	}
}

// ---- store error ----

func TestSessionCheck_StoreErrorReturns401(t *testing.T) {
	store := newMapSessionStore(nil)
	store.err = errors.New("db connection refused")

	mw := SessionCheck(store, &fixedSessionValidator{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "sess-1")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when store returns error, got %d", rec.Code)
	}
}

// ---- nil store ----

func TestSessionCheck_NilStoreReturns500(t *testing.T) {
	mw := SessionCheck(nil, &fixedSessionValidator{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "sess-x")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for nil store, got %d", rec.Code)
	}
}

// ---- session not found, requireSessionID=true (default) ----

func TestSessionCheck_SessionNotFoundRequiredReturns401(t *testing.T) {
	mw := SessionCheck(newMapSessionStore(nil), &fixedSessionValidator{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "missing-sess")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when session not found and required, got %d", rec.Code)
	}
}

// ---- session not found, requireSessionID=false ----

func TestSessionCheck_SessionNotFoundOptionalPassthrough(t *testing.T) {
	mw := SessionCheck(newMapSessionStore(nil), &fixedSessionValidator{}, WithSessionRequired(false))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withEmptyClaims(req)
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when session not required and absent, got %d", rec.Code)
	}
}

// ---- ErrSessionExpired ----

func TestSessionCheck_ExpiredSessionReturns401(t *testing.T) {
	sess := &Session{SessionID: "s-exp", Status: SessionActive}
	store := newMapSessionStore(map[string]*Session{"s-exp": sess})

	mw := SessionCheck(store, &fixedSessionValidator{err: ErrSessionExpired})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "s-exp")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired session, got %d", rec.Code)
	}
}

// ---- ErrRefreshReused ----

func TestSessionCheck_RefreshReusedReturns401(t *testing.T) {
	sess := &Session{SessionID: "s-reuse"}
	store := newMapSessionStore(map[string]*Session{"s-reuse": sess})

	mw := SessionCheck(store, &fixedSessionValidator{err: ErrRefreshReused})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "s-reuse")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for refresh token reuse, got %d", rec.Code)
	}
}

// ---- ErrTokenVersionMismatch ----

func TestSessionCheck_TokenVersionMismatchReturns401(t *testing.T) {
	sess := &Session{SessionID: "s-ver"}
	store := newMapSessionStore(map[string]*Session{"s-ver": sess})

	mw := SessionCheck(store, &fixedSessionValidator{err: ErrTokenVersionMismatch})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "s-ver")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for token version mismatch, got %d", rec.Code)
	}
}

// ---- valid session passes through ----

func TestSessionCheck_ValidSessionPassesThrough(t *testing.T) {
	sess := &Session{SessionID: "s-ok", Status: SessionActive}
	store := newMapSessionStore(map[string]*Session{"s-ok": sess})

	mw := SessionCheck(store, &fixedSessionValidator{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "s-ok")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid session, got %d", rec.Code)
	}
}

// ---- custom error handler ----

func TestSessionCheck_CustomErrorHandler(t *testing.T) {
	customCalled := false
	handler := WithSessionErrorHandler(func(w http.ResponseWriter, _ *http.Request, _ error) {
		customCalled = true
		w.WriteHeader(http.StatusTeapot)
	})

	sess := &Session{SessionID: "s-rev"}
	store := newMapSessionStore(map[string]*Session{"s-rev": sess})

	mw := SessionCheck(store, &fixedSessionValidator{err: ErrSessionRevoked}, handler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "s-rev")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if !customCalled {
		t.Fatal("custom error handler should have been called")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected 418 from custom handler, got %d", rec.Code)
	}
}

// ---- custom SessionLookup ----

func TestSessionCheck_CustomSessionLookup(t *testing.T) {
	customLookupCalled := false
	sess := &Session{SessionID: "injected"}

	lookup := WithSessionLookup(func(_ context.Context, _ *http.Request, _ *authn.Principal) (*Session, error) {
		customLookupCalled = true
		return sess, nil
	})

	mw := SessionCheck(nil, &fixedSessionValidator{}, lookup)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withSessionClaim(req, "ignored")
	rec := httptest.NewRecorder()
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if !customLookupCalled {
		t.Fatal("custom SessionLookup should have been called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with custom lookup, got %d", rec.Code)
	}
}

// ---- custom session ID extractor ----

func TestSessionCheck_CustomSessionIDExtractor(t *testing.T) {
	sess := &Session{SessionID: "custom-id"}
	store := newMapSessionStore(map[string]*Session{"custom-id": sess})

	extractor := WithSessionIDExtractor(func(p *authn.Principal) (string, bool) {
		id, ok := p.Claims["custom_session"]
		return id, ok
	})

	p := &authn.Principal{Claims: map[string]string{"custom_session": "custom-id"}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()

	mw := SessionCheck(store, &fixedSessionValidator{}, extractor)
	mw(simpleOKHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with custom extractor, got %d", rec.Code)
	}
}
