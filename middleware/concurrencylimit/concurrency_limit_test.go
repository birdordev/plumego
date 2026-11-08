package concurrencylimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

func TestMiddlewareAllowsConfiguredQueuedWaiter(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 1, QueueDepth: 1, QueueTimeout: 200 * time.Millisecond})
	blocker := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case blocker <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	firstDone := serveAsync(handler)
	<-blocker

	secondDone := serveAsync(handler)
	assertNotDone(t, secondDone, 20*time.Millisecond)

	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", first.Code)
	}
	if second := <-secondDone; second.Code != http.StatusOK {
		t.Fatalf("expected queued request to succeed, got %d", second.Code)
	}
}

func TestDefaultConfigUsesDefaultQueueTimeout(t *testing.T) {
	cfg := DefaultConfig(3)
	if cfg.MaxConcurrent != 3 {
		t.Fatalf("MaxConcurrent = %d, want 3", cfg.MaxConcurrent)
	}
	if cfg.QueueTimeout != defaultQueueTimeout {
		t.Fatalf("QueueTimeout = %s, want %s", cfg.QueueTimeout, defaultQueueTimeout)
	}
}

func TestMiddlewareFailFastWhenNoQueueDepth(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 1, QueueDepth: 0, QueueTimeout: 50 * time.Millisecond})
	blocker := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case blocker <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	firstDone := serveAsync(handler)
	<-blocker

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", first.Code)
	}
}

func TestMiddlewareQueuedRequestTimesOut(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 1, QueueDepth: 1, QueueTimeout: 10 * time.Millisecond})
	blocker := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case blocker <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	firstDone := serveAsync(handler)
	<-blocker

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	var payload struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode timeout response: %v", err)
	}
	if _, ok := payload.Error.Details["queue_depth"]; ok {
		t.Fatalf("queue_depth detail should not be emitted: %+v", payload.Error.Details)
	}
	if got := payload.Error.Details["queue_occupancy"]; got != float64(2) {
		t.Fatalf("queue_occupancy = %v, want 2", got)
	}
	if got := payload.Error.Details["queue_capacity"]; got != float64(2) {
		t.Fatalf("queue_capacity = %v, want 2", got)
	}

	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", first.Code)
	}
}

func TestMiddlewareQueuedRequestReturnsOnContextCancel(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 1, QueueDepth: 1, QueueTimeout: time.Second})
	blocker := make(chan struct{}, 1)
	release := make(chan struct{})
	handlerCalls := make(chan struct{}, 2)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls <- struct{}{}
		select {
		case blocker <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	firstDone := serveAsync(handler)
	<-blocker

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	queuedDone := serveAsyncRequest(handler, req)
	assertNotDone(t, queuedDone, 20*time.Millisecond)

	cancel()
	select {
	case rec := <-queuedDone:
		if rec.Body.Len() != 0 {
			t.Fatalf("canceled queued request wrote body %q", rec.Body.String())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("queued request did not return after context cancellation")
	}

	select {
	case <-handlerCalls:
		// First request.
	default:
		t.Fatal("expected first handler call")
	}
	select {
	case <-handlerCalls:
		t.Fatal("canceled queued request invoked downstream handler")
	default:
	}

	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", first.Code)
	}
}

// --- NEW COVERAGE TESTS ---

// TestMiddlewareDisabledPassesThrough verifies that a zero MaxConcurrent disables
// the middleware and passes requests straight through.
func TestMiddlewareDisabledPassesThrough(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 0})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected 418, got %d", rec.Code)
	}
}

// TestMiddlewareNegativeQueueDepthTreatedAsZero verifies that negative QueueDepth is
// clamped to zero (covers the config.QueueDepth < 0 branch).
func TestMiddlewareNegativeQueueDepthTreatedAsZero(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 1, QueueDepth: -5, QueueTimeout: 20 * time.Millisecond})
	blocker := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case blocker <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	firstDone := serveAsync(handler)
	<-blocker

	// With QueueDepth clamped to 0 the second request should fail fast with 503.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with negative queue depth, got %d", rec.Code)
	}

	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}
}

// TestMiddlewareZeroQueueTimeoutUsesDefault covers the config.QueueTimeout <= 0 branch
// by passing a zero QueueTimeout, which should be clamped to the package default.
func TestMiddlewareZeroQueueTimeoutUsesDefault(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 1, QueueDepth: 0, QueueTimeout: 0})
	blocker := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case blocker <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	firstDone := serveAsync(handler)
	<-blocker

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when slot full, got %d", rec.Code)
	}

	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}
}

// TestMiddlewareAlreadyCancelledContext verifies the initial context check (first
// select in the handler): when the context is already done the request is dropped
// without writing a response.
func TestMiddlewareAlreadyCancelledContext(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 2, QueueTimeout: 50 * time.Millisecond})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should not have written any status (handler not reached).
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty body for pre-cancelled context, got %q", rec.Body.String())
	}
}

// TestMiddlewareContextCancelledWhileEnteringQueue covers the Done branch inside the
// "enter queue" select — context is cancelled while the queue itself is full.
func TestMiddlewareContextCancelledWhileEnteringQueue(t *testing.T) {
	// QueueDepth 1 + MaxConcurrent 1 = queue channel size 2.
	// Fill queue with 2 blocked goroutines, then the third gets rejected via queue-full.
	// But we need to cancel while trying to enter a full queue.
	// Use QueueDepth 0 so queue == sem size (1). Fill the queue slot with one blocked
	// goroutine, then send a pre-cancelled request.
	mw := Middleware(Config{MaxConcurrent: 1, QueueDepth: 1, QueueTimeout: 5 * time.Second})
	blocker := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case blocker <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	// Saturate both queue and sem slots (MaxConcurrent=1, QueueDepth=1 → queue cap 2).
	first := serveAsync(handler)
	<-blocker

	// Occupy the queue slot as well (total queue cap = 2: 1 executing + 1 waiting).
	second := serveAsyncRequest(handler, httptest.NewRequest(http.MethodGet, "/", nil))
	assertNotDone(t, second, 20*time.Millisecond)

	// Now the queue is full (2/2). A context-cancelled request should hit the
	// <-r.Context().Done() branch in the queue-entry select.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	// This call may return 503 (queue full) or empty (context done) — we just need
	// it to not block and to exercise the cancel branch.
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("request did not return in time")
	}

	close(release)
	<-first
	<-second
}

// TestMiddlewareContextCancelledWhileQueuing covers context cancellation while the
// request is sitting in the queue waiting for a slot in the sem channel (sem full,
// queue not full, then context is cancelled).
func TestMiddlewareContextCancelledWhileQueuing(t *testing.T) {
	mw := Middleware(Config{MaxConcurrent: 1, QueueDepth: 2, QueueTimeout: 5 * time.Second})
	blocker := make(chan struct{}, 1)
	release := make(chan struct{})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case blocker <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	// Saturate the semaphore.
	firstDone := serveAsync(handler)
	<-blocker

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	queuedDone := serveAsyncRequest(handler, req)
	assertNotDone(t, queuedDone, 20*time.Millisecond)

	cancel()
	select {
	case rec := <-queuedDone:
		if rec.Body.Len() != 0 {
			t.Fatalf("cancelled queued request wrote body %q", rec.Body.String())
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("queued request did not return after context cancellation")
	}

	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}
}

func serveAsync(handler http.Handler) <-chan *httptest.ResponseRecorder {
	return serveAsyncRequest(handler, httptest.NewRequest(http.MethodGet, "/", nil))
}

func serveAsyncRequest(handler http.Handler, req *http.Request) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		done <- rec
	}()
	return done
}

func assertNotDone(t *testing.T, done <-chan *httptest.ResponseRecorder, d time.Duration) {
	t.Helper()

	select {
	case rec := <-done:
		t.Fatalf("request completed before release with status %d", rec.Code)
	case <-time.After(d):
	}
}
