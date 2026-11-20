package tenant

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryRoutePolicyStore(t *testing.T) {
	store := NewInMemoryRoutePolicyStore()
	ctx := t.Context()

	_, err := store.RoutePolicy(ctx, "missing")
	if err != ErrRoutePolicyNotFound {
		t.Fatalf("expected ErrRoutePolicyNotFound, got %v", err)
	}

	policy := RoutePolicy{
		TenantID: "t-1",
		Strategy: "weighted",
		Payload:  []byte(`{"rules":[]}`),
	}
	if err := store.SetRoutePolicy(ctx, policy); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	got, err := store.RoutePolicy(ctx, "t-1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.TenantID != "t-1" || got.Strategy != "weighted" {
		t.Fatalf("unexpected policy: %+v", got)
	}
}

func TestCachedRoutePolicyProvider(t *testing.T) {
	ctx := t.Context()
	provider := &testRoutePolicyProvider{}
	cache := NewInMemoryRoutePolicyCache(10, 50*time.Millisecond)
	cached := NewCachedRoutePolicyProvider(provider, cache)

	_, err := cached.RoutePolicy(ctx, "t-1")
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	_, err = cached.RoutePolicy(ctx, "t-1")
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("expected 1 provider call, got %d", provider.calls)
	}

	if err := cached.Invalidate(ctx, "t-1"); err != nil {
		t.Fatalf("invalidate failed: %v", err)
	}
	_, _ = cached.RoutePolicy(ctx, "t-1")
	if provider.calls != 2 {
		t.Fatalf("expected 2 provider calls after invalidate, got %d", provider.calls)
	}

	time.Sleep(60 * time.Millisecond)
	_, _ = cached.RoutePolicy(ctx, "t-1")
	if provider.calls != 3 {
		t.Fatalf("expected cache refresh after ttl, got %d", provider.calls)
	}
}

func TestInMemoryRoutePolicyCache_ExpiredReadLeavesEntryForEviction(t *testing.T) {
	ctx := t.Context()
	cache := NewInMemoryRoutePolicyCache(1, time.Hour)
	cache.entries["tenant"] = routePolicyCacheEntry{
		policy:    RoutePolicy{Strategy: "old"},
		expiresAt: time.Now().Add(-time.Hour),
	}
	if _, ok := cache.Get(ctx, "tenant"); ok {
		t.Fatal("expired policy was returned")
	}
	if _, ok := cache.entries["tenant"]; !ok {
		t.Fatal("Get removed an expired entry")
	}
	if err := cache.Set(ctx, "tenant", RoutePolicy{Strategy: "new"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := cache.Get(ctx, "tenant"); !ok || got.Strategy != "new" {
		t.Fatalf("refreshed policy = %+v, found = %v", got, ok)
	}
	if err := cache.Set(ctx, "other", RoutePolicy{Strategy: "new"}); err != nil {
		t.Fatal(err)
	}
	if got := len(cache.entries); got != 1 {
		t.Errorf("cache size = %d, want 1", got)
	}
}

func TestInMemoryRoutePolicyCache_EvictExpiredFirst(t *testing.T) {
	ctx := t.Context()
	maxSize := 3
	// Use a tiny TTL so the pre-filled entries expire immediately.
	cache := NewInMemoryRoutePolicyCache(maxSize, 1*time.Millisecond)
	policy := RoutePolicy{TenantID: "x", Strategy: "weighted"}

	for i := 0; i < maxSize; i++ {
		id := string(rune('a' + i))
		_ = cache.Set(ctx, id, RoutePolicy{TenantID: id})
	}

	// Wait for all entries to expire.
	time.Sleep(5 * time.Millisecond)

	// Adding a new entry should evict an expired one, not fail.
	if err := cache.Set(ctx, "new", policy); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	cache.mu.RLock()
	size := len(cache.entries)
	_, hasNew := cache.entries["new"]
	cache.mu.RUnlock()

	if !hasNew {
		t.Error("new entry should be present after eviction")
	}
	if size > maxSize {
		t.Errorf("cache size %d exceeds maxSize %d", size, maxSize)
	}
}

func TestInMemoryRoutePolicyCache_EvictArbitraryWhenNoneExpired(t *testing.T) {
	ctx := t.Context()
	maxSize := 3
	cache := NewInMemoryRoutePolicyCache(maxSize, 1*time.Hour)

	for i := 0; i < maxSize; i++ {
		id := string(rune('a' + i))
		_ = cache.Set(ctx, id, RoutePolicy{TenantID: id})
	}

	if err := cache.Set(ctx, "new", RoutePolicy{TenantID: "new"}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	cache.mu.RLock()
	size := len(cache.entries)
	_, hasNew := cache.entries["new"]
	cache.mu.RUnlock()

	if !hasNew {
		t.Error("new entry should be present")
	}
	if size > maxSize {
		t.Errorf("cache size %d exceeds maxSize %d", size, maxSize)
	}
}

type testRoutePolicyProvider struct {
	calls int
}

func (p *testRoutePolicyProvider) RoutePolicy(ctx context.Context, tenantID string) (RoutePolicy, error) {
	p.calls++
	return RoutePolicy{
		TenantID:  tenantID,
		Strategy:  "weighted",
		Payload:   []byte(`{"rules":[]}`),
		UpdatedAt: time.Now().UTC(),
	}, nil
}

func TestInMemoryRoutePolicySnapshots(t *testing.T) {
	for _, name := range []string{"store", "cache"} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			const tenantID = "sample"
			var save func(RoutePolicy) error
			var read func() RoutePolicy
			if name == "store" {
				store := NewInMemoryRoutePolicyStore()
				save = func(policy RoutePolicy) error { return store.SetRoutePolicy(ctx, policy) }
				read = func() RoutePolicy {
					policy, err := store.RoutePolicy(ctx, tenantID)
					if err != nil {
						t.Fatal(err)
					}
					return policy
				}
			} else {
				cache := NewInMemoryRoutePolicyCache(2, time.Hour)
				save = func(policy RoutePolicy) error { return cache.Set(ctx, tenantID, policy) }
				read = func() RoutePolicy {
					policy, ok := cache.Get(ctx, tenantID)
					if !ok {
						t.Fatal("cached policy missing")
					}
					return policy
				}
			}

			if err := save(RoutePolicy{TenantID: tenantID}); err != nil {
				t.Fatal(err)
			}
			if got := read(); got.Payload != nil || got.Metadata != nil {
				t.Errorf("nil values were replaced: %+v", got)
			}
			empty := RoutePolicy{TenantID: tenantID, Payload: []byte{}, Metadata: map[string]string{}}
			if err := save(empty); err != nil {
				t.Fatal(err)
			}
			if got := read(); got.Payload == nil || len(got.Payload) != 0 || got.Metadata == nil || len(got.Metadata) != 0 {
				t.Errorf("empty values were replaced: %+v", got)
			}

			input := RoutePolicy{
				TenantID:  tenantID,
				Strategy:  "weighted",
				UpdatedAt: time.Now().UTC(),
				Payload:   []byte("original"),
				Metadata:  map[string]string{"key": "value"},
			}
			if err := save(input); err != nil {
				t.Fatal(err)
			}
			input.Payload[0] = 'x'
			input.Metadata["key"] = "changed"
			first := read()
			if first.TenantID != tenantID || first.Strategy != input.Strategy || first.UpdatedAt != input.UpdatedAt ||
				string(first.Payload) != "original" || first.Metadata["key"] != "value" {
				t.Fatalf("unexpected policy after input mutation: %+v", first)
			}
			first.Payload[0] = 'y'
			first.Metadata["key"] = "changed again"
			if got := read(); string(got.Payload) != "original" || got.Metadata["key"] != "value" {
				t.Errorf("unexpected policy after result mutation: %+v", got)
			}
		})
	}
}
