package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	plumelog "github.com/birdordev/plumego/log"
)

const codeComponentUnhealthy = "health.component.unhealthy"

// HealthHandler serves the liveness and readiness endpoints.
// Logger must not be nil; pass a.Core.Logger() from routes.go.
type HealthHandler struct {
	ServiceName string
	Logger      plumelog.StructuredLogger
	// Checkers is an optional list of dependency readiness probes.
	// When nil or empty the readiness endpoint reports ready immediately.
	Checkers []health.ComponentChecker
}

type livenessResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

// Live reports that the process is serving HTTP traffic.
// Kubernetes liveness probes call this endpoint; it must never be gated on
// dependency health — if the backend is down the proxy process is still alive.
func (h HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, livenessResponse{
		Status:    "ok",
		Service:   h.ServiceName,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, nil))
}

// Ready probes all registered health.ComponentChecker instances.
// Returns 200 with component status when all pass; 503 with failing component
// names when any fail. All checkers are probed even if one fails so operators
// see every unhealthy dependency in a single response.
func (h HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	components := make(map[string]bool, len(h.Checkers))
	eb := contract.NewErrorBuilder().
		Type(contract.TypeUnavailable).
		Code(codeComponentUnhealthy).
		Message("one or more components are not ready")
	anyFailed := false
	for _, checker := range h.Checkers {
		if err := checker.Check(r.Context()); err != nil {
			components[checker.Name()] = false
			eb = eb.Detail(checker.Name(), err.Error())
			anyFailed = true
		} else {
			components[checker.Name()] = true
		}
	}
	if anyFailed {
		logWriteErr(h.Logger, contract.WriteError(w, r, eb.Build()))
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, health.ReadinessStatus{
		Ready:      true,
		Timestamp:  time.Now().UTC(),
		Components: components,
	}, nil))
}
