package recovery

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/log"
)

func BenchmarkRecoveryOverhead(b *testing.B) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	mw, err := Middleware(Config{Logger: logger})
	if err != nil {
		b.Fatal(err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		handler.ServeHTTP(w, req)
	}
}

func BenchmarkRecoveryWithPanic(b *testing.B) {
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	mw, err := Middleware(Config{Logger: logger})
	if err != nil {
		b.Fatal(err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	handler := mw(inner)
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		handler.ServeHTTP(w, req)
	}
}
