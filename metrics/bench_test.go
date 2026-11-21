package metrics

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkBaselineMapAccess(b *testing.B) {
	m := map[string]int{"key": 100}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m["key"]
	}
}

func BenchmarkBaselineTimeNow(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = time.Now()
	}
}

func BenchmarkBaseCollectorRecord(b *testing.B) {
	collector := NewBaseMetricsCollector()
	ctx := b.Context()
	record := MetricRecord{
		Name:  "test",
		Value: 100,
		Labels: MetricLabels{
			"method": "GET",
			"path":   "/test",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.Record(ctx, record)
	}
}

func BenchmarkBaseCollectorObserveHTTP(b *testing.B) {
	collector := NewBaseMetricsCollector()
	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.ObserveHTTP(ctx, "GET", "/test", 200, 100, 50*time.Millisecond)
	}
}

func BenchmarkNoopCollectorObserveHTTP(b *testing.B) {
	collector := NewNoopCollector()
	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.ObserveHTTP(ctx, "GET", "/test", 200, 100, 50*time.Millisecond)
	}
}

func BenchmarkMultiCollectorObserveHTTP(b *testing.B) {
	left := NewBaseMetricsCollector()
	right := NewBaseMetricsCollector()
	collector := NewMultiCollector(left, right)
	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.ObserveHTTP(ctx, "GET", "/test", 200, 100, 50*time.Millisecond)
	}
}

func BenchmarkBaseCollectorNameCardinality(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			names := make([]string, count)
			for i := range names {
				names[i] = fmt.Sprintf("metric-%d", i)
			}
			b.ReportAllocs()
			for b.Loop() {
				collector := NewBaseMetricsCollector()
				for _, name := range names {
					collector.Record(b.Context(), MetricRecord{Name: name})
				}
				stats := collector.GetStats()
				if stats.ActiveSeries != count {
					b.Fatal(stats.ActiveSeries)
				}
				collector.Clear()
				if collector.GetStats().ActiveSeries != 0 {
					b.Fatal("Clear retained names")
				}
			}
		})
	}
}
