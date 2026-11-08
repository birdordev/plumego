package ratelimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

// stubRateLimiter returns a fixed result from Allow.
type stubRateLimiter struct {
	result tenantcore.RateLimitResult
	err    error
	mu     sync.Mutex
	calls  int
}

func (l *stubRateLimiter) Allow(_ context.Context, _ string, _ tenantcore.RateLimitRequest) (tenantcore.RateLimitResult, error) {
	l.mu.Lock()
	l.calls++
	l.mu.Unlock()
	return l.result, l.err
}

// captureRateLimitRequest records what was passed to Allow.
type captureRateLimitRequest struct {
	inner    tenantcore.RateLimiter
	captured tenantcore.RateLimitRequest
}

func (l *captureRateLimitRequest) Allow(ctx context.Context, id string, req tenantcore.RateLimitRequest) (tenantcore.RateLimitResult, error) {
	l.captured = req
	return l.inner.Allow(ctx, id, req)
}

func TestMiddlewareNilLimiterPassthrough(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Limiter: nil})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-1")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("handler should be called when Limiter is nil")
	}
}

func TestMiddlewareMissingTenantIDPassthrough(t *testing.T) {
	// When tenant ID is absent, the middleware must pass through unconditionally
	// regardless of limiter state.
	limiter := &stubRateLimiter{result: tenantcore.RateLimitResult{Allowed: false}}

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Limiter: limiter})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Intentionally no tenant ID attached.
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when tenant ID is absent, got %d", rec.Code)
	}
	if !called {
		t.Fatal("handler should be called when tenant ID is absent")
	}
	if limiter.calls != 0 {
		t.Fatal("limiter should not be invoked when tenant ID is absent")
	}
}

func TestMiddlewareAllowed(t *testing.T) {
	limiter := &stubRateLimiter{
		result: tenantcore.RateLimitResult{Allowed: true, Remaining: 9, Limit: 10},
	}

	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Limiter: limiter})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-allow")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !handlerCalled {
		t.Fatal("handler must be called when request is allowed")
	}
}

func TestMiddlewareLimiterErrorFailClosed(t *testing.T) {
	limiter := &stubRateLimiter{err: context.DeadlineExceeded}

	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Limiter: limiter})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-err")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if handlerCalled {
		t.Fatal("handler must not be called when limiter returns an error")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on limiter error, got %d", rec.Code)
	}
}

func TestMiddlewareEstimatorUsed(t *testing.T) {
	capture := &captureRateLimitRequest{
		inner: &stubRateLimiter{result: tenantcore.RateLimitResult{Allowed: true}},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{
		Limiter:   capture,
		Estimator: func(_ *http.Request) int64 { return 5 },
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-est")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if capture.captured.Tokens != 5 {
		t.Fatalf("expected 5 tokens from estimator, got %d", capture.captured.Tokens)
	}
}

func TestMiddlewareEstimatorNonPositiveDefaultsToOne(t *testing.T) {
	for _, v := range []int64{0, -1, -100} {
		capture := &captureRateLimitRequest{
			inner: &stubRateLimiter{result: tenantcore.RateLimitResult{Allowed: true}},
		}

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		mw := Middleware(Options{
			Limiter:   capture,
			Estimator: func(_ *http.Request) int64 { return v },
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = tenantcore.RequestWithTenantID(req, "t-zero")
		rec := httptest.NewRecorder()
		mw(handler).ServeHTTP(rec, req)

		if capture.captured.Tokens != 1 {
			t.Fatalf("estimator value %d: expected default 1 token, got %d", v, capture.captured.Tokens)
		}
	}
}

func TestMiddlewareOnRejectedCallback(t *testing.T) {
	limiter := &stubRateLimiter{result: tenantcore.RateLimitResult{Allowed: false}}

	callbackInvoked := false
	mw := Middleware(Options{
		Limiter: limiter,
		OnRejected: func(w http.ResponseWriter, r *http.Request, result tenantcore.RateLimitResult) {
			callbackInvoked = true
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-cb")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if !callbackInvoked {
		t.Fatal("OnRejected should have been called")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected callback status 503, got %d", rec.Code)
	}
}

func TestMiddlewareRejectionSetsHeaders(t *testing.T) {
	limiter := &stubRateLimiter{
		result: tenantcore.RateLimitResult{
			Allowed:    false,
			Remaining:  0,
			Limit:      10,
			RetryAfter: 30 * time.Second,
		},
	}

	mw := Middleware(Options{Limiter: limiter})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-hdrs")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header should be set on rejection")
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got == "" {
		t.Fatal("X-RateLimit-Remaining should be set on rejection")
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got == "" {
		t.Fatal("X-RateLimit-Limit should be set on rejection")
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != tenanttransport.CodeRateLimited {
		t.Fatalf("error code = %q, want %q", body.Error.Code, tenanttransport.CodeRateLimited)
	}
}

func TestMiddlewareHooksInvokedOnAllowed(t *testing.T) {
	limiter := &stubRateLimiter{
		result: tenantcore.RateLimitResult{Allowed: true, Remaining: 4, Limit: 5},
	}

	var hookDecision tenantcore.RateLimitDecision
	mw := Middleware(Options{
		Limiter: limiter,
		Hooks: tenantcore.Hooks{
			OnRateLimit: func(_ context.Context, d tenantcore.RateLimitDecision) {
				hookDecision = d
			},
		},
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-hook-ok")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if !hookDecision.Allowed {
		t.Fatal("hook decision should indicate allowed")
	}
	if hookDecision.TenantID != "t-hook-ok" {
		t.Fatalf("hook TenantID = %q, want t-hook-ok", hookDecision.TenantID)
	}
	if hookDecision.Status != http.StatusOK {
		t.Fatalf("hook Status = %d, want 200", hookDecision.Status)
	}
}

func TestMiddlewareHooksInvokedOnRejected(t *testing.T) {
	limiter := &stubRateLimiter{
		result: tenantcore.RateLimitResult{Allowed: false, RetryAfter: 5 * time.Second},
	}

	var hookDecision tenantcore.RateLimitDecision
	mw := Middleware(Options{
		Limiter: limiter,
		Hooks: tenantcore.Hooks{
			OnRateLimit: func(_ context.Context, d tenantcore.RateLimitDecision) {
				hookDecision = d
			},
		},
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-hook-no")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if hookDecision.Allowed {
		t.Fatal("hook decision should indicate rejected")
	}
	if hookDecision.Status != http.StatusTooManyRequests {
		t.Fatalf("hook Status = %d, want 429", hookDecision.Status)
	}
}

func TestMiddlewareConcurrentRequests(t *testing.T) {
	provider := tenantcore.NewInMemoryRateLimitManager()
	provider.SetRateLimit("t-conc", tenantcore.RateLimitConfig{
		RequestsPerSecond: 1000,
		Burst:             1000,
	})
	limiter := tenantcore.NewTokenBucketRateLimiter(provider)

	mw := Middleware(Options{Limiter: limiter})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = tenantcore.RequestWithTenantID(req, "t-conc")
			rec := httptest.NewRecorder()
			mw(handler).ServeHTTP(rec, req)
		}()
	}
	wg.Wait()
}
