package conformance_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spcent/plumego/contract"
	"github.com/spcent/plumego/middleware"
	"github.com/spcent/plumego/middleware/abuseguard"
	"github.com/spcent/plumego/middleware/bodylimit"
	"github.com/spcent/plumego/middleware/httpmetrics"
	"github.com/spcent/plumego/middleware/requestid"
	mwtracing "github.com/spcent/plumego/middleware/tracing"
	"github.com/spcent/plumego/security/authn"
)

func TestMiddlewareTypeShape(t *testing.T) {
	//nolint:staticcheck // QF1011: the explicit type is the compile-time assertion itself
	var _ middleware.Middleware = fixtureNoOpMiddleware()
}

func TestMiddlewareNextCallAtMostOnce(t *testing.T) {
	recoveryMw := newConformanceRecovery(t)
	tests := []struct {
		name string
		mw   middleware.Middleware
		req  *http.Request
	}{
		{
			name: "noop fixture",
			mw:   fixtureNoOpMiddleware(),
			req:  httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name: "request id",
			mw:   requestid.Middleware(),
			req:  httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name: "tracing",
			mw:   mwtracing.Middleware(nil),
			req:  httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name: "http metrics",
			mw:   httpmetrics.Middleware(nil),
			req:  httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name: "access log",
			mw:   newConformanceAccessLog(t),
			req:  httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name: "recovery",
			mw:   recoveryMw,
			req:  httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name: "auth valid token",
			mw:   newConformanceAuth(t, authn.StaticToken("secret")),
			req:  httptest.NewRequest(http.MethodGet, "/", nil),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "auth valid token" {
				tc.req.Header.Set("Authorization", "Bearer secret")
			}

			next := &callCountingHandler{}
			h := tc.mw(next)
			h.ServeHTTP(httptest.NewRecorder(), tc.req)

			if next.calls > 1 {
				t.Fatalf("middleware invoked next more than once: %d", next.calls)
			}
		})
	}
}

func TestMiddlewareNoSuccessResponseEmission(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "handler-success")
	})

	h := fixtureNoOpMiddleware()(next)
	rec := execute(h, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status from handler, got %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "handler-success" {
		t.Fatalf("expected body from handler, got %q", rec.Body.String())
	}
}

func TestMiddlewareOrderingDeterministic(t *testing.T) {
	order := make([]string, 0, 3)

	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "first:before")
			next.ServeHTTP(w, r)
			order = append(order, "first:after")
		})
	}
	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "second:before")
			next.ServeHTTP(w, r)
			order = append(order, "second:after")
		})
	}
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusNoContent)
	})

	h := middleware.NewChain(first, second).Build(final)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first:before", "second:before", "handler", "second:after", "first:after"}
	if len(order) != len(want) {
		t.Fatalf("order length mismatch: got %v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("unexpected execution order: got %v want %v", order, want)
		}
	}
}

func TestMiddlewareErrorSchemaCanonical(t *testing.T) {
	tests := []struct {
		name         string
		expectedCode string
		handler      http.Handler
		request      *http.Request
		prepare      func(http.Handler, *http.Request)
	}{
		{
			name:         "auth unauthenticated",
			expectedCode: contract.CodeUnauthorized,
			handler: newConformanceAuth(t, authn.StaticToken("secret"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			handler: newConformanceAbuseGuardMiddleware(t, abuseguard.AbuseGuardConfig{Rate: 1, Capacity: 1, KeyFunc: func(*http.Request) string { return "k" }})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})),
			request: httptest.NewRequest(http.MethodGet, "/", nil),
			prepare: func(handler http.Handler, req *http.Request) {
				handler.ServeHTTP(httptest.NewRecorder(), req.Clone(req.Context()))
			},
		},
		{
			name:         "recovery internal",
			expectedCode: contract.CodeInternalError,
			handler: newConformanceRecovery(t)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("boom")
			})),
			request: httptest.NewRequest(http.MethodGet, "/", nil),
		},
		{
			name:         "fixture canonical error",
			expectedCode: contract.CodeInternalError,
			handler: fixtureCanonicalErrorMiddleware(contract.CodeInternalError)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})),
			request: httptest.NewRequest(http.MethodGet, "/", nil),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prepare != nil {
				tc.prepare(tc.handler, tc.request)
			}

			rec := execute(tc.handler, tc.request)
			assertCanonicalErrorEnvelope(t, rec, tc.expectedCode)
		})
	}
}

func newConformanceAbuseGuardMiddleware(t *testing.T, config abuseguard.AbuseGuardConfig) middleware.Middleware {
	t.Helper()

	guard := abuseguard.NewAbuseGuard(config)
	t.Cleanup(guard.Stop)
	return guard.Middleware()
}
