package core

import (
	"fmt"
	"sort"
	"strings"

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

// validateMutableState returns an error if state is not PreparationStateMutable.
// Call while holding the app mutex so the state read is consistent with callers.
func validateMutableState(state PreparationState, operation, action string, params map[string]any) error {
	if state != PreparationStateMutable {
		return immutableAppError(operation, action, params)
	}
	return nil
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

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, params[key]))
	}
	return strings.Join(parts, " ")
}

func (a *App) freezeConfig() {
	a.mu.Lock()
	if a.preparationState == PreparationStateMutable {
		a.preparationState = PreparationStateHandlerPrepared
	}
	a.mu.Unlock()
}

func (a *App) ensureRouter() *router.Router {
	a.mu.RLock()
	r := a.router
	a.mu.RUnlock()
	return r
}
