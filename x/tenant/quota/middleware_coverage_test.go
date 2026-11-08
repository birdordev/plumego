package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

// errQuotaManager always returns an error from Allow.
type errQuotaManager struct{ err error }

func (m *errQuotaManager) Allow(_ context.Context, _ string, _ tenantcore.QuotaRequest) (tenantcore.QuotaResult, error) {
	return tenantcore.QuotaResult{}, m.err
}

// stubQuotaManager returns a fixed result.
type stubQuotaManager struct {
	result tenantcore.QuotaResult
	err    error
	calls  int
	mu     sync.Mutex
}

func (m *stubQuotaManager) Allow(_ context.Context, _ string, _ tenantcore.QuotaRequest) (tenantcore.QuotaResult, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return m.result, m.err
}

func TestMiddlewareNilManagerPassthrough(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Manager: nil})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("handler should have been called when Manager is nil")
	}
}

func TestMiddlewareAllowed(t *testing.T) {
	mgr := &stubQuotaManager{result: tenantcore.QuotaResult{Allowed: true, RemainingRequests: 9, RemainingTokens: 90}}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{Manager: mgr})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-allowed")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("X-Quota-Remaining-Requests"); got != "9" {
		t.Fatalf("X-Quota-Remaining-Requests = %q, want 9", got)
	}
	if got := rec.Header().Get("X-Quota-Remaining-Tokens"); got != "90" {
		t.Fatalf("X-Quota-Remaining-Tokens = %q, want 90", got)
	}
}

func TestMiddlewareTokensFromEstimator(t *testing.T) {
	var capturedReq tenantcore.QuotaRequest
	mgr := &stubQuotaManager{result: tenantcore.QuotaResult{Allowed: true}}
	// Override Allow to capture the request
	capture := &captureQuotaManager{inner: mgr, captured: &capturedReq}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{
		Manager:   capture,
		Estimator: func(_ *http.Request) int { return 42 },
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-est")
	// Header present but should be ignored when Estimator is set
	req.Header.Set(tenanttransport.DefaultTokensHeader, "999")

	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if capturedReq.Tokens != 42 {
		t.Fatalf("expected estimator token count 42, got %d", capturedReq.Tokens)
	}
}

func TestMiddlewareCustomTokensHeader(t *testing.T) {
	var capturedReq tenantcore.QuotaRequest
	mgr := &stubQuotaManager{result: tenantcore.QuotaResult{Allowed: true}}
	capture := &captureQuotaManager{inner: mgr, captured: &capturedReq}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{
		Manager:      capture,
		TokensHeader: "X-My-Tokens",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-hdr")
	req.Header.Set("X-My-Tokens", "7")

	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if capturedReq.Tokens != 7 {
		t.Fatalf("expected token count 7 from custom header, got %d", capturedReq.Tokens)
	}
}

func TestMiddlewareOnRejectedCallback(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-cb",
		Quota:    tenantcore.QuotaConfig{Limits: []tenantcore.QuotaLimit{{Window: tenantcore.QuotaWindowMinute, Requests: 1}}},
	})
	manager := tenantcore.NewFixedWindowQuotaManager(cfg)

	callbackInvoked := false
	mw := Middleware(Options{
		Manager: manager,
		OnRejected: func(w http.ResponseWriter, r *http.Request, result tenantcore.QuotaResult) {
			callbackInvoked = true
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-cb")

	// First request allowed
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", rec.Code)
	}

	// Second request rejected via callback
	rec = httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)
	if !callbackInvoked {
		t.Fatal("OnRejected should have been called")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected callback status 503, got %d", rec.Code)
	}
}

func TestMiddlewareManagerError(t *testing.T) {
	sentinel := errors.New("quota backend unavailable")
	mgr := &errQuotaManager{err: sentinel}

	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Manager: mgr})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-err")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if handlerCalled {
		t.Fatal("handler should not be called when Manager returns an error")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on manager error, got %d", rec.Code)
	}
}

func TestMiddlewareHooksInvokedOnAllowed(t *testing.T) {
	mgr := &stubQuotaManager{result: tenantcore.QuotaResult{Allowed: true, RemainingRequests: 5}}

	var hookDecision tenantcore.QuotaDecision
	mw := Middleware(Options{
		Manager: mgr,
		Hooks: tenantcore.Hooks{
			OnQuota: func(_ context.Context, d tenantcore.QuotaDecision) {
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
	mgr := &stubQuotaManager{result: tenantcore.QuotaResult{Allowed: false, RetryAfter: 60 * time.Second}}

	var hookDecision tenantcore.QuotaDecision
	mw := Middleware(Options{
		Manager: mgr,
		Hooks: tenantcore.Hooks{
			OnQuota: func(_ context.Context, d tenantcore.QuotaDecision) {
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

func TestMiddlewareMissingTenantID(t *testing.T) {
	// A stub manager that always allows — we want to verify the middleware
	// still calls the manager and forwards a request even when the tenant ID
	// is absent from the context.
	mgr := &stubQuotaManager{result: tenantcore.QuotaResult{Allowed: true, RemainingRequests: 5}}

	var capturedID string
	capture := &captureTenantIDManager{inner: mgr, capturedID: &capturedID}

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.WriteHeader(http.StatusOK)
	})
	mw := Middleware(Options{Manager: capture})

	// Request with no tenant ID attached.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when manager allows, got %d", rec.Code)
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls)
	}
	// Manager was called with empty tenant ID.
	if capturedID != "" {
		t.Fatalf("expected empty tenant ID, got %q", capturedID)
	}
}

func TestMiddlewareConcurrentRequests(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-concurrent",
		Quota:    tenantcore.QuotaConfig{Limits: []tenantcore.QuotaLimit{{Window: tenantcore.QuotaWindowMinute, Requests: 100}}},
	})
	manager := tenantcore.NewFixedWindowQuotaManager(cfg)

	mw := Middleware(Options{Manager: manager})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = tenantcore.RequestWithTenantID(req, "t-concurrent")
			rec := httptest.NewRecorder()
			mw(handler).ServeHTTP(rec, req)
		}()
	}
	wg.Wait()
}

func TestMiddlewareInvalidTokenHeaderIgnored(t *testing.T) {
	var capturedReq tenantcore.QuotaRequest
	mgr := &stubQuotaManager{result: tenantcore.QuotaResult{Allowed: true}}
	capture := &captureQuotaManager{inner: mgr, captured: &capturedReq}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{Manager: capture})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-badtok")
	req.Header.Set(tenanttransport.DefaultTokensHeader, "not-a-number")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if capturedReq.Tokens != 0 {
		t.Fatalf("invalid token header should default to 0, got %d", capturedReq.Tokens)
	}
}

// captureQuotaManager records the last QuotaRequest and delegates to inner.
type captureQuotaManager struct {
	inner    tenantcore.QuotaManager
	captured *tenantcore.QuotaRequest
}

func (m *captureQuotaManager) Allow(ctx context.Context, id string, req tenantcore.QuotaRequest) (tenantcore.QuotaResult, error) {
	*m.captured = req
	return m.inner.Allow(ctx, id, req)
}

// captureTenantIDManager records the tenant ID passed to Allow.
type captureTenantIDManager struct {
	inner      tenantcore.QuotaManager
	capturedID *string
}

func (m *captureTenantIDManager) Allow(ctx context.Context, id string, req tenantcore.QuotaRequest) (tenantcore.QuotaResult, error) {
	*m.capturedID = id
	return m.inner.Allow(ctx, id, req)
}
