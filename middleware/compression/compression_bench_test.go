package compression

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func BenchmarkCompressionDisabled(b *testing.B) {
	mw := Middleware(DefaultConfig())
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	handler := mw(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "identity")
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		*w = httptest.ResponseRecorder{Body: w.Body}
		w.Body.Reset()
		handler.ServeHTTP(w, req)
	}
}

func BenchmarkCompressionEnabled(b *testing.B) {
	mw := Middleware(Config{MaxBufferBytes: 10 << 20})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","data":"hello world"}`))
	})
	handler := mw(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		*w = httptest.ResponseRecorder{Body: w.Body}
		w.Body.Reset()
		handler.ServeHTTP(w, req)
		if w.Header().Get("Content-Encoding") != "gzip" {
			b.Fatal("expected gzip response")
		}
	}
}

func BenchmarkCompressionSkipBinary(b *testing.B) {
	mw := Middleware(Config{MaxBufferBytes: 10 << 20})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake image bytes"))
	})
	handler := mw(inner)

	req := httptest.NewRequest("GET", "/test.png", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		*w = httptest.ResponseRecorder{Body: w.Body}
		w.Body.Reset()
		handler.ServeHTTP(w, req)
	}
}

func BenchmarkCompressionLargePayload(b *testing.B) {
	mw := Middleware(Config{MaxBufferBytes: 10 << 20})
	payload := strings.Repeat("hello world ", 1000) // ~12KB
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	})
	handler := mw(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		*w = httptest.ResponseRecorder{Body: w.Body}
		w.Body.Reset()
		handler.ServeHTTP(w, req)
		if w.Header().Get("Content-Encoding") != "gzip" {
			b.Fatal("expected gzip response")
		}
	}
}
