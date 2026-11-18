package metrics

import (
	"context"
	"maps"
	"math"
	"reflect"
	"time"
)

// MultiCollector wraps multiple aggregate collectors and forwards all operations to each.
type MultiCollector struct {
	collectors []AggregateCollector
}

// NewMultiCollector ignores nil interfaces and nil collector pointers. With no remaining collectors it
// returns a no-op, and with one it returns that collector unchanged.
// With multiple collectors it forwards in order and gives each target its
// own copy of the labels on Record.
func NewMultiCollector(collectors ...AggregateCollector) AggregateCollector {
	var first AggregateCollector
	var filtered []AggregateCollector
	for i, collector := range collectors {
		value := reflect.ValueOf(collector)
		if !value.IsValid() || (value.Kind() == reflect.Pointer && value.IsNil()) {
			continue
		}
		if first == nil {
			first = collector
			continue
		}
		if filtered == nil {
			filtered = make([]AggregateCollector, 1, 1+len(collectors)-i)
			filtered[0] = first
		}
		filtered = append(filtered, collector)
	}
	if filtered != nil {
		return &MultiCollector{collectors: filtered}
	}
	if first != nil {
		return first
	}
	return NewNoopCollector()
}

// Record forwards the record to all collectors.
func (m *MultiCollector) Record(ctx context.Context, record MetricRecord) {
	if m == nil {
		return
	}
	for _, c := range m.collectors {
		if c == nil {
			continue
		}
		childRecord := record
		childRecord.Labels = maps.Clone(record.Labels)
		c.Record(ctx, childRecord)
	}
}

// ObserveHTTP forwards the HTTP observation to all collectors.
func (m *MultiCollector) ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
	if m == nil {
		return
	}
	for _, c := range m.collectors {
		if c == nil {
			continue
		}
		c.ObserveHTTP(ctx, method, path, status, bytes, duration)
	}
}

// GetStats sums counters, active series, and name breakdowns from individual
// child snapshots. A record counted by two children contributes twice.
// For a child with zero ActiveSeries, its NameBreakdown size contributes to
// the active series total.
// Sums saturate at the respective field's maximum value.
// StartTime is the earliest nonzero child start time. The child snapshots
// are read independently.
func (m *MultiCollector) GetStats() CollectorStats {
	if m == nil || len(m.collectors) == 0 {
		return emptyCollectorStats()
	}

	combined := CollectorStats{
		NameBreakdown: make(map[string]int64),
	}

	for _, c := range m.collectors {
		if c == nil {
			continue
		}
		stats := c.GetStats()
		combined.TotalRecords = addCounterSaturated(combined.TotalRecords, stats.TotalRecords, math.MaxInt64)
		combined.ErrorRecords = addCounterSaturated(combined.ErrorRecords, stats.ErrorRecords, math.MaxInt64)
		combined.ActiveSeries = addCounterSaturated(combined.ActiveSeries, normalizedActiveSeries(stats), math.MaxInt)

		for k, v := range stats.NameBreakdown {
			combined.NameBreakdown[k] = addCounterSaturated(combined.NameBreakdown[k], v, math.MaxInt64)
		}

		if combined.StartTime.IsZero() || (!stats.StartTime.IsZero() && stats.StartTime.Before(combined.StartTime)) {
			combined.StartTime = stats.StartTime
		}
	}

	return combined
}

func addCounterSaturated[T int | int64](total, delta, limit T) T {
	if delta > 0 && total > limit-delta {
		return limit
	}
	return total + delta
}

// Clear resets each child in order. Each child reset takes effect independently.
func (m *MultiCollector) Clear() {
	if m == nil {
		return
	}
	for _, c := range m.collectors {
		if c == nil {
			continue
		}
		c.Clear()
	}
}

var (
	_ AggregateCollector = (*MultiCollector)(nil)
	_ Recorder           = (*MultiCollector)(nil)
	_ HTTPObserver       = (*MultiCollector)(nil)
	_ StatsReader        = (*MultiCollector)(nil)
	_ Resetter           = (*MultiCollector)(nil)
)
