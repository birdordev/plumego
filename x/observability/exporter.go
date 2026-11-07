package observability

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrNilCollector is returned when a Prometheus exporter is created without a collector.
var ErrNilCollector = errors.New("observability: prometheus collector cannot be nil")

type Exporter interface {
	Handler() http.Handler
}

type PrometheusExporter struct {
	collector *PrometheusCollector
}

// NewPrometheusExporter creates a Prometheus exporter and returns an error for
// invalid dependencies.
func NewPrometheusExporter(collector *PrometheusCollector) (*PrometheusExporter, error) {
	if collector == nil {
		return nil, ErrNilCollector
	}
	return &PrometheusExporter{collector: collector}, nil
}

func (e *PrometheusExporter) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests, durations, uptime := e.collector.snapshot()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		_, _ = fmt.Fprintf(w, "# HELP %s_http_requests_total Total number of HTTP requests processed.\n", e.collector.namespace)
		_, _ = fmt.Fprintf(w, "# TYPE %s_http_requests_total counter\n", e.collector.namespace)

		for _, key := range sortedKeys(requests) {
			_, _ = fmt.Fprintf(w, "%s_http_requests_total{method=\"%s\",path=\"%s\",status=\"%s\"} %d\n",
				e.collector.namespace,
				escapeLabelValue(key.method),
				escapeLabelValue(key.path),
				escapeLabelValue(key.status),
				requests[key],
			)
		}

		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "# HELP %s_http_request_duration_seconds_sum Sum of HTTP request latencies in seconds.\n", e.collector.namespace)
		_, _ = fmt.Fprintf(w, "# TYPE %s_http_request_duration_seconds_summary summary\n", e.collector.namespace)

		for _, key := range sortedKeys(durations) {
			stats := durations[key]
			method := escapeLabelValue(key.method)
			path := escapeLabelValue(key.path)
			status := escapeLabelValue(key.status)
			_, _ = fmt.Fprintf(w, "%s_http_request_duration_seconds_sum{method=\"%s\",path=\"%s\",status=\"%s\"} %.9f\n",
				e.collector.namespace, method, path, status, stats.sum)
			_, _ = fmt.Fprintf(w, "%s_http_request_duration_seconds_count{method=\"%s\",path=\"%s\",status=\"%s\"} %d\n",
				e.collector.namespace, method, path, status, stats.count)
			_, _ = fmt.Fprintf(w, "%s_http_request_duration_seconds_min{method=\"%s\",path=\"%s\",status=\"%s\"} %.9f\n",
				e.collector.namespace, method, path, status, stats.min)
			_, _ = fmt.Fprintf(w, "%s_http_request_duration_seconds_max{method=\"%s\",path=\"%s\",status=\"%s\"} %.9f\n",
				e.collector.namespace, method, path, status, stats.max)
		}

		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "# HELP %s_uptime_seconds Total uptime in seconds.\n", e.collector.namespace)
		_, _ = fmt.Fprintf(w, "# TYPE %s_uptime_seconds gauge\n", e.collector.namespace)
		_, _ = fmt.Fprintf(w, "%s_uptime_seconds %.3f\n", e.collector.namespace, uptime.Seconds())

		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "# HELP %s_http_requests_total_all Total requests across all labels.\n", e.collector.namespace)
		_, _ = fmt.Fprintf(w, "# TYPE %s_http_requests_total_all counter\n", e.collector.namespace)
		var totalRequests uint64
		for _, count := range requests {
			totalRequests += count
		}
		_, _ = fmt.Fprintf(w, "%s_http_requests_total_all %d\n", e.collector.namespace, totalRequests)
	})
}
