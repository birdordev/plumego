package observability

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		series, totalRequests, uptime := e.collector.snapshot()
		namespace := e.collector.namespace
		sort.Slice(series, func(i, j int) bool {
			a, b := series[i].key, series[j].key
			if a.method != b.method {
				return a.method < b.method
			}
			if a.path != b.path {
				return a.path < b.path
			}
			return a.status < b.status
		})
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		out := bufio.NewWriterSize(w, 16<<10)
		_, _ = fmt.Fprintf(out, "# HELP %s_http_requests_total Total number of HTTP requests processed.\n# TYPE %s_http_requests_total counter\n", namespace, namespace)
		var labels, line []byte
		for _, entry := range series {
			labels = appendSeriesLabels(labels[:0], entry.key)
			line = append(line[:0], namespace...)
			line = append(line, "_http_requests_total"...)
			line = append(line, labels...)
			line = strconv.AppendUint(line, entry.count, 10)
			if _, err := out.Write(append(line, '\n')); err != nil {
				return
			}
		}

		_, _ = fmt.Fprintf(out, "\n# HELP %s_http_request_duration_seconds HTTP request latencies in seconds.\n# TYPE %s_http_request_duration_seconds summary\n", namespace, namespace)
		for _, entry := range series {
			labels = appendSeriesLabels(labels[:0], entry.key)
			stats := entry.duration
			for _, stat := range [...]struct {
				suffix string
				value  float64
			}{{"sum", stats.sum}, {"count", 0}, {"min", stats.min}, {"max", stats.max}} {
				line = append(line[:0], namespace...)
				line = append(line, "_http_request_duration_seconds_"...)
				line = append(line, stat.suffix...)
				line = append(line, labels...)
				if stat.suffix == "count" {
					line = strconv.AppendUint(line, stats.count, 10)
				} else {
					line = strconv.AppendFloat(line, stat.value, 'f', 9, 64)
				}
				if _, err := out.Write(append(line, '\n')); err != nil {
					return
				}
			}
		}
		_, _ = fmt.Fprintf(out, "\n# HELP %s_uptime_seconds Total uptime in seconds.\n# TYPE %s_uptime_seconds gauge\n%s_uptime_seconds %.3f\n", namespace, namespace, namespace, uptime.Seconds())
		_, _ = fmt.Fprintf(out, "\n# HELP %s_http_requests_total_all Total requests across all labels.\n# TYPE %s_http_requests_total_all counter\n%s_http_requests_total_all %d\n", namespace, namespace, namespace, totalRequests)
		_ = out.Flush()
	})
}

func appendSeriesLabels(dst []byte, key labelKey) []byte {
	dst = append(dst, "{method=\""...)
	dst = append(dst, escapeLabelValue(key.method)...)
	dst = append(dst, "\",path=\""...)
	dst = append(dst, escapeLabelValue(key.path)...)
	dst = append(dst, "\",status=\""...)
	dst = append(dst, escapeLabelValue(key.status)...)
	return append(dst, "\"} "...)
}
