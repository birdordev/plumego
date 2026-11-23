package conformance_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/middleware/concurrencylimit"
	"github.com/birdordev/plumego/middleware/timeout"
)

func TestMiddlewareShortCircuitErrorPathOrder(t *testing.T) {
	order := make([]string, 0, 4)
	handlerCalled := false

	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "outer:before")
			next.ServeHTTP(w, r)
			order = append(order, "outer:after")
		})
	}

	blocker := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "blocker:before")
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeRateLimited).
				Code(contract.CodeRateLimited).
				Message("rate limited").
				Build())
			order = append(order, "blocker:return")
		})
	}

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	middleware.NewChain(outer, blocker).Build(final).ServeHTTP(rec, req)

	assertCanonicalErrorEnvelope(t, rec, contract.CodeRateLimited)
	if handlerCalled {
		t.Fatalf("final handler should not be called on short-circuit error path")
	}

	want := []string{"outer:before", "blocker:before", "blocker:return", "outer:after"}
	if len(order) != len(want) {
		t.Fatalf("order length mismatch: got %v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("unexpected execution order: got %v want %v", order, want)
		}
	}
}

func TestRecoveryCatchesPanicFromDownstreamMiddlewareOrder(t *testing.T) {
	order := make([]string, 0, 4)
	handlerCalled := false

	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "outer:before")
			next.ServeHTTP(w, r)
			order = append(order, "outer:after")
		})
	}

	panicMw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "panic:before")
			panic("boom")
		})
	}

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	middleware.NewChain(outer, newConformanceRecovery(t), panicMw).Build(final).ServeHTTP(rec, req)

	assertCanonicalErrorEnvelope(t, rec, contract.CodeInternalError)
	if handlerCalled {
		t.Fatalf("final handler should not be called when downstream middleware panics")
	}

	want := []string{"outer:before", "panic:before", "outer:after"}
	if len(order) != len(want) {
		t.Fatalf("order length mismatch: got %v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("unexpected execution order: got %v want %v", order, want)
		}
	}
}

func TestTimedOutHandlerKeepsItsConcurrencySlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		canceled := make(chan struct{})
		release := make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		calls := 0
		h := middleware.NewChain(
			timeout.Middleware(timeout.Config{Timeout: time.Second}),
			concurrencylimit.Middleware(concurrencylimit.Config{MaxConcurrent: 1}),
		).Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				close(entered)
				<-r.Context().Done()
				close(canceled)
				<-release
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		first := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { defer close(done); h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/fixture", nil)) }()
		<-entered
		<-done
		<-canceled
		second := httptest.NewRecorder()
		h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/fixture", nil))
		if first.Code != 504 || second.Code != 503 || calls != 1 {
			t.Fatalf("first=%d second=%d calls=%d", first.Code, second.Code, calls)
		}
		close(release)
		synctest.Wait()
		third := httptest.NewRecorder()
		h.ServeHTTP(third, httptest.NewRequest(http.MethodGet, "/fixture", nil))
		if third.Code != http.StatusNoContent || calls != 2 {
			t.Fatalf("third=%d calls=%d", third.Code, calls)
		}
	})
}
