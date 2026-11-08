package recordbuffer

import (
	"context"
	"sync"
	"time"

	"github.com/birdordev/plumego/metrics"
)

const defaultMaxRecords = 10000

// Collector adds owner-side record buffering on top of the stable metrics collector contracts.
type Collector struct {
	base *metrics.BaseMetricsCollector

	mu         sync.RWMutex
	records    []metrics.MetricRecord
	maxRecords int
}

// NewCollector creates a buffered record collector for observability-owned record inspection.
func NewCollector() *Collector {
	return &Collector{
		base:       metrics.NewBaseMetricsCollector(),
		records:    make([]metrics.MetricRecord, 0, 1000),
		maxRecords: defaultMaxRecords,
	}
}

// WithMaxRecords limits how many records are retained in memory.
// A non-positive value disables the retention limit.
func (c *Collector) WithMaxRecords(max int) *Collector {
	c.mu.Lock()
	defer c.mu.Unlock()

	if max <= 0 {
		c.maxRecords = 0
		return c
	}

	c.maxRecords = max
	if len(c.records) > max {
		c.records = c.records[len(c.records)-max:]
	}
	return c
}

// Record implements metrics.AggregateCollector.
func (c *Collector) Record(ctx context.Context, record metrics.MetricRecord) {
	if c == nil {
		return
	}

	c.base.Record(ctx, record)

	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if len(record.Labels) > 0 {
		record.Labels = cloneLabels(record.Labels)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.maxRecords > 0 && len(c.records) >= c.maxRecords {
		c.records = c.records[1:]
	}
	c.records = append(c.records, record)
}

// ObserveHTTP implements metrics.AggregateCollector.
func (c *Collector) ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
	if c == nil {
		return
	}
	c.Record(ctx, metrics.NewHTTPRecord(method, path, status, duration))
}

// GetStats returns aggregated collector stats.
func (c *Collector) GetStats() metrics.CollectorStats {
	if c == nil {
		return metrics.CollectorStats{}
	}
	return c.base.GetStats()
}

// Clear resets buffered records and aggregated stats.
func (c *Collector) Clear() {
	if c == nil {
		return
	}
	c.base.Clear()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = c.records[:0]
}

// GetRecords returns a copy of buffered metric records.
func (c *Collector) GetRecords() []metrics.MetricRecord {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]metrics.MetricRecord, len(c.records))
	for i, record := range c.records {
		if len(record.Labels) > 0 {
			record.Labels = cloneLabels(record.Labels)
		}
		out[i] = record
	}
	return out
}

func cloneLabels(labels metrics.MetricLabels) metrics.MetricLabels {
	if len(labels) == 0 {
		return nil
	}
	out := make(metrics.MetricLabels, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

var _ metrics.AggregateCollector = (*Collector)(nil)
