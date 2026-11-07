package pubsub

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spcent/plumego/contract"
)

// PrometheusExporter exports pubsub metrics in Prometheus format
type PrometheusExporter struct {
	ps       *InProcBroker
	registry *metricsRegistry
	mu       sync.RWMutex

	// Additional metrics sources (optional)
	persistent  *PersistentPubSub
	distributed *DistributedPubSub
	ordered     *OrderedPubSub
	rateLimited *RateLimitedPubSub

	// Configuration
	namespace string
	subsystem string
	labels    map[string]string
}

// metricsRegistry holds all registered metrics
type metricsRegistry struct {
	counters map[string]*counter
	gauges   map[string]*gauge
	mu       sync.RWMutex
}

// counter represents a Prometheus counter
type counter struct {
	name   string
	help   string
	labels map[string]string
	value  float64
}

// gauge represents a Prometheus gauge
type gauge struct {
	name   string
	help   string
	labels map[string]string
	value  float64
}

// NewPrometheusExporter creates a new Prometheus exporter
func NewPrometheusExporter(ps *InProcBroker) *PrometheusExporter {
	return &PrometheusExporter{
		ps:        ps,
		registry:  newMetricsRegistry(),
		namespace: "plumego",
		subsystem: "pubsub",
		labels:    make(map[string]string),
	}
}

// newMetricsRegistry creates a new metrics registry
func newMetricsRegistry() *metricsRegistry {
	return &metricsRegistry{
		counters: make(map[string]*counter),
		gauges:   make(map[string]*gauge),
	}
}

// WithPersistent adds persistent pubsub metrics
func (pe *PrometheusExporter) WithPersistent(pps *PersistentPubSub) *PrometheusExporter {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.persistent = pps
	return pe
}

// WithDistributed adds distributed pubsub metrics
func (pe *PrometheusExporter) WithDistributed(dps *DistributedPubSub) *PrometheusExporter {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.distributed = dps
	return pe
}

// WithOrdered adds ordered pubsub metrics
func (pe *PrometheusExporter) WithOrdered(ops *OrderedPubSub) *PrometheusExporter {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.ordered = ops
	return pe
}

// WithRateLimited adds rate-limited pubsub metrics
func (pe *PrometheusExporter) WithRateLimited(rlps *RateLimitedPubSub) *PrometheusExporter {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.rateLimited = rlps
	return pe
}

// WithNamespace sets the metrics namespace
func (pe *PrometheusExporter) WithNamespace(ns string) *PrometheusExporter {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.namespace = ns
	return pe
}

// WithLabels adds global labels to all metrics
func (pe *PrometheusExporter) WithLabels(labels map[string]string) *PrometheusExporter {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	for k, v := range labels {
		pe.labels[k] = v
	}
	return pe
}

// Collect collects all metrics and formats them for Prometheus
func (pe *PrometheusExporter) Collect() string {
	pe.registry.mu.Lock()
	defer pe.registry.mu.Unlock()

	// Clear previous metrics
	pe.registry.counters = make(map[string]*counter)
	pe.registry.gauges = make(map[string]*gauge)

	// Collect from base pubsub
	pe.collectBasicMetrics()

	// Collect from extensions
	pe.mu.RLock()
	if pe.persistent != nil {
		pe.collectPersistenceMetrics()
	}
	if pe.distributed != nil {
		pe.collectDistributedMetrics()
	}
	if pe.ordered != nil {
		pe.collectOrderingMetrics()
	}
	if pe.rateLimited != nil {
		pe.collectRateLimitMetrics()
	}
	pe.mu.RUnlock()

	// Format as Prometheus text
	return pe.formatPrometheus()
}

// collectBasicMetrics collects metrics from base pubsub
func (pe *PrometheusExporter) collectBasicMetrics() {
	snapshot := pe.ps.Snapshot()

	// Per-topic metrics
	for topic, tm := range snapshot.Topics {
		labels := pe.mergeLabels(map[string]string{"topic": topic})

		pe.addCounter("messages_published_total",
			"Total number of messages published",
			labels, float64(tm.PublishTotal))

		pe.addCounter("messages_delivered_total",
			"Total number of messages delivered",
			labels, float64(tm.DeliveredTotal))

		pe.addGauge("subscribers_current",
			"Current number of subscribers",
			labels, float64(tm.SubscribersGauge))

		// Dropped messages by policy
		for policy, count := range tm.DroppedByPolicy {
			policyLabels := pe.mergeLabels(map[string]string{
				"topic":  topic,
				"policy": policy,
			})
			pe.addCounter("messages_dropped_total",
				"Total number of messages dropped",
				policyLabels, float64(count))
		}
	}

	// Global metrics
	totalTopics := len(snapshot.Topics)
	pe.addGauge("topics_total",
		"Total number of topics",
		pe.labels, float64(totalTopics))
}

// collectPersistenceMetrics collects persistence metrics
func (pe *PrometheusExporter) collectPersistenceMetrics() {
	stats := pe.persistent.PersistenceStats()

	pe.addCounter("persistence_wal_writes_total",
		"Total WAL writes",
		pe.labels, float64(stats.WALWrites))

	pe.addCounter("persistence_wal_flushes_total",
		"Total WAL flushes",
		pe.labels, float64(stats.WALFlushes))

	pe.addCounter("persistence_wal_bytes_written_total",
		"Total bytes written to WAL",
		pe.labels, float64(stats.WALBytesWrite))

	pe.addCounter("persistence_snapshots_total",
		"Total snapshots created",
		pe.labels, float64(stats.Snapshots))

	pe.addCounter("persistence_restores_total",
		"Total messages restored",
		pe.labels, float64(stats.RestoreCount))

	pe.addCounter("persistence_errors_total",
		"Total persistence errors",
		pe.labels, float64(stats.PersistErrors))

	pe.addGauge("persistence_wal_size_bytes",
		"Current WAL file size",
		pe.labels, float64(stats.CurrentWALSize))
}

// collectDistributedMetrics collects distributed metrics
func (pe *PrometheusExporter) collectDistributedMetrics() {
	stats := pe.distributed.ClusterStats()

	pe.addGauge("cluster_nodes_total",
		"Total cluster nodes",
		pe.labels, float64(stats.TotalNodes))

	pe.addGauge("cluster_nodes_healthy",
		"Healthy cluster nodes",
		pe.labels, float64(stats.HealthyNodes))

	pe.addCounter("cluster_publishes_total",
		"Total cluster publishes",
		pe.labels, float64(stats.ClusterPublishes))

	pe.addCounter("cluster_receives_total",
		"Total cluster receives",
		pe.labels, float64(stats.ClusterReceives))

	pe.addCounter("cluster_forwards_total",
		"Total cluster forwards",
		pe.labels, float64(stats.ClusterForwards))

	pe.addCounter("cluster_errors_total",
		"Total cluster errors",
		pe.labels, float64(stats.ClusterErrors))

	pe.addCounter("cluster_heartbeats_total",
		"Total cluster heartbeats",
		pe.labels, float64(stats.Heartbeats))
}

// collectOrderingMetrics collects ordering metrics
func (pe *PrometheusExporter) collectOrderingMetrics() {
	stats := pe.ordered.OrderingStats()

	pe.addCounter("ordering_publishes_total",
		"Total ordered publishes",
		pe.labels, float64(stats.OrderedPublishes))

	pe.addCounter("ordering_queued_total",
		"Total messages queued",
		pe.labels, float64(stats.QueuedMessages))

	pe.addCounter("ordering_sequence_errors_total",
		"Total sequence errors",
		pe.labels, float64(stats.SequenceErrors))

	pe.addGauge("ordering_topic_queues",
		"Number of topic queues",
		pe.labels, float64(stats.TopicQueues))

	pe.addGauge("ordering_key_queues",
		"Number of key queues",
		pe.labels, float64(stats.KeyQueues))
}

// collectRateLimitMetrics collects rate limit metrics
func (pe *PrometheusExporter) collectRateLimitMetrics() {
	stats := pe.rateLimited.RateLimitStats()

	pe.addCounter("ratelimit_exceeded_total",
		"Total rate limit exceeded",
		pe.labels, float64(stats.LimitExceeded))

	pe.addCounter("ratelimit_waited_total",
		"Total rate limit waits",
		pe.labels, float64(stats.LimitWaited))

	pe.addCounter("ratelimit_adaptive_adjustments_total",
		"Total adaptive adjustments",
		pe.labels, float64(stats.AdaptiveAdj))

	pe.addGauge("ratelimit_current_load",
		"Current system load",
		pe.labels, stats.CurrentLoad)

	pe.addGauge("ratelimit_adaptive_factor",
		"Current adaptive factor",
		pe.labels, stats.AdaptiveFactor)

	pe.addCounter("ratelimit_global_allowed_total",
		"Total globally allowed requests",
		pe.labels, float64(stats.GlobalAllowed))

	pe.addCounter("ratelimit_global_denied_total",
		"Total globally denied requests",
		pe.labels, float64(stats.GlobalDenied))

	pe.addCounter("ratelimit_topic_allowed_total",
		"Total topic allowed requests",
		pe.labels, float64(stats.TopicAllowed))

	pe.addCounter("ratelimit_topic_denied_total",
		"Total topic denied requests",
		pe.labels, float64(stats.TopicDenied))

	pe.addGauge("ratelimit_topic_limiters",
		"Number of topic limiters",
		pe.labels, float64(stats.TopicLimiters))

	pe.addGauge("ratelimit_subscriber_limiters",
		"Number of subscriber limiters",
		pe.labels, float64(stats.SubLimiters))
}

// addCounter adds a counter metric
func (pe *PrometheusExporter) addCounter(name, help string, labels map[string]string, value float64) {
	fullName := pe.metricName(name)
	key := pe.metricKey(fullName, labels)

	pe.registry.counters[key] = &counter{
		name:   fullName,
		help:   help,
		labels: labels,
		value:  value,
	}
}

// addGauge adds a gauge metric
func (pe *PrometheusExporter) addGauge(name, help string, labels map[string]string, value float64) {
	fullName := pe.metricName(name)
	key := pe.metricKey(fullName, labels)

	pe.registry.gauges[key] = &gauge{
		name:   fullName,
		help:   help,
		labels: labels,
		value:  value,
	}
}

// metricName creates a fully qualified metric name
func (pe *PrometheusExporter) metricName(name string) string {
	if pe.namespace == "" {
		return name
	}
	if pe.subsystem == "" {
		return pe.namespace + "_" + name
	}
	return pe.namespace + "_" + pe.subsystem + "_" + name
}

// metricKey creates a unique key for a metric
func (pe *PrometheusExporter) metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString(name)
	buf.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString(k)
		buf.WriteString("=")
		buf.WriteString(labels[k])
	}
	buf.WriteString("}")

	return buf.String()
}

// mergeLabels merges metric-specific labels with global labels
func (pe *PrometheusExporter) mergeLabels(labels map[string]string) map[string]string {
	merged := make(map[string]string)
	for k, v := range pe.labels {
		merged[k] = v
	}
	for k, v := range labels {
		merged[k] = v
	}
	return merged
}

// formatPrometheus formats metrics in Prometheus text format
func (pe *PrometheusExporter) formatPrometheus() string {
	var buf bytes.Buffer

	// Group metrics by name
	metricGroups := make(map[string][]string)

	// Counters
	for _, c := range pe.registry.counters {
		if _, exists := metricGroups[c.name]; !exists {
			metricGroups[c.name] = []string{
				fmt.Sprintf("# HELP %s %s", c.name, c.help),
				fmt.Sprintf("# TYPE %s counter", c.name),
			}
		}
		metricGroups[c.name] = append(metricGroups[c.name],
			formatMetric(c.name, c.labels, c.value))
	}

	// Gauges
	for _, g := range pe.registry.gauges {
		if _, exists := metricGroups[g.name]; !exists {
			metricGroups[g.name] = []string{
				fmt.Sprintf("# HELP %s %s", g.name, g.help),
				fmt.Sprintf("# TYPE %s gauge", g.name),
			}
		}
		metricGroups[g.name] = append(metricGroups[g.name],
			formatMetric(g.name, g.labels, g.value))
	}

	// Sort metric names for consistent output
	names := make([]string, 0, len(metricGroups))
	for name := range metricGroups {
		names = append(names, name)
	}
	sort.Strings(names)

	// Write all metrics
	for _, name := range names {
		for _, line := range metricGroups[name] {
			buf.WriteString(line)
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

// formatMetric formats a single metric line
func formatMetric(name string, labels map[string]string, value float64) string {
	if len(labels) == 0 {
		return fmt.Sprintf("%s %g", name, value)
	}

	// Sort labels for consistent output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, escapeLabel(labels[k])))
	}

	return fmt.Sprintf("%s{%s} %g", name, strings.Join(parts, ","), value)
}

// escapeLabel escapes label values for Prometheus
func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// Handler returns an HTTP handler for Prometheus metrics endpoint
func (pe *PrometheusExporter) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeMethodNotAllowed).
				Message("method not allowed").
				Build())
			return
		}

		metrics := pe.Collect()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, metrics)
	}
}

// StartServer starts a standalone metrics server
func (pe *PrometheusExporter) StartServer(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", pe.Handler())

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return server.ListenAndServe()
}
