package metrics

import (
	"context"
	"time"
)

// NoopCollector is a metrics collector that does nothing.
// It implements AggregateCollector but discards all metrics.
//
// This collector is useful for:
//   - Testing when you don't want to track metrics
//   - Production environments where metrics are disabled
//   - Benchmarking to measure overhead of metrics collection
//
// All operations are no-ops with minimal overhead.
//
// Example:
//
//	import "github.com/birdordev/plumego/metrics"
//
//	// Disable metrics collection
//	collector := metrics.NewNoopCollector()
//
//	// All methods do nothing
//	collector.ObserveHTTP(ctx, "GET", "/api/users", 200, 100, 50*time.Millisecond)
//	collector.Clear() // No-op
type NoopCollector struct{}

// NewNoopCollector creates a new no-op metrics collector.
//
// Example:
//
//	import "github.com/birdordev/plumego/metrics"
//
//	collector := metrics.NewNoopCollector()
func NewNoopCollector() *NoopCollector {
	return &NoopCollector{}
}

// Record does nothing and returns immediately.
func (n *NoopCollector) Record(ctx context.Context, record MetricRecord) {
	// No-op
}

// ObserveHTTP does nothing and returns immediately.
func (n *NoopCollector) ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
	// No-op
}

// GetStats returns empty statistics with an initialized caller-owned name
// breakdown map.
//
// Example:
//
//	import "github.com/birdordev/plumego/metrics"
//
//	collector := metrics.NewNoopCollector()
//	stats := collector.GetStats()
//	// stats counters will be zero; stats.NameBreakdown is safe to mutate.
func (n *NoopCollector) GetStats() CollectorStats {
	return emptyCollectorStats()
}

// Clear does nothing and returns immediately.
func (n *NoopCollector) Clear() {
	// No-op
}

var (
	_ AggregateCollector = (*NoopCollector)(nil)
	_ Recorder           = (*NoopCollector)(nil)
	_ HTTPObserver       = (*NoopCollector)(nil)
	_ StatsReader        = (*NoopCollector)(nil)
	_ Resetter           = (*NoopCollector)(nil)
)
