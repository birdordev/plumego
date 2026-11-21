package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// trace records the execution order of middleware and handler labels.
type trace []string

func (t *trace) mark(label string) {
	*t = append(*t, label)
}

func (t trace) assertEqual(tb testing.TB, want ...string) {
	tb.Helper()
	if len(t) != len(want) {
		tb.Fatalf("trace length mismatch: got %v, want %v", []string(t), want)
	}
	for i := range want {
		if t[i] != want[i] {
			tb.Fatalf("trace[%d]: got %q, want %q (full trace: %v)", i, t[i], want[i], []string(t))
		}
	}
}

// tracer returns a Middleware that appends label to tr before calling next.
func tracer(tr *trace, label string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tr.mark(label)
			next.ServeHTTP(w, r)
		})
	}
}

// tracingHandler returns an http.Handler that appends label to tr.
func tracingHandler(tr *trace, label string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr.mark(label)
	})
}

func serve(h http.Handler) {
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

// TestChainNewChainRegistrationEqualsExecutionOrder verifies that NewChain(A, B, C)
// produces execution order A → B → C → handler.
func TestChainNewChainRegistrationEqualsExecutionOrder(t *testing.T) {
	var tr trace
	chain := NewChain(
		tracer(&tr, "A"),
		tracer(&tr, "B"),
		tracer(&tr, "C"),
	)
	serve(chain.Build(tracingHandler(&tr, "handler")))
	tr.assertEqual(t, "A", "B", "C", "handler")
}

// TestChainUseAppendsInRegistrationOrder verifies that successive Use calls
// append middleware that execute after previously registered middleware.
func TestChainUseAppendsInRegistrationOrder(t *testing.T) {
	var tr trace
	chain := NewChain(tracer(&tr, "A"))
	chain.Use(tracer(&tr, "B"))
	chain.Use(tracer(&tr, "C"))
	serve(chain.Build(tracingHandler(&tr, "handler")))
	tr.assertEqual(t, "A", "B", "C", "handler")
}

// TestChainBuildIsImmutableSnapshot verifies that calling Use after Build
// does not affect the already-built handler.
func TestChainBuildIsImmutableSnapshot(t *testing.T) {
	var tr trace
	chain := NewChain(tracer(&tr, "A"))
	h := chain.Build(tracingHandler(&tr, "handler"))

	// Add B after snapshot is taken.
	chain.Use(tracer(&tr, "B"))

	serve(h)
	// B must not appear: the snapshot was taken before Use("B").
	tr.assertEqual(t, "A", "handler")
}

func TestChainSnapshotReturnsCopy(t *testing.T) {
	var tr trace
	chain := NewChain(tracer(&tr, "A"))

	snapshot := chain.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected snapshot length 1, got %d", len(snapshot))
	}
	snapshot[0] = tracer(&tr, "mutated")

	serve(chain.Build(tracingHandler(&tr, "handler")))
	tr.assertEqual(t, "A", "handler")
}

// TestChainLen verifies Len reflects the number of registered middlewares.
func TestChainLen(t *testing.T) {
	chain := NewChain()
	if chain.Len() != 0 {
		t.Fatalf("expected 0, got %d", chain.Len())
	}
	noop := func(next http.Handler) http.Handler { return next }
	chain.Use(noop).Use(noop).Use(noop)
	if chain.Len() != 3 {
		t.Fatalf("expected 3, got %d", chain.Len())
	}
}

// TestChainBuildEmptyChainPassesThrough verifies that an empty chain with no
// middlewares built around a handler calls the handler directly.
func TestChainBuildEmptyChainPassesThrough(t *testing.T) {
	var tr trace
	chain := NewChain()
	serve(chain.Build(tracingHandler(&tr, "handler")))
	tr.assertEqual(t, "handler")
}

// TestChainBuildTwiceProducesIdenticalOrder verifies that calling Build
// multiple times on the same chain produces the same execution order.
func TestChainBuildTwiceProducesIdenticalOrder(t *testing.T) {
	for range 3 {
		var tr trace
		chain := NewChain(tracer(&tr, "A"), tracer(&tr, "B"))
		serve(chain.Build(tracingHandler(&tr, "handler")))
		tr.assertEqual(t, "A", "B", "handler")
	}
}

// Benchmarks

func noopMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

var benchHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

func BenchmarkChainBuild(b *testing.B) {
	sizes := []int{1, 5, 10, 20}
	for _, n := range sizes {
		mws := make([]Middleware, n)
		for i := range mws {
			mws[i] = noopMiddleware
		}
		b.Run(benchName(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				NewChain(mws...).Build(benchHandler)
			}
		})
	}
}

func BenchmarkChainServeHTTP(b *testing.B) {
	sizes := []int{1, 5, 10, 20}
	req := httptest.NewRequest("GET", "/", nil)
	for _, n := range sizes {
		mws := make([]Middleware, n)
		for i := range mws {
			mws[i] = noopMiddleware
		}
		handler := NewChain(mws...).Build(benchHandler)
		b.Run(benchName(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				handler.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}

func benchName(n int) string {
	switch n {
	case 1:
		return "1mw"
	case 5:
		return "5mw"
	case 10:
		return "10mw"
	case 20:
		return "20mw"
	default:
		return "Nmw"
	}
}

func TestChainBuildRejectsNilMiddlewareBeforeCallingBuilders(t *testing.T) {
	builder := func(h http.Handler) http.Handler {
		t.Error("builder called for invalid chain")
		return h
	}
	for _, chain := range []*Chain{NewChain(builder, nil, builder), NewChain(builder).Use(nil)} {
		if got := chain.Build(http.NotFoundHandler()); got != nil {
			t.Fatal("invalid chain returned a handler")
		}
	}
}
