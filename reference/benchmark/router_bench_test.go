package benchmark_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/birdordev/plumego/router"
	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/labstack/echo/v4"
)

func init() {
	gin.SetMode(gin.ReleaseMode)
}

// ---------------------------------------------------------------------------
// Route shape benchmarks — static / single-param / multi-param
// ---------------------------------------------------------------------------

func BenchmarkRouterStatic(b *testing.B) {
	benchmarkRouteMatch(b, routeBenchCase{
		name:        "static",
		requestPath: "/assets/health",
		plumegoPath: "/assets/health",
		chiPath:     "/assets/health",
		ginPath:     "/assets/health",
		echoPath:    "/assets/health",
	})
}

func BenchmarkRouterSingleParam(b *testing.B) {
	benchmarkRouteMatch(b, routeBenchCase{
		name:        "single-param",
		requestPath: "/users/12345",
		plumegoPath: "/users/:id",
		chiPath:     "/users/{id}",
		ginPath:     "/users/:id",
		echoPath:    "/users/:id",
	})
}

func BenchmarkRouterMultiParam(b *testing.B) {
	benchmarkRouteMatch(b, routeBenchCase{
		name:        "multi-param",
		requestPath: "/orgs/acme/users/42/repos/plumego",
		plumegoPath: "/orgs/:org/users/:userID/repos/:repo",
		chiPath:     "/orgs/{org}/users/{userID}/repos/{repo}",
		ginPath:     "/orgs/:org/users/:userID/repos/:repo",
		echoPath:    "/orgs/:org/users/:userID/repos/:repo",
	})
}

// ---------------------------------------------------------------------------
// Route table scale benchmarks — 100 and 500 routes
// ---------------------------------------------------------------------------

func BenchmarkRouterScale100(b *testing.B) {
	benchmarkRouteScale(b, 100)
}

func BenchmarkRouterScale500(b *testing.B) {
	benchmarkRouteScale(b, 500)
}

// ---------------------------------------------------------------------------
// Parallel throughput benchmarks — single-param route under concurrent load
// ---------------------------------------------------------------------------

func BenchmarkRouterParallelPlumego(b *testing.B) {
	r := newPlumegoRouter(b, "/users/:id")
	req := httptest.NewRequest(http.MethodGet, "/users/12345", nil)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
		}
	})
}

func BenchmarkRouterParallelChi(b *testing.B) {
	r := chi.NewRouter()
	r.Method(http.MethodGet, "/users/{id}", benchmarkNoOpHandler())
	req := httptest.NewRequest(http.MethodGet, "/users/12345", nil)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
		}
	})
}

func BenchmarkRouterParallelGin(b *testing.B) {
	r := gin.New()
	r.GET("/users/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/users/12345", nil)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
		}
	})
}

func BenchmarkRouterParallelEcho(b *testing.B) {
	e := echo.New()
	e.GET("/users/:id", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/users/12345", nil)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
		}
	})
}

// ---------------------------------------------------------------------------
// Not-found (404) overhead benchmarks
// ---------------------------------------------------------------------------

func BenchmarkRouterNotFoundPlumego(b *testing.B) {
	r := newPlumegoRouter(b, "/users/:id")
	runHandlerBenchmark(b, r, "/nonexistent/path", http.StatusNotFound)
}

func BenchmarkRouterNotFoundChi(b *testing.B) {
	r := chi.NewRouter()
	r.Method(http.MethodGet, "/users/{id}", benchmarkNoOpHandler())
	runHandlerBenchmark(b, r, "/nonexistent/path", http.StatusNotFound)
}

func BenchmarkRouterNotFoundGin(b *testing.B) {
	r := gin.New()
	r.GET("/users/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	runHandlerBenchmark(b, r, "/nonexistent/path", http.StatusNotFound)
}

func BenchmarkRouterNotFoundEcho(b *testing.B) {
	e := echo.New()
	e.GET("/users/:id", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	runHandlerBenchmark(b, e, "/nonexistent/path", http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

type routeBenchCase struct {
	name        string
	requestPath string
	plumegoPath string
	chiPath     string
	ginPath     string
	echoPath    string
}

func benchmarkRouteMatch(b *testing.B, tc routeBenchCase) {
	b.Helper()

	b.Run("plumego", func(b *testing.B) {
		r := newPlumegoRouter(b, tc.plumegoPath)
		runHandlerBenchmark(b, r, tc.requestPath, http.StatusNoContent)
	})

	b.Run("chi", func(b *testing.B) {
		r := chi.NewRouter()
		r.Method(http.MethodGet, tc.chiPath, benchmarkNoOpHandler())
		runHandlerBenchmark(b, r, tc.requestPath, http.StatusNoContent)
	})

	b.Run("gin", func(b *testing.B) {
		r := gin.New()
		r.GET(tc.ginPath, func(c *gin.Context) { c.Status(http.StatusNoContent) })
		runHandlerBenchmark(b, r, tc.requestPath, http.StatusNoContent)
	})

	b.Run("echo", func(b *testing.B) {
		e := echo.New()
		e.GET(tc.echoPath, func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
		runHandlerBenchmark(b, e, tc.requestPath, http.StatusNoContent)
	})
}

// benchmarkRouteScale registers n routes and times a dispatch to the last one.
func benchmarkRouteScale(b *testing.B, n int) {
	b.Helper()

	b.Run("plumego", func(b *testing.B) {
		r := router.NewRouter()
		for i := range n {
			if err := r.AddRoute(http.MethodGet, routeScalePath(i), benchmarkNoOpHandler()); err != nil {
				b.Fatalf("add route: %v", err)
			}
		}
		runHandlerBenchmark(b, r, routeScalePath(n-1), http.StatusNoContent)
	})

	b.Run("chi", func(b *testing.B) {
		r := chi.NewRouter()
		for i := range n {
			r.Method(http.MethodGet, routeScalePath(i), benchmarkNoOpHandler())
		}
		runHandlerBenchmark(b, r, routeScalePath(n-1), http.StatusNoContent)
	})

	b.Run("gin", func(b *testing.B) {
		r := gin.New()
		for i := range n {
			idx := i
			r.GET(routeScalePath(idx), func(c *gin.Context) { c.Status(http.StatusNoContent) })
		}
		runHandlerBenchmark(b, r, routeScalePath(n-1), http.StatusNoContent)
	})

	b.Run("echo", func(b *testing.B) {
		e := echo.New()
		for i := range n {
			idx := i
			e.GET(routeScalePath(idx), func(c echo.Context) error {
				_ = idx
				return c.NoContent(http.StatusNoContent)
			})
		}
		runHandlerBenchmark(b, e, routeScalePath(n-1), http.StatusNoContent)
	})
}

// routeScalePath returns a unique parameterized path for route-table scale tests.
func routeScalePath(i int) string {
	return "/api/v1/resource" + strconv.Itoa(i) + "/items/:id"
}

func runHandlerBenchmark(b *testing.B, h http.Handler, requestPath string, wantStatus int) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, requestPath, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			b.Fatalf("unexpected status: got %d want %d", rec.Code, wantStatus)
		}
	}
}

func benchmarkNoOpHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func newPlumegoRouter(b *testing.B, path string) *router.Router {
	b.Helper()
	r := router.NewRouter()
	if err := r.AddRoute(http.MethodGet, path, benchmarkNoOpHandler()); err != nil {
		b.Fatalf("add route %s: %v", path, err)
	}
	return r
}
