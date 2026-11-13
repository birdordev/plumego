package metrics

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type stubAggregateCollector struct {
	*NoopCollector
	onGetStats func() CollectorStats
}

func newStubAggregateCollector(onGetStats func() CollectorStats) *stubAggregateCollector {
	return &stubAggregateCollector{
		NoopCollector: NewNoopCollector(),
		onGetStats:    onGetStats,
	}
}

func (s *stubAggregateCollector) GetStats() CollectorStats {
	if s.onGetStats != nil {
		return s.onGetStats()
	}
	return CollectorStats{NameBreakdown: make(map[string]int64)}
}

func TestMultiCollector(t *testing.T) {
	collector1 := NewBaseMetricsCollector()
	collector2 := NewBaseMetricsCollector()
	multi := NewMultiCollector(collector1, collector2)
	if multi == nil {
		t.Fatalf("expected multi collector, got nil")
	}

	ctx := t.Context()

	// Record HTTP metric
	multi.ObserveHTTP(ctx, "GET", "/test", 200, 100, 50*time.Millisecond)

	// Verify both collectors received the metric
	if collector1.GetStats().TotalRecords != 1 {
		t.Fatalf("expected 1 record in collector1 stats, got %d", collector1.GetStats().TotalRecords)
	}
	if collector2.GetStats().TotalRecords != 1 {
		t.Fatalf("expected 1 record in collector2 stats, got %d", collector2.GetStats().TotalRecords)
	}
}

func TestMultiCollectorAllMethods(t *testing.T) {
	collector1 := NewBaseMetricsCollector()
	collector2 := NewBaseMetricsCollector()
	multi := NewMultiCollector(collector1, collector2)
	if multi == nil {
		t.Fatalf("expected multi collector, got nil")
	}

	ctx := t.Context()

	// Test both stable observation paths
	multi.ObserveHTTP(ctx, "GET", "/test", 200, 100, 50*time.Millisecond)
	multi.Record(ctx, MetricRecord{Name: "owner_metric", Value: 1})

	// Each collector should have received both metrics
	if collector1.GetStats().TotalRecords != 2 {
		t.Fatalf("expected 2 records in collector1 stats, got %d", collector1.GetStats().TotalRecords)
	}
	if collector2.GetStats().TotalRecords != 2 {
		t.Fatalf("expected 2 records in collector2 stats, got %d", collector2.GetStats().TotalRecords)
	}
}

func TestMultiCollectorGetStats(t *testing.T) {
	collector1 := NewBaseMetricsCollector()
	collector2 := NewBaseMetricsCollector()
	multi := NewMultiCollector(collector1, collector2)
	if multi == nil {
		t.Fatalf("expected multi collector, got nil")
	}

	ctx := t.Context()

	// Add metrics to both collectors via multi
	multi.ObserveHTTP(ctx, "GET", "/test", 200, 100, 50*time.Millisecond)
	multi.ObserveHTTP(ctx, "POST", "/api", 201, 200, 100*time.Millisecond)

	stats := multi.GetStats()

	if stats.TotalRecords != 4 {
		t.Fatalf("expected total records from both collectors, got %d", stats.TotalRecords)
	}
	if stats.ErrorRecords != 0 {
		t.Fatalf("expected no error records, got %d", stats.ErrorRecords)
	}
	if stats.ActiveSeries != 2 {
		t.Fatalf("expected active series from both collectors, got %d", stats.ActiveSeries)
	}
	if stats.NameBreakdown[MetricHTTPRequest] != 4 {
		t.Fatalf("expected HTTP breakdown from both collectors, got %d", stats.NameBreakdown[MetricHTTPRequest])
	}
}

func TestMultiCollectorGetStatsSumsCountersAndBreakdowns(t *testing.T) {
	collector1 := newStubAggregateCollector(func() CollectorStats {
		return CollectorStats{
			TotalRecords:  10,
			ErrorRecords:  1,
			NameBreakdown: map[string]int64{MetricHTTPRequest: 10},
			StartTime:     time.Unix(100, 0),
		}
	})

	collector2 := newStubAggregateCollector(func() CollectorStats {
		return CollectorStats{
			TotalRecords:  5,
			ErrorRecords:  2,
			NameBreakdown: map[string]int64{MetricHTTPRequest: 3, "owner_metric": 2},
			StartTime:     time.Unix(200, 0),
		}
	})

	multi := NewMultiCollector(collector1, collector2)
	if multi == nil {
		t.Fatalf("expected multi collector, got nil")
	}

	stats := multi.GetStats()

	// TotalRecords should be summed
	if stats.TotalRecords != 15 {
		t.Fatalf("expected TotalRecords 15, got %d", stats.TotalRecords)
	}
	// ErrorRecords should be summed
	if stats.ErrorRecords != 3 {
		t.Fatalf("expected ErrorRecords 3, got %d", stats.ErrorRecords)
	}
	// NameBreakdown should be merged
	if stats.NameBreakdown[MetricHTTPRequest] != 13 {
		t.Fatalf("expected MetricHTTPRequest 13, got %d", stats.NameBreakdown[MetricHTTPRequest])
	}
	if stats.NameBreakdown["owner_metric"] != 2 {
		t.Fatalf("expected custom owner metric 2, got %d", stats.NameBreakdown["owner_metric"])
	}
	// StartTime should be the earliest
	if !stats.StartTime.Equal(time.Unix(100, 0)) {
		t.Fatalf("expected StartTime to be earliest, got %v", stats.StartTime)
	}
}

func TestMultiCollectorGetStatsNormalizesChildActiveSeries(t *testing.T) {
	collectorWithActive := newStubAggregateCollector(func() CollectorStats {
		return CollectorStats{
			TotalRecords:  1,
			ActiveSeries:  5,
			NameBreakdown: map[string]int64{"with_active": 1},
		}
	})
	collectorWithBreakdownOnly := newStubAggregateCollector(func() CollectorStats {
		return CollectorStats{
			TotalRecords:  2,
			NameBreakdown: map[string]int64{"a": 1, "b": 1},
		}
	})

	multi := NewMultiCollector(collectorWithActive, collectorWithBreakdownOnly)
	if multi == nil {
		t.Fatalf("expected multi collector, got nil")
	}

	stats := multi.GetStats()
	if stats.ActiveSeries != 7 {
		t.Fatalf("expected normalized active series 7, got %d", stats.ActiveSeries)
	}
}

func TestMultiCollectorGetStatsReturnsCallerOwnedBreakdown(t *testing.T) {
	collector := NewBaseMetricsCollector()
	collector.Record(t.Context(), MetricRecord{Name: "owner_metric"})
	multi := NewMultiCollector(collector, NewNoopCollector())
	if multi == nil {
		t.Fatalf("expected multi collector, got nil")
	}

	stats := multi.GetStats()
	stats.NameBreakdown["owner_metric"] = 99

	stats = multi.GetStats()
	if stats.NameBreakdown["owner_metric"] != 1 {
		t.Fatalf("expected multi stats breakdown to be caller-owned, got %d", stats.NameBreakdown["owner_metric"])
	}
}

func TestMultiCollectorGetStatsCallsEachCollectorOnce(t *testing.T) {
	callsA := 0
	callsB := 0

	collectorA := newStubAggregateCollector(func() CollectorStats {
		callsA++
		return CollectorStats{NameBreakdown: make(map[string]int64)}
	})
	collectorB := newStubAggregateCollector(func() CollectorStats {
		callsB++
		return CollectorStats{NameBreakdown: make(map[string]int64)}
	})

	multi := NewMultiCollector(collectorA, collectorB)
	if multi == nil {
		t.Fatalf("expected multi collector, got nil")
	}

	_ = multi.GetStats()

	if callsA != 1 {
		t.Fatalf("expected collectorA GetStats to be called once, got %d", callsA)
	}
	if callsB != 1 {
		t.Fatalf("expected collectorB GetStats to be called once, got %d", callsB)
	}
}

func TestMultiCollectorClear(t *testing.T) {
	collector1 := NewBaseMetricsCollector()
	collector2 := NewBaseMetricsCollector()
	multi := NewMultiCollector(collector1, collector2)
	if multi == nil {
		t.Fatalf("expected multi collector, got nil")
	}

	ctx := t.Context()

	// Add metrics
	multi.ObserveHTTP(ctx, "GET", "/test", 200, 100, 50*time.Millisecond)

	// Clear all collectors
	multi.Clear()

	// Verify both collectors are cleared
	if collector1.GetStats().TotalRecords != 0 {
		t.Fatalf("expected 0 records in collector1 after clear, got %d", collector1.GetStats().TotalRecords)
	}
	if collector2.GetStats().TotalRecords != 0 {
		t.Fatalf("expected 0 records in collector2 after clear, got %d", collector2.GetStats().TotalRecords)
	}
}

func TestMultiCollectorEmpty(t *testing.T) {
	multi := NewMultiCollector()
	if multi == nil {
		t.Fatalf("expected no-op collector for empty input, got nil")
	}
	multi.Record(t.Context(), MetricRecord{Name: "x"})
	if multi.GetStats().TotalRecords != 0 {
		t.Fatalf("expected no-op collector to record nothing, got %d", multi.GetStats().TotalRecords)
	}
}

func TestNewMultiCollectorNilFilteringAndSinglePassthrough(t *testing.T) {
	collector := NewBaseMetricsCollector()

	multi := NewMultiCollector(nil, collector, nil)
	if multi != collector {
		t.Fatalf("expected single non-nil collector to be returned unchanged")
	}
}

func TestNewMultiCollectorFiltersNilCollectors(t *testing.T) {
	left := NewBaseMetricsCollector()
	right := NewBaseMetricsCollector()

	multi := NewMultiCollector(nil, left, nil, right)
	if multi == nil {
		t.Fatalf("expected multi collector")
	}

	multi.ObserveHTTP(t.Context(), "GET", "/filtered", 200, 0, time.Millisecond)

	if got := left.GetStats().TotalRecords; got != 1 {
		t.Fatalf("expected left collector to receive one record, got %d", got)
	}
	if got := right.GetStats().TotalRecords; got != 1 {
		t.Fatalf("expected right collector to receive one record, got %d", got)
	}
}

func TestMultiCollectorNilReceiverNoops(t *testing.T) {
	var multi *MultiCollector

	multi.Record(t.Context(), MetricRecord{Name: "ignored"})
	multi.ObserveHTTP(t.Context(), "GET", "/ignored", 200, 0, time.Millisecond)
	multi.Clear()

	stats := multi.GetStats()
	if stats.NameBreakdown == nil {
		t.Fatalf("expected nil receiver stats to return initialized breakdown")
	}
	if stats.TotalRecords != 0 {
		t.Fatalf("expected nil receiver total records to stay zero, got %d", stats.TotalRecords)
	}
}

func TestMultiCollectorEmptyInternalCollectorsNoop(t *testing.T) {
	multi := &MultiCollector{}

	multi.Record(t.Context(), MetricRecord{Name: "ignored"})
	multi.ObserveHTTP(t.Context(), "GET", "/ignored", 200, 0, time.Millisecond)
	multi.Clear()

	stats := multi.GetStats()
	if stats.TotalRecords != 0 {
		t.Fatalf("expected empty internal collector total records to stay zero, got %d", stats.TotalRecords)
	}
	if stats.NameBreakdown == nil {
		t.Fatalf("expected initialized name breakdown")
	}
}

func TestMultiCollectorSkipsNilInternalCollectors(t *testing.T) {
	collector := NewBaseMetricsCollector()
	multi := &MultiCollector{collectors: []AggregateCollector{nil, collector}}

	multi.ObserveHTTP(t.Context(), "GET", "/test", 200, 0, time.Millisecond)
	multi.Record(t.Context(), MetricRecord{Name: "owner_metric"})

	stats := multi.GetStats()
	if stats.TotalRecords != 2 {
		t.Fatalf("expected non-nil collector records to be counted, got %d", stats.TotalRecords)
	}

	multi.Clear()
	if got := collector.GetStats().TotalRecords; got != 0 {
		t.Fatalf("expected clear to reach non-nil collector, got %d", got)
	}
}

type recordingCollector struct {
	NoopCollector
	onRecord func(MetricRecord)
}

func (c *recordingCollector) Record(_ context.Context, record MetricRecord) {
	c.onRecord(record)
}

func TestNewMultiCollectorFiltersNilPointers(t *testing.T) {
	var absent *recordingCollector
	if result := NewMultiCollector(nil, absent); reflect.TypeOf(result) != reflect.TypeFor[*NoopCollector]() {
		t.Fatalf("all absent returned %T, want *NoopCollector", result)
	}
	var order []string
	first := &recordingCollector{onRecord: func(record MetricRecord) {
		order = append(order, "first")
		record.Labels["test"] = "changed"
	}}
	second := &recordingCollector{onRecord: func(record MetricRecord) {
		order = append(order, "second")
		if value := record.Labels["test"]; value != "original" {
			t.Errorf("second collector saw label %q", value)
		}
	}}
	if result := NewMultiCollector(absent, first); result != first {
		t.Fatalf("single remaining collector = %T, want original instance", result)
	}
	labels := MetricLabels{"test": "original"}
	NewMultiCollector(absent, first, absent, second).Record(t.Context(), MetricRecord{Labels: labels})
	if !reflect.DeepEqual(order, []string{"first", "second"}) || labels["test"] != "original" {
		t.Fatalf("fan-out order = %v, original labels = %v", order, labels)
	}
}
