package bodylimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/log"
)

func BenchmarkBodyLimitDisabled(b *testing.B) {
	mw := Middleware(Config{MaxBytes: 0})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	body := strings.NewReader(`{"data":"benchmark payload"}`)
	req := httptest.NewRequest("POST", "/test", body)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		handler.ServeHTTP(w, req)
	}
}

func BenchmarkBodyLimitEnforced(b *testing.B) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	mw := Middleware(Config{MaxBytes: 1 << 20, Logger: logger})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	body := strings.NewReader(`{"data":"benchmark payload"}`)
	req := httptest.NewRequest("POST", "/test", body)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		handler.ServeHTTP(w, req)
	}
}
