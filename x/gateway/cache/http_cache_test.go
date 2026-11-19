package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"
)

func TestMemoryStore_BasicOperations(t *testing.T) {
	store := NewMemoryStore(10)

	// Test Set and Get
	resp := &CachedResponse{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       []byte("test response"),
		CachedAt:   time.Now(),
		ExpiresAt:  time.Now().Add(5 * time.Minute),
		Size:       13,
	}

	store.Set("key1", resp, 5*time.Minute)

	// Get should return cached response
	cached, found := store.Get("key1")
	if !found {
		t.Fatal("Expected cached response to be found")
	}

	if cached.StatusCode != 200 {
		t.Errorf("Expected status code 200, got %d", cached.StatusCode)
	}

	if string(cached.Body) != "test response" {
		t.Errorf("Expected body 'test response', got '%s'", string(cached.Body))
	}
}

func TestMemoryStore_Expiration(t *testing.T) {
	store := NewMemoryStore(10)

	// Create expired response
	resp := &CachedResponse{
		StatusCode: 200,
		Header:     make(http.Header),
		Body:       []byte("test"),
		CachedAt:   time.Now().Add(-10 * time.Minute),
		ExpiresAt:  time.Now().Add(-5 * time.Minute), // Expired 5 minutes ago
		Size:       4,
	}

	store.Set("expired", resp, 0)

	// Get should not return expired response
	_, found := store.Get("expired")
	if found {
		t.Error("Expected expired response to not be found")
	}
}

func TestMemoryStoreUsesTTLForUnsetExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(3)
		input := &CachedResponse{StatusCode: http.StatusOK}
		now := time.Now()
		store.Set("derived", input, time.Second)
		if cached, found := store.Get("derived"); !found || !cached.ExpiresAt.Equal(now.Add(time.Second)) {
			t.Fatalf("initial entry = %+v, found = %v", cached, found)
		}
		store.Set("derived", input, 2*time.Second)
		if cached, found := store.Get("derived"); !found || !cached.ExpiresAt.Equal(now.Add(2*time.Second)) {
			t.Fatalf("replacement = %+v, found = %v", cached, found)
		}
		if !input.ExpiresAt.IsZero() {
			t.Fatalf("caller expiry = %v, want zero", input.ExpiresAt)
		}
		future := now.Add(time.Hour)
		store.Set("future", &CachedResponse{ExpiresAt: future}, time.Second)
		store.Set("expired", &CachedResponse{ExpiresAt: now.Add(-time.Second)}, time.Hour)
		if _, found := store.Get("expired"); found {
			t.Error("explicit expired response was retained")
		}
		store.Set("zero", input, 0)
		if _, found := store.Get("zero"); found {
			t.Error("zero TTL changed the behavior of an unset expiry")
		}
		time.Sleep(3 * time.Second)
		if _, found := store.Get("derived"); found {
			t.Error("derived expiry was not applied")
		}
		if cached, found := store.Get("future"); !found || !cached.ExpiresAt.Equal(future) {
			t.Errorf("explicit expiry = %+v, found = %v", cached, found)
		}
	})
}

func TestMemoryStore_LRUEviction(t *testing.T) {
	store := NewMemoryStore(3) // Small capacity

	// Add 3 items
	for i := 1; i <= 3; i++ {
		resp := &CachedResponse{
			StatusCode: 200,
			Body:       []byte("test"),
			CachedAt:   time.Now(),
			ExpiresAt:  time.Now().Add(5 * time.Minute),
			Size:       4,
		}
		store.Set(string(rune('a'+i-1)), resp, 5*time.Minute)
	}

	// All 3 should be present
	if store.Len() != 3 {
		t.Errorf("Expected 3 items, got %d", store.Len())
	}

	// Add 4th item - should evict oldest (a)
	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("test"),
		CachedAt:   time.Now(),
		ExpiresAt:  time.Now().Add(5 * time.Minute),
		Size:       4,
	}
	store.Set("d", resp, 5*time.Minute)

	// Should still have 3 items
	if store.Len() != 3 {
		t.Errorf("Expected 3 items after eviction, got %d", store.Len())
	}

	// First item should be evicted
	_, found := store.Get("a")
	if found {
		t.Error("Expected oldest item to be evicted")
	}
}

func TestMemoryStore_Stats(t *testing.T) {
	store := NewMemoryStore(10)

	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("test"),
		CachedAt:   time.Now(),
		ExpiresAt:  time.Now().Add(5 * time.Minute),
		Size:       4,
	}
	store.Set("key", resp, 5*time.Minute)

	// Hit
	store.Get("key")

	// Miss
	store.Get("nonexistent")

	stats := store.Stats()

	if stats.Hits != 1 {
		t.Errorf("Expected 1 hit, got %d", stats.Hits)
	}

	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}

	if stats.Items != 1 {
		t.Errorf("Expected 1 item, got %d", stats.Items)
	}
}

func TestCacheMiddleware_CacheHit(t *testing.T) {
	callCount := 0

	// Backend handler
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend response"))
	})

	// Create cache middleware
	cache := Middleware(Config{
		DefaultTTL: 5 * time.Minute,
	})

	handler := cache(backend)

	// First request - cache miss
	req1 := httptest.NewRequest("GET", "/test", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	if callCount != 1 {
		t.Errorf("Expected backend to be called once, got %d", callCount)
	}

	// Second request - cache hit
	req2 := httptest.NewRequest("GET", "/test", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if callCount != 1 {
		t.Errorf("Expected backend to not be called again, got %d calls", callCount)
	}

	// Check cache header
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Error("Expected X-Cache: HIT header")
	}
}

func TestCacheMiddleware_NonCacheableMethod(t *testing.T) {
	callCount := 0

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	})

	cache := Middleware(Config{
		DefaultTTL: 5 * time.Minute,
	})

	handler := cache(backend)

	// POST request - should not be cached
	req1 := httptest.NewRequest("POST", "/test", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	req2 := httptest.NewRequest("POST", "/test", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if callCount != 2 {
		t.Errorf("Expected backend to be called twice for POST, got %d", callCount)
	}
}

func TestDefaultKeyStrategy(t *testing.T) {
	strategy := NewDefaultKeyStrategy()

	req1 := httptest.NewRequest("GET", "/api/users/123", nil)
	req1.Header.Set("Accept", "application/json")

	req2 := httptest.NewRequest("GET", "/api/users/123", nil)
	req2.Header.Set("Accept", "application/json")

	key1 := strategy.Generate(req1)
	key2 := strategy.Generate(req2)

	if key1 != key2 {
		t.Error("Expected identical requests to generate same key")
	}

	// Different Accept header should generate different key
	req3 := httptest.NewRequest("GET", "/api/users/123", nil)
	req3.Header.Set("Accept", "application/xml")

	key3 := strategy.Generate(req3)

	if key1 == key3 {
		t.Error("Expected different Accept headers to generate different keys")
	}
}

func TestURLOnlyKeyStrategy(t *testing.T) {
	strategy := NewURLOnlyKeyStrategy()

	req1 := httptest.NewRequest("GET", "/api/users/123", nil)
	req1.Header.Set("Accept", "application/json")

	req2 := httptest.NewRequest("GET", "/api/users/123", nil)
	req2.Header.Set("Accept", "application/xml")

	key1 := strategy.Generate(req1)
	key2 := strategy.Generate(req2)

	// URL-only strategy should ignore headers
	if key1 != key2 {
		t.Error("Expected URL-only strategy to generate same key regardless of headers")
	}
}

func TestCacheControl_NoStore(t *testing.T) {
	callCount := 0

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test"))
	})

	cache := Middleware(Config{
		DefaultTTL:          5 * time.Minute,
		RespectCacheControl: true,
	})

	handler := cache(backend)

	// First request
	req1 := httptest.NewRequest("GET", "/test", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	// Second request - should not be cached due to no-store
	req2 := httptest.NewRequest("GET", "/test", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if callCount != 2 {
		t.Errorf("Expected backend to be called twice with no-store, got %d", callCount)
	}
}
