package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func handlerOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func BenchmarkAppRouteRegistration(b *testing.B) {
	app := New(DefaultConfig(), AppDependencies{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = app.Get("/bench", handlerOK())
	}
}

func BenchmarkAppServeHTTPStatic(b *testing.B) {
	app := New(DefaultConfig(), AppDependencies{})
	_ = app.Get("/api/v1/users", handlerOK())
	_ = app.Post("/api/v1/users", handlerOK())
	_ = app.Get("/health", handlerOK())

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		app.ServeHTTP(w, req)
	}
}

func BenchmarkAppServeHTTPParam(b *testing.B) {
	app := New(DefaultConfig(), AppDependencies{})
	_ = app.Get("/users/:id", handlerOK())

	req := httptest.NewRequest("GET", "/users/123", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		app.ServeHTTP(w, req)
	}
}

func BenchmarkAppServeHTTPWithMiddleware(b *testing.B) {
	app := New(DefaultConfig(), AppDependencies{})
	_ = app.Get("/api/items", handlerOK())

	// Add a minimal passthrough middleware to measure chain overhead
	_ = app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	})

	req := httptest.NewRequest("GET", "/api/items", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		app.ServeHTTP(w, req)
	}
}
