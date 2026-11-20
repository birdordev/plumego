package cache

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ---- Conditional request (ETag / If-None-Match) ----

func TestCacheMiddleware_ConditionalRequest_304(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("payload"))
	})

	handler := Middleware(Config{
		DefaultTTL:                5 * time.Minute,
		EnableConditionalRequests: true,
	})(backend)

	// First request: cache miss, seeds the cache.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/cond", nil))
	if callCount != 1 {
		t.Fatalf("expected 1 backend call, got %d", callCount)
	}

	// Second request: cache hit — ETag is in the cached response header.
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/cond", nil))
	etag := w2.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag should be present in the cache-hit response")
	}
	if callCount != 1 {
		t.Fatalf("backend should not be called on cache hit, total calls = %d", callCount)
	}

	// Third request: conditional with matching ETag → 304.
	req3 := httptest.NewRequest(http.MethodGet, "/cond", nil)
	req3.Header.Set("If-None-Match", etag)
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if callCount != 1 {
		t.Fatalf("backend should not be called on conditional hit, total calls = %d", callCount)
	}
	if w3.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", w3.Code)
	}
}

func TestCacheMiddleware_ConditionalRequest_NonMatchingETag(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("updated payload"))
	})

	handler := Middleware(Config{
		DefaultTTL:                5 * time.Minute,
		EnableConditionalRequests: true,
	})(backend)

	// Seed cache.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/cond2", nil))

	// Request with a stale / wrong ETag → normal cache hit (200), no backend call.
	req := httptest.NewRequest(http.MethodGet, "/cond2", nil)
	req.Header.Set("If-None-Match", `"stale-etag"`)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if callCount != 1 {
		t.Fatalf("backend should not be called for a cache hit, total calls = %d", callCount)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-matching ETag (full response), got %d", w.Code)
	}
	if w.Header().Get("X-Cache") != "HIT" {
		t.Error("expected X-Cache: HIT header")
	}
}

// ---- OnCacheHit / OnCacheMiss callbacks ----

func TestCacheMiddleware_OnCacheMiss(t *testing.T) {
	var missedKeys []string

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data"))
	})

	handler := Middleware(Config{
		DefaultTTL: 5 * time.Minute,
		OnCacheMiss: func(key string) {
			missedKeys = append(missedKeys, key)
		},
	})(backend)

	req := httptest.NewRequest(http.MethodGet, "/miss", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if len(missedKeys) != 1 {
		t.Fatalf("expected 1 cache miss callback, got %d", len(missedKeys))
	}
}

func TestCacheMiddleware_OnCacheHit(t *testing.T) {
	var hitKeys []string

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data"))
	})

	handler := Middleware(Config{
		DefaultTTL: 5 * time.Minute,
		OnCacheHit: func(key string, resp *CachedResponse) {
			hitKeys = append(hitKeys, key)
		},
	})(backend)

	// First request seeds cache → miss.
	req1 := httptest.NewRequest(http.MethodGet, "/hit", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	// Second request → hit.
	req2 := httptest.NewRequest(http.MethodGet, "/hit", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req2)

	if len(hitKeys) != 1 {
		t.Fatalf("expected 1 cache hit callback, got %d", len(hitKeys))
	}
}

func TestCacheMiddleware_OnCacheHit_ConditionalRequest(t *testing.T) {
	var hitCount int

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("body"))
	})

	handler := Middleware(Config{
		DefaultTTL:                5 * time.Minute,
		EnableConditionalRequests: true,
		OnCacheHit: func(key string, resp *CachedResponse) {
			hitCount++
		},
	})(backend)

	// Seed cache.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/hitcond", nil))

	// Second request: cache hit — grab ETag from cache-hit response.
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/hitcond", nil))
	etag := w2.Header().Get("ETag")

	// Conditional hit (304) should also fire OnCacheHit.
	req := httptest.NewRequest(http.MethodGet, "/hitcond", nil)
	req.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// hitCount: 1 from the plain cache hit + 1 from the conditional hit.
	if hitCount != 2 {
		t.Fatalf("expected OnCacheHit called twice (plain hit + 304), got %d", hitCount)
	}
}

// ---- Cache-Control from response (max-age TTL) ----

func TestCacheMiddleware_ResponseMaxAge(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Cache-Control", "max-age=3600")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("cached-for-an-hour"))
	})

	handler := Middleware(Config{
		DefaultTTL:          30 * time.Second, // short default
		RespectCacheControl: true,
	})(backend)

	// First request.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/maxage", nil))

	// Second request must be a cache hit (max-age=3600 far exceeds elapsed time).
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/maxage", nil))

	if callCount != 1 {
		t.Fatalf("backend calls = %d, want 1 (response should be cached via max-age)", callCount)
	}
}

// ---- Request Cache-Control no-cache / no-store bypass ----

func TestCacheMiddleware_RequestNoCacheBypassesCache(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fresh"))
	})

	handler := Middleware(Config{
		DefaultTTL:          5 * time.Minute,
		RespectCacheControl: true,
	})(backend)

	// Seed cache.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/bypass", nil))

	// Request with Cache-Control: no-cache should bypass cache and hit backend.
	req := httptest.NewRequest(http.MethodGet, "/bypass", nil)
	req.Header.Set("Cache-Control", "no-cache")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if callCount != 2 {
		t.Fatalf("backend calls = %d, want 2 (no-cache must bypass cache)", callCount)
	}
}

func TestCacheMiddleware_RequestNoStoreBypassesCache(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fresh"))
	})

	handler := Middleware(Config{
		DefaultTTL:          5 * time.Minute,
		RespectCacheControl: true,
	})(backend)

	// Seed cache.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/bypassns", nil))

	// Request with Cache-Control: no-store should bypass cache.
	req := httptest.NewRequest(http.MethodGet, "/bypassns", nil)
	req.Header.Set("Cache-Control", "no-store")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if callCount != 2 {
		t.Fatalf("backend calls = %d, want 2 (no-store must bypass cache)", callCount)
	}
}

// ---- Response size limit ----

func TestCacheMiddleware_OversizedResponseNotCached(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		// Write 2 bytes over the 10-byte limit.
		w.Write([]byte("123456789012"))
	})

	handler := Middleware(Config{
		DefaultTTL: 5 * time.Minute,
		MaxSize:    10, // 10-byte limit
	})(backend)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/big", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/big", nil))

	if callCount != 2 {
		t.Fatalf("backend calls = %d, want 2 (oversized response must not be cached)", callCount)
	}
}

// ---- Non-cacheable status code ----

func TestCacheMiddleware_NonCacheableStatusNotCached(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusCreated) // 201 not in default cacheable set
		w.Write([]byte("created"))
	})

	handler := Middleware(Config{DefaultTTL: 5 * time.Minute})(backend)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/created", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/created", nil))

	if callCount != 2 {
		t.Fatalf("backend calls = %d, want 2 (201 must not be cached)", callCount)
	}
}

// ---- Custom key strategy ----

func TestCacheMiddleware_CustomKeyStrategy(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data"))
	})

	// Strategy that ignores everything — all requests share one key.
	staticKey := &CustomKeyStrategy{KeyFunc: func(_ *http.Request) string { return "static" }}

	handler := Middleware(Config{
		DefaultTTL:  5 * time.Minute,
		KeyStrategy: staticKey,
	})(backend)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/a", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/b", nil))

	if callCount != 1 {
		t.Fatalf("backend calls = %d, want 1 (both paths share the same cache key)", callCount)
	}
}

// ---- HEAD method cached with GET ----

func TestCacheMiddleware_HEADMethod(t *testing.T) {
	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(Config{DefaultTTL: 5 * time.Minute})(backend)

	// HEAD is in the default cacheable methods.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodHead, "/head", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodHead, "/head", nil))

	if callCount != 1 {
		t.Fatalf("backend calls = %d, want 1 (HEAD should be cached)", callCount)
	}
}

// ---- Age header set on cache hit ----

func TestCacheMiddleware_AgeHeaderOnHit(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("payload"))
	})

	handler := Middleware(Config{DefaultTTL: 5 * time.Minute})(backend)

	// Seed.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/age", nil))

	// Hit.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/age", nil))

	if w.Header().Get("Age") == "" {
		t.Fatal("Age header must be set on cache hit")
	}
}

// ---- MemoryStore concurrent access ----

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryStore(100)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		key := string(rune('a' + i%26))
		go func(k string) {
			defer wg.Done()
			resp := &CachedResponse{
				StatusCode: 200,
				Body:       []byte("v"),
				CachedAt:   time.Now(),
				ExpiresAt:  time.Now().Add(time.Minute),
				Size:       1,
			}
			store.Set(k, resp, time.Minute)
		}(key)
		go func(k string) {
			defer wg.Done()
			store.Get(k)
		}(key)
	}
	wg.Wait()
}

// ---- parseCacheControl edge cases ----

func TestParseCacheControl_MaxAgeZero(t *testing.T) {
	ttl, noStore := parseCacheControl("max-age=0")
	if ttl != 0 {
		t.Fatalf("max-age=0 should yield 0 TTL, got %v", ttl)
	}
	if noStore {
		t.Fatal("max-age=0 should not set noStore")
	}
}

func TestParseCacheControl_NoStore(t *testing.T) {
	_, noStore := parseCacheControl("no-store")
	if !noStore {
		t.Fatal("no-store directive should set noStore=true")
	}
}

func TestParseCacheControl_Empty(t *testing.T) {
	ttl, noStore := parseCacheControl("")
	if ttl != 0 || noStore {
		t.Fatalf("empty header: ttl=%v noStore=%v, want 0 false", ttl, noStore)
	}
}

func TestParseCacheControl_MaxAgeParsed(t *testing.T) {
	ttl, noStore := parseCacheControl("max-age=120")
	if ttl != 120*time.Second {
		t.Fatalf("expected 120s, got %v", ttl)
	}
	if noStore {
		t.Fatal("unexpected noStore")
	}
}

// ---- generateETag determinism ----

func TestGenerateETag_Deterministic(t *testing.T) {
	body := []byte("hello world")
	e1 := generateETag(body)
	e2 := generateETag(body)
	if e1 != e2 {
		t.Fatalf("ETag not deterministic: %q != %q", e1, e2)
	}
}

func TestGenerateETag_DifferentBodies(t *testing.T) {
	e1 := generateETag([]byte("body A"))
	e2 := generateETag([]byte("body B"))
	if e1 == e2 {
		t.Fatal("different bodies should produce different ETags")
	}
}

// ---- isCacheableMethod ----

func TestIsCacheableMethod_CaseInsensitive(t *testing.T) {
	if !isCacheableMethod("get", []string{"GET", "HEAD"}) {
		t.Fatal("lower-case 'get' should match 'GET'")
	}
	if isCacheableMethod("POST", []string{"GET", "HEAD"}) {
		t.Fatal("POST should not match")
	}
}

// ---- Stats helper ----

func TestStatsHelper(t *testing.T) {
	store := NewMemoryStore(10)
	s := Stats(store)
	if s.Items != 0 {
		t.Fatalf("expected 0 items, got %d", s.Items)
	}
}

func TestMemoryStoreResizeNonPositive(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		for _, populated := range []bool{false, true} {
			store := NewMemoryStore(2)
			resp := &CachedResponse{ExpiresAt: time.Now().Add(time.Hour), Size: 1}
			if populated {
				store.Set("existing", resp, time.Hour)
			}
			store.Resize(capacity)
			store.Set("discard", resp, time.Hour)
			if stats := store.Stats(); stats.Items != 0 || stats.Size != 0 {
				t.Fatalf("capacity=%d populated=%t: %+v", capacity, populated, stats)
			}
			store.Resize(1)
			store.Set("new", resp, time.Hour)
			if _, ok := store.Get("new"); !ok {
				t.Fatal("cache did not resume after resize")
			}
		}
	}
}

func TestMemoryStoreCleanupPreservesAccounting(t *testing.T) {
	store := NewMemoryStore(3)
	now := time.Now()
	expired, valid := now.Add(-time.Hour), now.Add(time.Hour)
	put := func(key, body string, expires time.Time) {
		store.Set(key, &CachedResponse{Body: []byte(body), Size: int64(len(body)), ExpiresAt: expires}, 0)
	}
	put("expired-1", "old", expired)
	put("keep", "valid", valid)
	put("expired-2", "older", expired)

	if removed := store.CleanupExpired(); removed != 2 {
		t.Fatalf("CleanupExpired = %d, want 2", removed)
	}
	if stats := store.Stats(); stats.Items != 1 || stats.Size != 5 || stats.Evictions != 0 {
		t.Errorf("stats after cleanup = %+v", stats)
	}
	if got, ok := store.Get("keep"); !ok || string(got.Body) != "valid" {
		t.Fatalf("surviving response = %+v, %t", got, ok)
	}
	if _, ok := store.Get("expired-1"); ok {
		t.Error("expired response is still present")
	}
	put("oldest", "one", valid)
	put("newer", "two", valid)
	store.Get("keep")
	put("newest", "three", valid)
	if _, ok := store.Get("oldest"); ok {
		t.Error("oldest remaining entry was not evicted")
	}
	if stats := store.Stats(); stats.Items != 3 || stats.Size != 13 || stats.Evictions != 1 {
		t.Errorf("stats after eviction = %+v", stats)
	}
}
