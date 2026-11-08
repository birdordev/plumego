// Package handler contains HTTP handlers for with-ops application routes.
package handler

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/metrics"
	"with-ops/internal/config"
)

// Handler serves the root and metrics endpoints.
type Handler struct {
	cfg       config.Config
	collector *metrics.BaseMetricsCollector
	Logger    plumelog.StructuredLogger
}

// New constructs a Handler with the given config and metrics collector.
func New(cfg config.Config, collector *metrics.BaseMetricsCollector, logger plumelog.StructuredLogger) *Handler {
	return &Handler{cfg: cfg, collector: collector, Logger: logger}
}

// Root serves the application index with links to available endpoints.
func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, map[string]any{
		"service": "with-ops",
		"ops":     h.cfg.OpsBasePath,
		"metrics": "/metrics",
	}, nil))
}

// Metrics serves raw application metrics from the collector.
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, h.collector.GetStats(), nil))
}
