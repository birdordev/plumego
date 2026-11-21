package metrics

import (
	"context"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// HTTP metrics
	MetricHTTPRequest = "http_request"
)

// MetricLabels represents key-value labels for metrics
type MetricLabels map[string]string

const (
	labelMethod = "method"
	labelPath   = "path"
	labelStatus = "status"
)

// MetricRecord represents a single metric record
type MetricRecord struct {
	// Name is the canonical metric identity across stable and extension-owned records.
	Name string
	// Value uses seconds as the canonical unit for duration/latency metrics.
	// Non-duration metrics (for example counters or queue depth) may use domain-specific units.
	Value     float64
	Labels    MetricLabels
	Timestamp time.Time
	Duration  time.Duration
	Error     error
}

func durationValueSeconds(duration time.Duration) float64 {
	return duration.Seconds()
}

// NewHTTPRecord returns the canonical stable record shape for HTTP request metrics.
//
// Response bytes are intentionally not encoded as labels because byte counts are
// measurements, not dimensions. Collectors that track bytes should keep that
// aggregation in their own implementation.
func NewHTTPRecord(method, path string, status int, duration time.Duration) MetricRecord {
	return MetricRecord{
		Name:      MetricHTTPRequest,
		Value:     durationValueSeconds(duration),
		Timestamp: time.Now(),
		Labels: MetricLabels{
			labelMethod: method,
			labelPath:   path,
			labelStatus: strconv.Itoa(status),
		},
		Duration: duration,
	}
}

// AggregateCollector is the stable collector surface.
// Prefer narrower interfaces at module boundaries and reserve this contract for
// collector implementations or fan-out adapters that need generic recording,
// shared HTTP observation, stats, and reset semantics.
type AggregateCollector interface {
	// Record records a single metric
	Record(ctx context.Context, record MetricRecord)

	// ObserveHTTP is a convenience method for HTTP request metrics
	ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration)

	// GetStats returns current statistics
	GetStats() CollectorStats

	// Clear resets all metrics
	Clear()
}

// Recorder captures generic metric records without any domain-specific helpers.
type Recorder interface {
	Record(ctx context.Context, record MetricRecord)
}

// HTTPObserver captures only HTTP request metrics.
// Use this narrower contract in transport middleware that should not depend on
// the full cross-module metrics surface.
type HTTPObserver interface {
	ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration)
}

// StatsReader exposes aggregated collector statistics.
type StatsReader interface {
	GetStats() CollectorStats
}

// Resetter clears accumulated metrics state.
type Resetter interface {
	Clear()
}

// CollectorStats provides a contract for collector statistics payloads.
//
// Counts supplied by collectors must be nonnegative.
//
// Mandatory fields for all collectors:
//   - TotalRecords: total number of records processed by the collector.
//   - ErrorRecords: number of records classified as errors.
//   - ActiveSeries: number of currently active metric series maintained.
//   - StartTime: collector start/reset time (zero only when intentionally unknown).
//
// Optional fields:
//   - NameBreakdown: per-metric-name counters when the collector can provide them.
type CollectorStats struct {
	TotalRecords  int64            `json:"total_records"`
	ErrorRecords  int64            `json:"error_records"`
	ActiveSeries  int              `json:"active_series"`
	StartTime     time.Time        `json:"start_time"`
	NameBreakdown map[string]int64 `json:"name_breakdown"`
}

// BaseMetricsCollector provides a base implementation for metrics collectors
type BaseMetricsCollector struct {
	mu    sync.RWMutex
	stats CollectorStats
}

// NewBaseMetricsCollector creates a new base metrics collector.
func NewBaseMetricsCollector() *BaseMetricsCollector {
	return &BaseMetricsCollector{
		stats: newCollectorStats(time.Now()),
	}
}

// Record implements the aggregate collector contract.
func (b *BaseMetricsCollector) Record(ctx context.Context, record MetricRecord) {
	if b == nil {
		return
	}
	b.record(record.Name, metricRecordIsError(record))
}

func (b *BaseMetricsCollector) record(name string, isError bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureInitializedLocked()

	b.stats.TotalRecords++
	if name != "" {
		b.stats.NameBreakdown[name]++
	}

	if isError {
		b.stats.ErrorRecords++
	}
}

// ObserveHTTP counts each observation under [MetricHTTPRequest]. Status codes
// of 400 or higher count as errors. Method, path, byte count, and duration do
// not contribute to these statistics.
func (b *BaseMetricsCollector) ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
	if b != nil {
		b.record(MetricHTTPRequest, isHTTPErrorStatus(status))
	}
}

// GetStats returns a snapshot of the current statistics.
// NameBreakdown is a non-nil, writable map owned by the caller.
func (b *BaseMetricsCollector) GetStats() CollectorStats {
	if b == nil {
		return emptyCollectorStats()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	stats := b.stats
	stats.NameBreakdown = cloneBreakdown(stats.NameBreakdown)
	stats.ActiveSeries = normalizedActiveSeries(stats)
	return stats
}

// Clear resets all metrics
func (b *BaseMetricsCollector) Clear() {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.stats = newCollectorStats(time.Now())
}

func (b *BaseMetricsCollector) ensureInitializedLocked() {
	if b.stats.NameBreakdown == nil {
		b.stats.NameBreakdown = make(map[string]int64)
	}
	if b.stats.StartTime.IsZero() {
		b.stats.StartTime = time.Now()
	}
}

func cloneBreakdown(breakdown map[string]int64) map[string]int64 {
	if breakdown == nil {
		return make(map[string]int64)
	}
	return maps.Clone(breakdown)
}

func emptyCollectorStats() CollectorStats {
	return newCollectorStats(time.Time{})
}

func newCollectorStats(startTime time.Time) CollectorStats {
	return CollectorStats{
		StartTime:     startTime,
		NameBreakdown: make(map[string]int64),
	}
}

func normalizedActiveSeries(stats CollectorStats) int {
	if stats.ActiveSeries != 0 {
		return stats.ActiveSeries
	}
	return len(stats.NameBreakdown)
}

func metricRecordIsError(record MetricRecord) bool {
	if record.Error != nil {
		return true
	}
	if record.Name != MetricHTTPRequest {
		return false
	}
	status, err := strconv.Atoi(record.Labels[labelStatus])
	return err == nil && isHTTPErrorStatus(status)
}

func isHTTPErrorStatus(status int) bool { return status >= http.StatusBadRequest }

var (
	_ AggregateCollector = (*BaseMetricsCollector)(nil)
	_ Recorder           = (*BaseMetricsCollector)(nil)
	_ HTTPObserver       = (*BaseMetricsCollector)(nil)
	_ StatsReader        = (*BaseMetricsCollector)(nil)
	_ Resetter           = (*BaseMetricsCollector)(nil)
)
