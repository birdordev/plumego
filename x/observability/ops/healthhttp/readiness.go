package healthhttp

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
)

// ReadinessResponse is the JSON body for readiness endpoints.
type ReadinessResponse struct {
	Ready     bool               `json:"ready"`
	Status    health.HealthState `json:"status"`
	Message   string             `json:"message,omitempty"`
	Timestamp time.Time          `json:"timestamp"`
}

// ReadinessHandler exposes the current readiness state as JSON.
// It returns HTTP 200 when ready and 503 otherwise.
func ReadinessHandler(manager Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireManager(manager, w, r) {
			return
		}

		status := manager.Readiness()
		code := http.StatusOK
		if !status.Ready {
			code = http.StatusServiceUnavailable
		}

		_ = writeHealthResponse(w, r, code, status)
	})
}

// ReadinessHandlerWithManager checks component health and exposes the derived readiness state.
// It returns HTTP 200 when ready and 503 otherwise.
func ReadinessHandlerWithManager(manager Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireManager(manager, w, r) {
			return
		}

		ctx, cancel := withCheckTimeout(r.Context(), readinessHandlerTimeout)
		defer cancel()

		overall := manager.CheckAllComponents(ctx)
		ready := overall.Status.IsReady()
		code := http.StatusOK
		if !ready {
			code = http.StatusServiceUnavailable
		}

		_ = writeHealthResponse(w, r, code, ReadinessResponse{
			Ready:     ready,
			Status:    overall.Status,
			Message:   overall.Message,
			Timestamp: overall.Timestamp,
		})
	})
}

// BuildInfoHandler exposes build metadata as JSON for diagnostics and release verification.
func BuildInfoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = contract.WriteResponse(w, r, http.StatusOK, GetBuildInfo(), nil)
	})
}
