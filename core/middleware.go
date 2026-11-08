package core

import (
	"fmt"

	"github.com/birdordev/plumego/middleware"
)

// Use adds middleware to the application's middleware chain.
func (a *App) Use(middlewares ...middleware.Middleware) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := validateMutableState(a.preparationState, operationUseMiddleware, "add middleware", nil); err != nil {
		return err
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

// buildHandler builds the combined handler with current middleware stack.
func (a *App) buildHandler() {
	a.mu.RLock()
	chain := a.middlewareChain
	r := a.router
	a.mu.RUnlock()

	if chain == nil || r == nil {
		a.mu.Lock()
		a.handler = nil
		a.mu.Unlock()
		return
	}

	handler := chain.Build(r)

	a.mu.Lock()
	a.handler = handler
	a.mu.Unlock()

	if handler != nil {
		a.handlerFast.Store(&handlerRef{h: handler})
	}
}
