package healthhttp

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
)

// RuntimeInfoHandler exposes runtime diagnostics only.
func RuntimeInfoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_ = contract.WriteResponse(w, r, http.StatusOK, getRuntimeInfo(), nil)
	})
}

type diagnosticsResponse struct {
	Timestamp  time.Time                          `json:"timestamp"`
	BuildInfo  BuildInfo                          `json:"build_info"`
	Runtime    *RuntimeInfo                       `json:"runtime"`
	Readiness  *health.ReadinessStatus            `json:"readiness,omitempty"`
	Health     *health.HealthStatus               `json:"health,omitempty"`
	Components map[string]*health.ComponentHealth `json:"components,omitempty"`
	Config     *Config                            `json:"config,omitempty"`
}

// DiagnosticsHandler returns comprehensive system diagnostics.
// enabled controls whether the endpoint is active; set to false in production.
// It should only be mounted in development or internal environments.
func DiagnosticsHandler(manager Manager, enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			http.NotFound(w, r)
			return
		}

		response := diagnosticsResponse{
			Timestamp: time.Now(),
			BuildInfo: GetBuildInfo(),
			Runtime:   getRuntimeInfo(),
		}

		if manager != nil {
			ctx, cancel := withCheckTimeout(r.Context(), healthHandlerTimeout)
			defer cancel()

			readiness := manager.Readiness()
			healthStatus := manager.CheckAllComponents(ctx)
			config := manager.GetConfig()

			response.Readiness = &readiness
			response.Health = &healthStatus
			response.Components = manager.GetAllHealth()
			response.Config = &config
		}

		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_ = contract.WriteResponse(w, r, http.StatusOK, response, nil)
	})
}
