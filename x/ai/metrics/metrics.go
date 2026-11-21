// Package metrics provides AI-specific observability interfaces for the x/ai
// extension family. Its [Collector] interface uses a Counter/Gauge/Histogram/Timing
// method set optimised for AI provider telemetry (model, latency, token counts).
//
// This interface is intentionally distinct from the stable [metrics.AggregateCollector]
// in the root metrics package, which uses a Record(ctx, MetricRecord) model suited
// for general HTTP and app-level metrics. The two interfaces serve different
// collection shapes and are not interchangeable. Callers that need both should
// inject each through separate fields.
//
// M-022 card 1512 tracks the question of whether these surfaces should converge
// or remain separate. Until that decision lands, treat x/ai/metrics as an
// AI-scoped adapter boundary and do not embed it in stable-root types.
package metrics

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrOddTagArguments is returned when tag key-value input has an odd number of entries.
var ErrOddTagArguments = errors.New("metrics: tag arguments must be key-value pairs")

// Collector is the main interface for collecting metrics.
// Implementations can export to various backends (Prometheus, OpenTelemetry, StatsD, etc.)
type Collector interface {
	// Counter increments a counter metric
	Counter(name string, value float64, tags ...Tag)

	// Gauge sets a gauge metric to a specific value
	Gauge(name string, value float64, tags ...Tag)

	// Histogram records a histogram/summary metric
	Histogram(name string, value float64, tags ...Tag)

	// Timing records a timing metric (convenience method for histograms in seconds)
	Timing(name string, duration time.Duration, tags ...Tag)
}

// Tag represents a metric label/tag (key-value pair)
type Tag struct {
	Key   string
	Value string
}

// TagsE creates a slice of tags from key-value pairs and returns an error for
// malformed runtime input.
func Tags(keyValues ...string) ([]Tag, error) {
	if len(keyValues)%2 != 0 {
		return nil, ErrOddTagArguments
	}

	tags := make([]Tag, 0, len(keyValues)/2)
	for i := 0; i < len(keyValues); i += 2 {
		tags = append(tags, Tag{Key: keyValues[i], Value: keyValues[i+1]})
	}
	return tags, nil
}

// NoOpCollector is a collector that does nothing (for testing/disabled metrics)
type NoOpCollector struct{}

func (n *NoOpCollector) Counter(name string, value float64, tags ...Tag)         {}
func (n *NoOpCollector) Gauge(name string, value float64, tags ...Tag)           {}
func (n *NoOpCollector) Histogram(name string, value float64, tags ...Tag)       {}
func (n *NoOpCollector) Timing(name string, duration time.Duration, tags ...Tag) {}

// MemoryCollector is an in-memory metrics collector.
// Useful for development, testing, and small deployments.
type MemoryCollector struct {
	counters   map[string]*counterMetric
	gauges     map[string]*gaugeMetric
	histograms map[string]*histogramMetric
	mu         sync.RWMutex
}

type counterMetric struct {
	value float64
	tags  map[string]string
	mu    sync.RWMutex
}

type gaugeMetric struct {
	value float64
	tags  map[string]string
	mu    sync.RWMutex
}

type histogramMetric struct {
	count  int64
	sum    float64
	min    float64
	max    float64
	values []float64 // Keep last N values for percentiles
	tags   map[string]string
	mu     sync.RWMutex
}

// NewMemoryCollector creates a new in-memory metrics collector.
func NewMemoryCollector() *MemoryCollector {
	return &MemoryCollector{
		counters:   make(map[string]*counterMetric),
		gauges:     make(map[string]*gaugeMetric),
		histograms: make(map[string]*histogramMetric),
	}
}

// Counter implements Collector
func (mc *MemoryCollector) Counter(name string, value float64, tags ...Tag) {
	labels := tagsToMap(tags)
	key := buildKey(name, labels)

	mc.mu.Lock()
	counter, exists := mc.counters[key]
	if !exists {
		counter = &counterMetric{
			value: 0,
			tags:  labels,
		}
		mc.counters[key] = counter
	}
	mc.mu.Unlock()

	counter.mu.Lock()
	counter.value += value
	counter.mu.Unlock()
}

// Gauge implements Collector
func (mc *MemoryCollector) Gauge(name string, value float64, tags ...Tag) {
	labels := tagsToMap(tags)
	key := buildKey(name, labels)

	mc.mu.Lock()
	gauge, exists := mc.gauges[key]
	if !exists {
		gauge = &gaugeMetric{
			value: 0,
			tags:  labels,
		}
		mc.gauges[key] = gauge
	}
	mc.mu.Unlock()

	gauge.mu.Lock()
	gauge.value = value
	gauge.mu.Unlock()
}

// Histogram implements Collector
func (mc *MemoryCollector) Histogram(name string, value float64, tags ...Tag) {
	labels := tagsToMap(tags)
	key := buildKey(name, labels)

	mc.mu.Lock()
	hist, exists := mc.histograms[key]
	if !exists {
		hist = &histogramMetric{
			count:  0,
			sum:    0,
			min:    value,
			max:    value,
			values: make([]float64, 0, 1000), // Keep last 1000 values
			tags:   labels,
		}
		mc.histograms[key] = hist
	}
	mc.mu.Unlock()

	hist.mu.Lock()
	hist.count++
	hist.sum += value
	if value < hist.min {
		hist.min = value
	}
	if value > hist.max {
		hist.max = value
	}

	// Percentiles use the latest samples; count, sum, and extrema are cumulative.
	if len(hist.values) < 1000 {
		hist.values = append(hist.values, value)
	} else {
		// Circular buffer: replace oldest
		hist.values[(hist.count-1)%int64(len(hist.values))] = value
	}
	hist.mu.Unlock()
}

// Timing implements Collector
func (mc *MemoryCollector) Timing(name string, duration time.Duration, tags ...Tag) {
	mc.Histogram(name, duration.Seconds(), tags...)
}

// Snapshot holds a copy of collected metrics for export.
// Series keys use name{key=value,...}, with labels sorted by key. Percent signs,
// backslashes, braces, commas, and equal signs within components are percent
// escaped. Ordinary untagged names are used directly as keys.
type Snapshot struct {
	Counters   map[string]CounterSnapshot
	Gauges     map[string]GaugeSnapshot
	Histograms map[string]HistogramSnapshot
	Timestamp  time.Time
}

type CounterSnapshot struct {
	Value float64
	Tags  map[string]string
}

type GaugeSnapshot struct {
	Value float64
	Tags  map[string]string
}

type HistogramSnapshot struct {
	Count int64
	Sum   float64
	Min   float64
	Max   float64
	Avg   float64
	P50   float64
	P95   float64
	P99   float64
	Tags  map[string]string
}

// Snapshot returns a consistent snapshot of all metrics
func (mc *MemoryCollector) Snapshot() *Snapshot {
	snapshot := &Snapshot{
		Counters:   make(map[string]CounterSnapshot),
		Gauges:     make(map[string]GaugeSnapshot),
		Histograms: make(map[string]HistogramSnapshot),
		Timestamp:  time.Now(),
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Snapshot counters
	for key, counter := range mc.counters {
		counter.mu.RLock()
		snapshot.Counters[key] = CounterSnapshot{
			Value: counter.value,
			Tags:  copyMap(counter.tags),
		}
		counter.mu.RUnlock()
	}

	// Snapshot gauges
	for key, gauge := range mc.gauges {
		gauge.mu.RLock()
		snapshot.Gauges[key] = GaugeSnapshot{
			Value: gauge.value,
			Tags:  copyMap(gauge.tags),
		}
		gauge.mu.RUnlock()
	}

	// Snapshot histograms
	for key, hist := range mc.histograms {
		hist.mu.RLock()
		avg := float64(0)
		if hist.count > 0 {
			avg = hist.sum / float64(hist.count)
		}

		snapshot.Histograms[key] = HistogramSnapshot{
			Count: hist.count,
			Sum:   hist.sum,
			Min:   hist.min,
			Max:   hist.max,
			Avg:   avg,
			P50:   percentile(hist.values, 0.50),
			P95:   percentile(hist.values, 0.95),
			P99:   percentile(hist.values, 0.99),
			Tags:  copyMap(hist.tags),
		}
		hist.mu.RUnlock()
	}

	return snapshot
}

// Reset clears all metrics (useful for testing)
func (mc *MemoryCollector) Reset() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.counters = make(map[string]*counterMetric)
	mc.gauges = make(map[string]*gaugeMetric)
	mc.histograms = make(map[string]*histogramMetric)
}

// Helper functions

var metricKeyEscape = strings.NewReplacer(
	"%", "%25", "\\", "%5C", "{", "%7B", "}", "%7D", ",", "%2C", "=", "%3D",
)

var metricKeyUnescape = strings.NewReplacer(
	"%25", "%", "%5C", "\\", "%7B", "{", "%7D", "}", "%2C", ",", "%3D", "=",
)

func buildKey(name string, labels map[string]string) string {
	name = metricKeyEscape.Replace(name)
	if len(labels) == 0 {
		return name
	}

	var key strings.Builder
	key.WriteString(name)
	key.WriteByte('{')
	for i, label := range slices.Sorted(maps.Keys(labels)) {
		if i != 0 {
			key.WriteByte(',')
		}
		key.WriteString(metricKeyEscape.Replace(label))
		key.WriteByte('=')
		key.WriteString(metricKeyEscape.Replace(labels[label]))
	}
	key.WriteByte('}')
	return key.String()
}

func metricNameFromKey(key string) string {
	name, _, _ := strings.Cut(key, "{")
	return metricKeyUnescape.Replace(name)
}

func tagsToMap(tags []Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		m[tag.Key] = tag.Value
	}
	return m
}

func copyMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Simple percentile calculation (for production, use a better algorithm)
	sorted := make([]float64, len(values))
	copy(sorted, values)

	// Simple bubble sort (good enough for small datasets)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Use proper percentile calculation
	index := p * float64(len(sorted)-1)
	lower := int(index)
	upper := lower + 1

	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}

	// Linear interpolation
	weight := index - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// TimerContext helps measure elapsed time for metrics
type TimerContext struct {
	start     time.Time
	collector Collector
	name      string
	tags      []Tag
}

// NewTimer creates a timer context for measuring duration
func NewTimer(collector Collector, name string, tags ...Tag) *TimerContext {
	return &TimerContext{
		start:     time.Now(),
		collector: collector,
		name:      name,
		tags:      tags,
	}
}

// Stop records the elapsed time
func (tc *TimerContext) Stop() {
	tc.collector.Timing(tc.name, time.Since(tc.start), tc.tags...)
}

// ObserveFunc measures the duration of a function call
func ObserveFunc(ctx context.Context, collector Collector, name string, fn func() error, tags ...Tag) error {
	timer := NewTimer(collector, name, tags...)
	defer timer.Stop()
	return fn()
}
