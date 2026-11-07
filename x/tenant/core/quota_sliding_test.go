package tenant

import (
	"errors"
	"testing"
	"time"
)

func TestSlidingWindowQuotaManager_UnsupportedWindow(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{
				{Window: QuotaWindowHour, Requests: 1000},
			},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()

	result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Now:      time.Now().UTC(),
	})
	if !errors.Is(err, ErrUnsupportedQuotaWindow) {
		t.Fatalf("expected ErrUnsupportedQuotaWindow for hour window, got %v", err)
	}
	if result.Allowed {
		t.Error("expected request to be denied when window is unsupported")
	}
}

func TestSlidingWindowQuotaManager_Basic(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 5, Tokens: 100}},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()
	now := time.Now()

	// First request should be allowed
	result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Tokens:   10,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected first request to be allowed")
	}
	if result.RemainingRequests != 4 {
		t.Errorf("expected 4 remaining requests, got %d", result.RemainingRequests)
	}
	if result.RemainingTokens != 90 {
		t.Errorf("expected 90 remaining tokens, got %d", result.RemainingTokens)
	}
}

func TestSlidingWindowQuotaManager_RequestLimit(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 3, Tokens: 1000}},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()
	now := time.Now()

	// Allow 3 requests
	for i := 0; i < 3; i++ {
		result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
			Requests: 1,
			Tokens:   10,
			Now:      now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("unexpected error on request %d: %v", i+1, err)
		}
		if !result.Allowed {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Tokens:   10,
		Now:      now.Add(3 * time.Second),
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if result.Allowed {
		t.Error("expected 4th request to be denied")
	}
	if result.RetryAfter <= 0 {
		t.Errorf("expected positive RetryAfter, got %v", result.RetryAfter)
	}
}

func TestSlidingWindowQuotaManager_TokenLimit(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 100, Tokens: 50}},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()
	now := time.Now()

	// First request: 30 tokens
	result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Tokens:   30,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected first request to be allowed")
	}

	// Second request: 25 tokens - should be denied (30 + 25 > 50)
	result, err = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Tokens:   25,
		Now:      now.Add(time.Second),
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if result.Allowed {
		t.Error("expected second request to be denied (token limit)")
	}
	if result.RemainingTokens != 20 {
		t.Errorf("expected 20 remaining tokens, got %d", result.RemainingTokens)
	}
}

func TestSlidingWindowQuotaManager_SlidingBehavior(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 2, Tokens: 100}},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()
	now := time.Now()

	// Request 1 at T=0
	result, _ := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Now:      now,
	})
	if !result.Allowed {
		t.Error("request 1 should be allowed")
	}

	// Request 2 at T=30s
	result, _ = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Now:      now.Add(30 * time.Second),
	})
	if !result.Allowed {
		t.Error("request 2 should be allowed")
	}

	// Request 3 at T=40s - should be denied (requests at T=0 and T=30 still in window)
	result, _ = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Now:      now.Add(40 * time.Second),
	})
	if result.Allowed {
		t.Error("request 3 should be denied (within sliding window)")
	}

	// Request 4 at T=61s - should be allowed (request at T=0 expired)
	result, _ = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Now:      now.Add(61 * time.Second),
	})
	if !result.Allowed {
		t.Error("request 4 should be allowed (old request expired)")
	}
}

func TestSlidingWindowQuotaManager_WindowCleanup(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 5, Tokens: 100}},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()
	now := time.Now()

	// Make 5 requests
	for i := 0; i < 5; i++ {
		_, _ = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
			Requests: 1,
			Tokens:   10,
			Now:      now,
		})
	}

	// After 61 seconds, all old requests should be cleaned up
	result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Tokens:   10,
		Now:      now.Add(61 * time.Second),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("request should be allowed after window expired")
	}

	// Should have full quota available again
	if result.RemainingRequests != 4 {
		t.Errorf("expected 4 remaining requests after cleanup, got %d", result.RemainingRequests)
	}
}

func TestSlidingWindowQuotaManager_ZeroQuota(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota:    QuotaConfig{}, // no limits = unlimited
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()
	now := time.Now()

	// Should allow any number of requests when quota is unset (unlimited)
	for i := 0; i < 100; i++ {
		result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
			Requests: 1,
			Tokens:   100,
			Now:      now.Add(time.Duration(i) * time.Millisecond),
		})
		if err != nil {
			t.Fatalf("unexpected error on request %d: %v", i+1, err)
		}
		if !result.Allowed {
			t.Errorf("request %d should be allowed (unlimited quota)", i+1)
		}
	}
}

func TestSlidingWindowQuotaManager_MultipleRequests(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 10, Tokens: 100}},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()
	now := time.Now()

	// Request batch of 5
	result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 5,
		Tokens:   50,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("batch request should be allowed")
	}
	if result.RemainingRequests != 5 {
		t.Errorf("expected 5 remaining requests, got %d", result.RemainingRequests)
	}

	// Another batch of 6 should be denied
	result, err = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 6,
		Tokens:   10,
		Now:      now.Add(time.Second),
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if result.Allowed {
		t.Error("second batch should be denied (exceeds quota)")
	}
}

func TestSlidingWindowQuotaManager_ConcurrentAccess(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 100, Tokens: 1000}},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()

	// Concurrent requests
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 5; j++ {
				_, _ = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
					Requests: 1,
					Tokens:   10,
					Now:      time.Now(),
				})
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify quota is still enforced
	result, err := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Tokens:   10,
		Now:      time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have consumed 50 requests (10 goroutines * 5 requests)
	if result.RemainingRequests > 50 {
		t.Errorf("expected consumed requests, got %d remaining", result.RemainingRequests)
	}
}

func TestSlidingWindowQuotaManager_RetryAfterAccuracy(t *testing.T) {
	cfgMgr := NewInMemoryConfigManager()
	cfgMgr.SetTenantConfig(Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 2, Tokens: 100}},
		},
	})

	quotaMgr := NewSlidingWindowQuotaManager(cfgMgr)
	ctx := t.Context()
	now := time.Now()

	// Request 1 at T=0
	_, _ = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Now:      now,
	})

	// Request 2 at T=10s
	_, _ = quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Now:      now.Add(10 * time.Second),
	})

	// Request 3 at T=20s - denied
	result, _ := quotaMgr.Allow(ctx, "test-tenant", QuotaRequest{
		Requests: 1,
		Now:      now.Add(20 * time.Second),
	})

	if result.Allowed {
		t.Error("request should be denied")
	}

	// RetryAfter should be approximately 40 seconds (60s - 20s from oldest request)
	expectedRetryAfter := 40 * time.Second
	tolerance := 2 * time.Second

	if result.RetryAfter < expectedRetryAfter-tolerance || result.RetryAfter > expectedRetryAfter+tolerance {
		t.Errorf("expected RetryAfter ~%v, got %v", expectedRetryAfter, result.RetryAfter)
	}
}
