package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	plumelog "github.com/birdordev/plumego/log"
)

const codeComponentUnhealthy = "health.component.unhealthy"

// HealthHandler serves the canonical liveness and readiness endpoints.
//
// Liveness (/healthz) always returns 200 as long as the process is alive.
// It uses a simple local response struct — no dependency probing needed.
//
// Readiness (/readyz) probes each health.ComponentChecker in order and
// surfaces component names in both success and error responses so operators
// immediately know which dependency is unhealthy without inspecting logs.
//
// Use health.ComponentChecker (from github.com/birdordev/plumego/health) as the
// checker type. It carries a stable Name() for labeling plus Check() for the
// probe. Implement health.ComponentChecker directly or adapt an existing
// client's health method with a small wrapper struct in routes.go.
//
// Logger must not be nil; pass a.Core.Logger() from routes.go.
// Use plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard}) in tests.
type HealthHandler struct {
	ServiceName string
	Logger      plumelog.StructuredLogger
	// Checkers is an optional list of dependency readiness probes.
	// Each checker's Name() labels its result in the readiness response.
	// When nil or empty the readiness endpoint reports ready immediately.
	Checkers []health.ComponentChecker
}

// livenessResponse is the body returned by the liveness probe.
// Liveness only confirms the process is up; it carries no component state.
type livenessResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

// Live reports that the process is serving HTTP traffic.
// Kubernetes liveness probes call this endpoint; it must never be gated on
// dependency health — if a dependency is down the process is still alive.
func (h HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, livenessResponse{
		Status:    "ok",
		Service:   h.ServiceName,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, nil))
}

// Ready probes all registered health.ComponentChecker instances.
//
// All checkers are always probed regardless of prior failures so operators
// see every unhealthy dependency in a single response, not just the first one.
// On success the response body is a health.ReadinessStatus with a per-component
// map so load balancers and dashboards can see which dependencies passed.
// On failure a 503 TypeUnavailable error carries each failing component name
// as a detail key and its error message as the value.
//
//	GET /readyz (all pass)       → 200 health.ReadinessStatus{Ready:true, Components:{db:true,cache:true}}
//	GET /readyz (db fails)       → 503 TypeUnavailable detail.database="connection refused"
//	GET /readyz (db+cache fail)  → 503 TypeUnavailable detail.database="…" detail.cache="…"
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
