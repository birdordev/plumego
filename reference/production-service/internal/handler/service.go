// Package handler contains the HTTP handlers for the production reference application.
// Each handler is a struct with only the dependencies it needs, wired via constructor
// injection in internal/app/routes.go. This keeps handlers independently testable
// and avoids coupling every handler to the full App struct.
package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	plumelog "github.com/birdordev/plumego/log"
)

const codeComponentUnhealthy = "health.component.unhealthy"

// ServiceHandler serves the root, liveness, and readiness endpoints.
// These endpoints need only the service name and a static features list.
// Checkers is an optional list of dependency readiness probes for /readyz.
// Each checker's Name() labels its result in the readiness response.
// When nil or empty the readiness endpoint reports ready immediately.
// Logger must not be nil; pass a.Core.Logger() from routes.go.
type ServiceHandler struct {
	ServiceName string
	Features    []string
	Logger      plumelog.StructuredLogger
	Checkers    []health.ComponentChecker
}

type serviceResponse struct {
	Service   string   `json:"service"`
	Timestamp string   `json:"timestamp"`
	Features  []string `json:"features"`
}

type livenessResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Check     string `json:"check"`
	Timestamp string `json:"timestamp"`
}

// Root responds with service identity and enabled features.
func (h ServiceHandler) Root(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, serviceResponse{
		Service:   h.ServiceName,
		Timestamp: utcNow(),
		Features:  h.Features,
	}, nil))
}

// Live reports that the process is serving HTTP traffic.
// Kubernetes liveness probes call this endpoint; it must never be gated on
// dependency health — if a dependency is down the process is still alive.
func (h ServiceHandler) Live(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, livenessResponse{
		Status:    "ok",
		Service:   h.ServiceName,
		Check:     "liveness",
		Timestamp: utcNow(),
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
func (h ServiceHandler) Ready(w http.ResponseWriter, r *http.Request) {
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

func utcNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}
