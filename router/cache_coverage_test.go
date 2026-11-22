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

	for i := range cache.capacity + 3 {
		cache.Set(matchCacheKey{path: fmt.Sprintf("key-%d", i)}, &matchResult{})
	}

	if cache.head == 0 {
		t.Fatal("expected FIFO head to advance before Clear")
	}

	cache.Clear()

	if count := matchCacheEntryCount(cache); count != 0 {
		t.Errorf("cache entries after Clear = %d, want 0", count)
	}
	if cache.head != 0 || cache.size != 0 {
		t.Fatalf("FIFO state after Clear = (head %d, size %d), want (0, 0)", cache.head, cache.size)
	}
	for i, key := range cache.ring {
		if key != (matchCacheKey{}) {
			t.Errorf("stale key %v in ring slot %d after Clear", key, i)
		}
	}
	for _, key := range []string{"key-3", "key-12"} {
		if _, found := cache.Get(matchCacheKey{path: key}); found {
			t.Fatalf("stale key %v survived Clear", key)
		}
	}
	for i := range cache.capacity + 1 {
		cache.Set(matchCacheKey{path: fmt.Sprintf("new-%d", i)}, &matchResult{})
	}
	if _, found := cache.Get(matchCacheKey{path: "new-0"}); found {
		t.Fatal("first key inserted after Clear should be evicted first")
	}
	if _, found := cache.Get(matchCacheKey{path: "new-1"}); !found {
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
	cache.Set(matchCacheKey{path: "GET:/health"}, mr)

	result, found := cache.Get(matchCacheKey{path: "GET:/health"})
	if !found {
		t.Fatal("expected hit on Get")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestMatcherCacheGetMiss(t *testing.T) {
	cache := newMatchCache(10)

	if result, found := cache.Get(matchCacheKey{path: "GET:/not/found"}); found || result != nil {
		t.Fatalf("Get miss = (%v, %v), want (nil, false)", result, found)
	}
}

func TestMatcherCacheEviction(t *testing.T) {
	capacity := 5
	cache := newMatchCache(capacity)

	for i := 0; i < capacity+3; i++ {
		cache.Set(matchCacheKey{path: fmt.Sprintf("key-%d", i)}, &matchResult{})
	}

	if count := matchCacheEntryCount(cache); count > capacity {
		t.Errorf("cache entries = %d, should be <= %d after eviction", count, capacity)
	}
}

func TestMatcherCacheEvictsFIFO(t *testing.T) {
	cache := newMatchCache(3)

	cache.Set(matchCacheKey{path: "key1"}, &matchResult{})
	cache.Set(matchCacheKey{path: "key2"}, &matchResult{})
	cache.Set(matchCacheKey{path: "key3"}, &matchResult{})

	// Accessing key1 does NOT promote it — FIFO has no LRU reordering.
	if _, found := cache.Get(matchCacheKey{path: "key1"}); !found {
		t.Fatal("key1 should exist before eviction")
	}

	// key1 was inserted first, so it is evicted when key4 arrives.
	cache.Set(matchCacheKey{path: "key4"}, &matchResult{})

	for _, key := range []string{"key2", "key3", "key4"} {
		if _, found := cache.Get(matchCacheKey{path: key}); !found {
			t.Fatalf("%s should still exist after eviction", key)
		}
	}
	if _, found := cache.Get(matchCacheKey{path: "key1"}); found {
		t.Fatal("key1 should have been evicted (FIFO: oldest inserted)")
	}
}

func TestReplacingCacheEntryDoesNotPromoteItsFIFOPosition(t *testing.T) {
	cache := newMatchCache(2)
	first, replacement := new(matchResult), new(matchResult)
	cache.Set(matchCacheKey{path: "a"}, first)
	cache.Set(matchCacheKey{path: "b"}, new(matchResult))
	claimed, ok := cache.Get(matchCacheKey{path: "a"})
	if !ok || claimed != first {
		t.Fatal("unexpected initial match")
	}
	cache.Set(matchCacheKey{path: "a"}, replacement)
	if cached, ok := cache.Get(matchCacheKey{path: "a"}); !ok || cached != replacement {
		t.Fatal("cache did not publish replacement")
	}
	cache.Set(matchCacheKey{path: "c"}, new(matchResult))
	if _, ok := cache.Get(matchCacheKey{path: "a"}); ok || claimed != first {
		t.Fatal("replacement changed FIFO position or the previously acquired value")
	}
	cache.Clear()
	cache.Set(matchCacheKey{path: "fresh"}, first)
	if _, ok := cache.Get(matchCacheKey{path: "b"}); ok {
		t.Fatal("Clear retained a stale match")
	}
	if got, ok := cache.Get(matchCacheKey{path: "fresh"}); !ok || got != first {
		t.Fatal("first Set after Clear failed")
	}
}
