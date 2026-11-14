package core

import (
	"fmt"

	"github.com/birdordev/plumego/middleware"
)

// Use adds middleware to the application's middleware chain. Builders run
// during handler preparation and must not call Prepare or ServeHTTP on this app.
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
