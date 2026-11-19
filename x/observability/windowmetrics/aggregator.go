package windowmetrics

import (
	"container/list"
	"sort"
	"sync"
	"time"
)

// Aggregator aggregates metrics over time windows.
//
// This is useful for computing rolling averages, rates, and percentiles
// without storing all individual metric records.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/observability/windowmetrics"
//
//	agg := windowmetrics.NewAggregator(1 * time.Minute)
//
//	// Record values
//	agg.Record("api_latency", 50.0)
//	agg.Record("api_latency", 75.0)
//	agg.Record("api_latency", 100.0)
//
//	// Get statistics
//	stats := agg.GetStats("api_latency")
//	fmt.Printf("Count: %d, Mean: %.2f, Min: %.2f, Max: %.2f\n",
//		stats.Count, stats.Mean, stats.Min, stats.Max)
type Aggregator struct {
	mu       *sync.RWMutex
	window   time.Duration
	buckets  map[string]*bucket
	activity *list.List // oldest to newest last observation, under mu
	now      func() time.Time
}

const maxIdleRemovalsPerRecord = 8

type bucket struct {
	name             string
	activity         *list.Element
	samples          []sample
	maxLen           int
	sortedCache      []float64
	sortedCacheValid bool
}

type sample struct {
	value      float64
	observedAt time.Time
}

// AggregatorStats contains statistical information about aggregated metrics.
type AggregatorStats struct {
	Count int64   `json:"count"`
	Sum   float64 `json:"sum"`
	Mean  float64 `json:"mean"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
}

// NewAggregator creates a new metrics aggregator with the specified time window.
//
// Retention is bounded by both time and size:
//   - During Record and GetStats, samples older than now-window are evicted.
//   - Each metric bucket keeps at most 10,000 newest samples.
//
// This means statistics are always calculated from the intersection of:
//   - samples observed within the configured window, and
//   - the newest 10,000 samples for that metric.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/observability/windowmetrics"
//
//	// Aggregate metrics over a rolling 5-minute window,
//	// capped to the newest 10,000 samples per metric.
//	agg := windowmetrics.NewAggregator(5 * time.Minute)
func NewAggregator(window time.Duration) *Aggregator {
	return &Aggregator{
		mu:       &sync.RWMutex{},
		window:   window,
		buckets:  make(map[string]*bucket),
		activity: list.New(),
		now:      time.Now,
	}
}

// Record adds a value to the aggregator for the specified metric name.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/observability/windowmetrics"
//
//	agg := windowmetrics.NewAggregator(1 * time.Minute)
//	agg.Record("request_duration", 50.5)
//	agg.Record("request_duration", 75.2)
func (a *Aggregator) Record(name string, value float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.discardIdle(now, maxIdleRemovalsPerRecord)

	b, exists := a.buckets[name]
	if !exists {
		b = &bucket{
			name:    name,
			samples: make([]sample, 0, 1000),
			maxLen:  10000,
		}
		a.buckets[name] = b
		b.activity = a.activity.PushBack(b)
	} else {
		a.activity.MoveToBack(b.activity)
	}

	b.evictExpired(a.window, now)
	b.samples = append(b.samples, sample{value: value, observedAt: now})
	b.sortedCacheValid = false

	if len(b.samples) > b.maxLen {
		overflow := len(b.samples) - b.maxLen
		b.samples = b.samples[overflow:]
		b.sortedCacheValid = false
	}
}

// GetStats returns aggregated statistics for the specified metric name.
//
// Before calculating statistics, GetStats evicts samples older than now-window.
// Returned statistics include only retained samples (window + maxLen bounded).
//
// Example:
//
//	import "github.com/birdordev/plumego/x/observability/windowmetrics"
//
//	agg := windowmetrics.NewAggregator(1 * time.Minute)
//	agg.Record("latency", 50.0)
//	agg.Record("latency", 75.0)
//
//	stats := agg.GetStats("latency")
//	// stats are based on samples observed within the configured window,
//	// capped to the newest 10,000 samples.
//	fmt.Printf("Mean: %.2f, P95: %.2f\n", stats.Mean, stats.P95)
func (a *Aggregator) GetStats(name string) AggregatorStats {
	a.mu.Lock()
	defer a.mu.Unlock()

	b, exists := a.buckets[name]
	if !exists {
		return AggregatorStats{}
	}
	b.evictExpired(a.window, a.now())
	if len(b.samples) == 0 {
		a.removeBucket(b)
		return AggregatorStats{}
	}
	return b.buildStats()
}

func (b *bucket) buildStats() AggregatorStats {
	samples := b.samples
	if len(samples) == 0 {
		return AggregatorStats{}
	}

	stats := AggregatorStats{Min: samples[0].value, Max: samples[0].value}
	sortedValues := b.getSortedValues()

	for _, s := range samples {
		stats.Count++
		stats.Sum += s.value
		if s.value < stats.Min {
			stats.Min = s.value
		}
		if s.value > stats.Max {
			stats.Max = s.value
		}
	}

	stats.Mean = stats.Sum / float64(stats.Count)

	stats.P50 = percentileSorted(sortedValues, 0.50)
	stats.P95 = percentileSorted(sortedValues, 0.95)
	stats.P99 = percentileSorted(sortedValues, 0.99)

	return stats
}

func (b *bucket) getSortedValues() []float64 {
	if b.sortedCacheValid {
		return b.sortedCache
	}

	b.sortedCache = make([]float64, len(b.samples))
	for i, s := range b.samples {
		b.sortedCache[i] = s.value
	}
	sort.Float64s(b.sortedCache)
	b.sortedCacheValid = true

	return b.sortedCache
}

func (b *bucket) evictExpired(window time.Duration, now time.Time) {
	if window <= 0 || len(b.samples) == 0 {
		return
	}

	cutoff := now.Add(-window)
	firstValid := 0
	for firstValid < len(b.samples) && b.samples[firstValid].observedAt.Before(cutoff) {
		firstValid++
	}

	if firstValid > 0 {
		b.samples = b.samples[firstValid:]
		b.sortedCacheValid = false
	}
}

// discardIdle reclaims whole expired buckets in last-observation order.
// Record bounds reclamation work per call, even after a long idle period.
func (a *Aggregator) discardIdle(now time.Time, limit int) {
	if a.window <= 0 {
		return
	}
	cutoff := now.Add(-a.window)
	for removed := 0; removed < limit; removed++ {
		first := a.activity.Front()
		if first == nil {
			return
		}
		b := first.Value.(*bucket)
		if !b.samples[len(b.samples)-1].observedAt.Before(cutoff) {
			return
		}
		a.removeBucket(b)
	}
}

func (a *Aggregator) removeBucket(b *bucket) {
	a.activity.Remove(b.activity)
	delete(a.buckets, b.name)
}

// Clear removes all aggregated metrics.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/observability/windowmetrics"
//
//	agg := windowmetrics.NewAggregator(1 * time.Minute)
//	// ... record metrics ...
//	agg.Clear()
func (a *Aggregator) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.buckets = make(map[string]*bucket)
	a.activity = list.New()
}

// GetAllStats returns statistics for metrics with retained samples at the time
// of the snapshot. It removes expired metric names from the aggregator.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/observability/windowmetrics"
//
//	agg := windowmetrics.NewAggregator(1 * time.Minute)
//	agg.Record("latency", 50.0)
//	agg.Record("throughput", 1000.0)
//
//	allStats := agg.GetAllStats()
//	for name, stats := range allStats {
//		fmt.Printf("%s: mean=%.2f\n", name, stats.Mean)
//	}
func (a *Aggregator) GetAllStats() map[string]AggregatorStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.discardIdle(now, len(a.buckets))

	result := make(map[string]AggregatorStats, len(a.buckets))
	for name, b := range a.buckets {
		b.evictExpired(a.window, now)
		result[name] = b.buildStats()
	}

	return result
}

// percentile calculates the nth percentile of a slice of float64 values.
// The values slice is not modified.
func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	return percentileSorted(sorted, p)
}

func percentileSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}

	index := int(float64(len(sorted)-1) * p)
	return sorted[index]
}
