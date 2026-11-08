package middleware_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/birdordev/plumego/middleware"
)

// ExampleNewChain demonstrates composing middleware with NewChain and Build.
func ExampleNewChain() {
	// A middleware that adds a response header.
	addHeader := func(name, value string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(name, value)
				next.ServeHTTP(w, r)
			})
		}
	}

	chain := middleware.NewChain(
		addHeader("X-First", "1"),
		addHeader("X-Second", "2"),
	)
	handler := chain.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	fmt.Println(rec.Header().Get("X-First"))
	fmt.Println(rec.Header().Get("X-Second"))
	fmt.Println(rec.Body.String())

	// Output:
	// 1
	// 2
	// ok
}

// ExampleChain_Use demonstrates appending middleware to an existing chain.
func ExampleChain_Use() {
	addHeader := func(name, value string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(name, value)
				next.ServeHTTP(w, r)
			})
		}
	}

	chain := middleware.NewChain(addHeader("X-First", "1"))
	chain.Use(addHeader("X-Second", "2"))

	fmt.Println("chain length:", chain.Len())

	handler := chain.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// First middleware runs outermost, so X-First is set first.
	// X-Second is set by the inner middleware and overwrites won't
	// affect the outer header.
	fmt.Println(rec.Header().Get("X-First"))
	fmt.Println(rec.Header().Get("X-Second"))

	// Output:
	// chain length: 2
	// 1
	// 2
}

// ExampleChain_Snapshot demonstrates capturing a point-in-time copy of the chain.
func ExampleChain_Snapshot() {
	chain := middleware.NewChain()
	chain.Use(func(next http.Handler) http.Handler { return next })

	snap := chain.Snapshot()
	fmt.Println("snapshot length:", len(snap))

	chain.Use(func(next http.Handler) http.Handler { return next })

	// Snapshot is immutable — it still reflects the original length.
	fmt.Println("chain length after second Use:", chain.Len())
	fmt.Println("snapshot still:", len(snap))

	// Output:
	// snapshot length: 1
	// chain length after second Use: 2
	// snapshot still: 1
}
