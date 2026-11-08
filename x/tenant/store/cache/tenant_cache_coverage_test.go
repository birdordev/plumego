package cache

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	storecache "github.com/birdordev/plumego/store/cache"
	tenant "github.com/birdordev/plumego/x/tenant/core"
)

// minimalCache implements only storecache.Cache (no Incr/Decr/Append).
type minimalCache struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMinimalCache() *minimalCache {
	return &minimalCache{data: make(map[string][]byte)}
}

func (m *minimalCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	if !ok {
		return nil, storecache.ErrNotFound
	}
	return v, nil
}

func (m *minimalCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *minimalCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *minimalCache) Exists(_ context.Context, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[key]
	return ok, nil
}

func (m *minimalCache) Clear(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make(map[string][]byte)
	return nil
}

// ---- Tenant ID validation ----

func TestTenantCache_TenantIDWithSeparatorRejected(t *testing.T) {
	tc := NewTenantCache(newMinimalCache(), WithSeparator(":"))

	// "t:1" contains the default separator ':'.
	ctx := tenant.WithTenantID(t.Context(), "t:1")

	if err := tc.Set(ctx, "key", []byte("v"), time.Minute); err == nil {
		t.Fatal("expected error for tenant ID containing separator, got nil")
	}
	if _, err := tc.Get(ctx, "key"); err == nil {
		t.Fatal("Get: expected error for tenant ID containing separator")
	}
}

func TestTenantCache_TenantIDWithControlCharRejected(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())

	// Tenant ID with a null byte (control character < 0x20).
	badID := "bad\x00tenant"
	ctx := tenant.WithTenantID(t.Context(), badID)

	if err := tc.Set(ctx, "k", []byte("v"), time.Minute); err == nil {
		t.Fatal("expected error for tenant ID with control char, got nil")
	}
}

func TestTenantCache_TenantIDWithTabRejected(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())

	ctx := tenant.WithTenantID(t.Context(), "tenant\t1")
	if err := tc.Set(ctx, "k", []byte("v"), time.Minute); err == nil {
		t.Fatal("expected error for tenant ID with tab (0x09 < 0x20), got nil")
	}
}

func TestTenantCache_TenantIDWithDELRejected(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())

	ctx := tenant.WithTenantID(t.Context(), "tenant\x7ftail")
	if err := tc.Set(ctx, "k", []byte("v"), time.Minute); err == nil {
		t.Fatal("expected error for tenant ID with DEL (0x7F), got nil")
	}
}

// ---- Empty key prefix ----

func TestTenantCache_EmptyKeyPrefix(t *testing.T) {
	underlying := newMinimalCache()
	tc := NewTenantCache(underlying, WithKeyPrefix(""))

	ctx := tenant.WithTenantID(t.Context(), "t1")
	if err := tc.Set(ctx, "mykey", []byte("val"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// With empty prefix the key should be "t1:mykey".
	v, err := underlying.Get(t.Context(), "t1:mykey")
	if err != nil {
		t.Fatalf("underlying Get: %v", err)
	}
	if string(v) != "val" {
		t.Fatalf("got %q, want val", v)
	}
}

// ---- Capability checks (Incr / Decr / Append on non-supporting cache) ----

func TestTenantCache_IncrOnNonCounterCacheReturnsUnsupported(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())
	ctx := tenant.WithTenantID(t.Context(), "t1")

	_, err := tc.Incr(ctx, "counter", 1)
	if !errors.Is(err, storecache.ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported, got %v", err)
	}
}

func TestTenantCache_DecrOnNonCounterCacheReturnsUnsupported(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())
	ctx := tenant.WithTenantID(t.Context(), "t1")

	_, err := tc.Decr(ctx, "counter", 1)
	if !errors.Is(err, storecache.ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported, got %v", err)
	}
}

func TestTenantCache_AppendOnNonAppenderCacheReturnsUnsupported(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())
	ctx := tenant.WithTenantID(t.Context(), "t1")

	err := tc.Append(ctx, "data", []byte("chunk"))
	if !errors.Is(err, storecache.ErrCapabilityUnsupported) {
		t.Fatalf("expected ErrCapabilityUnsupported, got %v", err)
	}
}

// ---- Clear with invalid tenant ID ----

func TestTenantCache_ClearWithSeparatorInTenantIDRejected(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())
	ctx := tenant.WithTenantID(t.Context(), "bad:tenant")

	err := tc.Clear(ctx)
	if err == nil {
		t.Fatal("expected error for separator in tenant ID, got nil")
	}
	if errors.Is(err, storecache.ErrCapabilityUnsupported) {
		t.Fatal("should have failed on validation before reaching ErrCapabilityUnsupported")
	}
}

// ---- Concurrent access from multiple tenants ----

func TestTenantCache_ConcurrentMultiTenantAccess(t *testing.T) {
	tc := NewTenantCache(storecache.NewMemoryCache())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		tenantID := "tenant-" + string(rune('a'+i%10))
		go func(id string) {
			defer wg.Done()
			ctx := tenant.WithTenantID(context.Background(), id)
			for j := 0; j < 5; j++ {
				_ = tc.Set(ctx, "key", []byte("value"), time.Minute)
				_, _ = tc.Get(ctx, "key")
			}
		}(tenantID)
	}
	wg.Wait()
}

// ---- Delete with invalid tenant ID ----

func TestTenantCache_DeleteWithNoTenantID(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())
	err := tc.Delete(t.Context(), "k")
	if !errors.Is(err, tenant.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}

// ---- Exists with invalid tenant ID ----

func TestTenantCache_ExistsWithNoTenantID(t *testing.T) {
	tc := NewTenantCache(newMinimalCache())
	_, err := tc.Exists(t.Context(), "k")
	if !errors.Is(err, tenant.ErrTenantNotFound) {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}

// ---- Custom separator changes key format ----

func TestTenantCache_CustomSeparatorKeyFormat(t *testing.T) {
	underlying := newMinimalCache()
	tc := NewTenantCache(underlying, WithKeyPrefix("ns"), WithSeparator("/"))

	ctx := tenant.WithTenantID(t.Context(), "acme")
	if err := tc.Set(ctx, "config", []byte("cfg"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Key should be "ns/acme/config".
	v, err := underlying.Get(t.Context(), "ns/acme/config")
	if err != nil {
		t.Fatalf("underlying Get with custom separator: %v", err)
	}
	if string(v) != "cfg" {
		t.Fatalf("got %q, want cfg", v)
	}
}
