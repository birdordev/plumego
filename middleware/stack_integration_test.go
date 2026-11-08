package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/middleware/securityheaders"
	"github.com/birdordev/plumego/middleware/timeout"
)

// mustRecoveryMw is a test helper that constructs a recovery middleware or
// fails the test.
func mustRecoveryMw(t *testing.T) middleware.Middleware {
	t.Helper()
	mw, err := recovery.Middleware(recovery.Config{
		Logger: log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard}),
	})
	if err != nil {
		t.Fatalf("recovery.Middleware: %v", err)
	}
	return mw
}

// mustSecurityHeadersMw is a test helper that constructs a security headers
// middleware with default config or fails the test.
func mustSecurityHeadersMw(t *testing.T) middleware.Middleware {
	t.Helper()
	mw, err := securityheaders.Middleware(securityheaders.DefaultConfig())
	if err != nil {
		t.Fatalf("securityheaders.Middleware: %v", err)
	}
	return mw
}

// TestStackRequestIDPropagatesContextToHandler verifies that the requestid
// middleware stamps a request ID into the context so that handlers downstream
// in the chain can read it.
func TestStackRequestIDPropagatesContextToHandler(t *testing.T) {
	const fixedID = "integration-req-id"
	var gotID string

	h := middleware.NewChain(
		requestid.Middleware(requestid.WithGenerator(func() string { return fixedID })),
		mustRecoveryMw(t),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = contract.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotID != fixedID {
		t.Fatalf("context request ID = %q, want %q", gotID, fixedID)
	}
}

// TestStackRequestIDAppearsInResponseHeader verifies that the requestid
// middleware writes the request ID to the response header so clients can
// correlate log entries.
func TestStackRequestIDAppearsInResponseHeader(t *testing.T) {
	const fixedID = "header-req-id"

	h := middleware.NewChain(
		requestid.Middleware(requestid.WithGenerator(func() string { return fixedID })),
		mustRecoveryMw(t),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get(contract.RequestIDHeader); got != fixedID {
		t.Fatalf("response %s = %q, want %q", contract.RequestIDHeader, got, fixedID)
	}
}

// TestStackRequestIDPreservedFromIncomingRequest verifies that when an
// inbound request already carries a request ID, the middleware reuses it
// rather than generating a new one.
func TestStackRequestIDPreservedFromIncomingRequest(t *testing.T) {
	const incomingID = "upstream-correlation-id"
	var contextID string

	h := middleware.NewChain(
		requestid.Middleware(),
		mustRecoveryMw(t),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextID = contract.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(contract.RequestIDHeader, incomingID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if contextID != incomingID {
		t.Fatalf("context request ID = %q, want incoming %q", contextID, incomingID)
	}
	if got := rec.Header().Get(contract.RequestIDHeader); got != incomingID {
		t.Fatalf("response header ID = %q, want %q", got, incomingID)
	}
}

// TestStackRecoveryCoversDownstreamPanic verifies that in the recommended
// order (requestid → recovery → handler), a panic from the handler is
// caught by recovery and returns a 500 canonical error envelope instead of
// crashing the server.
func TestStackRecoveryCoversDownstreamPanic(t *testing.T) {
	h := middleware.NewChain(
		requestid.Middleware(),
		mustRecoveryMw(t),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("downstream panic")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	assertCanonicalEnvelope(t, rec, contract.CodeInternalError)
}

// TestStackRecoveryDoesNotCoverPanicOutsideItsLayer verifies that a panic
// raised by middleware registered before recovery is NOT caught by the
// recovery layer. Recovery only covers the layers downstream of itself.
func TestStackRecoveryDoesNotCoverPanicOutsideItsLayer(t *testing.T) {
	panicMw := func(next http.Handler) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				panic("outer panic before recovery")
			})
		}
	}

	h := middleware.NewChain(
		panicMw(nil),
		mustRecoveryMw(t),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic to propagate past recovery since it is upstream")
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

// TestStackTimeoutContextCanceledObservableByHandler verifies that when the
// timeout middleware fires, the request context is canceled and the handler
// can observe it through r.Context().Done().
func TestStackTimeoutContextCanceledObservableByHandler(t *testing.T) {
	canceled := make(chan struct{}, 1)

	h := middleware.NewChain(
		mustRecoveryMw(t),
		timeout.Middleware(timeout.Config{Timeout: 10 * time.Millisecond}),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			canceled <- struct{}{}
		case <-time.After(200 * time.Millisecond):
		}
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rec.Code)
	}
	select {
	case <-canceled:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("handler did not observe context cancellation within timeout")
	}
}

// TestStackTimeoutDisabledWhenZeroPassesThrough verifies that a zero-value
// timeout Config is a no-op: the chain behaves as if timeout were absent.
func TestStackTimeoutDisabledWhenZeroPassesThrough(t *testing.T) {
	h := middleware.NewChain(
		mustRecoveryMw(t),
		timeout.Middleware(timeout.Config{}),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.Context().Err(); err != nil {
			t.Fatalf("unexpected context error on zero-timeout chain: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestStackSecurityHeadersAppliedInChain verifies that security headers set
// by securityheaders.Middleware appear in the response when it is wired into
// a middleware chain, regardless of what the downstream handler does.
func TestStackSecurityHeadersAppliedInChain(t *testing.T) {
	h := middleware.NewChain(
		requestid.Middleware(),
		mustRecoveryMw(t),
		mustSecurityHeadersMw(t),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got == "" {
		t.Fatal("X-Frame-Options not set by security headers middleware in chain")
	}
}

// TestStackSecurityHeadersPersistAfterDownstreamPanic verifies that security
// headers set before the handler runs are not discarded when the recovery
// layer rewrites the response to a 500. Since security headers mutate the
// header map before WriteHeader is called, they survive panic recovery.
func TestStackSecurityHeadersPersistAfterDownstreamPanic(t *testing.T) {
	h := middleware.NewChain(
		requestid.Middleware(),
		mustRecoveryMw(t),
		mustSecurityHeadersMw(t),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("handler panic")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q after panic recovery, want nosniff", got)
	}
}

// TestStackTimeoutContextCanceledWithAlreadyDeadlineContext verifies that
// middleware observes a pre-canceled context correctly. If the request context
// is already done before the stack runs, the chain should not block.
func TestStackTimeoutContextCanceledWithAlreadyDeadlineContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	h := middleware.NewChain(
		timeout.Middleware(timeout.Config{Timeout: 500 * time.Millisecond}),
	).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		}
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
}
