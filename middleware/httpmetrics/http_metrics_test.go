package httpmetrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

type stubMetrics struct {
	mu     sync.Mutex
	count  int
	path   string
	status int
	bytes  int
	panic  bool
}

func (m *stubMetrics) ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
	if m.panic {
		panic("metrics panic")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count++
	m.path = path
	m.status = status
	m.bytes = bytes
}

func TestMiddlewareObservesRoutePattern(t *testing.T) {
	collector := &stubMetrics{}
	handler := Middleware(collector)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/users/42", nil)
	req = req.WithContext(contract.WithRequestContext(req.Context(), contract.RequestContext{
		RoutePattern: "/users/:id",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if collector.count != 1 {
		t.Fatalf("expected one metrics observation, got %d", collector.count)
	}
	if collector.path != "/users/:id" {
		t.Fatalf("expected route pattern to be observed, got %q", collector.path)
	}
	if collector.status != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", collector.status)
	}
	if collector.bytes != len("ok") {
		t.Fatalf("expected bytes %d, got %d", len("ok"), collector.bytes)
	}
}

func TestMiddlewareObservesPanicPath(t *testing.T) {
	collector := &stubMetrics{}
	handler := Middleware(collector)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		handler.ServeHTTP(rec, req)
	}()

	if !panicked {
		t.Fatal("expected panic to propagate")
	}
	if collector.count != 1 {
		t.Fatalf("expected one metrics observation, got %d", collector.count)
	}
	if collector.status != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, collector.status)
	}
	if collector.bytes != len("partial") {
		t.Fatalf("expected bytes %d, got %d", len("partial"), collector.bytes)
	}
}

func TestMiddlewarePreservesDownstreamPanicWhenCollectorPanics(t *testing.T) {
	collector := &stubMetrics{panic: true}
	handler := Middleware(collector)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("downstream panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	defer func() {
		if rec := recover(); rec != "downstream panic" {
			t.Fatalf("panic = %v, want downstream panic", rec)
		}
	}()
	handler.ServeHTTP(rec, req)
}

// TestMiddlewareNilCollectorPassthrough exercises the nil-collector branch that
// returns the next handler directly without wrapping.
func TestMiddlewareNilCollectorPassthrough(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler := Middleware(nil)(inner)

	req := httptest.NewRequest(http.MethodGet, "/passthrough", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected inner handler to be called when collector is nil")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
