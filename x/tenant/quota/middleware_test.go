package quota

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

func TestMiddlewareExceeded(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-1",
		Quota: tenantcore.QuotaConfig{
			Limits: []tenantcore.QuotaLimit{{Window: tenantcore.QuotaWindowMinute, Requests: 1}},
		},
	})
	manager := tenantcore.NewFixedWindowQuotaManager(cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Manager: manager})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-1")

	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", rec.Code)
	}
}

func TestMiddlewareExceededSetsRetryAfterAndQuotaHeaders(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-1",
		Quota: tenantcore.QuotaConfig{
			Limits: []tenantcore.QuotaLimit{{Window: tenantcore.QuotaWindowMinute, Requests: 2, Tokens: 10}},
		},
	})
	manager := tenantcore.NewFixedWindowQuotaManager(cfg)

	handlerCalls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware(Options{Manager: manager})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = tenantcore.RequestWithTenantID(req, "t-1")
	req.Header.Set(tenanttransport.DefaultTokensHeader, "8")

	rec := httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Quota-Remaining-Requests"); got != "1" {
		t.Fatalf("first request remaining requests = %q, want 1", got)
	}
	if got := rec.Header().Get("X-Quota-Remaining-Tokens"); got != "2" {
		t.Fatalf("first request remaining tokens = %q, want 2", got)
	}

	rec = httptest.NewRecorder()
	mw(handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rec.Code)
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header should be set on quota rejection")
	}
	if got := rec.Header().Get("X-Quota-Remaining-Requests"); got != "1" {
		t.Fatalf("rejection remaining requests = %q, want 1", got)
	}
	if got := rec.Header().Get("X-Quota-Remaining-Tokens"); got != "2" {
		t.Fatalf("rejection remaining tokens = %q, want 2", got)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != tenanttransport.CodeQuotaExceeded {
		t.Fatalf("error code = %q, want %q", body.Error.Code, tenanttransport.CodeQuotaExceeded)
	}
}
