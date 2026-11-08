package middleware_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/middleware/abuseguard"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/middleware/bodylimit"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/security/authn"
)

type middlewareErrorEnvelope struct {
	Error struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Category string `json:"category"`
	} `json:"error"`
}

func TestMiddlewareErrorConformance(t *testing.T) {
	tests := []struct {
		name         string
		expectedCode string
		handler      http.Handler
		request      *http.Request
	}{
		{
			name:         "auth unauthenticated",
			expectedCode: contract.CodeUnauthorized,
			handler: mustAuthMiddleware(t, authn.StaticToken("secret"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})),
			request: httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name:         "body too large",
			expectedCode: contract.CodeRequestBodyTooLarge,
			handler: bodylimit.Middleware(bodylimit.Config{MaxBytes: 4})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
			})),
			request: httptest.NewRequest(http.MethodPost, "/", strings.NewReader("toolarge")),
		},
		{
			name:         "abuse guard rate limited",
			expectedCode: contract.CodeRateLimited,
			handler: newTestAbuseGuardMiddleware(t, abuseguard.AbuseGuardConfig{Rate: 1, Capacity: 1, KeyFunc: func(*http.Request) string { return "k" }})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})),
			request: httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name:         "recovery internal",
			expectedCode: contract.CodeInternalError,
			handler: newConformanceRecovery(t)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("boom")
			})),
			request: httptest.NewRequest(http.MethodGet, "/", nil),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			if tc.name == "abuse guard rate limited" {
				tc.handler.ServeHTTP(httptest.NewRecorder(), tc.request.Clone(tc.request.Context()))
			}

			tc.handler.ServeHTTP(rec, tc.request)

			assertCanonicalEnvelope(t, rec, tc.expectedCode)
		})
	}
}

func mustAuthMiddleware(t *testing.T, authenticator authn.Authenticator, opts ...auth.AuthOption) middleware.Middleware {
	t.Helper()
	mw, err := auth.Authenticate(authenticator, opts...)
	if err != nil {
		t.Fatalf("auth middleware: %v", err)
	}
	return mw
}

func newConformanceRecovery(t *testing.T) middleware.Middleware {
	t.Helper()
	mw, err := recovery.Middleware(recovery.Config{
		Logger: log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard}),
	})
	if err != nil {
		t.Fatalf("recovery middleware: %v", err)
	}
	return mw
}

func newTestAbuseGuardMiddleware(t *testing.T, config abuseguard.AbuseGuardConfig) middleware.Middleware {
	t.Helper()

	guard := abuseguard.NewAbuseGuard(config)
	t.Cleanup(guard.Stop)
	return guard.Middleware()
}

func assertCanonicalEnvelope(t *testing.T, rec *httptest.ResponseRecorder, expectedCode string) {
	t.Helper()

	if rec.Code < 400 {
		t.Fatalf("expected error status, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json content type, got %q", got)
	}

	var payload middlewareErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if payload.Error.Message == "" {
		t.Fatalf("expected error.message field in payload %#v", payload)
	}
	if payload.Error.Category == "" {
		t.Fatalf("expected error.category field in payload %#v", payload)
	}
	if payload.Error.Code != expectedCode {
		t.Fatalf("expected code %q, got %q", expectedCode, payload.Error.Code)
	}
}
