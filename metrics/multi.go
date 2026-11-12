package metrics

import (
	"context"
	"time"
)

// MultiCollector wraps multiple aggregate collectors and forwards all operations to each.
type MultiCollector struct {
	collectors []AggregateCollector
}

// NewMultiCollector ignores nil inputs. With no remaining collectors it
// returns a no-op, and with one it returns that collector unchanged.
// With multiple collectors it forwards in order and gives each target its
// own copy of the labels on Record.
func NewMultiCollector(collectors ...AggregateCollector) AggregateCollector {
	filtered := make([]AggregateCollector, 0, len(collectors))
	for _, collector := range collectors {
		if collector != nil {
			filtered = append(filtered, collector)
		}
	}
	if len(filtered) == 0 {
		return NewNoopCollector()
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &MultiCollector{collectors: filtered}
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
		if record.Labels != nil {
			childRecord.Labels = make(MetricLabels, len(record.Labels))
			for key, value := range record.Labels {
				childRecord.Labels[key] = value
			}
		}
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

// GetStats returns combined statistics from all collectors.
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
		combined.TotalRecords += stats.TotalRecords
		combined.ErrorRecords += stats.ErrorRecords
		combined.ActiveSeries += normalizedActiveSeries(stats)

		for k, v := range stats.NameBreakdown {
			combined.NameBreakdown[k] += v
		}

		if combined.StartTime.IsZero() || (!stats.StartTime.IsZero() && stats.StartTime.Before(combined.StartTime)) {
			combined.StartTime = stats.StartTime
		}
	}

	return combined
}

// Clear clears all collectors.
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
