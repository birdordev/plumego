package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

func TestSchedulerHistogramBucketsAndCardinality(t *testing.T) {
	m := &schedulerMetrics{}
	m.observe(queueLatency, "opaque-topic-a", "failed", -time.Second)
	m.observe(queueLatency, "opaque-topic-b", "failed", 2*time.Second)
	m.observe(queueLatency, "opaque-topic-c", "failed", 2*time.Hour)
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	const labels = `queue="unknown",task_type="unknown",result="failed"`
	for _, line := range []string{
		`plumego_scheduler_queue_delay_seconds_bucket{` + labels + `,le="0.001"} 1`,
		`plumego_scheduler_queue_delay_seconds_bucket{` + labels + `,le="2"} 2`,
		`plumego_scheduler_queue_delay_seconds_bucket{` + labels + `,le="+Inf"} 3`,
		`plumego_scheduler_queue_delay_seconds_sum{` + labels + `} 7202`,
		`plumego_scheduler_queue_delay_seconds_count{` + labels + `} 3`,
	} {
		if !strings.Contains(w.Body.String(), line+"\n") {
			t.Errorf("missing histogram line %q", line)
		}
	}
	if w.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" ||
		strings.Contains(w.Body.String(), "opaque-topic") {
		t.Fatalf("unexpected exposition %s", w.Body)
	}
}

func TestSchedulerQueueTimingPrecedesExecutionResult(t *testing.T) {
	m := &schedulerMetrics{}
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	failure := &mq.TaskFailure{Kind: mq.TaskFailureRateLimited}
	go func() {
		handler := m.handler(func(ctx context.Context, _ mq.Task) error {
			close(started)
			select {
			case <-release:
				return failure
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		done <- handler(t.Context(), mq.Task{Topic: recordTaskType, AvailableAt: time.Now().Add(-time.Minute)})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), `plumego_scheduler_queue_delay_seconds_count{queue="demo.record",task_type="demo.record",result="started"} 1`) ||
		strings.Contains(w.Body.String(), "plumego_scheduler_execution_duration_seconds") {
		t.Fatalf("metrics while handler is active: %s", w.Body)
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatalf("wrapped handler returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not finish")
	}
	w = httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(w.Body.String(), `plumego_scheduler_execution_duration_seconds_count{queue="demo.record",task_type="demo.record",result="rate_limited"} 1`) {
		t.Fatalf("missing rate-limit outcome: %s", w.Body)
	}
}
