package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestProgressReporterEnforcesBothThresholds(t *testing.T) {
	start := time.Now()
	writeFailed := errors.New("write failed")
	var persisted []int
	attempts := 0
	reporter := progressReporter{write: func(_ context.Context, percent int) error {
		attempts++
		if attempts == 3 {
			return writeFailed
		}
		persisted = append(persisted, percent)
		return nil
	}}
	for _, step := range []struct {
		at      time.Duration
		percent int
		err     error
	}{
		{0, 0, nil},
		{100 * time.Millisecond, 6, nil},
		{300 * time.Millisecond, 4, nil},
		{400 * time.Millisecond, 6, nil},
		{700 * time.Millisecond, 11, writeFailed},
		{701 * time.Millisecond, 11, nil},
		{time.Second, 100, nil},
	} {
		if err := reporter.report(t.Context(), start.Add(step.at), step.percent); !errors.Is(err, step.err) {
			t.Fatalf("report %d at %s: %v, want %v", step.percent, step.at, err, step.err)
		}
	}
	if attempts != 4 || !slices.Equal(persisted, []int{0, 6, 11}) {
		t.Fatalf("attempts=%d persisted=%v", attempts, persisted)
	}
}
