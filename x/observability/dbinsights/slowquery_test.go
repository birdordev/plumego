package dbinsights

import (
	"errors"
	"strings"
	"testing"
	"time"

	testmetrics "github.com/birdordev/plumego/x/observability/testmetrics"
)

func TestDetectorCheck(t *testing.T) {
	detector := NewDetector(WithThreshold(100 * time.Millisecond))

	if detector.Check("query", "postgres", "SELECT * FROM users", 50*time.Millisecond, nil) {
		t.Error("expected fast query not to be detected as slow")
	}
	if !detector.Check("query", "postgres", "SELECT * FROM huge_table", 200*time.Millisecond, nil) {
		t.Error("expected slow query to be detected")
	}

	slowQueries := detector.GetSlowQueries()
	if len(slowQueries) != 1 {
		t.Fatalf("expected 1 slow query, got %d", len(slowQueries))
	}
	if slowQueries[0].Query != "SELECT * FROM huge_table" {
		t.Errorf("unexpected query: %s", slowQueries[0].Query)
	}
}

func TestDetectorMaxRecords(t *testing.T) {
	detector := NewDetector(WithThreshold(50*time.Millisecond), WithMaxRecords(5))
	for i := 0; i < 10; i++ {
		detector.Check("query", "postgres", "SELECT * FROM users", 100*time.Millisecond, nil)
	}

	if len(detector.GetSlowQueries()) != 5 {
		t.Errorf("expected 5 slow queries, got %d", len(detector.GetSlowQueries()))
	}
	if detector.GetTotalDetected() != 10 {
		t.Errorf("expected total detected to be 10, got %d", detector.GetTotalDetected())
	}
}

func TestDetectorCallback(t *testing.T) {
	callbackCalled := false
	var recordedQuery SlowQuery

	detector := NewDetector(
		WithThreshold(50*time.Millisecond),
		WithCallback(func(sq SlowQuery) {
			callbackCalled = true
			recordedQuery = sq
		}),
	)

	detector.Check("query", "postgres", "SELECT * FROM users", 100*time.Millisecond, nil)
	if !callbackCalled {
		t.Error("expected callback to be called")
	}
	if recordedQuery.Query != "SELECT * FROM users" {
		t.Errorf("unexpected query in callback: %s", recordedQuery.Query)
	}
}

func TestDetectorGetRecentSlowQueries(t *testing.T) {
	detector := NewDetector(WithThreshold(50 * time.Millisecond))
	for i := 0; i < 10; i++ {
		query := "SELECT * FROM users WHERE id = " + string(rune('0'+i))
		detector.Check("query", "postgres", query, 100*time.Millisecond, nil)
	}

	recent := detector.GetRecentSlowQueries(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent queries, got %d", len(recent))
	}
	for i, sq := range recent {
		expectedID := '7' + rune(i)
		if !strings.Contains(sq.Query, string(expectedID)) {
			t.Errorf("unexpected query at index %d: %s", i, sq.Query)
		}
	}
}

func TestDetectorClear(t *testing.T) {
	detector := NewDetector(WithThreshold(50 * time.Millisecond))
	detector.Check("query", "postgres", "SELECT * FROM users", 100*time.Millisecond, nil)
	detector.Check("query", "postgres", "SELECT * FROM posts", 150*time.Millisecond, nil)

	detector.Clear()

	if detector.GetTotalDetected() != 0 {
		t.Errorf("expected 0 total detected after clear, got %d", detector.GetTotalDetected())
	}
	if len(detector.GetSlowQueries()) != 0 {
		t.Errorf("expected 0 slow queries after clear, got %d", len(detector.GetSlowQueries()))
	}
}

func TestDetectorSummary(t *testing.T) {
	detector := NewDetector(WithThreshold(100 * time.Millisecond))

	summary := detector.Summary()
	if !strings.Contains(summary, "No slow queries detected") {
		t.Error("expected summary to indicate no slow queries")
	}

	detector.Check("query", "postgres", "SELECT * FROM users", 200*time.Millisecond, nil)
	summary = detector.Summary()
	if !strings.Contains(summary, "Total detected: 1") {
		t.Error("expected summary to show 1 detected query")
	}
	if !strings.Contains(summary, "SELECT * FROM users") {
		t.Error("expected summary to contain the query")
	}
}

func TestDetectorWithError(t *testing.T) {
	detector := NewDetector(WithThreshold(50 * time.Millisecond))
	testErr := errors.New("query failed")
	detector.Check("query", "postgres", "SELECT * FROM invalid", 100*time.Millisecond, testErr)

	slowQueries := detector.GetSlowQueries()
	if len(slowQueries) != 1 {
		t.Fatalf("expected 1 slow query, got %d", len(slowQueries))
	}
	if slowQueries[0].Error == nil {
		t.Error("expected error to be recorded")
	}
	if slowQueries[0].Error.Error() != testErr.Error() {
		t.Errorf("unexpected error: %v", slowQueries[0].Error)
	}
}

func TestDetectorDefaultThreshold(t *testing.T) {
	detector := NewDetector()
	if detector.Check("query", "postgres", "SELECT * FROM users", 500*time.Millisecond, nil) {
		t.Error("expected query under 1s not to be slow with default threshold")
	}
	if !detector.Check("query", "postgres", "SELECT * FROM huge_table", 1500*time.Millisecond, nil) {
		t.Error("expected query over 1s to be slow with default threshold")
	}
}

func TestDetectorConcurrency(t *testing.T) {
	detector := NewDetector(WithThreshold(50*time.Millisecond), WithMaxRecords(1000))

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				detector.Check("query", "postgres", "SELECT * FROM users", 100*time.Millisecond, nil)
			}
			done <- true
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	if detector.GetTotalDetected() != 100 {
		t.Errorf("expected 100 total detected, got %d", detector.GetTotalDetected())
	}
}

func TestObserver(t *testing.T) {
	callbackCalled := false
	base := testmetrics.NewMockCollector()

	observer := NewObserver(
		base,
		WithThreshold(100*time.Millisecond),
		WithCallback(func(SlowQuery) {
			callbackCalled = true
		}),
	)

	observer.ObserveDB(t.Context(), "query", "postgres", "SELECT * FROM users", 10, 200*time.Millisecond, nil)

	if base.DBCallCount() != 1 {
		t.Errorf("expected base collector to receive 1 call, got %d", base.DBCallCount())
	}
	if !callbackCalled {
		t.Error("expected slow query callback to be called")
	}
	if observer.GetDetector().GetTotalDetected() != 1 {
		t.Errorf("expected 1 slow query detected, got %d", observer.GetDetector().GetTotalDetected())
	}
}

func TestTruncateQuery(t *testing.T) {
	tests := []struct {
		query  string
		maxLen int
		want   string
	}{
		{"SELECT * FROM users", 50, "SELECT * FROM users"},
		{"SELECT * FROM users WHERE name = 'very long name that exceeds limit'", 30, "SELECT * FROM users WHERE name..."},
		{"", 10, ""},
		{"SHORT", 100, "SHORT"},
	}

	for _, tt := range tests {
		got := truncateQuery(tt.query, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateQuery(%q, %d) = %q, want %q", tt.query, tt.maxLen, got, tt.want)
		}
	}
}
