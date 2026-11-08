package metrics

import (
	"context"
	"time"

	stablemetrics "github.com/birdordev/plumego/metrics"
)

// AggregateCollectorAdapter bridges the AI metrics Collector interface onto the
// stable metrics.AggregateCollector contract.
//
// The adapter records AI counter, gauge, histogram, and timing events as
// stable MetricRecord values. Metric kind is not preserved as a separate stable
// contract field; callers should keep metric names distinct when they need
// different semantic series.
type AggregateCollectorAdapter struct {
	collector stablemetrics.AggregateCollector
}

// NewAggregateCollectorAdapter wraps a stable aggregate collector so AI
// instrumentation can emit into the stable metrics surface.
func NewAggregateCollectorAdapter(collector stablemetrics.AggregateCollector) *AggregateCollectorAdapter {
	return &AggregateCollectorAdapter{collector: collector}
}

// Counter implements Collector.
func (a *AggregateCollectorAdapter) Counter(name string, value float64, tags ...Tag) {
	a.record(name, value, 0, tags...)
}

// Gauge implements Collector.
func (a *AggregateCollectorAdapter) Gauge(name string, value float64, tags ...Tag) {
	a.record(name, value, 0, tags...)
}

// Histogram implements Collector.
func (a *AggregateCollectorAdapter) Histogram(name string, value float64, tags ...Tag) {
	a.record(name, value, 0, tags...)
}

// Timing implements Collector.
func (a *AggregateCollectorAdapter) Timing(name string, duration time.Duration, tags ...Tag) {
	a.record(name, duration.Seconds(), duration, tags...)
}

func (a *AggregateCollectorAdapter) record(name string, value float64, duration time.Duration, tags ...Tag) {
	if a == nil || a.collector == nil {
		return
	}

	a.collector.Record(context.Background(), stablemetrics.MetricRecord{
		Name:      name,
		Value:     value,
		Labels:    toStableLabels(tags),
		Timestamp: time.Now(),
		Duration:  duration,
	})
}

func toStableLabels(tags []Tag) stablemetrics.MetricLabels {
	if len(tags) == 0 {
		return nil
	}

	labels := make(stablemetrics.MetricLabels, len(tags))
	for _, tag := range tags {
		labels[tag.Key] = tag.Value
	}
	return labels
}

var _ Collector = (*AggregateCollectorAdapter)(nil)
