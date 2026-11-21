package tenant

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateTenantID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr error
	}{
		{"valid alphanumeric", "tenant-123", nil},
		{"valid with dots", "org.team.svc", nil},
		{"valid with underscores", "my_tenant_1", nil},
		{"valid uuid-like", "550e8400-e29b-41d4-a716-446655440000", nil},
		{"empty", "", ErrInvalidTenantID},
		{"too long", strings.Repeat("a", 256), ErrInvalidTenantID},
		{"max length", strings.Repeat("a", 255), nil},
		{"contains space", "tenant 123", ErrInvalidTenantID},
		{"contains slash", "tenant/123", ErrInvalidTenantID},
		{"contains newline", "tenant\n123", ErrInvalidTenantID},
		{"contains null byte", "tenant\x00123", ErrInvalidTenantID},
		{"contains angle bracket", "tenant<script>", ErrInvalidTenantID},
		{"contains colon", "tenant:123", ErrInvalidTenantID},
		{"unicode characters", "tenant-日本語", ErrInvalidTenantID},
		{"emoji", "tenant-😀", ErrInvalidTenantID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTenantID(tt.id)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("ValidateTenantID(%q) = %v, want nil", tt.id, err)
				}
				return
			}
			if err == nil {
				t.Errorf("ValidateTenantID(%q) = nil, want %v", tt.id, tt.wantErr)
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateTenantID(%q) = %v, want %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestFromHeader(t *testing.T) {
	extractor := FromHeader("X-Custom-Tenant")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Custom-Tenant", "my-tenant")

	id, err := extractor(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "my-tenant" {
		t.Errorf("expected 'my-tenant', got %q", id)
	}
}

func TestFromHeader_Missing(t *testing.T) {
	extractor := FromHeader("X-Custom-Tenant")
	req := httptest.NewRequest("GET", "/", nil)

	_, err := extractor(req)
	if err != ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestFromQuery(t *testing.T) {
	extractor := FromQuery("tid")
	req := httptest.NewRequest("GET", "/?tid=q-tenant", nil)

	id, err := extractor(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "q-tenant" {
		t.Errorf("expected 'q-tenant', got %q", id)
	}
}

func TestFromCookie(t *testing.T) {
	extractor := FromCookie("tenant_cookie")
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "tenant_cookie", Value: "cookie-tenant"})

	id, err := extractor(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "cookie-tenant" {
		t.Errorf("expected 'cookie-tenant', got %q", id)
	}
}

func TestFromCookie_Missing(t *testing.T) {
	extractor := FromCookie("tenant_cookie")
	req := httptest.NewRequest("GET", "/", nil)

	_, err := extractor(req)
	if err != ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestFromSubdomain(t *testing.T) {
	extractor := FromSubdomain()

	tests := []struct {
		name     string
		host     string
		expected string
		wantErr  bool
	}{
		{"standard", "acme.example.com", "acme", false},
		{"with port", "acme.example.com:8080", "acme", false},
		{"no subdomain", "localhost", "", true},
		{"bare domain no dot", "example", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host

			id, err := extractor(req)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, id)
			}
		})
	}
}

type testContextKey struct{}

func TestFromContextValue(t *testing.T) {
	extractor := FromContextValue(testContextKey{}, func(v any) string {
		claims, ok := v.(map[string]string)
		if !ok {
			return ""
		}
		return claims["tenant_id"]
	})

	ctx := context.WithValue(t.Context(), testContextKey{}, map[string]string{
		"tenant_id": "jwt-tenant",
	})
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)

	id, err := extractor(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "jwt-tenant" {
		t.Errorf("expected 'jwt-tenant', got %q", id)
	}

	req2 := httptest.NewRequest("GET", "/", nil)
	_, err = extractor(req2)
	if err != ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestChain(t *testing.T) {
	extractor := Chain(
		FromHeader("X-Tenant-ID"),
		FromQuery("tenant_id"),
	)

	req1 := httptest.NewRequest("GET", "/", nil)
	req1.Header.Set("X-Tenant-ID", "header-tenant")
	id, err := extractor(req1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "header-tenant" {
		t.Errorf("expected 'header-tenant', got %q", id)
	}

	req2 := httptest.NewRequest("GET", "/?tenant_id=query-tenant", nil)
	id, err = extractor(req2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "query-tenant" {
		t.Errorf("expected 'query-tenant', got %q", id)
	}

	req3 := httptest.NewRequest("GET", "/", nil)
	_, err = extractor(req3)
	if err != ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestHooksForwardConfiguredCallbacks(t *testing.T) {
	ctx := t.Context()
	resolve := ResolveInfo{TenantID: "tenant", Source: "extractor"}
	policy := PolicyDecision{TenantID: "tenant", Allowed: true}
	quota := QuotaDecision{TenantID: "tenant", RemainingTokens: 10}
	rate := RateLimitDecision{TenantID: "tenant", Limit: 10}
	calls := map[string]int{}
	check := func(name string, in context.Context, matched bool) {
		calls[name]++
		if in != ctx || !matched {
			t.Errorf("%s hook argument or context changed", name)
		}
	}
	h := Hooks{
		OnResolve:   func(in context.Context, got ResolveInfo) { check("resolve", in, got == resolve) },
		OnPolicy:    func(in context.Context, got PolicyDecision) { check("policy", in, got == policy) },
		OnQuota:     func(in context.Context, got QuotaDecision) { check("quota", in, got == quota) },
		OnRateLimit: func(in context.Context, got RateLimitDecision) { check("rate", in, got == rate) },
	}
	for _, hooks := range []Hooks{{}, h} {
		hooks.Resolve(ctx, resolve)
		hooks.Policy(ctx, policy)
		hooks.Quota(ctx, quota)
		hooks.RateLimit(ctx, rate)
	}
	for _, name := range []string{"resolve", "policy", "quota", "rate"} {
		if calls[name] != 1 {
			t.Errorf("%s calls=%d", name, calls[name])
		}
	}
}
