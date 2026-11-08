package abuseguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	internaltransport "github.com/birdordev/plumego/internal/httputil"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/security/abuse"
)

type panicLogger struct{}

func (panicLogger) WithFields(log.Fields) log.StructuredLogger      { return panicLogger{} }
func (panicLogger) With(string, any) log.StructuredLogger           { return panicLogger{} }
func (panicLogger) Debug(string, ...log.Fields)                     {}
func (panicLogger) Info(string, ...log.Fields)                      {}
func (panicLogger) Warn(string, ...log.Fields)                      { panic("logger panic") }
func (panicLogger) Error(string, ...log.Fields)                     {}
func (panicLogger) DebugCtx(context.Context, string, ...log.Fields) {}
func (panicLogger) InfoCtx(context.Context, string, ...log.Fields)  {}
func (panicLogger) WarnCtx(context.Context, string, ...log.Fields)  { panic("logger panic") }
func (panicLogger) ErrorCtx(context.Context, string, ...log.Fields) {}
func (panicLogger) Fatal(string, ...log.Fields)                     {}
func (panicLogger) FatalCtx(context.Context, string, ...log.Fields) {}

type abuseClock struct {
	now time.Time
}

func (c *abuseClock) Now() time.Time {
	return c.now
}

func (c *abuseClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func newTestMiddleware(t *testing.T, config AbuseGuardConfig) func(http.Handler) http.Handler {
	t.Helper()

	guard := NewAbuseGuard(config)
	t.Cleanup(guard.Stop)
	return guard.Middleware()
}

func TestAbuseGuardBlocks(t *testing.T) {
	clock := &abuseClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	limiter := abuse.NewLimiter(abuse.Config{
		Rate:            1,
		Capacity:        1,
		Now:             clock.Now,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
	})
	defer limiter.Stop()

	include := true
	mw := newTestMiddleware(t, AbuseGuardConfig{
		Limiter:        limiter,
		KeyFunc:        func(*http.Request) string { return "client" },
		IncludeHeaders: &include,
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get(headerRateLimitRemaining); got != "0" {
		t.Fatalf("expected remaining 0, got %q", got)
	}

	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)
	resp = w.Result()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get(headerRetryAfter); got == "" {
		t.Fatalf("expected Retry-After header")
	}
}

func TestNewAbuseGuardMiddlewareStopIsIdempotent(t *testing.T) {
	guard := NewAbuseGuard(AbuseGuardConfig{
		Rate:            1,
		Capacity:        1,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
		KeyFunc:         func(*http.Request) string { return "client" },
	})
	defer guard.Stop()

	wrapped := guard.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	guard.Stop()
	guard.Stop()
}

func TestNewAbuseGuardProductionPathBlocksAndStopsOwnedLimiter(t *testing.T) {
	guard := NewAbuseGuard(AbuseGuardConfig{
		Rate:            1,
		Capacity:        1,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
		KeyFunc:         func(*http.Request) string { return "client" },
	})
	defer guard.Stop()

	wrapped := guard.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}

	guard.Stop()
	guard.Stop()
}

func TestNewAbuseGuardWithInjectedLimiterLeavesStopToCaller(t *testing.T) {
	limiter := abuse.NewLimiter(abuse.Config{
		Rate:            1,
		Capacity:        1,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
	})
	defer limiter.Stop()

	guard := NewAbuseGuard(AbuseGuardConfig{Limiter: limiter})
	guard.Stop()

	rec := httptest.NewRecorder()
	guard.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
}

func TestAbuseGuardSkip(t *testing.T) {
	clock := &abuseClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	limiter := abuse.NewLimiter(abuse.Config{
		Rate:            1,
		Capacity:        1,
		Now:             clock.Now,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
	})
	defer limiter.Stop()

	include := true
	mw := newTestMiddleware(t, AbuseGuardConfig{
		Limiter:        limiter,
		KeyFunc:        func(*http.Request) string { return "client" },
		IncludeHeaders: &include,
		Skip:           func(*http.Request) bool { return true },
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	}

	decision := limiter.Allow("client")
	if !decision.Allowed {
		t.Fatalf("expected limiter bucket to be untouched by Skip")
	}
}

func TestAbuseGuardHeaderToggle(t *testing.T) {
	clock := &abuseClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	limiter := abuse.NewLimiter(abuse.Config{
		Rate:            1,
		Capacity:        2,
		Now:             clock.Now,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
	})
	defer limiter.Stop()

	include := false
	mw := newTestMiddleware(t, AbuseGuardConfig{
		Limiter:        limiter,
		KeyFunc:        func(*http.Request) string { return "client" },
		IncludeHeaders: &include,
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if got := rec.Header().Get(headerRateLimitLimit); got != "" {
		t.Fatalf("expected rate limit headers to be omitted, got %q", got)
	}
}

func TestAbuseGuardDefaultKeyIgnoresSpoofedForwardedFor(t *testing.T) {
	clock := &abuseClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	limiter := abuse.NewLimiter(abuse.Config{
		Rate:            1,
		Capacity:        1,
		Now:             clock.Now,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
	})
	defer limiter.Stop()

	mw := newTestMiddleware(t, AbuseGuardConfig{Limiter: limiter})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	first.RemoteAddr = "9.9.9.9:1234"
	first.Header.Set(internaltransport.HeaderForwardedFor, "1.1.1.1")
	first.Header.Set(internaltransport.HeaderRealIP, "2.2.2.2")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, first)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec.Code)
	}

	second := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	second.RemoteAddr = "9.9.9.9:1234"
	second.Header.Set(internaltransport.HeaderForwardedFor, "3.3.3.3")
	second.Header.Set(internaltransport.HeaderRealIP, "4.4.4.4")
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, second)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rec.Code)
	}
}

func TestAbuseGuardBlankCustomKeyFallsBackToDirectClientIP(t *testing.T) {
	clock := &abuseClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	limiter := abuse.NewLimiter(abuse.Config{
		Rate:            1,
		Capacity:        1,
		Now:             clock.Now,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
	})
	defer limiter.Stop()

	mw := newTestMiddleware(t, AbuseGuardConfig{
		Limiter: limiter,
		KeyFunc: func(*http.Request) string { return " \t " },
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	first.RemoteAddr = "9.9.9.9:1234"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, first)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec.Code)
	}

	second := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	second.RemoteAddr = "8.8.8.8:1234"
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, second)
	if rec.Code != http.StatusOK {
		t.Fatalf("second request status = %d, want 200 for different fallback peer", rec.Code)
	}

	third := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	third.RemoteAddr = "9.9.9.9:9999"
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, third)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request status = %d, want 429 for same fallback peer", rec.Code)
	}
}

func TestAbuseGuardLoggerPanicDoesNotEscapeRateLimitResponse(t *testing.T) {
	clock := &abuseClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	limiter := abuse.NewLimiter(abuse.Config{
		Rate:            1,
		Capacity:        1,
		Now:             clock.Now,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
	})
	defer limiter.Stop()

	mw := newTestMiddleware(t, AbuseGuardConfig{
		Limiter: limiter,
		KeyFunc: func(*http.Request) string { return "client" },
		Logger:  panicLogger{},
	})
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	if got := internaltransport.ClientIP(nil); got != "" {
		t.Fatalf("expected empty key for nil request, got %q", got)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set(internaltransport.HeaderForwardedFor, " 1.1.1.1 , 2.2.2.2 ")
	req.RemoteAddr = "9.9.9.9:1234"
	if got := internaltransport.ClientIP(req); got != "1.1.1.1" {
		t.Fatalf("expected X-Forwarded-For to win, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set(internaltransport.HeaderRealIP, " 3.3.3.3 ")
	req.RemoteAddr = "9.9.9.9:1234"
	if got := internaltransport.ClientIP(req); got != "3.3.3.3" {
		t.Fatalf("expected X-Real-IP to win, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.RemoteAddr = "5.5.5.5:8080"
	if got := internaltransport.ClientIP(req); got != "5.5.5.5" {
		t.Fatalf("expected host part from RemoteAddr, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.RemoteAddr = "6.6.6.6"
	if got := internaltransport.ClientIP(req); got != "6.6.6.6" {
		t.Fatalf("expected raw RemoteAddr when no port, got %q", got)
	}
}

func BenchmarkAbuseGuardMiddleware(b *testing.B) {
	limit := b.N + 1
	limiter := abuse.NewLimiter(abuse.Config{
		Rate:            float64(limit),
		Capacity:        limit,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Hour,
	})
	defer limiter.Stop()

	include := true
	guard := NewAbuseGuard(AbuseGuardConfig{
		Limiter:        limiter,
		KeyFunc:        func(*http.Request) string { return "client" },
		IncludeHeaders: &include,
	})
	defer guard.Stop()
	mw := guard.Middleware()
	wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}
}
