package metrics

import (
	"context"
	"sync"
	"testing"
)

type labelMutatingCollector struct{ *NoopCollector }

func (c *labelMutatingCollector) Record(_ context.Context, record MetricRecord) {
	record.Labels["status"] = "200"
}

func TestFanoutLabelsAreIsolated(t *testing.T) {
	observer := NewBaseMetricsCollector()
	mutator := &labelMutatingCollector{NewNoopCollector()}
	multi := NewMultiCollector(mutator, observer)
	record := MetricRecord{Name: MetricHTTPRequest, Labels: MetricLabels{"status": "500"}}
	multi.Record(t.Context(), record)
	if record.Labels["status"] != "500" {
		t.Fatal("child mutated caller labels")
	}
	if observer.GetStats().ErrorRecords != 1 {
		t.Fatal("child mutation changed sibling error classification")
	}
}

func TestConcurrentRecordClearAndSnapshots(t *testing.T) {
	var collector BaseMetricsCollector
	const writers, iterations = 8, 250
	runWriters := func() {
		var wg sync.WaitGroup
		for range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range iterations {
					status := "200"
					if i%2 == 0 {
						status = "500"
					}
					collector.Record(nil, MetricRecord{Name: MetricHTTPRequest, Labels: MetricLabels{"status": status}})
				}
			}()
		}
		wg.Wait()
	}
	done, reading := make(chan struct{}), make(chan struct{})
	failures := make(chan string, 1)
	go func() {
		defer close(reading)
		for {
			stats := collector.GetStats()
			var total int64
			for _, count := range stats.NameBreakdown {
				total += count
			}
			if total != stats.TotalRecords || stats.ErrorRecords > total || stats.ErrorRecords < 0 || stats.ActiveSeries != len(stats.NameBreakdown) {
				failures <- "inconsistent statistics snapshot"
				return
			}
			stats.NameBreakdown["caller-owned"] = 123
			select {
			case <-done:
				return
			default:
			}
		}
	}()
	clearing := make(chan struct{})
	go func() {
		defer close(clearing)
		for range 100 {
			collector.Clear()
		}
	}()
	runWriters()
	<-clearing
	close(done)
	<-reading
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
	collector.Clear()
	runWriters()
	stats := collector.GetStats()
	if stats.TotalRecords != writers*iterations || stats.ErrorRecords != writers*iterations/2 || stats.NameBreakdown[MetricHTTPRequest] != writers*iterations || stats.ActiveSeries != 1 {
		t.Fatalf("lost updates: %+v", stats)
	}
}

func TestConcurrentFanoutDoesNotShareInputLabels(t *testing.T) {
	base := NewBaseMetricsCollector()
	multi := NewMultiCollector(&labelMutatingCollector{NewNoopCollector()}, base)
	record := MetricRecord{Name: MetricHTTPRequest, Labels: MetricLabels{"status": "500"}}
	const writers, iterations = 8, 100
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				multi.Record(nil, record)
			}
		}()
	}
	wg.Wait()
	if stats := base.GetStats(); stats.TotalRecords != writers*iterations || stats.ErrorRecords != writers*iterations {
		t.Fatalf("inconsistent fanout: %+v", stats)
	}
	if record.Labels["status"] != "500" {
		t.Fatal("fanout mutated shared input")
	}
}
