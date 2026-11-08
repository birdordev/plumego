package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/security/authn"
)

type staticAuthenticator struct {
	principal *authn.Principal
	err       error
}

func (s staticAuthenticator) Authenticate(_ *http.Request) (*authn.Principal, error) {
	return s.principal, s.err
}

type contextMarkerKey struct{}

type enrichingAuthenticator struct {
	principal *authn.Principal
}

func (e enrichingAuthenticator) Authenticate(_ *http.Request) (*authn.Principal, error) {
	return nil, nil
}

func (e enrichingAuthenticator) AuthenticateRequest(r *http.Request) (*authn.Principal, *http.Request, error) {
	ctx := context.WithValue(r.Context(), contextMarkerKey{}, "enriched")
	return e.principal, r.WithContext(ctx), nil
}

type staticAuthorizer struct {
	err error
}

func (s staticAuthorizer) Authorize(_ *authn.Principal, _, _ string) error {
	return s.err
}

func mustAuthenticate(t *testing.T, authenticator authn.Authenticator, opts ...AuthOption) middleware.Middleware {
	t.Helper()
	mw, err := Authenticate(authenticator, opts...)
	if err != nil {
		t.Fatalf("Authenticate returned error: %v", err)
	}
	return mw
}

func mustAuthorize(t *testing.T, authorizer authn.Authorizer, action, resource string, opts ...AuthOption) middleware.Middleware {
	t.Helper()
	mw, err := Authorize(authorizer, action, resource, opts...)
	if err != nil {
		t.Fatalf("Authorize returned error: %v", err)
	}
	return mw
}

func TestAuthenticateMiddlewareSuccess(t *testing.T) {
	principal := &authn.Principal{Subject: "user-1"}
	authenticator := staticAuthenticator{principal: principal}

	handler := mustAuthenticate(t, authenticator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := authn.PrincipalFromContext(r.Context())
		if got == nil || got.Subject != "user-1" {
			t.Fatalf("expected principal in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthenticateMiddlewareInvalidToken(t *testing.T) {
	authenticator := staticAuthenticator{err: authn.ErrInvalidToken}

	handler := mustAuthenticate(t, authenticator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	var payload struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unexpected response body: %v", err)
	}
	if payload.Error.Code != contract.CodeUnauthorized {
		t.Fatalf("expected code %s, got %s", contract.CodeUnauthorized, payload.Error.Code)
	}
}

func TestAuthenticateMiddlewareQuotesRealmHeader(t *testing.T) {
	authenticator := staticAuthenticator{err: authn.ErrInvalidToken}

	handler := mustAuthenticate(t, authenticator, WithAuthRealm(`api "private"\zone`))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got, want := rec.Header().Get("WWW-Authenticate"), `Bearer realm="api \"private\"\\zone"`; got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
}

func TestAuthenticateMiddlewareStripsRealmControls(t *testing.T) {
	authenticator := staticAuthenticator{err: authn.ErrInvalidToken}

	handler := mustAuthenticate(t, authenticator, WithAuthRealm("api\r\nbad\trealm"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got, want := rec.Header().Get("WWW-Authenticate"), `Bearer realm="apibadrealm"`; got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
}

func TestAuthenticateMiddlewareUsesEnrichedRequestContext(t *testing.T) {
	authenticator := enrichingAuthenticator{
		principal: &authn.Principal{Subject: "user-2"},
	}

	handler := mustAuthenticate(t, authenticator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, _ := r.Context().Value(contextMarkerKey{}).(string); got != "enriched" {
			t.Fatalf("expected enriched request context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthorizeAllowsRequest(t *testing.T) {
	principal := &authn.Principal{Subject: "user-authz"}
	authorizer := staticAuthorizer{}

	handler := mustAuthorize(t, authorizer, "read", "widgets")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestAuthorizeRejectsForbiddenRequest(t *testing.T) {
	principal := &authn.Principal{Subject: "user-authz"}
	authorizer := staticAuthorizer{err: authn.ErrUnauthorized}

	handler := mustAuthorize(t, authorizer, "write", "widgets")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestAuthenticateRejectsNilAuthenticator(t *testing.T) {
	if _, err := Authenticate(nil); !errors.Is(err, ErrNilAuthenticator) {
		t.Fatalf("Authenticate error = %v, want %v", err, ErrNilAuthenticator)
	}
}

func TestAuthorizeRejectsNilAuthorizer(t *testing.T) {
	if _, err := Authorize(nil, "read", "widgets"); !errors.Is(err, ErrNilAuthorizer) {
		t.Fatalf("Authorize error = %v, want %v", err, ErrNilAuthorizer)
	}
}

func TestAuthorizeFuncRejectsNilResolver(t *testing.T) {
	if _, err := AuthorizeFunc(staticAuthorizer{}, nil); !errors.Is(err, ErrNilAuthorizeResolver) {
		t.Fatalf("AuthorizeFunc error = %v, want %v", err, ErrNilAuthorizeResolver)
	}
}

// --- NEW COVERAGE TESTS ---

// TestWithAuthErrorHandler verifies that a custom error handler overrides the default.
func TestWithAuthErrorHandler(t *testing.T) {
	customCalled := false
	customHandler := func(w http.ResponseWriter, r *http.Request, err error) {
		customCalled = true
		w.WriteHeader(http.StatusTeapot)
	}

	authenticator := staticAuthenticator{err: authn.ErrInvalidToken}
	handler := mustAuthenticate(t, authenticator, WithAuthErrorHandler(customHandler))(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !customCalled {
		t.Fatal("custom error handler was not called")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected 418 from custom handler, got %d", rec.Code)
	}
}

// TestWithAuthErrorHandler_Nil verifies that a nil error handler is ignored
// and the default is used instead.
func TestWithAuthErrorHandler_Nil(t *testing.T) {
	authenticator := staticAuthenticator{err: authn.ErrInvalidToken}
	handler := mustAuthenticate(t, authenticator, WithAuthErrorHandler(nil))(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from default handler after nil override, got %d", rec.Code)
	}
}

// TestAuthenticateMiddleware_NilPrincipal verifies that a nil principal triggers
// the ErrUnauthenticated path.
func TestAuthenticateMiddleware_NilPrincipal(t *testing.T) {
	authenticator := staticAuthenticator{principal: nil, err: nil}
	handler := mustAuthenticate(t, authenticator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for nil principal, got %d", rec.Code)
	}
}

// TestAuthenticateEnrichedNilRequest verifies that when an enriching authenticator
// returns a nil request, the middleware falls back to the original request.
func TestAuthenticateEnrichedNilRequest(t *testing.T) {
	nilReqAuthenticator := &nilRequestAuthenticator{
		principal: &authn.Principal{Subject: "user-3"},
	}

	handler := mustAuthenticate(t, nilReqAuthenticator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := authn.PrincipalFromContext(r.Context()); got == nil || got.Subject != "user-3" {
			t.Fatalf("expected principal in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// nilRequestAuthenticator returns a nil *http.Request from AuthenticateRequest.
type nilRequestAuthenticator struct {
	principal *authn.Principal
}

func (n *nilRequestAuthenticator) Authenticate(_ *http.Request) (*authn.Principal, error) {
	return nil, nil
}

func (n *nilRequestAuthenticator) AuthenticateRequest(_ *http.Request) (*authn.Principal, *http.Request, error) {
	return n.principal, nil, nil // nil request
}

// TestAuthorizeFunc_NoPrincipalInContext verifies that Authorize returns 401 when
// no principal is stored in the request context.
func TestAuthorizeFunc_NoPrincipalInContext(t *testing.T) {
	mw, err := AuthorizeFunc(staticAuthorizer{}, func(*http.Request) (string, string) {
		return "read", "widgets"
	})
	if err != nil {
		t.Fatalf("AuthorizeFunc error: %v", err)
	}

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/secure", nil) // no principal in ctx
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without principal, got %d", rec.Code)
	}
}

// TestAuthErrorToAPIError_AllBranches covers every case in authErrorToAPIError.
func TestAuthErrorToAPIError_AllBranches(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "ErrUnauthorized", err: authn.ErrUnauthorized, wantStatus: http.StatusForbidden},
		{name: "ErrInvalidToken", err: authn.ErrInvalidToken, wantStatus: http.StatusUnauthorized},
		{name: "ErrExpiredToken", err: authn.ErrExpiredToken, wantStatus: http.StatusUnauthorized},
		{name: "ErrUnauthenticated", err: authn.ErrUnauthenticated, wantStatus: http.StatusUnauthorized},
		{name: "generic error", err: errors.New("unknown"), wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authenticator := staticAuthenticator{err: tt.err}
			handler := mustAuthenticate(t, authenticator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("%s: status = %d, want %d", tt.name, rec.Code, tt.wantStatus)
			}
		})
	}
}

// TestBearerChallenge_EmptyRealmReturnsEmpty covers the empty realm → empty challenge branch.
func TestBearerChallenge_EmptyRealmReturnsEmpty(t *testing.T) {
	// A realm that sanitizes to empty (only control chars) should produce no challenge.
	authenticator := staticAuthenticator{err: authn.ErrInvalidToken}
	handler := mustAuthenticate(t, authenticator,
		WithAuthRealm("\x00\x01\x02"), // all control characters → sanitized to ""
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q, want empty for all-control realm", got)
	}
}

// TestDefaultAuthErrorHandler_APIErrorPassThrough verifies the APIError fast path in
// defaultAuthErrorHandler (when the wrapped error already implements contract.APIError).
func TestDefaultAuthErrorHandler_APIErrorPassThrough(t *testing.T) {
	// Build an APIError and wrap it as an error that satisfies errors.As(err, &apiErr).
	apiErr := contract.NewErrorBuilder().
		Type(contract.TypeForbidden).
		Message("already an API error").
		Build()

	// Provide an authenticator that returns this APIError directly.
	authenticator := staticAuthenticator{err: apiErr}
	handler := mustAuthenticate(t, authenticator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 from API error passthrough, got %d", rec.Code)
	}
}

// TestDefaultAuthErrorHandler_NilErr verifies that a nil error is treated as
// ErrUnauthenticated (covers the `if err == nil` branch in defaultAuthErrorHandler).
func TestDefaultAuthErrorHandler_NilErr(t *testing.T) {
	// We call defaultAuthErrorHandler directly to exercise the nil-err branch.
	h := defaultAuthErrorHandler("")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h(rec, req, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for nil error, got %d", rec.Code)
	}
}

// TestAuthRealm_NotSetNoHeader verifies no WWW-Authenticate header is emitted when
// no realm is configured.
func TestAuthRealm_NotSetNoHeader(t *testing.T) {
	authenticator := staticAuthenticator{err: authn.ErrInvalidToken}
	// No WithAuthRealm option.
	handler := mustAuthenticate(t, authenticator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q, want empty when realm is not set", got)
	}
}
