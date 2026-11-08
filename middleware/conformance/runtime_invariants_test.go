package conformance_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/middleware/abuseguard"
	"github.com/birdordev/plumego/middleware/bodylimit"
	"github.com/birdordev/plumego/middleware/compression"
	"github.com/birdordev/plumego/middleware/concurrencylimit"
	"github.com/birdordev/plumego/middleware/cors"
	"github.com/birdordev/plumego/middleware/debug"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/middleware/securityheaders"
	"github.com/birdordev/plumego/middleware/singleflight"
	"github.com/birdordev/plumego/middleware/timeout"
	mwtracing "github.com/birdordev/plumego/middleware/tracing"
	"github.com/birdordev/plumego/security/authn"
)

func TestMiddlewareTypeShape(t *testing.T) {
	//nolint:staticcheck // QF1011: the explicit type is the compile-time assertion itself
	var _ middleware.Middleware = fixtureNoOpMiddleware()
}

func passThroughCases(t *testing.T) []struct {
	name string
	mw   middleware.Middleware
} {
	t.Helper()
	security, err := securityheaders.Middleware(securityheaders.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return []struct {
		name string
		mw   middleware.Middleware
	}{
		{"noop fixture", fixtureNoOpMiddleware()},
		{"request id", requestid.Middleware()},
		{"tracing", mwtracing.Middleware(&conformanceTracer{})},
		{"http metrics", httpmetrics.Middleware(conformanceObserver{})},
		{"access log", newConformanceAccessLog(t)},
		{"recovery", newConformanceRecovery(t)},
		{"auth", newConformanceAuth(t, authn.StaticToken("secret"))},
		{"body limit", bodylimit.Middleware(bodylimit.Config{MaxBytes: 1024})},
		{"concurrency limit", concurrencylimit.Middleware(concurrencylimit.Config{MaxConcurrent: 1})},
		{"cors", cors.Middleware(cors.CORSOptions{})},
		{"compression", compression.Middleware(compression.Config{})},
		{"debug", debug.Middleware(debug.DefaultConfig())},
		{"security headers", security},
		{"singleflight leader", singleflight.New(singleflight.Config{}).Middleware()},
		{"timeout", timeout.Middleware(timeout.Config{Timeout: time.Second})},
		{"abuse guard", newConformanceAbuseGuardMiddleware(t, abuseguard.AbuseGuardConfig{Rate: 100, Capacity: 100})},
	}
}

func passThroughRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Accept-Encoding", "gzip")
	return req
}

func TestMiddlewareNextCallExactlyOnce(t *testing.T) {
	for _, tc := range passThroughCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			next := &callCountingHandler{}
			rec := execute(tc.mw(next), passThroughRequest())
			if next.calls != 1 {
				t.Fatalf("next calls = %d, want 1", next.calls)
			}
			if rec.Code != http.StatusNoContent || rec.Header().Get("X-Conformance-Handler") != "called" {
				t.Fatalf("downstream response was lost: status=%d headers=%v", rec.Code, rec.Header())
			}
		})
	}
}

func TestMiddlewareNoSuccessResponseEmission(t *testing.T) {
	for _, tc := range passThroughCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, "handler-success")
			})
			rec := execute(tc.mw(next), passThroughRequest())
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", rec.Code)
			}
			var reader io.Reader = rec.Body
			if rec.Header().Get("Content-Encoding") == "gzip" {
				compressed, err := gzip.NewReader(rec.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer compressed.Close()
				reader = compressed
			}
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(body)) != "handler-success" {
				t.Fatalf("unexpected middleware output: %q", body)
			}
		})
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
