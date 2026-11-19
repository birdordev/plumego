package tenant

import (
	"testing"
	"time"
)

func TestInMemoryRateLimitManager(t *testing.T) {
	provider := NewInMemoryRateLimitManager()
	provider.SetRateLimit("t-1", RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             20,
	})

	cfg, err := provider.RateLimitConfig(t.Context(), "t-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.RequestsPerSecond != 10 || cfg.Burst != 20 {
		t.Fatalf("unexpected config: %+v", cfg)
	}

	// Unknown tenant returns ErrTenantNotFound.
	_, err = provider.RateLimitConfig(t.Context(), "missing")
	if err != ErrTenantNotFound {
		t.Fatalf("expected ErrTenantNotFound for unconfigured tenant, got %v", err)
	}
}

func TestTokenBucketRateLimiter_UnknownTenantAllowed(t *testing.T) {
	// Unknown tenants (ErrTenantNotFound from provider) are allowed — treated as unlimited.
	provider := NewInMemoryRateLimitManager()
	limiter := NewTokenBucketRateLimiter(provider)
	res, err := limiter.Allow(t.Context(), "unknown-tenant", RateLimitRequest{})
	if err != nil || !res.Allowed {
		t.Fatalf("expected unknown tenant to be allowed, got err=%v res=%+v", err, res)
	}
}

func TestTokenBucketRateLimiter_AllowAndRefill(t *testing.T) {
	provider := NewInMemoryRateLimitManager()
	provider.SetRateLimit("t-1", RateLimitConfig{
		RequestsPerSecond: 2,
		Burst:             2,
	})

	limiter := NewTokenBucketRateLimiter(provider)
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	res, err := limiter.Allow(t.Context(), "t-1", RateLimitRequest{
		Tokens: 1,
		Now:    now,
	})
	if err != nil || !res.Allowed {
		t.Fatalf("expected first request allowed, got err=%v res=%+v", err, res)
	}

	res, err = limiter.Allow(t.Context(), "t-1", RateLimitRequest{
		Tokens: 1,
		Now:    now,
	})
	if err != nil || !res.Allowed {
		t.Fatalf("expected second request allowed, got err=%v res=%+v", err, res)
	}
	if res.Remaining != 0 {
		t.Fatalf("expected remaining 0, got %d", res.Remaining)
	}

	res, err = limiter.Allow(t.Context(), "t-1", RateLimitRequest{
		Tokens: 1,
		Now:    now,
	})
	if err != ErrRateLimitExceeded || res.Allowed {
		t.Fatalf("expected rate limited, got err=%v res=%+v", err, res)
	}
	if res.RetryAfter <= 0 {
		t.Fatalf("expected retry after > 0, got %v", res.RetryAfter)
	}

	halfSecond := now.Add(500 * time.Millisecond)
	res, err = limiter.Allow(t.Context(), "t-1", RateLimitRequest{
		Tokens: 1,
		Now:    halfSecond,
	})
	if err != nil || !res.Allowed {
		t.Fatalf("expected refill allow, got err=%v res=%+v", err, res)
	}
}

func TestTokenBucketRateLimiter_Unlimited(t *testing.T) {
	provider := NewInMemoryRateLimitManager()
	provider.SetRateLimit("t-1", RateLimitConfig{})

	limiter := NewTokenBucketRateLimiter(provider)
	res, err := limiter.Allow(t.Context(), "t-1", RateLimitRequest{})
	if err != nil || !res.Allowed {
		t.Fatalf("expected allowed for unlimited config, got err=%v res=%+v", err, res)
	}
}

func TestTenantRateLimitRefillsBeforeReconfiguration(t *testing.T) {
	const id = "fixture"
	provider := NewInMemoryRateLimitManager()
	limiter := NewTokenBucketRateLimiter(provider)
	start := time.Unix(100, 0)
	for _, tc := range []struct {
		name                string
		at                  time.Duration
		rate, burst, tokens int64
		remaining           int64
		allowed             bool
	}{
		{"drain initial bucket", 0, 1, 10, 10, 0, true},
		{"increase uses old rate for elapsed time", time.Second, 4, 10, 1, 0, true},
		{"same time adds nothing", time.Second, 4, 10, 1, 0, false},
		{"new rate applies in the next interval", 2 * time.Second, 4, 10, 1, 3, true},
		{"decrease uses old rate for elapsed time", 3 * time.Second, 2, 10, 1, 6, true},
		{"shrink caps the settled balance", 4 * time.Second, 2, 3, 1, 2, true},
		{"past request does not move refill point", 3 * time.Second, 2, 3, 3, 2, false},
		{"same time consumes without refill", 4 * time.Second, 2, 3, 2, 0, true},
		{"new rate after decrease", 5 * time.Second, 2, 3, 2, 0, true},
	} {
		provider.SetRateLimit(id, RateLimitConfig{RequestsPerSecond: tc.rate, Burst: tc.burst})
		result, err := limiter.Allow(t.Context(), id, RateLimitRequest{Now: start.Add(tc.at), Tokens: tc.tokens})
		wantErr := error(nil)
		if !tc.allowed {
			wantErr = ErrRateLimitExceeded
		}
		if result.Allowed != tc.allowed || result.Remaining != tc.remaining || err != wantErr ||
			result.Limit != tc.rate || result.Burst != tc.burst {
			t.Errorf("%s: got %+v, %v; want allowed=%v remaining=%d limit=%d burst=%d err=%v", tc.name, result, err, tc.allowed, tc.remaining, tc.rate, tc.burst, wantErr)
		}
	}
}
