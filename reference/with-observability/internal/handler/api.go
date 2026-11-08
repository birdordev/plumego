// Package handler contains the HTTP handlers for the with-observability reference app.
package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/x/observability"
)

// APIHandler handles the core JSON API endpoints.
type APIHandler struct {
	Logger      plumelog.StructuredLogger
	ServiceName string
	Version     string
}

type rootResponse struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Docs    string `json:"docs"`
}

// Root responds with minimal service identity.
func (h APIHandler) Root(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, rootResponse{
		Service: h.ServiceName,
		Version: h.Version,
		Docs:    "/api/hello",
	}, nil))
}

type helloResponse struct {
	Message   string `json:"message"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
	Version   string `json:"version"`
}

// Hello responds with service metadata.
func (h APIHandler) Hello(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, helloResponse{
		Message:   "hello from plumego with-observability",
		Service:   h.ServiceName,
		Timestamp: time.Now().Format(time.RFC3339),
		Version:   h.Version,
	}, nil))
}

// ObservabilityHandler exposes the Prometheus collector and OpenTelemetry tracer
// through HTTP endpoints for development inspection and Prometheus scraping.
//
// In production:
//   - Keep GET /metrics but gate it behind an internal network or bearer auth.
//   - Remove GET /api/v1/spans (it exposes trace internals) and replace the
//     in-process OpenTelemetryTracer with an OTLP gRPC exporter.
type ObservabilityHandler struct {
	Logger    plumelog.StructuredLogger
	Collector *observability.PrometheusCollector
	Tracer    *observability.OpenTelemetryTracer
}

type statsResponse struct {
	TotalRequests int64            `json:"total_requests"`
	ErrorRequests int64            `json:"error_requests"`
	ActiveSeries  int              `json:"active_series"`
	UptimeSince   time.Time        `json:"uptime_since"`
	ByName        map[string]int64 `json:"by_name"`
}

// Stats returns a summary of collected metrics from the PrometheusCollector.
// Demonstrates how to expose collector stats in the standard JSON envelope.
// For Prometheus scraping use GET /metrics (Prometheus text format) instead.
//
//	GET /api/v1/stats → 200 statsResponse
func (h ObservabilityHandler) Stats(w http.ResponseWriter, r *http.Request) {
	s := h.Collector.GetStats()
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, statsResponse{
		TotalRequests: s.TotalRecords,
		ErrorRequests: s.ErrorRecords,
		ActiveSeries:  s.ActiveSeries,
		UptimeSince:   s.StartTime,
		ByName:        s.NameBreakdown,
	}, nil))
}

type spanSummary struct {
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	DurationMS int64             `json:"duration_ms"`
	Timestamp  time.Time         `json:"timestamp"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type spansResponse struct {
	Total      int           `json:"total"`
	ErrorSpans int           `json:"error_spans"`
	Spans      []spanSummary `json:"spans"`
}

// Spans returns the spans collected by the OpenTelemetryTracer.
// Each HTTP request produces one span; this endpoint lets you inspect trace IDs,
// latencies, and status codes without an external tracing backend.
//
// Remove this endpoint in production and export spans via OTLP instead.
//
//	GET /api/v1/spans           → 200 all collected spans (newest last)
//	GET /api/v1/spans?limit=10  → 200 the 10 most recent spans
func (h ObservabilityHandler) Spans(w http.ResponseWriter, r *http.Request) {
	rawSpans := h.Tracer.Spans()

	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n := parsePositiveInt(raw); n > 0 && n < len(rawSpans) {
			rawSpans = rawSpans[len(rawSpans)-n:]
		}
	}

	stats := h.Tracer.GetSpanStats()
	summaries := make([]spanSummary, len(rawSpans))
	for i, s := range rawSpans {
		summaries[i] = spanSummary{
			TraceID:    s.TraceID,
			SpanID:     s.SpanID,
			Name:       s.Name,
			Status:     s.Status,
			DurationMS: s.Duration.Milliseconds(),
			Timestamp:  s.Timestamp,
			Attributes: s.Attributes,
		}
	}

	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, spansResponse{
		Total:      stats.TotalSpans,
		ErrorSpans: stats.ErrorSpans,
		Spans:      summaries,
	}, nil))
}

func parsePositiveInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// MetricsHandler demonstrates reading collector stats through the narrow
// metrics.StatsReader interface rather than the concrete PrometheusCollector,
// so the pattern works with any AggregateCollector implementation.
type MetricsHandler struct {
	Logger   plumelog.StructuredLogger
	Observer metrics.StatsReader
}

type collectorStatsResponse struct {
	TotalRecords  int64            `json:"total_records"`
	ErrorRecords  int64            `json:"error_records"`
	ActiveSeries  int              `json:"active_series"`
	StartTime     time.Time        `json:"start_time"`
	NameBreakdown map[string]int64 `json:"name_breakdown"`
}

// CollectorStats returns raw collector statistics via the metrics.StatsReader interface.
// Demonstrates reading stats without depending on the concrete collector type.
//
//	GET /api/v1/collector-stats → 200 collectorStatsResponse
func (h MetricsHandler) CollectorStats(w http.ResponseWriter, r *http.Request) {
	s := h.Observer.GetStats()
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, collectorStatsResponse{
		TotalRecords:  s.TotalRecords,
		ErrorRecords:  s.ErrorRecords,
		ActiveSeries:  s.ActiveSeries,
		StartTime:     s.StartTime,
		NameBreakdown: s.NameBreakdown,
	}, nil))
}
