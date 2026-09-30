package metrics

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// PrometheusExporter exports metrics in Prometheus text format.
// This is a zero-dependency implementation that produces
// Prometheus-compatible metrics without requiring the Prometheus client library.
type PrometheusExporter struct {
	collector *MemoryCollector
	namespace string // Optional namespace prefix (e.g., "ai_gateway")
}

// NewPrometheusExporter creates a new Prometheus exporter
func NewPrometheusExporter(collector *MemoryCollector, namespace string) *PrometheusExporter {
	return &PrometheusExporter{
		collector: collector,
		namespace: namespace,
	}
}

// Handler returns an HTTP handler that exports metrics in Prometheus format
func (pe *PrometheusExporter) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot := pe.collector.Snapshot()
		var buf bytes.Buffer
		if err := pe.writePrometheusFormat(&buf, snapshot); err != nil {
			return
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	}
}

// writePrometheusFormat writes metrics in Prometheus text exposition format
func (pe *PrometheusExporter) writePrometheusFormat(w io.Writer, snapshot *Snapshot) error {
	// Write counters
	for name, counter := range snapshot.Counters {
		metricName, labels := pe.parseKey(name)
		fullName := pe.addNamespace(metricName)

		// Write HELP
		_, _ = fmt.Fprintf(w, "# HELP %s Counter metric\n", fullName)
		// Write TYPE
		_, _ = fmt.Fprintf(w, "# TYPE %s counter\n", fullName)
		// Write value
		_, _ = fmt.Fprintf(w, "%s%s %.6f\n", fullName, pe.formatLabels(labels), counter.Value)
		_, _ = fmt.Fprintln(w)
	}

	// Write gauges
	for name, gauge := range snapshot.Gauges {
		metricName, labels := pe.parseKey(name)
		fullName := pe.addNamespace(metricName)

		// Write HELP
		_, _ = fmt.Fprintf(w, "# HELP %s Gauge metric\n", fullName)
		// Write TYPE
		_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n", fullName)
		// Write value
		_, _ = fmt.Fprintf(w, "%s%s %.6f\n", fullName, pe.formatLabels(labels), gauge.Value)
		_, _ = fmt.Fprintln(w)
	}

	// Write histograms
	for name, hist := range snapshot.Histograms {
		metricName, labels := pe.parseKey(name)
		fullName := pe.addNamespace(metricName)

		// Write HELP
		_, _ = fmt.Fprintf(w, "# HELP %s Histogram metric\n", fullName)
		// Write TYPE
		_, _ = fmt.Fprintf(w, "# TYPE %s histogram\n", fullName)

		// Write histogram buckets (simulated)
		buckets := []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
		cumulativeCount := int64(0)
		for _, bucket := range buckets {
			// Estimate count in bucket
			bucketCount := pe.estimateBucketCount(hist, bucket)
			cumulativeCount += bucketCount
			labelsWithBucket := pe.addBucketLabel(labels, bucket)
			_, _ = fmt.Fprintf(w, "%s_bucket%s %d\n", fullName, pe.formatLabels(labelsWithBucket), cumulativeCount)
		}

		// Write +Inf bucket
		labelsWithInf := pe.addBucketLabel(labels, 0) // 0 represents +Inf
		_, _ = fmt.Fprintf(w, "%s_bucket%s %d\n", fullName, pe.formatLabelsWithInf(labelsWithInf), hist.Count)

		// Write sum and count
		_, _ = fmt.Fprintf(w, "%s_sum%s %.6f\n", fullName, pe.formatLabels(labels), hist.Sum)
		_, _ = fmt.Fprintf(w, "%s_count%s %d\n", fullName, pe.formatLabels(labels), hist.Count)
		_, _ = fmt.Fprintln(w)
	}

	return nil
}

// parseKey parses a metric key into name and labels
// Example: "requests_total{method=GET,status=200}" -> ("requests_total", {method:GET, status:200})
func (pe *PrometheusExporter) parseKey(key string) (string, map[string]string) {
	// Find the opening brace
	idx := strings.Index(key, "{")
	if idx == -1 {
		return key, nil
	}

	name := key[:idx]
	labelsStr := key[idx+1 : len(key)-1] // Remove { and }

	labels := make(map[string]string)
	if labelsStr == "" {
		return name, labels
	}

	// Parse labels
	pairs := strings.Split(labelsStr, ",")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			labels[kv[0]] = kv[1]
		}
	}

	return name, labels
}

// formatLabels formats labels for Prometheus format
func (pe *PrometheusExporter) formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	// Sort labels for consistent output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := labels[k]
		// Escape quotes and backslashes
		v = strings.ReplaceAll(v, "\\", "\\\\")
		v = strings.ReplaceAll(v, "\"", "\\\"")
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, v))
	}

	return "{" + strings.Join(parts, ",") + "}"
}

// formatLabelsWithInf formats labels with le="+Inf"
func (pe *PrometheusExporter) formatLabelsWithInf(labels map[string]string) string {
	labelsCopy := make(map[string]string, len(labels))
	for k, v := range labels {
		if k != "le" {
			labelsCopy[k] = v
		}
	}
	labelsCopy["le"] = "+Inf"
	return pe.formatLabels(labelsCopy)
}

// addBucketLabel adds the "le" (less than or equal) label for histogram buckets
func (pe *PrometheusExporter) addBucketLabel(labels map[string]string, bucket float64) map[string]string {
	result := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		result[k] = v
	}
	if bucket == 0 {
		result["le"] = "+Inf"
	} else {
		result["le"] = fmt.Sprintf("%.3f", bucket)
	}
	return result
}

// estimateBucketCount estimates the count of values <= bucket
func (pe *PrometheusExporter) estimateBucketCount(hist HistogramSnapshot, bucket float64) int64 {
	// Simple estimation: use percentiles
	if hist.Count == 0 {
		return 0
	}

	// If bucket is beyond max, all values are in this bucket
	if bucket >= hist.Max {
		return hist.Count
	}

	// If bucket is below min, no values are in this bucket
	if bucket <= hist.Min {
		return 0
	}

	// Linear interpolation between min and max
	ratio := (bucket - hist.Min) / (hist.Max - hist.Min)
	return int64(float64(hist.Count) * ratio)
}

// addNamespace adds the namespace prefix to a metric name
func (pe *PrometheusExporter) addNamespace(name string) string {
	if pe.namespace == "" {
		return name
	}
	return pe.namespace + "_" + name
}

// ScrapeConfig returns a Prometheus scrape configuration snippet
func (pe *PrometheusExporter) ScrapeConfig(jobName, targetAddress string) string {
	return fmt.Sprintf(`# Prometheus scrape configuration
scrape_configs:
  - job_name: '%s'
    scrape_interval: 15s
    static_configs:
      - targets: ['%s']
`, jobName, targetAddress)
}
