package concurrencylimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/contract"
)

// admissionContext runs a hook at a particular admission check.
type admissionContext struct {
	context.Context
	onCheck func()
	checkAt int
	checks  int
}

func (c *admissionContext) Done() <-chan struct{} {
	c.checks++
	if c.checks == c.checkAt {
		c.onCheck()
	}
	return c.Context.Done()
}

func TestCancellationWinsSlotAdmission(t *testing.T) {
	calls := 0
	handler := Middleware(Config{MaxConcurrent: 1, QueueDepth: 1})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		controlled := &admissionContext{Context: ctx, onCheck: cancel, checkAt: 3}
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

func TestQueueTimeoutWinsSlotAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const timeout = time.Millisecond
		for range 32 {
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			handler := Middleware(Config{MaxConcurrent: 1, QueueDepth: 1, QueueTimeout: timeout})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/active" {
					close(started)
					<-release
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			go func() {
				defer close(done)
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/active", nil))
			}()
			<-started

			ctx := &admissionContext{
				Context: t.Context(),
				checkAt: 4, // The timed wait evaluates Done after starting its timer.
				onCheck: func() {
					time.Sleep(timeout + time.Nanosecond)
					close(release)
					<-done
				},
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/queued", nil).WithContext(ctx))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("expired request status = %d, want 503", rec.Code)
			}
			var payload struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Error.Code != contract.CodeServerQueueTimeout {
				t.Fatalf("expired request error code = %q", payload.Error.Code)
			}

			later := httptest.NewRecorder()
			handler.ServeHTTP(later, httptest.NewRequest(http.MethodGet, "/later", nil))
			if later.Code != http.StatusNoContent {
				t.Fatalf("later request status = %d, want 204", later.Code)
			}
		}
	})
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
