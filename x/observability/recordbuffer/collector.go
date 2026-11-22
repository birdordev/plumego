package recordbuffer

import (
	"context"
	"maps"
	"slices"
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
	oldest     int
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

	if c.oldest != 0 {
		rotateRecords(c.records, c.oldest)
		c.oldest = 0
	}
	if max <= 0 {
		c.maxRecords = 0
		return c
	}

	c.maxRecords = max
	if len(c.records) > max {
		cutoff := len(c.records) - max
		clear(c.records[:cutoff])
		c.records = c.records[cutoff:]
	}
	return c
}

func rotateRecords(records []metrics.MetricRecord, oldest int) {
	slices.Reverse(records[:oldest])
	slices.Reverse(records[oldest:])
	slices.Reverse(records)
}

// Record implements metrics.AggregateCollector.
func (c *Collector) Record(ctx context.Context, record metrics.MetricRecord) {
	if c == nil {
		return
	}

	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	record.Labels = maps.Clone(record.Labels)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.base.Record(ctx, record)
	if c.maxRecords > 0 && len(c.records) >= c.maxRecords {
		c.records[c.oldest] = record
		c.oldest = (c.oldest + 1) % len(c.records)
		return
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

// GetStats returns an aggregate snapshot independent of any GetRecords call.
func (c *Collector) GetStats() metrics.CollectorStats {
	if c == nil {
		return metrics.CollectorStats{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.base.GetStats()
}

// Clear resets buffered records and aggregated stats as one operation.
func (c *Collector) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base.Clear()
	clear(c.records)
	c.records = c.records[:0]
	c.oldest = 0
}

// GetRecords returns a copy of buffered metric records.
func (c *Collector) GetRecords() []metrics.MetricRecord {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]metrics.MetricRecord, len(c.records))
	for i := range out {
		record := c.records[(c.oldest+i)%len(c.records)]
		record.Labels = maps.Clone(record.Labels)
		out[i] = record
	}
	return out
}

var _ metrics.AggregateCollector = (*Collector)(nil)
