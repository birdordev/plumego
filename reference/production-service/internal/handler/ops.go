package handler

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/metrics"
)

// MetricsCollector is the minimal interface that OpsHandler depends on.
// *metrics.BaseMetricsCollector satisfies this interface; tests may pass a stub.
type MetricsCollector interface {
	GetStats() metrics.CollectorStats
}

// OpsHandler serves GET /ops/metrics.
// It returns in-process HTTP request metrics collected by the httpmetrics middleware.
// Logger must not be nil; pass a.Core.Logger() from routes.go.
type OpsHandler struct {
	Metrics MetricsCollector
	Logger  plumelog.StructuredLogger
}

// MetricStats handles GET /ops/metrics.
// This route is protected by bearer-token auth wired in routes.go.
func (h OpsHandler) MetricStats(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, h.Metrics.GetStats(), nil))
}
