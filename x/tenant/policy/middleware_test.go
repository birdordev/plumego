package policy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

func TestMiddlewareDenied(t *testing.T) {
	evaluator := tenantcore.NewConfigPolicyEvaluator(&tenantcore.InMemoryConfigManager{})
	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-1")
	req.Header.Set("X-Model", "gpt-4o")
	rec := httptest.NewRecorder()

	mw := Middleware(Options{Evaluator: evaluator})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", rec.Code)
	}
	if handlerCalls != 0 {
		t.Fatalf("handler calls = %d, want 0", handlerCalls)
	}
}

func TestMiddlewareAllowed(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-1",
		Policy: tenantcore.PolicyConfig{
			AllowedModels: []string{"gpt-4o"},
			AllowedTools:  []string{"search"},
		},
	})

	evaluator := tenantcore.NewConfigPolicyEvaluator(cfg)
	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-1")
	req.Header.Set("X-Model", "gpt-4o")
	req.Header.Set("X-Tool", "search")
	rec := httptest.NewRecorder()

	mw := Middleware(Options{Evaluator: evaluator})
	mw(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls)
	}
}

func TestMiddlewareDeniedWritesCanonicalCode(t *testing.T) {
	evaluator := tenantcore.NewConfigPolicyEvaluator(&tenantcore.InMemoryConfigManager{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-1")
	req.Header.Set("X-Model", "gpt-4o")
	rec := httptest.NewRecorder()

	mw := Middleware(Options{Evaluator: evaluator})
	mw(handler).ServeHTTP(rec, req)

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != tenanttransport.CodePolicyDenied {
		t.Fatalf("error code = %q, want %q", body.Error.Code, tenanttransport.CodePolicyDenied)
	}
}
