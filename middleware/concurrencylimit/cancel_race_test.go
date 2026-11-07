package concurrencylimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// cancelAtAdmission makes cancellation and slot admission ready together.
type cancelAtAdmission struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelAtAdmission) Done() <-chan struct{} {
	c.checks++
	if c.checks == 3 {
		c.cancel()
	}
	return c.Context.Done()
}

func TestCancellationWinsSlotAdmission(t *testing.T) {
	calls := 0
	handler := Middleware(Config{MaxConcurrent: 1, QueueDepth: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		controlled := &cancelAtAdmission{Context: ctx, cancel: cancel}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(controlled))
		cancel()
		if rec.Body.Len() != 0 || len(rec.Header()) != 0 {
			t.Fatal("canceled admission wrote a response")
		}
	}
	if calls != 0 {
		t.Fatalf("canceled requests invoked downstream %d times", calls)
	}
	// Canceled admissions must return both their queue and worker slots.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if calls != 1 {
		t.Fatalf("valid request calls=%d, want 1", calls)
	}
}

func TestPanicReturnsAdmissionSlots(t *testing.T) {
	calls := 0
	handler := Middleware(Config{MaxConcurrent: 1, QueueDepth: 0})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			panic("handler panic")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	func() {
		defer func() {
			if value := recover(); value != "handler panic" {
				t.Errorf("panic = %v", value)
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if calls != 2 || rec.Code != http.StatusAccepted {
		t.Fatalf("calls=%d status=%d, slots were not returned", calls, rec.Code)
	}
}
