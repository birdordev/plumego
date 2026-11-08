package healthhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
)

const (
	healthHandlerTimeout       = 10 * time.Second
	componentHealthTimeout     = 5 * time.Second
	allComponentsHealthTimeout = 15 * time.Second
	readinessHandlerTimeout    = 5 * time.Second
)

func withCheckTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}

	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining <= timeout {
			return ctx, func() {}
		}
	}

	return context.WithTimeout(ctx, timeout)
}

func requireManager(manager Manager, w http.ResponseWriter, r *http.Request) bool {
	if manager == nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeUnavailable).
			Code("HEALTH_MANAGER_UNAVAILABLE").
			Message("health manager is not configured").
			Build())
		return false
	}
	return true
}

func requireTracker(tracker *Tracker, w http.ResponseWriter, r *http.Request) bool {
	if tracker == nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeUnavailable).
			Code("HEALTH_TRACKER_UNAVAILABLE").
			Message("health tracker is not configured").
			Build())
		return false
	}
	return true
}

func httpStatusForHealth(state health.HealthState) int {
	switch state {
	case health.StatusUnhealthy:
		return http.StatusServiceUnavailable
	case health.StatusDegraded:
		return http.StatusPartialContent
	default:
		return http.StatusOK
	}
}

type healthResponseEnvelope struct {
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// writeHealthResponse writes health status documents, including unhealthy 503
// bodies. These are health resource representations, not contract success
// responses, so dynamic health status codes stay outside contract.WriteResponse.
func writeHealthResponse(w http.ResponseWriter, r *http.Request, status int, data any) error {
	if w == nil {
		return contract.ErrResponseWriterNil
	}

	resp := healthResponseEnvelope{Data: data}
	if r != nil {
		resp.RequestID = contract.RequestIDFromContext(r.Context())
	}

	w.Header().Set(contract.HeaderContentType, contract.ContentTypeJSON)
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(resp)
}

// RuntimeInfo contains runtime diagnostics for development and debug endpoints.
type RuntimeInfo struct {
	GoVersion    string `json:"go_version"`
	NumGoroutine int    `json:"num_goroutine"`
	NumCPU       int    `json:"num_cpu"`
	GOARCH       string `json:"goarch"`
	GOOS         string `json:"goos"`
	MemAlloc     uint64 `json:"mem_alloc_bytes"`
	MemSys       uint64 `json:"mem_sys_bytes"`
	NumGC        uint32 `json:"num_gc"`
}

func getRuntimeInfo() *RuntimeInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return &RuntimeInfo{
		GoVersion:    runtime.Version(),
		NumGoroutine: runtime.NumGoroutine(),
		NumCPU:       runtime.NumCPU(),
		GOARCH:       runtime.GOARCH,
		GOOS:         runtime.GOOS,
		MemAlloc:     m.Alloc,
		MemSys:       m.Sys,
		NumGC:        m.NumGC,
	}
}
