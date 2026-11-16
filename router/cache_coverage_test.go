package router

import (
	"fmt"
	"testing"
)

func TestNewMatchCacheZeroCapacity(t *testing.T) {
	cache := newMatchCache(0)
	if cache.capacity != 100 {
		t.Errorf("capacity = %d, want 100 for zero input", cache.capacity)
	}

	cache = newMatchCache(-5)
	if cache.capacity != 100 {
		t.Errorf("capacity = %d, want 100 for negative input", cache.capacity)
	}
}

func TestMatcherCacheClear(t *testing.T) {
	cache := newMatchCache(10)

	for i := 0; i < 5; i++ {
		cache.Set(fmt.Sprintf("key-%d", i), &matchResult{})
	}

	cache.Get("key-0")
	cache.Get("missing")

	if matchCacheEntryCount(cache) == 0 {
		t.Fatal("cache should have entries before Clear")
	}

	cache.Clear()

	if count := matchCacheEntryCount(cache); count != 0 {
		t.Errorf("cache entries after Clear = %d, want 0", count)
	}
	for _, key := range []string{"key-0", "key-1"} {
		if _, found := cache.Get(key); found {
			t.Fatalf("stale key %q survived Clear", key)
		}
	}
	for i := range cache.capacity + 1 {
		cache.Set(fmt.Sprintf("new-%d", i), &matchResult{})
	}
	if _, found := cache.Get("new-0"); found {
		t.Fatal("first key inserted after Clear should be evicted first")
	}
	if _, found := cache.Get("new-1"); !found {
		t.Fatal("second key inserted after Clear should remain")
	}
}

func TestMatcherCacheClearEmptyCache(t *testing.T) {
	cache := newMatchCache(10)
	if allocs := testing.AllocsPerRun(10, cache.Clear); allocs != 0 {
		t.Errorf("empty Clear allocated %g times, want 0", allocs)
	}

	if count := matchCacheEntryCount(cache); count != 0 {
		t.Errorf("cache entries after Clear of empty = %d, want 0", count)
	}
}

func TestMatcherCacheGetExactHit(t *testing.T) {
	cache := newMatchCache(10)
	mr := &matchResult{RoutePattern: "/health", RouteMethod: "GET"}
	cache.Set("GET:/health", mr)

	result, found := cache.Get("GET:/health")
	if !found {
		t.Fatal("expected hit on Get")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestMatcherCacheGetMiss(t *testing.T) {
	cache := newMatchCache(10)

	if result, found := cache.Get("GET:/not/found"); found || result != nil {
		t.Fatalf("Get miss = (%v, %v), want (nil, false)", result, found)
	}
}

func TestMatcherCacheEviction(t *testing.T) {
	capacity := 5
	cache := newMatchCache(capacity)

	for i := 0; i < capacity+3; i++ {
		cache.Set(fmt.Sprintf("key-%d", i), &matchResult{})
	}

	if count := matchCacheEntryCount(cache); count > capacity {
		t.Errorf("cache entries = %d, should be <= %d after eviction", count, capacity)
	}
}

func TestMatcherCacheEvictsFIFO(t *testing.T) {
	cache := newMatchCache(3)

	cache.Set("key1", &matchResult{})
	cache.Set("key2", &matchResult{})
	cache.Set("key3", &matchResult{})

	// Accessing key1 does NOT promote it — FIFO has no LRU reordering.
	if _, found := cache.Get("key1"); !found {
		t.Fatal("key1 should exist before eviction")
	}

	// key1 was inserted first, so it is evicted when key4 arrives.
	cache.Set("key4", &matchResult{})

	for _, key := range []string{"key2", "key3", "key4"} {
		if _, found := cache.Get(key); !found {
			t.Fatalf("%s should still exist after eviction", key)
		}
	}
	if _, found := cache.Get("key1"); found {
		t.Fatal("key1 should have been evicted (FIFO: oldest inserted)")
	}
}
