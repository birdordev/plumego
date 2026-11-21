package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func BenchmarkRegisterColdRoutes(b *testing.B) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	paths := make([]string, 200)
	for i := range paths {
		paths[i] = fmt.Sprintf("/page-%03d", i)
	}
	for _, capacity := range []int{100, 4096} {
		b.Run(fmt.Sprint(capacity), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r := NewRouter(WithCacheCapacity(capacity))
				for _, path := range paths {
					if err := r.AddRoute(http.MethodGet, path, handler); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// Benchmarks for optimized router performance.
// Run with: go test -bench=BenchmarkOpt -benchmem -count=3 ./router/

// BenchmarkOptStaticRoute benchmarks static route matching (no params).
func BenchmarkOptStaticRoute(b *testing.B) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/about", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mustAddRoute(r, http.MethodGet, "/contact", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mustAddRoute(r, http.MethodGet, "/api/v1/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/about", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}

// BenchmarkOptSingleParam benchmarks single-parameter route matching.
func BenchmarkOptSingleParam(b *testing.B) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/users/123", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}

// BenchmarkOptMultiParam benchmarks multi-parameter route matching.
func BenchmarkOptMultiParam(b *testing.B) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id/posts/:postId", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/users/123/posts/456", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}

// BenchmarkOptParamRoute captures the hot parameterized dispatch paths that
// justify the current match cache and pooling helpers.
func BenchmarkOptParamRoute(b *testing.B) {
	b.Run("cache_hit", func(b *testing.B) {
		r := NewRouter(withCacheCapacity(16))
		mustAddRoute(r, http.MethodGet, "/users/:id/posts/:postId", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if Param(req, "id") == "" || Param(req, "postId") == "" {
				b.Fatalf("expected route params")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/users/123/posts/456", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("warm request status = %d, want %d", w.Code, http.StatusOK)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			w.Body.Reset()
			r.ServeHTTP(w, req)
		}
	})

	b.Run("cache_miss", func(b *testing.B) {
		r := NewRouter(withCacheCapacity(1))
		mustAddRoute(r, http.MethodGet, "/users/:id/posts/:postId", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if Param(req, "id") == "" || Param(req, "postId") == "" {
				b.Fatalf("expected route params")
			}
			w.WriteHeader(http.StatusOK)
		}))

		reqs := []*http.Request{
			httptest.NewRequest("GET", "/users/123/posts/456", nil),
			httptest.NewRequest("GET", "/users/456/posts/789", nil),
		}
		w := httptest.NewRecorder()

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			w.Body.Reset()
			r.ServeHTTP(w, reqs[i%len(reqs)])
		}
	})
}

// BenchmarkOptWildcard benchmarks wildcard route matching.
func BenchmarkOptWildcard(b *testing.B) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/static/*filepath", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/static/css/main.css", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}

// BenchmarkOptManyRoutes benchmarks matching in a router with many registered routes.
func BenchmarkOptManyRoutes(b *testing.B) {
	r := NewRouter()

	// Register 50 static routes
	for i := 0; i < 50; i++ {
		path := fmt.Sprintf("/api/v1/resource%d", i)
		mustAddRoute(r, http.MethodGet, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	}
	// Register 20 parameterized routes
	for i := 0; i < 20; i++ {
		path := fmt.Sprintf("/api/v1/entity%d/:id", i)
		mustAddRoute(r, http.MethodGet, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
	}

	paths := []string{
		"/api/v1/resource0",
		"/api/v1/resource25",
		"/api/v1/resource49",
		"/api/v1/entity0/123",
		"/api/v1/entity10/456",
		"/api/v1/entity19/789",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := paths[i%len(paths)]
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}
}

// BenchmarkOptDeepPath benchmarks deeply nested path matching.
func BenchmarkOptDeepPath(b *testing.B) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/api/v1/users/:userId/orgs/:orgId/teams/:teamId/members/:memberId",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest("GET", "/api/v1/users/1/orgs/2/teams/3/members/4", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		r.ServeHTTP(w, req)
	}
}

// BenchmarkOptNormalizePath benchmarks path normalization.
func BenchmarkOptNormalizePath(b *testing.B) {
	paths := []string{
		"/",
		"/users",
		"/users/123",
		"/api/v1/users/123/posts/456",
		"/static/css/main.css",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := paths[i%len(paths)]
		fastNormalizePath(p)
	}
}

// BenchmarkOptCacheKey benchmarks the inlined cache key construction used in ServeHTTP.
func BenchmarkOptCacheKey(b *testing.B) {
	methods := []string{"GET", "POST", "PUT"}
	paths := []string{"/users", "/users/123", "/api/v1/health"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := methods[i%len(methods)]
		p := paths[i%len(paths)]
		_ = m + ":" + p
	}
}

// BenchmarkOptSplitPath benchmarks request path splitting.
func BenchmarkOptSplitPath(b *testing.B) {
	paths := []string{
		"/users/123",
		"/api/v1/users/123/posts/456",
		"/static/css/main.css",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := paths[i%len(paths)]
		parts := splitPathToParts(p)
		putPathParts(parts)
	}
}

// BenchmarkOptParallelStatic benchmarks static route matching under contention.
func BenchmarkOptParallelStatic(b *testing.B) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/about", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r.Freeze()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest("GET", "/about", nil)
		w := httptest.NewRecorder()
		for pb.Next() {
			w.Body.Reset()
			r.ServeHTTP(w, req)
		}
	})
}

// BenchmarkOptParallelParam benchmarks parameterized route matching under contention.
func BenchmarkOptParallelParam(b *testing.B) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r.Freeze()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest("GET", "/users/123", nil)
		w := httptest.NewRecorder()
		for pb.Next() {
			w.Body.Reset()
			r.ServeHTTP(w, req)
		}
	})
}

// BenchmarkOptFrozenVsUnfrozen compares trie-traversal cost with and without
// the RLock that is skipped after Freeze(). Cache miss is forced by using a
// cache of capacity 1 and rotating between two URLs.
func BenchmarkOptFrozenVsUnfrozen(b *testing.B) {
	newRouter := func() *Router {
		r := NewRouter(withCacheCapacity(1))
		mustAddRoute(r, http.MethodGet, "/api/v1/users/:id", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		return r
	}
	reqs := []*http.Request{
		httptest.NewRequest("GET", "/api/v1/users/1", nil),
		httptest.NewRequest("GET", "/api/v1/users/2", nil),
	}

	b.Run("unfrozen", func(b *testing.B) {
		r := newRouter()
		w := httptest.NewRecorder()
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				w.Body.Reset()
				r.ServeHTTP(w, reqs[i%len(reqs)])
				i++
			}
		})
	})

	b.Run("frozen", func(b *testing.B) {
		r := newRouter()
		r.Freeze()
		w := httptest.NewRecorder()
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				w.Body.Reset()
				r.ServeHTTP(w, reqs[i%len(reqs)])
				i++
			}
		})
	})
}

func BenchmarkRouteCachePathLength(b *testing.B) {
	for _, length := range []int{16, 256, 2048} {
		for _, distinct := range []int{1, defaultCacheCapacity + 1} {
			b.Run(fmt.Sprintf("length=%d/paths=%d", length, distinct), func(b *testing.B) {
				r := NewRouter()
				if err := r.AddRoute(http.MethodGet, "/items/:id", http.NotFoundHandler()); err != nil {
					b.Fatal(err)
				}
				r.Freeze()
				paths := make([]string, distinct)
				for i := range paths {
					paths[i] = fmt.Sprintf("/items/%s%d", strings.Repeat("a", length), i)
				}
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					result := r.cachedMatch(http.MethodGet, paths[i%len(paths)])
					if result == nil || result.RoutePattern != "/items/:id" {
						b.Fatal("cache changed route selection")
					}
					i++
				}
			})
		}
	}
}

func BenchmarkFrozenAllowLookup(b *testing.B) {
	for _, methods := range []int{2, 16, 64} {
		b.Run(fmt.Sprint(methods), func(b *testing.B) {
			r := NewRouter(WithMethodNotAllowed(true))
			for i := range methods {
				name := fmt.Sprintf("CUSTOM%02d", i)
				if err := r.AddRoute(name, "/docs/fixed/dead", http.NotFoundHandler()); err != nil {
					b.Fatal(err)
				}
				if err := r.AddRoute(name, "/docs/:id/live", http.NotFoundHandler()); err != nil {
					b.Fatal(err)
				}
			}
			r.Freeze()
			miss := httptest.NewRecorder()
			r.ServeHTTP(miss, httptest.NewRequest(http.MethodGet, "/other", nil))
			if miss.Code != http.StatusNotFound {
				b.Fatal(miss.Code)
			}
			req := httptest.NewRequest(http.MethodGet, "/docs/fixed/live", nil)
			b.ReportAllocs()
			for b.Loop() {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				allow := w.Header().Get("Allow")
				if w.Code != http.StatusMethodNotAllowed || strings.Count(allow, ",")+1 != methods ||
					!strings.HasPrefix(allow, "CUSTOM00, CUSTOM01") {
					b.Fatalf("status=%d allow=%q", w.Code, allow)
				}
			}
		})
	}
}

func TestRouteStringPoolDiscardsDeepSlicesAndClearsTail(t *testing.T) {
	parts := splitPathToParts(strings.Repeat("/segment", maxPooledRouteSegments+1))
	if len(*parts) != maxPooledRouteSegments+1 {
		t.Fatalf("segments = %d", len(*parts))
	}
	putPathParts(parts)
	if *parts != nil {
		t.Fatal("deep path storage was retained")
	}
	pool := new(sync.Pool)
	tail := []string{"kept", "stale"}
	short := tail[:1]
	putRouteStrings(pool, &short)
	if len(short) != 0 || tail[0] != "" || tail[1] != "" {
		t.Fatal("pooled storage retained path references")
	}
}

func BenchmarkPathPoolDeepPaths(b *testing.B) {
	for _, segments := range []int{8, maxPooledRouteSegments, maxPooledRouteSegments + 1, 256} {
		b.Run(fmt.Sprint(segments), func(b *testing.B) {
			path := strings.Repeat("/segment", segments)
			b.ReportAllocs()
			for b.Loop() {
				putPathParts(splitPathToParts(path))
			}
		})
	}
}

func BenchmarkRoutePathPoolBoundary(b *testing.B) {
	for _, segments := range []int{63, 64, 65} {
		for _, warm := range []bool{false, true} {
			b.Run(fmt.Sprintf("segments=%d/warm=%t", segments, warm), func(b *testing.B) {
				r := NewRouter(WithCacheCapacity(1))
				path := strings.Repeat("/fixture", segments-1)
				h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if Param(req, "tail") != "value" {
						b.Fatal("route metadata changed")
					}
					w.WriteHeader(http.StatusNoContent)
				})
				if err := r.AddRoute("GET", path+"/:tail", h); err != nil {
					b.Fatal(err)
				}
				req := httptest.NewRequest("GET", path+"/value", nil)
				rec := httptest.NewRecorder()
				b.ReportAllocs()
				for b.Loop() {
					if !warm {
						r.state.matchCache.Clear()
					}
					r.ServeHTTP(rec, req)
				}
				if rec.Code != 204 {
					b.Fatalf("status=%d", rec.Code)
				}
			})
		}
	}
}
