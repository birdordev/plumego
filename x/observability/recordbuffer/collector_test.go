package recordbuffer_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/x/observability/recordbuffer"
)

func TestNewCollector(t *testing.T) {
	c := recordbuffer.NewCollector()
	if c == nil {
		t.Fatal("NewCollector returned nil")
	}
	if got := c.GetRecords(); len(got) != 0 {
		t.Fatalf("expected 0 records, got %d", len(got))
	}
}

func TestRecord_AppendAndRetrieve(t *testing.T) {
	c := recordbuffer.NewCollector()
	ctx := context.Background()

	rec := metrics.MetricRecord{Name: "test_metric", Value: 1.0}
	c.Record(ctx, rec)

	got := c.GetRecords()
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if got[0].Name != "test_metric" {
		t.Errorf("unexpected record name: %q", got[0].Name)
	}
}

func TestRecord_TimestampBackfilled(t *testing.T) {
	c := recordbuffer.NewCollector()
	ctx := context.Background()

	before := time.Now()
	c.Record(ctx, metrics.MetricRecord{Name: "ts_test"})
	after := time.Now()

	got := c.GetRecords()
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	ts := got[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v not in expected range [%v, %v]", ts, before, after)
	}
}

func TestWithMaxRecords_Capacity(t *testing.T) {
	c := recordbuffer.NewCollector().WithMaxRecords(3)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		c.Record(ctx, metrics.MetricRecord{Name: "item", Value: float64(i)})
	}

	got := c.GetRecords()
	if len(got) != 3 {
		t.Fatalf("expected 3 records after cap=3, got %d", len(got))
	}
	// Last 3 records should have values 2, 3, 4
	if got[0].Value != 2 || got[1].Value != 3 || got[2].Value != 4 {
		t.Errorf("unexpected record values: %v", got)
	}
}

func TestClear_ResetsBuffer(t *testing.T) {
	c := recordbuffer.NewCollector()
	ctx := context.Background()

	c.Record(ctx, metrics.MetricRecord{Name: "before_clear"})
	if len(c.GetRecords()) != 1 {
		t.Fatal("expected 1 record before clear")
	}

	c.Clear()
	if got := c.GetRecords(); len(got) != 0 {
		t.Fatalf("expected 0 records after clear, got %d", len(got))
	}
}

func TestGetRecords_ReturnsCopy(t *testing.T) {
	c := recordbuffer.NewCollector()
	ctx := context.Background()

	c.Record(ctx, metrics.MetricRecord{Name: "original"})
	got := c.GetRecords()
	got[0].Name = "mutated"

	// Re-fetch should still return original name
	got2 := c.GetRecords()
	if got2[0].Name != "original" {
		t.Errorf("GetRecords did not return an independent copy; got %q", got2[0].Name)
	}
}

func TestRecord_ConcurrentSafe(t *testing.T) {
	c := recordbuffer.NewCollector().WithMaxRecords(1000)
	ctx := context.Background()
	const goroutines = 20
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				c.Record(ctx, metrics.MetricRecord{Name: "concurrent"})
			}
		}()
	}
	wg.Wait()

	got := c.GetRecords()
	if len(got) != goroutines*perGoroutine {
		t.Errorf("expected %d records, got %d", goroutines*perGoroutine, len(got))
	}
}

func TestObserveHTTP_AddsRecord(t *testing.T) {
	c := recordbuffer.NewCollector()
	ctx := context.Background()

	c.ObserveHTTP(ctx, "GET", "/test", 200, 512, 10*time.Millisecond)

	got := c.GetRecords()
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if got[0].Name != "http_request" {
		t.Errorf("unexpected record name: %q", got[0].Name)
	}
	if got[0].Value != (10 * time.Millisecond).Seconds() {
		t.Errorf("unexpected record value: %v", got[0].Value)
	}
	if got[0].Labels["method"] != "GET" {
		t.Errorf("unexpected method label: %q", got[0].Labels["method"])
	}
	if got[0].Labels["path"] != "/test" {
		t.Errorf("unexpected path label: %q", got[0].Labels["path"])
	}
	if got[0].Labels["status"] != "200" {
		t.Errorf("unexpected status label: %q", got[0].Labels["status"])
	}
}
