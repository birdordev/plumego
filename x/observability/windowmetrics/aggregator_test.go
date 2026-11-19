package windowmetrics

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestAggregator(t *testing.T) {
	agg := NewAggregator(1 * time.Minute)

	agg.Record("test_metric", 50.0)
	agg.Record("test_metric", 75.0)
	agg.Record("test_metric", 100.0)

	stats := agg.GetStats("test_metric")

	if stats.Count != 3 {
		t.Fatalf("expected count 3, got %d", stats.Count)
	}
	if stats.Sum != 225.0 {
		t.Fatalf("expected sum 225, got %.2f", stats.Sum)
	}
	if stats.Mean != 75.0 {
		t.Fatalf("expected mean 75, got %.2f", stats.Mean)
	}
	if stats.Min != 50.0 {
		t.Fatalf("expected min 50, got %.2f", stats.Min)
	}
	if stats.Max != 100.0 {
		t.Fatalf("expected max 100, got %.2f", stats.Max)
	}
}

func TestAggregatorPercentiles(t *testing.T) {
	agg := NewAggregator(1 * time.Minute)

	// Add values: 1, 2, 3, ..., 100
	for i := 1; i <= 100; i++ {
		agg.Record("test", float64(i))
	}

	stats := agg.GetStats("test")

	// P50 should be around 50
	if stats.P50 < 45 || stats.P50 > 55 {
		t.Fatalf("expected P50 around 50, got %.2f", stats.P50)
	}

	// P95 should be around 95
	if stats.P95 < 90 || stats.P95 > 100 {
		t.Fatalf("expected P95 around 95, got %.2f", stats.P95)
	}

	// P99 should be around 99
	if stats.P99 < 95 || stats.P99 > 100 {
		t.Fatalf("expected P99 around 99, got %.2f", stats.P99)
	}
}

func TestAggregatorEmpty(t *testing.T) {
	agg := NewAggregator(1 * time.Minute)

	stats := agg.GetStats("nonexistent")

	if stats.Count != 0 {
		t.Fatalf("expected count 0 for nonexistent metric, got %d", stats.Count)
	}
	if stats.Sum != 0 {
		t.Fatalf("expected sum 0 for nonexistent metric, got %.2f", stats.Sum)
	}
}

func TestAggregatorClear(t *testing.T) {
	agg := NewAggregator(1 * time.Minute)

	agg.Record("test", 50.0)
	agg.Record("test", 75.0)

	agg.Clear()

	stats := agg.GetStats("test")
	if stats.Count != 0 {
		t.Fatalf("expected count 0 after clear, got %d", stats.Count)
	}
}

func TestAggregatorGetAllStats(t *testing.T) {
	agg := NewAggregator(1 * time.Minute)

	agg.Record("metric1", 50.0)
	agg.Record("metric2", 100.0)
	agg.Record("metric1", 75.0)

	allStats := agg.GetAllStats()

	if len(allStats) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(allStats))
	}

	if stats, exists := allStats["metric1"]; !exists {
		t.Fatalf("expected metric1 to exist")
	} else if stats.Count != 2 {
		t.Fatalf("expected metric1 count 2, got %d", stats.Count)
	}

	if stats, exists := allStats["metric2"]; !exists {
		t.Fatalf("expected metric2 to exist")
	} else if stats.Count != 1 {
		t.Fatalf("expected metric2 count 1, got %d", stats.Count)
	}
}

func TestAggregatorConcurrency(t *testing.T) {
	agg := NewAggregator(1 * time.Minute)

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				agg.Record("concurrent", float64(j))
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	stats := agg.GetStats("concurrent")
	if stats.Count != 1000 {
		t.Fatalf("expected count 1000, got %d", stats.Count)
	}
}

func TestAggregatorEvictsSamplesOutsideWindowOnRecord(t *testing.T) {
	base := time.Unix(1000, 0)
	current := base

	agg := NewAggregator(100 * time.Millisecond)
	agg.now = func() time.Time { return current }

	agg.Record("latency", 10)

	current = base.Add(50 * time.Millisecond)
	agg.Record("latency", 20)

	current = base.Add(200 * time.Millisecond)
	agg.Record("latency", 30)

	stats := agg.GetStats("latency")
	if stats.Count != 1 {
		t.Fatalf("expected 1 retained sample, got %d", stats.Count)
	}
	if stats.Sum != 30 {
		t.Fatalf("expected retained sum 30, got %.2f", stats.Sum)
	}
}

func TestAggregatorEvictsSamplesOutsideWindowOnGetStats(t *testing.T) {
	base := time.Unix(2000, 0)
	current := base

	agg := NewAggregator(100 * time.Millisecond)
	agg.now = func() time.Time { return current }

	agg.Record("latency", 10)

	current = base.Add(50 * time.Millisecond)
	agg.Record("latency", 20)

	current = base.Add(250 * time.Millisecond)
	stats := agg.GetStats("latency")

	if stats.Count != 0 {
		t.Fatalf("expected all samples to expire, got count %d", stats.Count)
	}
}

func TestPercentile(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	p50 := percentile(values, 0.5)
	if p50 != 5 && p50 != 6 {
		t.Fatalf("expected P50 to be 5 or 6, got %.2f", p50)
	}

	p95 := percentile(values, 0.95)
	if p95 < 9 || p95 > 10 {
		t.Fatalf("expected P95 to be between 9 and 10, got %.2f", p95)
	}

	// Empty slice
	p := percentile([]float64{}, 0.5)
	if p != 0 {
		t.Fatalf("expected percentile of empty slice to be 0, got %.2f", p)
	}

	// Single value
	p = percentile([]float64{42}, 0.5)
	if p != 42 {
		t.Fatalf("expected percentile of single value to be 42, got %.2f", p)
	}
}

// Benchmarks
func BenchmarkAggregatorRecord(b *testing.B) {
	agg := NewAggregator(1 * time.Minute)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg.Record("test", float64(i))
	}
}

func BenchmarkAggregatorGetStats(b *testing.B) {
	agg := NewAggregator(1 * time.Minute)

	// Pre-populate
	for i := 0; i < 1000; i++ {
		agg.Record("test", float64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = agg.GetStats("test")
	}
}

func TestAggregatorReclaimsIdleMetricNames(t *testing.T) {
	agg := NewAggregator(time.Minute)
	current := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	agg.now = func() time.Time { return current }
	for i := range 2 * maxIdleRemovalsPerRecord {
		agg.Record(strconv.Itoa(i), 1)
	}

	current = current.Add(time.Minute)
	agg.Record("active", 40)
	if agg.activity.Len() != 2*maxIdleRemovalsPerRecord+1 {
		t.Fatal("samples exactly at the cutoff should be retained")
	}
	current = current.Add(time.Second)
	agg.Record("active", 60)
	if len(agg.buckets) != maxIdleRemovalsPerRecord+1 {
		t.Fatalf("record cleanup retained %d buckets", len(agg.buckets))
	}

	var group sync.WaitGroup
	for range 10 {
		group.Go(func() { agg.Record("active", 50) })
		group.Go(func() { _ = agg.GetAllStats() })
	}
	group.Wait()
	stats := agg.GetAllStats()
	if got := stats["active"]; len(stats) != 1 || got.Count != 12 || got.Mean != 50 || got.P50 != 50 {
		t.Fatalf("snapshot = %+v", stats)
	}
}
