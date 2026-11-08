package tenant_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/security/authn"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	"github.com/birdordev/plumego/x/tenant/policy"
	"github.com/birdordev/plumego/x/tenant/quota"
	"github.com/birdordev/plumego/x/tenant/ratelimit"
	"github.com/birdordev/plumego/x/tenant/resolve"
	tenanttransport "github.com/birdordev/plumego/x/tenant/transport"
)

// buildChain assembles the canonical resolve → policy → quota → ratelimit stack
// and returns it as a handler wrapping the provided final handler.
func buildChain(
	resolveOpts resolve.Options,
	policyOpts policy.Options,
	quotaOpts quota.Options,
	rateLimitOpts ratelimit.Options,
	final http.Handler,
) http.Handler {
	return middleware.NewChain(
		resolve.Middleware(resolveOpts),
		policy.Middleware(policyOpts),
		quota.Middleware(quotaOpts),
		ratelimit.Middleware(rateLimitOpts),
	).Build(final)
}

// okHandler is a simple handler that always returns 200.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// newFullConfig returns a ConfigManager with a fully permissive tenant "t-1".
func newFullConfig() *tenantcore.InMemoryConfigManager {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-1",
		Policy: tenantcore.PolicyConfig{
			AllowedModels: []string{"gpt-4o"},
			AllowedTools:  []string{"search"},
		},
		Quota: tenantcore.QuotaConfig{
			Limits: []tenantcore.QuotaLimit{
				{Window: tenantcore.QuotaWindowMinute, Requests: 100},
			},
		},
	})
	return cfg
}

// TestChain_AllPass verifies that a properly configured request passes all four
// middleware layers and reaches the handler with a 200 response.
func TestChain_AllPass(t *testing.T) {
	cfg := newFullConfig()

	rlProvider := tenantcore.NewInMemoryRateLimitManager()
	rlProvider.SetRateLimit("t-1", tenantcore.RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             10,
	})

	h := buildChain(
		resolve.Options{},
		policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(cfg)},
		quota.Options{Manager: tenantcore.NewFixedWindowQuotaManager(cfg)},
		ratelimit.Options{Limiter: tenantcore.NewTokenBucketRateLimiter(rlProvider)},
		okHandler(),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), &authn.Principal{TenantID: "t-1"}))
	req.Header.Set("X-Model", "gpt-4o")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// TestChain_ResolveFails verifies that a request with no tenant identity is
// rejected at the resolve layer (401) before reaching downstream middleware.
func TestChain_ResolveFails(t *testing.T) {
	cfg := newFullConfig()

	h := buildChain(
		resolve.Options{},
		policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(cfg)},
		quota.Options{Manager: tenantcore.NewFixedWindowQuotaManager(cfg)},
		ratelimit.Options{},
		okHandler(),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil) // no principal, no header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != tenanttransport.CodeRequired {
		t.Errorf("code = %q, want %q", body.Error.Code, tenanttransport.CodeRequired)
	}
}

// TestChain_PolicyDenies verifies that a tenant whose policy forbids the
// requested model is blocked at the policy layer (403).
func TestChain_PolicyDenies(t *testing.T) {
	cfg := newFullConfig()

	h := buildChain(
		resolve.Options{},
		policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(cfg)},
		quota.Options{Manager: tenantcore.NewFixedWindowQuotaManager(cfg)},
		ratelimit.Options{},
		okHandler(),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), &authn.Principal{TenantID: "t-1"}))
	req.Header.Set("X-Model", "claude-opus-4") // not in AllowedModels
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}

	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != tenanttransport.CodePolicyDenied {
		t.Errorf("code = %q, want %q", body.Error.Code, tenanttransport.CodePolicyDenied)
	}
}

// TestChain_QuotaExceeded verifies that once a tenant exhausts its quota the
// quota layer rejects subsequent requests with 429 and sets the expected headers.
func TestChain_QuotaExceeded(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-1",
		Policy: tenantcore.PolicyConfig{
			AllowedModels: []string{"gpt-4o"},
		},
		Quota: tenantcore.QuotaConfig{
			Limits: []tenantcore.QuotaLimit{
				{Window: tenantcore.QuotaWindowMinute, Requests: 1},
			},
		},
	})

	h := buildChain(
		resolve.Options{},
		policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(cfg)},
		quota.Options{Manager: tenantcore.NewFixedWindowQuotaManager(cfg)},
		ratelimit.Options{},
		okHandler(),
	)

	makeReq := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(authn.WithPrincipal(r.Context(), &authn.Principal{TenantID: "t-1"}))
		r.Header.Set("X-Model", "gpt-4o")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if first := makeReq(); first.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", first.Code)
	}

	second := makeReq()
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header should be set on quota rejection")
	}
}

// TestChain_RateLimited verifies that once burst is consumed the rate-limit
// layer rejects with 429 and X-RateLimit headers are set.
func TestChain_RateLimited(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	cfg.SetTenantConfig(tenantcore.Config{
		TenantID: "t-1",
		Policy: tenantcore.PolicyConfig{
			AllowedModels: []string{"gpt-4o"},
		},
		Quota: tenantcore.QuotaConfig{
			Limits: []tenantcore.QuotaLimit{
				{Window: tenantcore.QuotaWindowMinute, Requests: 100},
			},
		},
	})

	rlProvider := tenantcore.NewInMemoryRateLimitManager()
	rlProvider.SetRateLimit("t-1", tenantcore.RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1, // single burst slot — second request is immediately rejected
	})

	h := buildChain(
		resolve.Options{},
		policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(cfg)},
		quota.Options{Manager: tenantcore.NewFixedWindowQuotaManager(cfg)},
		ratelimit.Options{Limiter: tenantcore.NewTokenBucketRateLimiter(rlProvider)},
		okHandler(),
	)

	makeReq := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(authn.WithPrincipal(r.Context(), &authn.Principal{TenantID: "t-1"}))
		r.Header.Set("X-Model", "gpt-4o")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if first := makeReq(); first.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", first.Code)
	}

	second := makeReq()
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", second.Code)
	}
	if second.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("X-RateLimit-Limit header should be set on rate-limit rejection")
	}
}

// TestChain_TenantIsolation verifies that quota and rate-limit accounting is
// per-tenant: exhausting tenant "t-1" does not affect tenant "t-2".
func TestChain_TenantIsolation(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	for _, id := range []string{"t-1", "t-2"} {
		cfg.SetTenantConfig(tenantcore.Config{
			TenantID: id,
			Policy: tenantcore.PolicyConfig{
				AllowedModels: []string{"gpt-4o"},
			},
			Quota: tenantcore.QuotaConfig{
				Limits: []tenantcore.QuotaLimit{
					{Window: tenantcore.QuotaWindowMinute, Requests: 1},
				},
			},
		})
	}

	h := buildChain(
		resolve.Options{},
		policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(cfg)},
		quota.Options{Manager: tenantcore.NewFixedWindowQuotaManager(cfg)},
		ratelimit.Options{},
		okHandler(),
	)

	makeReq := func(tenantID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(authn.WithPrincipal(r.Context(), &authn.Principal{TenantID: tenantID}))
		r.Header.Set("X-Model", "gpt-4o")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Exhaust t-1's quota.
	if r := makeReq("t-1"); r.Code != http.StatusOK {
		t.Fatalf("t-1 first: expected 200, got %d", r.Code)
	}
	if r := makeReq("t-1"); r.Code != http.StatusTooManyRequests {
		t.Errorf("t-1 second: expected 429, got %d", r.Code)
	}

	// t-2 must still succeed.
	if r := makeReq("t-2"); r.Code != http.StatusOK {
		t.Errorf("t-2 first: expected 200, got %d — tenant isolation violated", r.Code)
	}
}

// TestChain_UnknownTenantDeniedByPolicy verifies that a tenant whose ID is present
// in the request but absent from the policy configuration is denied at the policy
// layer (403). The resolve layer accepts any well-formed ID; policy fails closed.
func TestChain_UnknownTenantDeniedByPolicy(t *testing.T) {
	cfg := tenantcore.NewInMemoryConfigManager()
	// Intentionally no config for "unknown-tenant".

	h := buildChain(
		resolve.Options{},
		policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(cfg)},
		quota.Options{},
		ratelimit.Options{},
		okHandler(),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(authn.WithPrincipal(req.Context(), &authn.Principal{TenantID: "unknown-tenant"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unknown tenant, got %d", rec.Code)
	}

	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != tenanttransport.CodePolicyDenied {
		t.Errorf("code = %q, want %q", body.Error.Code, tenanttransport.CodePolicyDenied)
	}
}

// TestChain_ExtractorErrorTreatedAsMissing verifies that a custom extractor
// returning an error causes the resolve layer to fail closed with 401, and that
// the downstream policy/quota/ratelimit layers are never reached.
func TestChain_ExtractorErrorTreatedAsMissing(t *testing.T) {
	sentinel := errors.New("identity provider unavailable")
	failExtractor := func(*http.Request) (string, error) {
		return "", sentinel
	}

	handlerCalled := false
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	h := buildChain(
		resolve.Options{Extractor: failExtractor, DisablePrincipal: true},
		policy.Options{},
		quota.Options{},
		ratelimit.Options{},
		final,
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if handlerCalled {
		t.Fatal("handler must not be reached when extractor fails")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 on extractor error, got %d", rec.Code)
	}

	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != tenanttransport.CodeRequired {
		t.Errorf("code = %q, want %q", body.Error.Code, tenanttransport.CodeRequired)
	}
}

// TestChain_InvalidTenantIDFormat verifies that a malformed tenant ID in the
// request header is rejected at the resolve layer with 400, before any policy
// or quota check runs.
func TestChain_InvalidTenantIDFormat(t *testing.T) {
	cfg := newFullConfig()

	handlerCalled := false
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	h := buildChain(
		resolve.Options{DisablePrincipal: true},
		policy.Options{Evaluator: tenantcore.NewConfigPolicyEvaluator(cfg)},
		quota.Options{Manager: tenantcore.NewFixedWindowQuotaManager(cfg)},
		ratelimit.Options{},
		final,
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "bad tenant/id") // contains space and slash — invalid
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if handlerCalled {
		t.Fatal("handler must not be reached for invalid tenant ID format")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid tenant ID format, got %d", rec.Code)
	}

	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != tenanttransport.CodeInvalidID {
		t.Errorf("code = %q, want %q", body.Error.Code, tenanttransport.CodeInvalidID)
	}
}
