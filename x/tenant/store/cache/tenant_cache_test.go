package cache

import (
	"testing"
	"time"

	storecache "github.com/birdordev/plumego/store/cache"
	"github.com/birdordev/plumego/x/tenant/core"
)

func TestTenantCache_BasicOperations(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx1 := tenant.WithTenantID(t.Context(), "tenant-1")
	ctx2 := tenant.WithTenantID(t.Context(), "tenant-2")

	err := tenantCache.Set(ctx1, "key1", []byte("value1"), time.Minute)
	if err != nil {
		t.Fatalf("unexpected error setting key: %v", err)
	}

	val, err := tenantCache.Get(ctx1, "key1")
	if err != nil {
		t.Fatalf("unexpected error getting key: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("expected 'value1', got '%s'", string(val))
	}

	_, err = tenantCache.Get(ctx2, "key1")
	if err != storecache.ErrNotFound {
		t.Errorf("expected ErrNotFound for tenant 2, got %v", err)
	}

	err = tenantCache.Set(ctx2, "key1", []byte("value2"), time.Minute)
	if err != nil {
		t.Fatalf("unexpected error setting key for tenant 2: %v", err)
	}

	val, err = tenantCache.Get(ctx1, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("tenant 1 value changed unexpectedly: got '%s'", string(val))
	}

	val, err = tenantCache.Get(ctx2, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "value2" {
		t.Errorf("expected 'value2' for tenant 2, got '%s'", string(val))
	}
}

func TestTenantCache_NoTenantID(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx := t.Context()

	err := tenantCache.Set(ctx, "key1", []byte("value1"), time.Minute)
	if err != tenant.ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}

	_, err = tenantCache.Get(ctx, "key1")
	if err != tenant.ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound for Get, got %v", err)
	}
}

func TestTenantCache_KeyPrefix(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache, WithKeyPrefix("app"), WithSeparator(":"))

	ctx := tenant.WithTenantID(t.Context(), "test-tenant")

	err := tenantCache.Set(ctx, "mykey", []byte("myvalue"), time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := cache.Get(t.Context(), "app:test-tenant:mykey")
	if err != nil {
		t.Fatalf("key not found with expected prefix: %v", err)
	}
	if string(val) != "myvalue" {
		t.Errorf("expected 'myvalue', got '%s'", string(val))
	}
}

func TestTenantCache_Delete(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx := tenant.WithTenantID(t.Context(), "tenant-1")

	tenantCache.Set(ctx, "key1", []byte("value1"), time.Minute)
	val, _ := tenantCache.Get(ctx, "key1")
	if string(val) != "value1" {
		t.Fatal("value not set correctly")
	}

	err := tenantCache.Delete(ctx, "key1")
	if err != nil {
		t.Fatalf("unexpected error deleting: %v", err)
	}

	_, err = tenantCache.Get(ctx, "key1")
	if err != storecache.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestTenantCache_Exists(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx1 := tenant.WithTenantID(t.Context(), "tenant-1")
	ctx2 := tenant.WithTenantID(t.Context(), "tenant-2")

	exists, err := tenantCache.Exists(ctx1, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("key should not exist initially")
	}

	tenantCache.Set(ctx1, "key1", []byte("value1"), time.Minute)

	exists, err = tenantCache.Exists(ctx1, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("key should exist for tenant 1")
	}

	exists, err = tenantCache.Exists(ctx2, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("key should not exist for tenant 2")
	}
}

func TestTenantCache_ClearUnsupportedDoesNotClearOtherTenants(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx1 := tenant.WithTenantID(t.Context(), "tenant-1")
	ctx2 := tenant.WithTenantID(t.Context(), "tenant-2")

	if err := tenantCache.Set(ctx1, "config", []byte("value-1"), time.Minute); err != nil {
		t.Fatalf("unexpected error setting tenant 1 value: %v", err)
	}
	if err := tenantCache.Set(ctx2, "config", []byte("value-2"), time.Minute); err != nil {
		t.Fatalf("unexpected error setting tenant 2 value: %v", err)
	}

	if err := tenantCache.Clear(ctx1); err != storecache.ErrCapabilityUnsupported {
		t.Fatalf("expected ErrCapabilityUnsupported, got %v", err)
	}

	val, err := tenantCache.Get(ctx1, "config")
	if err != nil {
		t.Fatalf("tenant 1 value should remain after unsupported clear: %v", err)
	}
	if string(val) != "value-1" {
		t.Fatalf("tenant 1 value changed after clear: %q", string(val))
	}

	val, err = tenantCache.Get(ctx2, "config")
	if err != nil {
		t.Fatalf("tenant 2 value should remain after unsupported clear: %v", err)
	}
	if string(val) != "value-2" {
		t.Fatalf("tenant 2 value changed after clear: %q", string(val))
	}
}

func TestTenantCache_ClearRequiresTenant(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	if err := tenantCache.Clear(t.Context()); err != tenant.ErrTenantNotFound {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestTenantCache_Incr(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx1 := tenant.WithTenantID(t.Context(), "tenant-1")
	ctx2 := tenant.WithTenantID(t.Context(), "tenant-2")

	val, err := tenantCache.Incr(ctx1, "counter", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 5 {
		t.Errorf("expected 5, got %d", val)
	}

	val, err = tenantCache.Incr(ctx1, "counter", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 8 {
		t.Errorf("expected 8, got %d", val)
	}

	val, err = tenantCache.Incr(ctx2, "counter", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 10 {
		t.Errorf("expected 10 for tenant 2, got %d", val)
	}

	val, err = tenantCache.Incr(ctx1, "counter", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 8 {
		t.Errorf("expected tenant 1 counter to still be 8, got %d", val)
	}
}

func TestTenantCache_Decr(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx := tenant.WithTenantID(t.Context(), "tenant-1")

	_, _ = tenantCache.Incr(ctx, "counter", 100)

	val, err := tenantCache.Decr(ctx, "counter", 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 70 {
		t.Errorf("expected 70, got %d", val)
	}
}

func TestTenantCache_Append(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx := tenant.WithTenantID(t.Context(), "tenant-1")

	err := tenantCache.Append(ctx, "data", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = tenantCache.Append(ctx, "data", []byte(" world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := tenantCache.Get(ctx, "data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", string(val))
	}
}

func TestTenantCache_TTLExpiration(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx := tenant.WithTenantID(t.Context(), "tenant-1")

	err := tenantCache.Set(ctx, "key1", []byte("value1"), 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := tenantCache.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("expected 'value1', got '%s'", string(val))
	}

	time.Sleep(150 * time.Millisecond)

	_, err = tenantCache.Get(ctx, "key1")
	if err != storecache.ErrNotFound {
		t.Errorf("expected ErrNotFound after expiration, got %v", err)
	}
}

func TestTenantCache_RawCache(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	raw := tenantCache.RawCache()
	if raw != cache {
		t.Error("RawCache should return underlying cache")
	}

	ctx := t.Context()
	err := raw.Set(ctx, "global-key", []byte("global-value"), time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := raw.Get(ctx, "global-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "global-value" {
		t.Errorf("expected 'global-value', got '%s'", string(val))
	}
}

func TestTenantCache_IsolationBetweenTenants(t *testing.T) {
	cache := storecache.NewMemoryCache()
	tenantCache := NewTenantCache(cache)

	ctx1 := tenant.WithTenantID(t.Context(), "tenant-1")
	ctx2 := tenant.WithTenantID(t.Context(), "tenant-2")
	ctx3 := tenant.WithTenantID(t.Context(), "tenant-3")

	tenantCache.Set(ctx1, "config", []byte("config-1"), time.Minute)
	tenantCache.Set(ctx2, "config", []byte("config-2"), time.Minute)
	tenantCache.Set(ctx3, "config", []byte("config-3"), time.Minute)

	val1, _ := tenantCache.Get(ctx1, "config")
	val2, _ := tenantCache.Get(ctx2, "config")
	val3, _ := tenantCache.Get(ctx3, "config")

	if string(val1) != "config-1" {
		t.Errorf("tenant 1: expected 'config-1', got '%s'", string(val1))
	}
	if string(val2) != "config-2" {
		t.Errorf("tenant 2: expected 'config-2', got '%s'", string(val2))
	}
	if string(val3) != "config-3" {
		t.Errorf("tenant 3: expected 'config-3', got '%s'", string(val3))
	}

	_ = tenantCache.Delete(ctx2, "config")

	val1, _ = tenantCache.Get(ctx1, "config")
	if string(val1) != "config-1" {
		t.Error("tenant 1's value should not be affected by tenant 2's delete")
	}

	_, err := tenantCache.Get(ctx2, "config")
	if err != storecache.ErrNotFound {
		t.Error("tenant 2's value should be deleted")
	}

	val3, _ = tenantCache.Get(ctx3, "config")
	if string(val3) != "config-3" {
		t.Error("tenant 3's value should not be affected by tenant 2's delete")
	}
}
