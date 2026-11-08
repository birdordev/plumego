package healthhttp

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
)

// HealthResponse is the JSON body for the main health endpoint.
type HealthResponse struct {
	health.HealthStatus
	BuildInfo BuildInfo    `json:"build_info,omitempty"`
	Runtime   *RuntimeInfo `json:"runtime,omitempty"`
}

// ComponentsListResponse is the JSON body for the component list endpoint.
type ComponentsListResponse struct {
	Components []string `json:"components"`
	Count      int      `json:"count"`
}

// SummaryHandler returns aggregate component health only.
func SummaryHandler(manager Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireManager(manager, w, r) {
			return
		}

		ctx, cancel := withCheckTimeout(r.Context(), healthHandlerTimeout)
		defer cancel()

		status := manager.CheckAllComponents(ctx)
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_ = writeHealthResponse(w, r, httpStatusForHealth(status.Status), status)
	})
}

// DetailedHandler returns aggregate health plus build metadata.
func DetailedHandler(manager Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireManager(manager, w, r) {
			return
		}

		ctx, cancel := withCheckTimeout(r.Context(), healthHandlerTimeout)
		defer cancel()

		status := manager.CheckAllComponents(ctx)
		resp := HealthResponse{
			HealthStatus: status,
			BuildInfo:    GetBuildInfo(),
		}

		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_ = writeHealthResponse(w, r, httpStatusForHealth(status.Status), resp)
	})
}

// HealthHandler creates a comprehensive health check handler.
// Pass includeRuntime=true to include runtime diagnostics in the response.
func HealthHandler(manager Manager, includeRuntime bool) http.Handler {
	if !includeRuntime {
		return DetailedHandler(manager)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireManager(manager, w, r) {
			return
		}

		ctx, cancel := withCheckTimeout(r.Context(), healthHandlerTimeout)
		defer cancel()

		status := manager.CheckAllComponents(ctx)
		resp := HealthResponse{
			HealthStatus: status,
			BuildInfo:    GetBuildInfo(),
			Runtime:      getRuntimeInfo(),
		}

		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_ = writeHealthResponse(w, r, httpStatusForHealth(status.Status), resp)
	})
}

// ComponentHealthHandler creates a handler for checking a specific component's health.
func ComponentHealthHandler(manager Manager, componentName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireManager(manager, w, r) {
			return
		}

		ctx, cancel := withCheckTimeout(r.Context(), componentHealthTimeout)
		defer cancel()

		checkErr := manager.CheckComponent(ctx, componentName)

		componentHealth, exists := manager.GetComponentHealth(componentName)
		if !exists {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeNotFound).
				Code("COMPONENT_NOT_FOUND").
				Message("component not found").
				Build())
			return
		}

		if checkErr != nil {
			// A cached success cannot represent a failed or unavailable fresh check.
			componentHealth.Status = health.StatusUnhealthy
			componentHealth.Message = "component check failed"
		}
		_ = writeHealthResponse(w, r, httpStatusForHealth(componentHealth.Status), componentHealth)
	})
}

// AllComponentsHealthHandler creates a handler for checking all components' health.
func AllComponentsHealthHandler(manager Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireManager(manager, w, r) {
			return
		}

		ctx, cancel := withCheckTimeout(r.Context(), allComponentsHealthTimeout)
		defer cancel()

		manager.CheckAllComponents(ctx)
		_ = contract.WriteResponse(w, r, http.StatusOK, manager.GetAllHealth(), nil)
	})
}

// LiveHandler creates a liveness probe handler that always returns 200.
func LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("alive"))
	})
}

// ComponentsListHandler creates a handler that lists all registered components.
func ComponentsListHandler(manager Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireManager(manager, w, r) {
			return
		}

		allHealth := manager.GetAllHealth()
		names := make([]string, 0, len(allHealth))
		for name := range allHealth {
			names = append(names, name)
		}

		_ = contract.WriteResponse(w, r, http.StatusOK, ComponentsListResponse{
			Components: names,
			Count:      len(names),
		}, nil)
	})
}
