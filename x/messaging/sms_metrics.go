package messaging

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/metrics"
)

// ErrNilMetricRecordSource is returned when an SMS Prometheus exporter is created without a metric source.
var ErrNilMetricRecordSource = errors.New("messaging: sms prometheus exporter requires a metric record source")

const (
	LabelQueue    = "queue"
	LabelState    = "state"
	LabelProvider = "provider"
	LabelTenant   = "tenant"
	LabelResult   = "result"
	LabelStatus   = "status"
	LabelAttempt  = "attempt"
)

const (
	MetricQueueDepth     = "sms_gateway_queue_depth"
	MetricSendLatency    = "sms_gateway_send_latency"
	MetricProviderResult = "sms_gateway_provider_result"
	MetricReceiptDelay   = "sms_gateway_receipt_delay"
	MetricRetry          = "sms_gateway_retry"
	MetricStatus         = "sms_gateway_status"
)

type SMSQueueStats struct {
	Queued  int64
	Leased  int64
	Dead    int64
	Expired int64
}

type SMSMetricsReporter struct {
	Recorder metrics.Recorder
	Now      func() time.Time
}

func NewSMSMetricsReporter(recorder metrics.Recorder) *SMSMetricsReporter {
	return &SMSMetricsReporter{Recorder: recorder, Now: time.Now}
}

func (r *SMSMetricsReporter) RecordQueueDepth(ctx context.Context, queue string, stats SMSQueueStats) {
	if r == nil || r.Recorder == nil {
		return
	}
	r.record(ctx, MetricQueueDepth, float64(stats.Queued), smsLabels(queue, messageStatusQueued, "", "", "", "", 0), 0, nil)
	r.record(ctx, MetricQueueDepth, float64(stats.Leased), smsLabels(queue, "leased", "", "", "", "", 0), 0, nil)
	r.record(ctx, MetricQueueDepth, float64(stats.Dead), smsLabels(queue, messageStatusDead, "", "", "", "", 0), 0, nil)
	r.record(ctx, MetricQueueDepth, float64(stats.Expired), smsLabels(queue, "expired", "", "", "", "", 0), 0, nil)
}

func (r *SMSMetricsReporter) RecordSendLatency(ctx context.Context, tenantID, provider string, duration time.Duration, err error) {
	if r == nil || r.Recorder == nil {
		return
	}
	r.record(ctx, MetricSendLatency, duration.Seconds(), smsLabels("", "", provider, tenantID, "", "", 0), duration, err)
}

func (r *SMSMetricsReporter) RecordProviderResult(ctx context.Context, tenantID, provider string, success bool) {
	if r == nil || r.Recorder == nil {
		return
	}
	result := "success"
	if !success {
		result = "failure"
	}
	r.record(ctx, MetricProviderResult, 1, smsLabels("", "", provider, tenantID, result, "", 0), 0, nil)
}

func (r *SMSMetricsReporter) RecordReceiptDelay(ctx context.Context, tenantID, provider string, delay time.Duration) {
	if r == nil || r.Recorder == nil {
		return
	}
	r.record(ctx, MetricReceiptDelay, delay.Seconds(), smsLabels("", "", provider, tenantID, "", "", 0), delay, nil)
}

func (r *SMSMetricsReporter) RecordRetry(ctx context.Context, tenantID, provider string, attempt int) {
	if r == nil || r.Recorder == nil {
		return
	}
	r.record(ctx, MetricRetry, 1, smsLabels("", "", provider, tenantID, "", "", attempt), 0, nil)
}

func (r *SMSMetricsReporter) RecordStatus(ctx context.Context, tenantID, status string) {
	if r == nil || r.Recorder == nil {
		return
	}
	r.record(ctx, MetricStatus, 1, smsLabels("", "", "", tenantID, "", status, 0), 0, nil)
}

func (r *SMSMetricsReporter) record(ctx context.Context, name string, value float64, labels metrics.MetricLabels, duration time.Duration, err error) {
	r.Recorder.Record(ctx, metrics.MetricRecord{
		Name:     name,
		Value:    value,
		Labels:   labels,
		Duration: duration,
		Error:    err,
	})
}

type metricRecordSource interface {
	GetRecords() []metrics.MetricRecord
}

type SMSPrometheusExporter struct {
	namespace string
	source    metricRecordSource
}

// NewSMSPrometheusExporter creates an SMS Prometheus exporter and returns an
// error for invalid dependencies.
func NewSMSPrometheusExporter(namespace string, source metricRecordSource) (*SMSPrometheusExporter, error) {
	if source == nil {
		return nil, ErrNilMetricRecordSource
	}
	if namespace == "" {
		namespace = "plumego"
	}
	return &SMSPrometheusExporter{namespace: namespace, source: source}, nil
}

func (e *SMSPrometheusExporter) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		e.writeMetrics(w, e.source.GetRecords())
	})
}

func (e *SMSPrometheusExporter) writeMetrics(w http.ResponseWriter, records []metrics.MetricRecord) {
	snapshot := aggregateSMSGatewayRecords(records)
	if snapshot == nil {
		return
	}

	_, _ = fmt.Fprintf(w, "# HELP %s_sms_gateway_queue_depth Current queue depth by state.\n", e.namespace)
	_, _ = fmt.Fprintf(w, "# TYPE %s_sms_gateway_queue_depth gauge\n", e.namespace)
	for _, key := range sortedSMSGaugeKeys(snapshot.queueDepth) {
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_queue_depth%s %.0f\n", e.namespace, formatSMSLabels(key, []string{"queue", "state"}), snapshot.queueDepth[key])
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "# HELP %s_sms_gateway_send_latency_seconds Send latency summary by provider.\n", e.namespace)
	_, _ = fmt.Fprintf(w, "# TYPE %s_sms_gateway_send_latency_seconds summary\n", e.namespace)
	for _, key := range sortedSMSLatencyKeys(snapshot.sendLatency) {
		stats := snapshot.sendLatency[key]
		labels := formatSMSLabels(key, []string{"tenant", "provider"})
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_send_latency_seconds_sum%s %.9f\n", e.namespace, labels, stats.sum)
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_send_latency_seconds_count%s %d\n", e.namespace, labels, stats.count)
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_send_latency_seconds_min%s %.9f\n", e.namespace, labels, stats.min)
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_send_latency_seconds_max%s %.9f\n", e.namespace, labels, stats.max)
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "# HELP %s_sms_gateway_receipt_delay_seconds Receipt delay summary by provider.\n", e.namespace)
	_, _ = fmt.Fprintf(w, "# TYPE %s_sms_gateway_receipt_delay_seconds summary\n", e.namespace)
	for _, key := range sortedSMSLatencyKeys(snapshot.receiptDelay) {
		stats := snapshot.receiptDelay[key]
		labels := formatSMSLabels(key, []string{"tenant", "provider"})
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_receipt_delay_seconds_sum%s %.9f\n", e.namespace, labels, stats.sum)
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_receipt_delay_seconds_count%s %d\n", e.namespace, labels, stats.count)
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_receipt_delay_seconds_min%s %.9f\n", e.namespace, labels, stats.min)
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_receipt_delay_seconds_max%s %.9f\n", e.namespace, labels, stats.max)
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "# HELP %s_sms_gateway_provider_result_total Provider send results.\n", e.namespace)
	_, _ = fmt.Fprintf(w, "# TYPE %s_sms_gateway_provider_result_total counter\n", e.namespace)
	for _, key := range sortedSMSCounterKeys(snapshot.providerResult) {
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_provider_result_total%s %d\n", e.namespace, formatSMSLabels(key, []string{"tenant", "provider", "result"}), snapshot.providerResult[key])
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "# HELP %s_sms_gateway_retry_total Retry attempts by provider.\n", e.namespace)
	_, _ = fmt.Fprintf(w, "# TYPE %s_sms_gateway_retry_total counter\n", e.namespace)
	for _, key := range sortedSMSCounterKeys(snapshot.retry) {
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_retry_total%s %d\n", e.namespace, formatSMSLabels(key, []string{"tenant", "provider", "attempt"}), snapshot.retry[key])
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "# HELP %s_sms_gateway_status_total Message status transitions.\n", e.namespace)
	_, _ = fmt.Fprintf(w, "# TYPE %s_sms_gateway_status_total counter\n", e.namespace)
	for _, key := range sortedSMSCounterKeys(snapshot.status) {
		_, _ = fmt.Fprintf(w, "%s_sms_gateway_status_total%s %d\n", e.namespace, formatSMSLabels(key, []string{"tenant", "status"}), snapshot.status[key])
	}
}

type smsKey struct {
	queue    string
	state    string
	provider string
	tenant   string
	result   string
	status   string
	attempt  string
}

type smsLatencyStats struct {
	count uint64
	sum   float64
	min   float64
	max   float64
}

type smsSnapshot struct {
	queueDepth     map[smsKey]float64
	sendLatency    map[smsKey]smsLatencyStats
	receiptDelay   map[smsKey]smsLatencyStats
	providerResult map[smsKey]uint64
	retry          map[smsKey]uint64
	status         map[smsKey]uint64
}

func aggregateSMSGatewayRecords(records []metrics.MetricRecord) *smsSnapshot {
	snapshot := &smsSnapshot{
		queueDepth:     make(map[smsKey]float64),
		sendLatency:    make(map[smsKey]smsLatencyStats),
		receiptDelay:   make(map[smsKey]smsLatencyStats),
		providerResult: make(map[smsKey]uint64),
		retry:          make(map[smsKey]uint64),
		status:         make(map[smsKey]uint64),
	}

	for _, record := range records {
		key := smsKey{
			queue:    record.Labels[LabelQueue],
			state:    record.Labels[LabelState],
			provider: record.Labels[LabelProvider],
			tenant:   record.Labels[LabelTenant],
			result:   record.Labels[LabelResult],
			status:   record.Labels[LabelStatus],
			attempt:  record.Labels[LabelAttempt],
		}

		switch record.Name {
		case MetricQueueDepth:
			snapshot.queueDepth[key] = record.Value
		case MetricSendLatency:
			snapshot.sendLatency[key] = accumulateLatency(snapshot.sendLatency[key], record)
		case MetricReceiptDelay:
			snapshot.receiptDelay[key] = accumulateLatency(snapshot.receiptDelay[key], record)
		case MetricProviderResult:
			snapshot.providerResult[key] += metricCounterIncrement(record)
		case MetricRetry:
			snapshot.retry[key] += metricCounterIncrement(record)
		case MetricStatus:
			snapshot.status[key] += metricCounterIncrement(record)
		}
	}

	if len(snapshot.queueDepth) == 0 && len(snapshot.sendLatency) == 0 && len(snapshot.receiptDelay) == 0 && len(snapshot.providerResult) == 0 && len(snapshot.retry) == 0 && len(snapshot.status) == 0 {
		return nil
	}

	return snapshot
}

func accumulateLatency(current smsLatencyStats, record metrics.MetricRecord) smsLatencyStats {
	seconds := record.Value
	if record.Duration > 0 {
		seconds = record.Duration.Seconds()
	}
	current.count++
	current.sum += seconds
	if current.count == 1 || seconds < current.min {
		current.min = seconds
	}
	if current.count == 1 || seconds > current.max {
		current.max = seconds
	}
	return current
}

func metricCounterIncrement(record metrics.MetricRecord) uint64 {
	if record.Value <= 0 {
		return 1
	}
	return uint64(record.Value)
}

func smsLabels(queue, state, provider, tenantID, result, status string, attempt int) metrics.MetricLabels {
	labels := metrics.MetricLabels{}
	if queue != "" {
		labels[LabelQueue] = queue
	}
	if state != "" {
		labels[LabelState] = state
	}
	if provider != "" {
		labels[LabelProvider] = provider
	}
	if tenantID != "" {
		labels[LabelTenant] = tenantID
	}
	if result != "" {
		labels[LabelResult] = result
	}
	if status != "" {
		labels[LabelStatus] = status
	}
	if attempt > 0 {
		labels[LabelAttempt] = strconv.Itoa(attempt)
	}
	return labels
}

func formatSMSLabels(key smsKey, order []string) string {
	parts := make([]string, 0, len(order))
	for _, label := range order {
		value := ""
		switch label {
		case LabelQueue:
			value = key.queue
		case LabelState:
			value = key.state
		case LabelProvider:
			value = key.provider
		case LabelTenant:
			value = key.tenant
		case LabelResult:
			value = key.result
		case LabelStatus:
			value = key.status
		case LabelAttempt:
			value = key.attempt
		}
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", label, escapePrometheusLabelValue(value)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapePrometheusLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func sortedSMSGaugeKeys(input map[smsKey]float64) []smsKey {
	keys := make([]smsKey, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sortSMSKeys(keys)
	return keys
}

func sortedSMSLatencyKeys(input map[smsKey]smsLatencyStats) []smsKey {
	keys := make([]smsKey, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sortSMSKeys(keys)
	return keys
}

func sortedSMSCounterKeys(input map[smsKey]uint64) []smsKey {
	keys := make([]smsKey, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sortSMSKeys(keys)
	return keys
}

func sortSMSKeys(keys []smsKey) {
	sort.Slice(keys, func(i, j int) bool {
		left := []string{keys[i].queue, keys[i].state, keys[i].provider, keys[i].tenant, keys[i].result, keys[i].status, keys[i].attempt}
		right := []string{keys[j].queue, keys[j].state, keys[j].provider, keys[j].tenant, keys[j].result, keys[j].status, keys[j].attempt}
		for idx := range left {
			if left[idx] == right[idx] {
				continue
			}
			return left[idx] < right[idx]
		}
		return false
	})
}
