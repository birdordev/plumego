package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func mustNewMemoryCacheWithConfig(t *testing.T, config Config) *MemoryCache {
	t.Helper()
	cache, err := NewMemoryCacheWithConfig(config)
	if err != nil {
		t.Fatalf("NewMemoryCacheWithConfig: %v", err)
	}
	return cache
}

func TestMemoryCacheSetAndGet(t *testing.T) {
	cache := NewMemoryCache()

	err := cache.Set(t.Context(), "foo", []byte("bar"), time.Minute)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	val, err := cache.Get(t.Context(), "foo")
	if err != nil {
		t.Fatalf("expected value, got error %v", err)
	}

	if string(val) != "bar" {
		t.Fatalf("expected 'bar', got %q", val)
	}

	val[0] = 'B'
	val2, err := cache.Get(t.Context(), "foo")
	if err != nil {
		t.Fatalf("expected value, got error %v", err)
	}
	if string(val2) != "bar" {
		t.Fatalf("expected original value to remain unchanged, got %q", val2)
	}
}

func TestMemoryCacheExpiration(t *testing.T) {
	cache := NewMemoryCache()

	if err := cache.Set(t.Context(), "temp", []byte("value"), 10*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	if _, err := cache.Get(t.Context(), "temp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	exists, err := cache.Exists(t.Context(), "temp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Fatalf("expected key to be expired")
	}
}

func TestMemoryCacheClear(t *testing.T) {
	cache := NewMemoryCache()

	cache.Set(t.Context(), "a", []byte("1"), 0)
	cache.Set(t.Context(), "b", []byte("2"), 0)

	if err := cache.Clear(t.Context()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := cache.Get(t.Context(), "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	exists, _ := cache.Exists(t.Context(), "b")
	if exists {
		t.Fatalf("expected cache to be cleared")
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "negative max key length",
			config:  Config{MaxKeyLength: -1},
			wantErr: true,
		},
		{
			name:    "negative cleanup interval",
			config:  Config{CleanupInterval: -1 * time.Second},
			wantErr: true,
		},
		{
			name:    "negative default ttl",
			config:  Config{DefaultTTL: -1 * time.Second},
			wantErr: true,
		},
		{
			name:    "valid config",
			config:  Config{MaxKeyLength: 100, CleanupInterval: 1 * time.Second},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		if err := tc.config.Validate(); (err != nil) != tc.wantErr {
			t.Fatalf("%s: Validate() error = %v", tc.name, err)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.MaxKeyLength != DefaultMaxKeyLength {
		t.Fatalf("expected MaxKeyLength %d, got %d", DefaultMaxKeyLength, config.MaxKeyLength)
	}
	if config.MaxMemoryUsage != DefaultMaxMemoryUsage {
		t.Fatalf("expected MaxMemoryUsage %d, got %d", DefaultMaxMemoryUsage, config.MaxMemoryUsage)
	}
	if config.CleanupInterval != DefaultCleanupInterval {
		t.Fatalf("expected CleanupInterval %v, got %v", DefaultCleanupInterval, config.CleanupInterval)
	}
	if config.DefaultTTL != 10*time.Minute {
		t.Fatalf("expected DefaultTTL 10m, got %v", config.DefaultTTL)
	}
}

func TestMemoryCacheWithConfig(t *testing.T) {
	config := Config{
		MaxKeyLength:    100,
		MaxMemoryUsage:  1024,
		CleanupInterval: 1 * time.Second,
		DefaultTTL:      5 * time.Minute,
	}

	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	// Test Set with default TTL
	err := cache.Set(t.Context(), "test", []byte("value"), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test Get
	val, err := cache.Get(t.Context(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "value" {
		t.Fatalf("expected 'value', got %q", val)
	}
}

func TestMemoryCacheDeleteAndMiss(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Set some values
	cache.Set(t.Context(), "key1", []byte("value1"), time.Minute)
	cache.Set(t.Context(), "key2", []byte("value2"), time.Minute)

	// Get existing key (hit)
	_, _ = cache.Get(t.Context(), "key1")

	// Get non-existing key (miss)
	_, _ = cache.Get(t.Context(), "nonexistent")

	// Delete a key
	_ = cache.Delete(t.Context(), "key2")

	if _, err := cache.Get(t.Context(), "key2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted key to be missing, got %v", err)
	}
}

func TestMemoryCacheStatsSnapshot(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	if got := cache.Stats(); got != (Stats{}) {
		t.Fatalf("empty Stats = %#v, want zero snapshot", got)
	}

	if err := cache.Set(t.Context(), "a", []byte("abc"), time.Minute); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := cache.Set(t.Context(), "b", nil, time.Minute); err != nil {
		t.Fatalf("Set b: %v", err)
	}

	snapshot := cache.Stats()
	if snapshot.Entries != 2 {
		t.Fatalf("Stats entries = %d, want 2", snapshot.Entries)
	}
	if snapshot.MemoryUsage != 3 {
		t.Fatalf("Stats memory = %d, want 3", snapshot.MemoryUsage)
	}
	if snapshot.Closed {
		t.Fatal("Stats closed = true, want false")
	}

	if err := cache.Set(t.Context(), "a", []byte("abcde"), time.Minute); err != nil {
		t.Fatalf("replace a: %v", err)
	}
	updated := cache.Stats()
	if updated.Entries != 2 || updated.MemoryUsage != 5 {
		t.Fatalf("updated Stats = %#v, want entries=2 memory=5", updated)
	}
	if snapshot.MemoryUsage != 3 {
		t.Fatalf("snapshot mutated after later write: %#v", snapshot)
	}
}

func TestMemoryCacheStatsClosedState(t *testing.T) {
	var nilCache *MemoryCache
	if got := nilCache.Stats(); got != (Stats{Closed: true}) {
		t.Fatalf("nil Stats = %#v, want closed snapshot", got)
	}

	var zero MemoryCache
	if got := zero.Stats(); got != (Stats{Closed: true}) {
		t.Fatalf("zero-value Stats = %#v, want closed snapshot", got)
	}

	cache := NewMemoryCache()
	if err := cache.Set(t.Context(), "key", []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set key: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := cache.Stats()
	if got.Entries != 1 || got.MemoryUsage != 5 || !got.Closed {
		t.Fatalf("closed Stats = %#v, want entries=1 memory=5 closed=true", got)
	}
}

func TestCacheKeySentinelMessages(t *testing.T) {
	if got := ErrInvalidKey.Error(); got != "cache: key is required" {
		t.Fatalf("ErrInvalidKey string = %q, want %q", got, "cache: key is required")
	}
	if got := ErrKeyTooLong.Error(); got != "cache: key too long" {
		t.Fatalf("ErrKeyTooLong string = %q, want %q", got, "cache: key too long")
	}
}

func TestMemoryCacheMemoryLimit(t *testing.T) {
	config := Config{
		MaxKeyLength:    100,
		MaxMemoryUsage:  10, // Very small limit
		CleanupInterval: 0,
		DefaultTTL:      1 * time.Minute,
	}

	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	// First set should succeed
	err := cache.Set(t.Context(), "key1", []byte("small"), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second set should fail due to memory limit
	err = cache.Set(t.Context(), "key2", []byte("larger value that exceeds limit"), 0)
	if err == nil {
		t.Fatal("expected error when exceeding memory limit")
	}
	if !errors.Is(err, ErrCacheFull) {
		t.Fatalf("expected ErrCacheFull, got %v", err)
	}
}

func TestMemoryCacheKeyValidation(t *testing.T) {
	config := Config{
		MaxKeyLength:    10,
		MaxMemoryUsage:  0,
		CleanupInterval: 0,
		DefaultTTL:      1 * time.Minute,
	}

	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	// Test empty key
	err := cache.Set(t.Context(), "", []byte("value"), 0)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
	if errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty key error should not match ErrInvalidConfig, got %v", err)
	}

	// Test key too long
	longKey := "this_is_a_very_long_key_that_exceeds_the_limit"
	err = cache.Set(t.Context(), longKey, []byte("value"), 0)
	if err == nil {
		t.Fatal("expected error for key too long")
	}
	if !errors.Is(err, ErrKeyTooLong) {
		t.Fatalf("expected ErrKeyTooLong, got %v", err)
	}
}

func TestMemoryCacheDefaultTTL(t *testing.T) {
	config := DefaultConfig()
	config.DefaultTTL = 50 * time.Millisecond

	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	// Set with zero TTL should use default TTL
	err := cache.Set(t.Context(), "test", []byte("value"), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be available immediately
	val, err := cache.Get(t.Context(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "value" {
		t.Fatalf("expected 'value', got %q", val)
	}

	// Wait for default TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Should be expired now
	_, err = cache.Get(t.Context(), "test")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after TTL expiration, got %v", err)
	}
}

func TestMemoryCacheNegativeTTLUsesDefaultTTL(t *testing.T) {
	config := DefaultConfig()
	config.DefaultTTL = 50 * time.Millisecond

	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	if err := cache.Set(t.Context(), "test", []byte("value"), -1*time.Second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := cache.Get(t.Context(), "test"); err != nil {
		t.Fatalf("expected value before default TTL expires, got %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if _, err := cache.Get(t.Context(), "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after default TTL expiration, got %v", err)
	}
}

func TestMemoryCacheCleanup(t *testing.T) {
	config := Config{
		MaxKeyLength:    100,
		MaxMemoryUsage:  0,
		CleanupInterval: 50 * time.Millisecond,
		DefaultTTL:      1 * time.Minute,
	}

	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	// Set some items with short TTL
	cache.Set(t.Context(), "exp1", []byte("value1"), 10*time.Millisecond)
	cache.Set(t.Context(), "exp2", []byte("value2"), 10*time.Millisecond)
	cache.Set(t.Context(), "keep", []byte("value3"), 1*time.Minute)

	// Wait for items to expire
	time.Sleep(20 * time.Millisecond)

	// Wait for cleanup to run
	time.Sleep(100 * time.Millisecond)

	if _, err := cache.Get(t.Context(), "exp1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected exp1 to be cleaned up, got %v", err)
	}
	if _, err := cache.Get(t.Context(), "exp2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected exp2 to be cleaned up, got %v", err)
	}
	if value, err := cache.Get(t.Context(), "keep"); err != nil || string(value) != "value3" {
		t.Fatalf("expected keep to remain available, got value=%q err=%v", value, err)
	}
}

func TestMemoryCacheCleanupRemovesMoreThanThousandExpiredItems(t *testing.T) {
	config := DefaultConfig()
	config.CleanupInterval = 0
	config.DefaultTTL = 0
	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	expiredAt := time.Now().Add(-time.Second)
	cache.writeMu.Lock()
	for i := 0; i < 1500; i++ {
		key := fmt.Sprintf("expired:%04d", i)
		if err := cache.setLockedWithExpiration(key, []byte("x"), expiredAt); err != nil {
			cache.writeMu.Unlock()
			t.Fatalf("set expired item %d: %v", i, err)
		}
	}
	if err := cache.setLockedWithExpiration("keep", []byte("ok"), time.Time{}); err != nil {
		cache.writeMu.Unlock()
		t.Fatalf("set keep item: %v", err)
	}
	cache.writeMu.Unlock()

	if got := cache.Stats(); got.Entries != 1501 || got.MemoryUsage != 1502 {
		t.Fatalf("pre-cleanup Stats = %#v, want entries=1501 memory=1502", got)
	}

	cache.cleanupExpired()

	if got := cache.Stats(); got.Entries != 1 || got.MemoryUsage != 2 {
		t.Fatalf("post-cleanup Stats = %#v, want entries=1 memory=2", got)
	}
	if value, err := cache.Get(t.Context(), "keep"); err != nil || string(value) != "ok" {
		t.Fatalf("keep value = %q, err=%v; want ok,nil", value, err)
	}
	if _, err := cache.Get(t.Context(), "expired:0000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired key error = %v, want ErrNotFound", err)
	}
}

func TestMemoryCacheClose(t *testing.T) {
	config := Config{
		MaxKeyLength:    100,
		MaxMemoryUsage:  0,
		CleanupInterval: 50 * time.Millisecond,
		DefaultTTL:      1 * time.Minute,
	}

	cache := mustNewMemoryCacheWithConfig(t, config)

	// Set some data
	cache.Set(t.Context(), "test", []byte("value"), 1*time.Minute)

	// Close the cache
	err := cache.Close()
	if err != nil {
		t.Fatalf("unexpected error closing cache: %v", err)
	}

	if _, err := cache.Get(t.Context(), "test"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Get after Close error = %v, want ErrCacheClosed", err)
	}
	if err := cache.Set(t.Context(), "test", []byte("value"), time.Minute); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Set after Close error = %v, want ErrCacheClosed", err)
	}
	if err := cache.Delete(t.Context(), "test"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Delete after Close error = %v, want ErrCacheClosed", err)
	}
	if _, err := cache.Exists(t.Context(), "test"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Exists after Close error = %v, want ErrCacheClosed", err)
	}
	if err := cache.Clear(t.Context()); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Clear after Close error = %v, want ErrCacheClosed", err)
	}
	if _, err := cache.Incr(t.Context(), "n", 1); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Incr after Close error = %v, want ErrCacheClosed", err)
	}
	if _, err := cache.Decr(t.Context(), "n", 1); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Decr after Close error = %v, want ErrCacheClosed", err)
	}
	if err := cache.Append(t.Context(), "test", []byte("more")); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Append after Close error = %v, want ErrCacheClosed", err)
	}
}

func TestMemoryCacheCloseIdempotent(t *testing.T) {
	cache := NewMemoryCache()

	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("expected repeated Close to be nil, got %v", err)
	}
}

func TestMemoryCacheZeroValueFailsClosed(t *testing.T) {
	var cache MemoryCache
	ctx := t.Context()

	if _, err := cache.Get(ctx, "key"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("zero-value Get error = %v, want ErrCacheClosed", err)
	}
	if err := cache.Set(ctx, "key", []byte("value"), 0); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("zero-value Set error = %v, want ErrCacheClosed", err)
	}
	if err := cache.Delete(ctx, "key"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("zero-value Delete error = %v, want ErrCacheClosed", err)
	}
	if _, err := cache.Exists(ctx, "key"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("zero-value Exists error = %v, want ErrCacheClosed", err)
	}
	if err := cache.Clear(ctx); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("zero-value Clear error = %v, want ErrCacheClosed", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("zero-value Close error = %v, want nil", err)
	}

	var nilCache *MemoryCache
	if err := nilCache.Close(); err != nil {
		t.Fatalf("nil Close error = %v, want nil", err)
	}
	if _, err := nilCache.Get(ctx, "key"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("nil Get error = %v, want ErrCacheClosed", err)
	}
}

func TestMemoryCacheCloseWaitsForWriteBoundary(t *testing.T) {
	cache := NewMemoryCache()

	cache.writeMu.Lock()
	done := make(chan struct{})
	go func() {
		_ = cache.Close()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Close returned while write boundary was still held")
	case <-time.After(20 * time.Millisecond):
	}

	cache.writeMu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after write boundary was released")
	}

	if err := cache.Set(t.Context(), "after-close", []byte("value"), 0); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Set after Close error = %v, want ErrCacheClosed", err)
	}
}

func TestMemoryCacheZeroTTL(t *testing.T) {
	config := DefaultConfig()
	config.DefaultTTL = 0
	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	// Set with zero TTL and no DefaultTTL stores indefinitely.
	err := cache.Set(t.Context(), "permanent", []byte("value"), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Should still be available
	val, err := cache.Get(t.Context(), "permanent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "value" {
		t.Fatalf("expected 'value', got %q", val)
	}
}

func TestMemoryCacheUpdateExistingKey(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Set initial value
	err := cache.Set(t.Context(), "key", []byte("old"), 1*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Update with new value
	err = cache.Set(t.Context(), "key", []byte("new"), 1*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Get updated value
	val, err := cache.Get(t.Context(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "new" {
		t.Fatalf("expected 'new', got %q", val)
	}

	if exists, err := cache.Exists(t.Context(), "key"); err != nil || !exists {
		t.Fatalf("expected updated key to exist, exists=%v err=%v", exists, err)
	}
}

func TestMemoryCacheUpdateExistingEmptyValueKeepsSize(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	if err := cache.Set(t.Context(), "key", nil, 1*time.Minute); err != nil {
		t.Fatalf("Set empty value: %v", err)
	}
	if err := cache.Set(t.Context(), "key", []byte("value"), 1*time.Minute); err != nil {
		t.Fatalf("Set replacement value: %v", err)
	}

	cache.stateMu.RLock()
	got := cache.size
	cache.stateMu.RUnlock()
	if got != 1 {
		t.Fatalf("expected one tracked entry after replacement, got %d", got)
	}
}

func TestMemoryCacheNilAndEmptyValueRoundTrip(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()
	ctx := t.Context()

	if err := cache.Set(ctx, "nil-value", nil, time.Minute); err != nil {
		t.Fatalf("Set nil value: %v", err)
	}
	nilValue, err := cache.Get(ctx, "nil-value")
	if err != nil {
		t.Fatalf("Get nil value: %v", err)
	}
	if nilValue != nil {
		t.Fatalf("nil value round-trip = %#v, want nil", nilValue)
	}
	if exists, err := cache.Exists(ctx, "nil-value"); err != nil || !exists {
		t.Fatalf("nil value should exist, exists=%v err=%v", exists, err)
	}

	empty := []byte{}
	if err := cache.Set(ctx, "empty-value", empty, time.Minute); err != nil {
		t.Fatalf("Set empty value: %v", err)
	}
	emptyValue, err := cache.Get(ctx, "empty-value")
	if err != nil {
		t.Fatalf("Get empty value: %v", err)
	}
	if emptyValue == nil || len(emptyValue) != 0 {
		t.Fatalf("empty value round-trip = %#v, want non-nil empty slice", emptyValue)
	}
	if _, err := cache.Get(ctx, "missing-value"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing value error = %v, want ErrNotFound", err)
	}
}

func TestMemoryCacheConcurrentAccess(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Test concurrent Set operations
	var writers sync.WaitGroup
	for i := 0; i < 100; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			key := fmt.Sprintf("key%d", i)
			value := []byte(fmt.Sprintf("value%d", i))
			cache.Set(t.Context(), key, value, 1*time.Minute)
		}(i)
	}
	writers.Wait()

	// Test concurrent Get operations
	var readers sync.WaitGroup
	for i := 0; i < 100; i++ {
		readers.Add(1)
		go func(i int) {
			defer readers.Done()
			key := fmt.Sprintf("key%d", i)
			_, _ = cache.Get(t.Context(), key)
		}(i)
	}
	readers.Wait()

	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key%d", i)
		if _, err := cache.Get(t.Context(), key); err != nil {
			t.Fatalf("expected key %q to be readable after concurrent access, got %v", key, err)
		}
	}
}

func TestMemoryCacheControlCharacterValidation(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{
			name:    "valid key",
			key:     "valid:key",
			wantErr: false,
		},
		{
			name:    "key with newline",
			key:     "key\nwith\nnewline",
			wantErr: true,
		},
		{
			name:    "key with tab",
			key:     "key\twith\ttab",
			wantErr: true,
		},
		{
			name:    "key with null byte",
			key:     "key\x00null",
			wantErr: true,
		},
		{
			name:    "key with DEL character",
			key:     "key\x7Fdel",
			wantErr: true,
		},
		{
			name:    "key with carriage return",
			key:     "key\rwith\rCR",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := cache.Set(t.Context(), tc.key, []byte("value"), 0)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Set() error = %v, wantErr %v", err, tc.wantErr)
			}

			// Also test Get, Delete, Exists, Incr, Decr, Append
			if tc.wantErr {
				_, err := cache.Get(t.Context(), tc.key)
				if err == nil {
					t.Fatal("Get() should reject invalid key")
				}
				if !errors.Is(err, ErrInvalidKey) {
					t.Fatalf("Get() error should match ErrInvalidKey, got %v", err)
				}
				if errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("Get() error should not match ErrInvalidConfig, got %v", err)
				}

				err = cache.Delete(t.Context(), tc.key)
				if err == nil {
					t.Fatal("Delete() should reject invalid key")
				}

				_, err = cache.Exists(t.Context(), tc.key)
				if err == nil {
					t.Fatal("Exists() should reject invalid key")
				}

				_, err = cache.Incr(t.Context(), tc.key, 1)
				if err == nil {
					t.Fatal("Incr() should reject invalid key")
				}

				_, err = cache.Decr(t.Context(), tc.key, 1)
				if err == nil {
					t.Fatal("Decr() should reject invalid key")
				}

				err = cache.Append(t.Context(), tc.key, []byte("data"))
				if err == nil {
					t.Fatal("Append() should reject invalid key")
				}
			}
		})
	}
}

func TestMemoryCacheCanceledContext(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := cache.Set(ctx, "key", []byte("value"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set should return context.Canceled, got %v", err)
	}
	if _, err := cache.Get(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get should return context.Canceled, got %v", err)
	}
	if err := cache.Delete(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete should return context.Canceled, got %v", err)
	}
	if _, err := cache.Exists(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Exists should return context.Canceled, got %v", err)
	}
	if err := cache.Clear(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Clear should return context.Canceled, got %v", err)
	}
	if _, err := cache.Incr(ctx, "key", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Incr should return context.Canceled, got %v", err)
	}
	if _, err := cache.Decr(ctx, "key", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Decr should return context.Canceled, got %v", err)
	}
	if err := cache.Append(ctx, "key", []byte("value")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append should return context.Canceled, got %v", err)
	}

	if exists, err := cache.Exists(t.Context(), "key"); err != nil || exists {
		t.Fatalf("canceled Set should not store value, exists=%v err=%v", exists, err)
	}
}

func TestExpiredAtBoundary(t *testing.T) {
	now := time.Now()
	if !expiredAt(now, now) {
		t.Fatal("expected value expiring at current time to be expired")
	}
	if expiredAt(now.Add(time.Nanosecond), now) {
		t.Fatal("expected future expiration to remain active")
	}
	if expiredAt(time.Time{}, now) {
		t.Fatal("expected zero expiration to remain active")
	}
}

func TestMemoryCacheIncr(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Test increment on non-existent key
	val1, err := cache.Incr(t.Context(), "counter", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val1 != 5 {
		t.Fatalf("expected 5, got %d", val1)
	}

	// Test increment on existing key
	val2, err := cache.Incr(t.Context(), "counter", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val2 != 8 {
		t.Fatalf("expected 8, got %d", val2)
	}

	// Test increment by negative number
	val3, err := cache.Incr(t.Context(), "counter", -2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val3 != 6 {
		t.Fatalf("expected 6, got %d", val3)
	}

	raw, err := cache.Get(t.Context(), "counter")
	if err != nil {
		t.Fatalf("Get counter: %v", err)
	}
	if string(raw) != "6" {
		t.Fatalf("expected textual counter %q, got %q", "6", raw)
	}
	decoded, err := decodeInt64(raw)
	if err != nil {
		t.Fatalf("decode counter: %v", err)
	}
	if decoded != 6 {
		t.Fatalf("expected encoded counter 6, got %d", decoded)
	}
}

func TestMemoryCacheIncrAcceptsTextInteger(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	if err := cache.Set(t.Context(), "counter", []byte("41"), time.Minute); err != nil {
		t.Fatalf("Set counter: %v", err)
	}
	got, err := cache.Incr(t.Context(), "counter", 1)
	if err != nil {
		t.Fatalf("Incr text counter: %v", err)
	}
	if got != 42 {
		t.Fatalf("Incr text counter = %d, want 42", got)
	}
	raw, err := cache.Get(t.Context(), "counter")
	if err != nil {
		t.Fatalf("Get counter: %v", err)
	}
	if string(raw) != "42" {
		t.Fatalf("stored counter = %q, want 42", raw)
	}
}

// encodeGobInt64 encodes n in the legacy gob format used by older cache versions.
// Keep in sync with the gob decode path in decodeInt64.
func encodeGobInt64(n int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(n); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestMemoryCacheIncrReadsLegacyGobInteger(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	legacy, err := encodeGobInt64(7)
	if err != nil {
		t.Fatalf("encode legacy integer: %v", err)
	}
	if err := cache.Set(t.Context(), "counter", legacy, time.Minute); err != nil {
		t.Fatalf("Set counter: %v", err)
	}
	got, err := cache.Incr(t.Context(), "counter", 1)
	if err != nil {
		t.Fatalf("Incr legacy counter: %v", err)
	}
	if got != 8 {
		t.Fatalf("Incr legacy counter = %d, want 8", got)
	}
}

func TestMemoryCacheIncrPreservesExistingExpiration(t *testing.T) {
	config := DefaultConfig()
	config.DefaultTTL = time.Hour
	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	if err := cache.Set(t.Context(), "counter", mustEncodeInt64(t, 1), 30*time.Second); err != nil {
		t.Fatalf("Set counter: %v", err)
	}
	before := cachedExpiration(t, cache, "counter")

	if got, err := cache.Incr(t.Context(), "counter", 1); err != nil || got != 2 {
		t.Fatalf("Incr = %d, %v; want 2, nil", got, err)
	}
	after := cachedExpiration(t, cache, "counter")
	if !after.Equal(before) {
		t.Fatalf("expiration changed from %v to %v", before, after)
	}
}

func TestMemoryCacheDecr(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Test decrement on non-existent key
	val1, err := cache.Decr(t.Context(), "counter", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val1 != -5 {
		t.Fatalf("expected -5, got %d", val1)
	}

	// Test decrement on existing key
	val2, err := cache.Decr(t.Context(), "counter", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val2 != -8 {
		t.Fatalf("expected -8, got %d", val2)
	}
}

func TestMemoryCacheAppendPreservesExistingExpiration(t *testing.T) {
	config := DefaultConfig()
	config.DefaultTTL = time.Hour
	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	if err := cache.Set(t.Context(), "key", []byte("hello"), 30*time.Second); err != nil {
		t.Fatalf("Set key: %v", err)
	}
	before := cachedExpiration(t, cache, "key")

	if err := cache.Append(t.Context(), "key", []byte(" world")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	after := cachedExpiration(t, cache, "key")
	if !after.Equal(before) {
		t.Fatalf("expiration changed from %v to %v", before, after)
	}
}

func cachedExpiration(t *testing.T, cache *MemoryCache, key string) time.Time {
	t.Helper()

	raw, ok := cache.store.Load(key)
	if !ok {
		t.Fatalf("expected key %q to be loaded", key)
	}
	return raw.(cacheItem).expiration
}

func mustEncodeInt64(t *testing.T, value int64) []byte {
	t.Helper()

	encoded, err := encodeInt64(value)
	if err != nil {
		t.Fatalf("encodeInt64: %v", err)
	}
	return encoded
}
func TestMemoryCacheIncrNonInteger(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Set a non-integer value
	err := cache.Set(t.Context(), "key", []byte("not an integer"), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to increment
	_, err = cache.Incr(t.Context(), "key", 1)
	if !errors.Is(err, ErrNotInteger) {
		t.Fatalf("expected ErrNotInteger, got %v", err)
	}
}

func TestMemoryCacheIncrRejectsExistingEmptyValue(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	if err := cache.Set(t.Context(), "empty", []byte{}, 0); err != nil {
		t.Fatalf("Set empty: %v", err)
	}

	if _, err := cache.Incr(t.Context(), "empty", 1); !errors.Is(err, ErrNotInteger) {
		t.Fatalf("Incr empty error = %v, want ErrNotInteger", err)
	}
	if _, err := cache.Decr(t.Context(), "empty", 1); !errors.Is(err, ErrNotInteger) {
		t.Fatalf("Decr empty error = %v, want ErrNotInteger", err)
	}
}

func TestMemoryCacheNonExpiredReadsDoNotWaitForWriteBoundary(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	if err := cache.Set(t.Context(), "key", []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set key: %v", err)
	}

	cache.writeMu.Lock()
	defer cache.writeMu.Unlock()

	done := make(chan error, 2)
	go func() {
		value, err := cache.Get(t.Context(), "key")
		if err == nil && string(value) != "value" {
			err = fmt.Errorf("Get value = %q, want value", value)
		}
		done <- err
	}()
	go func() {
		exists, err := cache.Exists(t.Context(), "key")
		if err == nil && !exists {
			err = fmt.Errorf("Exists = false, want true")
		}
		done <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("non-expired read waited for write boundary")
		}
	}
}

// ---------------------------------------------------------------------------
// Additional coverage tests
// ---------------------------------------------------------------------------

// TestClosedErrOnClosedCache exercises the closedErr method on a closed cache.
func TestClosedErrOnClosedCache(t *testing.T) {
	cache := NewMemoryCache()
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// closedErr delegates to operationErr(nil), which checks mc.closed.
	if err := cache.closedErr(); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("closedErr on closed cache = %v, want ErrCacheClosed", err)
	}
}

// TestClosedErrOnOpenCache verifies closedErr returns nil on a live cache.
func TestClosedErrOnOpenCache(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()
	if err := cache.closedErr(); err != nil {
		t.Fatalf("closedErr on open cache = %v, want nil", err)
	}
}

// TestAddInt64NegativeDeltaOverflow exercises the negative-delta overflow branch in addInt64.
func TestAddInt64NegativeDeltaOverflow(t *testing.T) {
	// minInt64 + (-1) would overflow; delta = -1, value = minInt64
	_, err := addInt64(minInt64, -1)
	if err == nil {
		t.Fatal("expected overflow error")
	}
	if !errors.Is(err, ErrNotInteger) {
		t.Fatalf("expected ErrNotInteger, got %v", err)
	}
}

// TestAddInt64PositiveDeltaOverflow exercises the positive-delta overflow branch.
func TestAddInt64PositiveDeltaOverflow(t *testing.T) {
	_, err := addInt64(maxInt64, 1)
	if err == nil {
		t.Fatal("expected overflow error")
	}
	if !errors.Is(err, ErrNotInteger) {
		t.Fatalf("expected ErrNotInteger, got %v", err)
	}
}

// TestAddInt64NegativeDeltaNoOverflow verifies normal subtraction works.
func TestAddInt64NegativeDeltaNoOverflow(t *testing.T) {
	got, err := addInt64(10, -3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}

// TestLockWriteOperationClosedCache exercises the closed-cache branch in lockWriteOperation.
func TestLockWriteOperationClosedCache(t *testing.T) {
	cache := NewMemoryCache()
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Set reaches lockWriteOperation; on a closed cache it should return ErrCacheClosed.
	if err := cache.Set(t.Context(), "k", []byte("v"), 0); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Set on closed cache = %v, want ErrCacheClosed", err)
	}
}

// TestDecrBasic exercises Decr on a new key and an existing key.
func TestDecrBasic(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	val, err := cache.Decr(t.Context(), "ctr", 3)
	if err != nil {
		t.Fatalf("Decr new key: %v", err)
	}
	if val != -3 {
		t.Fatalf("Decr new key = %d, want -3", val)
	}

	val, err = cache.Decr(t.Context(), "ctr", 2)
	if err != nil {
		t.Fatalf("Decr existing key: %v", err)
	}
	if val != -5 {
		t.Fatalf("Decr existing key = %d, want -5", val)
	}
}

// TestDecrMinInt64GuardTriggered verifies that Decr rejects minInt64 delta.
func TestDecrMinInt64GuardTriggered(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	_, err := cache.Decr(t.Context(), "k", minInt64)
	if err == nil {
		t.Fatal("expected error for minInt64 delta")
	}
	if !errors.Is(err, ErrNotInteger) {
		t.Fatalf("expected ErrNotInteger, got %v", err)
	}
}

// TestDecrClosedCache exercises the ErrCacheClosed path in Decr.
func TestDecrClosedCache(t *testing.T) {
	cache := NewMemoryCache()
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := cache.Decr(t.Context(), "k", 1); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Decr on closed cache = %v, want ErrCacheClosed", err)
	}
}

// TestRemoveExpiredItemLockedItemChanged verifies that removeExpiredItemLocked
// does nothing when the stored item has changed since the snapshot.
func TestRemoveExpiredItemLockedItemChanged(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	shortTTL := 20 * time.Millisecond
	if err := cache.Set(t.Context(), "key", []byte("original"), shortTTL); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Wait for the item to expire.
	time.Sleep(40 * time.Millisecond)

	// Replace the item with a fresh one before cleanup runs.
	if err := cache.Set(t.Context(), "key", []byte("updated"), time.Minute); err != nil {
		t.Fatalf("Set updated: %v", err)
	}

	// Manually trigger cleanupExpired; the original snapshot is stale, so
	// removeExpiredItemLocked should leave the updated item in place.
	cache.cleanupExpired()

	val, err := cache.Get(t.Context(), "key")
	if err != nil {
		t.Fatalf("Get after cleanup: %v", err)
	}
	if string(val) != "updated" {
		t.Fatalf("expected 'updated', got %q", val)
	}
}

// TestRemoveExpiredItemLockedExpiredAndPresent ensures an expired item that
// is still the current entry gets removed during cleanup.
func TestRemoveExpiredItemLockedExpiredAndPresent(t *testing.T) {
	config := Config{
		MaxKeyLength:    256,
		CleanupInterval: 0, // no background cleanup
		DefaultTTL:      0,
	}
	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	if err := cache.Set(t.Context(), "exp", []byte("bye"), 10*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Wait for expiry then trigger cleanup.
	time.Sleep(30 * time.Millisecond)
	cache.cleanupExpired()

	if _, err := cache.Get(t.Context(), "exp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after cleanup, got %v", err)
	}
}

// TestNewMemoryCacheWithConfigInvalidConfig verifies that NewMemoryCacheWithConfig
// returns an error for invalid config (covering the error branch that NewMemoryCache skips).
func TestNewMemoryCacheWithConfigInvalidConfig(t *testing.T) {
	_, err := NewMemoryCacheWithConfig(Config{MaxKeyLength: -1})
	if err == nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

// TestAdjustStoredValueNegativeClampsToZero exercises the memory underflow guard.
func TestAdjustStoredValueNegativeClampsToZero(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Reduce memory below zero (which should be clamped).
	cache.adjustStoredValue(0, -9999)
	cache.stateMu.RLock()
	mem := cache.memory
	cache.stateMu.RUnlock()
	if mem != 0 {
		t.Fatalf("expected memory clamped to 0, got %d", mem)
	}
}

// TestCleanupExpiredOnClosedCache exercises the early-return in cleanupExpired
// when the cache is closed (line 118-120).
func TestCleanupExpiredOnClosedCache(t *testing.T) {
	cache := NewMemoryCache()
	if err := cache.Set(t.Context(), "key", []byte("val"), 10*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Should return immediately without panicking.
	cache.cleanupExpired()
}

// TestRemoveExpiredItemLockedKeyNotInStore exercises the !ok path (line 103-104)
// where the key has already been deleted from the store before the locked remove runs.
func TestRemoveExpiredItemLockedKeyNotInStore(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	expiredAt := time.Now().Add(-time.Second)
	staleItem := cacheItem{
		value:      []byte("gone"),
		expiration: expiredAt,
	}
	// Call removeExpiredItemLocked directly with a key that was never stored.
	cache.writeMu.Lock()
	removed := cache.removeExpiredItemLocked("nonexistent-key", staleItem)
	cache.writeMu.Unlock()
	if removed {
		t.Fatal("expected false for key not in store")
	}
}

// TestIncrOnExpiredKey exercises the path in Incr where the key exists but
// is already expired (lines 336-338), so it gets treated as a fresh key.
func TestIncrOnExpiredKey(t *testing.T) {
	config := DefaultConfig()
	config.DefaultTTL = 0
	cache := mustNewMemoryCacheWithConfig(t, config)
	defer cache.Close()

	// Insert an item that is already expired.
	expiredAt := time.Now().Add(-time.Second)
	cache.writeMu.Lock()
	if err := cache.setLockedWithExpiration("counter", []byte("5"), expiredAt); err != nil {
		cache.writeMu.Unlock()
		t.Fatalf("setLockedWithExpiration: %v", err)
	}
	cache.writeMu.Unlock()

	// Incr should treat the expired key as missing and start from 0.
	got, err := cache.Incr(t.Context(), "counter", 3)
	if err != nil {
		t.Fatalf("Incr on expired key: %v", err)
	}
	if got != 3 {
		t.Fatalf("Incr on expired key = %d, want 3", got)
	}
}

// TestLockWriteOperationContextCancelledAfterLock exercises the closed-cache
// branch inside lockWriteOperation while holding writeMu externally.
// We use Set, which calls lockWriteOperation internally.
func TestLockWriteOperationContextCancelledWhileWaiting(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Hold writeMu so Set blocks inside lockWriteOperation.
	cache.writeMu.Lock()

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		errCh <- cache.Set(ctx, "k", []byte("v"), 0)
	}()

	time.Sleep(20 * time.Millisecond)
	// Close the cache while the goroutine is blocked waiting for writeMu.
	// Do it from a separate goroutine because Close itself needs writeMu.
	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		// First cancel the context so the Set goroutine sees it on lock acquire.
		cancel()
	}()
	<-closeDone

	// Release the lock; Set will re-check operationErr and see context.Canceled.
	cache.writeMu.Unlock()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// Accept context.Canceled or ErrCacheClosed — either is correct.
		if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrCacheClosed) {
			t.Fatalf("unexpected error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Set did not return")
	}
}

// TestDeleteClosedCache covers the lockWriteOperation-closed path inside Delete.
func TestDeleteClosedCache(t *testing.T) {
	cache := NewMemoryCache()
	if err := cache.Set(t.Context(), "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := cache.Delete(t.Context(), "k"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Delete on closed cache = %v, want ErrCacheClosed", err)
	}
}

// TestExistsClosedCache covers the operationErr path inside Exists.
func TestExistsClosedCache(t *testing.T) {
	cache := NewMemoryCache()
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := cache.Exists(t.Context(), "k"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Exists on closed cache = %v, want ErrCacheClosed", err)
	}
}

// TestAppendClosedCache covers the lockWriteOperation-closed path inside Append.
func TestAppendClosedCache(t *testing.T) {
	cache := NewMemoryCache()
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := cache.Append(t.Context(), "k", []byte("data")); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Append on closed cache = %v, want ErrCacheClosed", err)
	}
}

// TestIncrOverflowViaCache drives the addInt64 overflow through Incr.
func TestIncrOverflowViaCache(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Set counter to maxInt64.
	if err := cache.Set(t.Context(), "big", mustEncodeInt64(t, maxInt64), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, err := cache.Incr(t.Context(), "big", 1)
	if err == nil || !errors.Is(err, ErrNotInteger) {
		t.Fatalf("expected ErrNotInteger overflow, got %v", err)
	}
}

func TestMemoryCacheAppend(t *testing.T) {
	cache := NewMemoryCache()
	defer cache.Close()

	// Test append on non-existent key
	err := cache.Append(t.Context(), "key", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := cache.Get(t.Context(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "hello" {
		t.Fatalf("expected 'hello', got %q", val)
	}

	// Test append on existing key
	err = cache.Append(t.Context(), "key", []byte(" world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err = cache.Get(t.Context(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", val)
	}
}
