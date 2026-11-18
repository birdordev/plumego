package core

import (
	"fmt"

	"github.com/birdordev/plumego/middleware"
)

// Use adds middleware to the application's middleware chain. If any builder
// is nil, Use returns an error and adds none of the supplied builders.
// Builders run during handler preparation. Calls to Run, Prepare, or ServeHTTP
// on the same app must occur after the builder returns.
func (a *App) Use(middlewares ...middleware.Middleware) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.registrationMutable() {
		return immutableAppError(operationUseMiddleware, "add middleware", nil)
	}

	chain := a.middlewareChain
	if chain == nil {
		return wrapCoreError(fmt.Errorf("middleware chain not configured"), operationUseMiddleware, nil)
	}

	for i, mw := range middlewares {
		if mw == nil {
			return wrapCoreError(fmt.Errorf("middleware cannot be nil"), operationUseMiddleware, map[string]any{"index": i})
		}
	}
	for _, mw := range middlewares {
		chain.Use(mw)
	}
	return nil
}
