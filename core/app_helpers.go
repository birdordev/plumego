package core

import (
	"fmt"
	"sort"

	"github.com/birdordev/plumego/router"
)

const (
	operationPrepareServer = "prepare_server"
	operationGetServer     = "get_server"
	operationShutdownApp   = "shutdown_app"
	operationAddRoute      = "add_route"
	operationUseMiddleware = "use_middleware"
)

func immutableAppError(operation, action string, params map[string]any) error {
	return wrapCoreError(fmt.Errorf("cannot %s after app has been prepared", action), operation, params)
}

func wrapCoreError(err error, operation string, params map[string]any) error {
	if err == nil {
		return nil
	}
	if len(params) == 0 {
		return fmt.Errorf("core %s: %w", operation, err)
	}
	return fmt.Errorf("core %s %s: %w", operation, formatErrorParams(params), err)
}

func formatErrorParams(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var formatted []byte
	for i, key := range keys {
		if i > 0 {
			formatted = append(formatted, ' ')
		}
		formatted = append(formatted, key...)
		formatted = append(formatted, '=')
		formatted = fmt.Appendf(formatted, "%v", params[key])
	}
	return string(formatted)
}

// registrationMutable must be called with a.mu held.
func (a *App) registrationMutable() bool {
	return !a.registrationFrozen && a.preparationState == PreparationStateMutable
}

// freezeRegistration must be called with a.mu held.
func (a *App) freezeRegistration() {
	if !a.registrationFrozen {
		a.router.Freeze()
		a.registrationFrozen = true
	}
}

func (a *App) ensureRouter() *router.Router {
	a.mu.RLock()
	r := a.router
	a.mu.RUnlock()
	return r
}
