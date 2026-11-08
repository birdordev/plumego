package policy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

// errPolicyEvaluator always returns an error from Evaluate.
type errPolicyEvaluator struct{ err error }

func (e *errPolicyEvaluator) Evaluate(_ context.Context, _ string, _ tenantcore.PolicyRequest) (tenantcore.PolicyResult, error) {
	return tenantcore.PolicyResult{}, e.err
}

// capturePolicyEvaluator records the last PolicyRequest.
type capturePolicyEvaluator struct {
	inner    tenantcore.PolicyEvaluator
	captured tenantcore.PolicyRequest
}

func (e *capturePolicyEvaluator) Evaluate(ctx context.Context, id string, req tenantcore.PolicyRequest) (tenantcore.PolicyResult, error) {
	e.captured = req
	return e.inner.Evaluate(ctx, id, req)
}

func TestMiddlewareNilEvaluatorPassthrough(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Evaluator: nil})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("handler should have been called when Evaluator is nil")
	}
}

func TestMiddlewareCustomModelAndToolHeaders(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-custom-hdr",
		Policy: tenantcore.PolicyConfig{
			AllowedModels: []string{"my-model"},
			AllowedTools:  []string{"my-tool"},
		},
	})
	evaluator := tenantcore.NewConfigPolicyEvaluator(cfg)
	capture := &capturePolicyEvaluator{inner: evaluator}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{
		Evaluator:   capture,
		ModelHeader: "X-Custom-Model",
		ToolHeader:  "X-Custom-Tool",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1", nil)
	req = tenantcore.RequestWithTenantID(req, "t-custom-hdr")
	req.Header.Set("X-Custom-Model", "my-model")
	req.Header.Set("X-Custom-Tool", "my-tool")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if capture.captured.Model != "my-model" {
		t.Fatalf("Model = %q, want my-model", capture.captured.Model)
	}
	if capture.captured.Tool != "my-tool" {
		t.Fatalf("Tool = %q, want my-tool", capture.captured.Tool)
	}
}

func TestMiddlewareOnDeniedCallback(t *testing.T) {
	evaluator := tenantcore.NewConfigPolicyEvaluator(&tenantcore.InMemoryConfigManager{})
	callbackInvoked := false

	mw := Middleware(Options{
		Evaluator: evaluator,
		OnDenied: func(w http.ResponseWriter, r *http.Request, result tenantcore.PolicyResult) {
			callbackInvoked = true
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-denied-cb")
	req.Header.Set("X-Model", "blocked-model")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if !callbackInvoked {
		t.Fatal("OnDenied should have been called")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected callback status 503, got %d", rec.Code)
	}
}

func TestMiddlewareEvaluatorError(t *testing.T) {
	sentinel := errors.New("policy backend down")
	eval := &errPolicyEvaluator{err: sentinel}

	handlerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Evaluator: eval})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-eval-err")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if handlerCalled {
		t.Fatal("handler must not be called when Evaluator returns an error")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestMiddlewareHooksInvokedOnAllowed(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-hook-allow",
		Policy:   tenantcore.PolicyConfig{AllowedModels: []string{"ok-model"}},
	})
	evaluator := tenantcore.NewConfigPolicyEvaluator(cfg)

	var hookDecision tenantcore.PolicyDecision
	mw := Middleware(Options{
		Evaluator: evaluator,
		Hooks: tenantcore.Hooks{
			OnPolicy: func(_ context.Context, d tenantcore.PolicyDecision) {
				hookDecision = d
			},
		},
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-hook-allow")
	req.Header.Set(tenanttransport.DefaultModelHeader, "ok-model")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if !hookDecision.Allowed {
		t.Fatal("hook decision should indicate allowed")
	}
	if hookDecision.TenantID != "t-hook-allow" {
		t.Fatalf("hook TenantID = %q, want t-hook-allow", hookDecision.TenantID)
	}
	if hookDecision.Status != http.StatusOK {
		t.Fatalf("hook Status = %d, want 200", hookDecision.Status)
	}
	if hookDecision.Model != "ok-model" {
		t.Fatalf("hook Model = %q, want ok-model", hookDecision.Model)
	}
}

func TestMiddlewareHooksInvokedOnDenied(t *testing.T) {
	evaluator := tenantcore.NewConfigPolicyEvaluator(&tenantcore.InMemoryConfigManager{})

	var hookDecision tenantcore.PolicyDecision
	mw := Middleware(Options{
		Evaluator: evaluator,
		Hooks: tenantcore.Hooks{
			OnPolicy: func(_ context.Context, d tenantcore.PolicyDecision) {
				hookDecision = d
			},
		},
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-hook-deny")
	req.Header.Set(tenanttransport.DefaultModelHeader, "blocked")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if hookDecision.Allowed {
		t.Fatal("hook decision should indicate denied")
	}
	if hookDecision.Status != http.StatusForbidden {
		t.Fatalf("hook Status = %d, want 403", hookDecision.Status)
	}
}

func TestMiddlewarePolicyRequestPopulated(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-req",
		Policy:   tenantcore.PolicyConfig{AllowedModels: []string{"m1"}, AllowedTools: []string{"t1"}},
	})
	evaluator := tenantcore.NewConfigPolicyEvaluator(cfg)
	capture := &capturePolicyEvaluator{inner: evaluator}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{Evaluator: capture})

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	req = tenantcore.RequestWithTenantID(req, "t-req")
	req.Header.Set(tenanttransport.DefaultModelHeader, "m1")
	req.Header.Set(tenanttransport.DefaultToolHeader, "t1")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if capture.captured.Method != http.MethodPost {
		t.Fatalf("Method = %q, want POST", capture.captured.Method)
	}
	if capture.captured.Path != "/api/chat" {
		t.Fatalf("Path = %q, want /api/chat", capture.captured.Path)
	}
	if capture.captured.Model != "m1" {
		t.Fatalf("Model = %q, want m1", capture.captured.Model)
	}
	if capture.captured.Tool != "t1" {
		t.Fatalf("Tool = %q, want t1", capture.captured.Tool)
	}
}

func TestMiddlewareMissingTenantIDDenied(t *testing.T) {
	// No tenant config → policy evaluation for empty ID → denied.
	evaluator := tenantcore.NewConfigPolicyEvaluator(&tenantcore.InMemoryConfigManager{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{Evaluator: evaluator})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Intentionally no tenant ID on context
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with no tenant config, got %d", rec.Code)
	}
}

func TestMiddlewareEmptyAllowListsPermitAll(t *testing.T) {
	// Empty allow lists mean "allow all" for that dimension.
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-open",
		Policy:   tenantcore.PolicyConfig{},
	})
	evaluator := tenantcore.NewConfigPolicyEvaluator(cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{Evaluator: evaluator})

	for _, model := range []string{"gpt-4o", "claude-3", "gemini"} {
		req := httptest.NewRequest(http.MethodGet, "/any/path", nil)
		req = tenantcore.RequestWithTenantID(req, "t-open")
		req.Header.Set(tenanttransport.DefaultModelHeader, model)
		rec := httptest.NewRecorder()
		mw(handler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("model %q: expected 200 (open policy), got %d", model, rec.Code)
		}
	}
}

func TestMiddlewareAllowedModelBlockedByTool(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-model-ok-tool-blocked",
		Policy: tenantcore.PolicyConfig{
			AllowedModels: []string{"gpt-4o"},
			AllowedTools:  []string{"search"},
		},
	})
	evaluator := tenantcore.NewConfigPolicyEvaluator(cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := Middleware(Options{Evaluator: evaluator})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-model-ok-tool-blocked")
	req.Header.Set(tenanttransport.DefaultModelHeader, "gpt-4o")
	req.Header.Set(tenanttransport.DefaultToolHeader, "forbidden-tool")
	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when tool is blocked, got %d", rec.Code)
	}
}
