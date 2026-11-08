package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	plumelog "github.com/birdordev/plumego/log"
)

const codeComponentUnhealthy = "health.component.unhealthy"

// HealthHandler serves liveness and readiness endpoints.
// Checkers may be empty; readiness will return success if no checkers fail.
// To add health checks (e.g. database connectivity), pass ComponentChecker instances
// when constructing the handler. See health.ComponentChecker for the interface.
type HealthHandler struct {
	ServiceName string
	Logger      plumelog.StructuredLogger
	Checkers    []health.ComponentChecker
}

type livenessResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

// Live reports that the process is serving HTTP traffic.
func (h HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, livenessResponse{
		Status:    "ok",
		Service:   h.ServiceName,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, nil))
}

// Ready probes all registered health.ComponentChecker instances.
// All checkers are probed even when an earlier one fails so operators see every
// unhealthy dependency in a single response.
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
