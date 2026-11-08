// Package middleware provides the canonical HTTP middleware composition
// primitive for Plumego services.
//
// # Composition Model
//
// The single composition primitive is [Chain]. Middleware are registered in
// the order they should execute, and [Chain.Build] produces an immutable
// handler snapshot that is safe to use at runtime without further
// synchronisation.
//
// # Ordering Semantics
//
// Registration order equals execution order: the first middleware passed to
// [NewChain] (or the first call to [Chain.Use]) wraps the outermost layer of
// the pipeline and therefore executes first on every request.
//
//	Chain.Use(A).Use(B).Use(C) → A → B → C → handler
//
// There is no prepend primitive. Callers that need a specific leading
// middleware should pass it first when constructing the chain.
//
// # Canonical Example
//
//	import (
//	    "net/http"
//
//	    "github.com/birdordev/plumego/middleware"
//	    "github.com/birdordev/plumego/middleware/accesslog"
//	    "github.com/birdordev/plumego/middleware/recovery"
//	    "github.com/birdordev/plumego/middleware/requestid"
//	)
//
//	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: logger})
//	if err != nil {
//	    return err
//	}
//	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: logger})
//	if err != nil {
//	    return err
//	}
//	chain := middleware.NewChain(
//	    requestid.Middleware(), // executes first – stamps ID before recovery/logging
//	    recoveryMw,             // executes second – catches panics from downstream layers
//	    accesslogMw,            // executes third
//	)
//	http.Handle("/", chain.Build(myHandler))
package middleware

import "net/http"

// Middleware is the canonical middleware type used across Plumego.
//
// Every middleware must call next.ServeHTTP exactly once on the happy path.
// Omitting the call silently drops the request; calling it more than once
// causes double-response bugs.
//
// Usage with core.App:
//
//	app.Use(requestid.Middleware())
//
// Usage with a standalone handler:
//
//	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: logger})
//	if err != nil {
//		return err
//	}
//	h := middleware.NewChain(recoveryMw, requestid.Middleware()).Build(finalHandler)
type Middleware func(http.Handler) http.Handler

// Chain composes middleware in registration order.
//
// Registration order equals execution order: middleware added first runs first.
//
//	chain := NewChain(A, B, C)
//	// On each request: A → B → C → handler
//
// Chain is not goroutine-safe during construction. Call [Chain.Build] once at
// startup and share the resulting [http.Handler] across goroutines.
type Chain struct {
	middlewares []Middleware
}

// NewChain creates a chain from the provided middlewares.
// Execution order matches the argument order: NewChain(A, B) runs A then B.
func NewChain(middlewares ...Middleware) *Chain {
	mws := make([]Middleware, len(middlewares), len(middlewares)+4)
	copy(mws, middlewares)
	return &Chain{middlewares: mws}
}

// Use appends a middleware to the chain.
// The new middleware executes after all previously registered middlewares.
//
//	chain.Use(A).Use(B) // execution: A → B → handler
func (c *Chain) Use(middleware Middleware) *Chain {
	c.middlewares = append(c.middlewares, middleware)
	return c
}

// Len returns the number of middlewares in the chain.
func (c *Chain) Len() int {
	return len(c.middlewares)
}

// Snapshot returns a copy of the registered middlewares for introspection.
// Modifying the returned slice does not affect the chain.
func (c *Chain) Snapshot() []Middleware {
	out := make([]Middleware, len(c.middlewares))
	copy(out, c.middlewares)
	return out
}

// Build wraps h with all registered middlewares and returns an immutable
// handler snapshot. The snapshot does not reference the chain's internal
// slice, so subsequent [Chain.Use] calls do not affect the returned handler.
//
// Build should be called once during startup. The returned handler is safe
// for concurrent use by multiple goroutines.
//
// Execution order: first registered middleware runs outermost (first on request).
//
//	chain := NewChain(A, B, C)
//	h := chain.Build(finalHandler)
//	// request path: A → B → C → finalHandler
func (c *Chain) Build(h http.Handler) http.Handler {
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		h = c.middlewares[i](h)
	}
	return h
}
