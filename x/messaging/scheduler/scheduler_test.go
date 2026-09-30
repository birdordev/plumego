package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type metricsSink struct {
	run   int32
	retry int32
	drop  int32
}

func (s *metricsSink) ObserveRun(jobID JobID, duration time.Duration, err error, queueDelay time.Duration) {
	atomic.AddInt32(&s.run, 1)
}

func (s *metricsSink) ObserveRetry(jobID JobID, attempt int, delay time.Duration) {
	atomic.AddInt32(&s.retry, 1)
}

func (s *metricsSink) ObserveDrop(jobID JobID) {
	atomic.AddInt32(&s.drop, 1)
}

func TestParseCronSpecAndNext(t *testing.T) {
	spec, err := ParseCronSpec("*/15 * * * *")
	if err != nil {
		t.Fatalf("parse cron: %v", err)
	}

	base := time.Date(2025, time.January, 1, 10, 7, 30, 0, time.UTC)
	next := spec.Next(base)
	expected := time.Date(2025, time.January, 1, 10, 15, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}
}

func TestParseCronSpecSeconds(t *testing.T) {
	spec, err := ParseCronSpec("*/10 * * * * *")
	if err != nil {
		t.Fatalf("parse cron: %v", err)
	}
	base := time.Date(2025, time.January, 1, 10, 7, 3, 0, time.UTC)
	next := spec.Next(base)
	expected := time.Date(2025, time.January, 1, 10, 7, 10, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}

	spec, err = ParseCronSpec("* * * * *")
	if err != nil {
		t.Fatalf("parse cron: %v", err)
	}
	base = time.Date(2025, time.January, 1, 10, 7, 59, 0, time.UTC)
	next = spec.Next(base)
	expected = time.Date(2025, time.January, 1, 10, 8, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %v, got %v", expected, next)
	}
}

func TestDelayJobRuns(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	done := make(chan struct{}, 1)
	_, err := s.Delay("delay-1", 25*time.Millisecond, func(ctx context.Context) error {
		done <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected delayed job to run")
	}
}

func TestJobContextValues(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	done := make(chan struct{}, 1)
	_, err := s.Delay("ctx-1", 5*time.Millisecond, func(ctx context.Context) error {
		if id, ok := JobIDFromContext(ctx); !ok || id != "ctx-1" {
			t.Fatalf("expected job id ctx-1")
		}
		if attempt, ok := JobAttemptFromContext(ctx); !ok || attempt < 1 {
			t.Fatalf("expected attempt >= 1")
		}
		done <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected job to run")
	}
}

func TestRetryPolicy(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	var calls int32
	_, err := s.Delay("retry-1", 5*time.Millisecond, func(ctx context.Context) error {
		if atomic.AddInt32(&calls, 1) < 2 {
			return context.Canceled
		}
		return nil
	}, WithRetryPolicy(RetryPolicy{MaxAttempts: 2, Backoff: func(attempt int) time.Duration {
		return 5 * time.Millisecond
	}}))
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 attempts, got %d", calls)
	}
}

func TestPauseResumeAndStatus(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	var calls int32
	id, err := s.Delay("pause-1", 10*time.Millisecond, func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	if !s.Pause(id) {
		t.Fatalf("expected pause to succeed")
	}

	time.Sleep(30 * time.Millisecond)
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no calls while paused")
	}

	if !s.Resume(id) {
		t.Fatalf("expected resume to succeed")
	}

	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 call after resume, got %d", calls)
	}

	status, ok := s.Status(id)
	if !ok {
		t.Fatalf("expected status")
	}
	if status.LastRun.IsZero() {
		t.Fatalf("expected last run to be set")
	}
}

func TestDeadLetterCallback(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	dead := make(chan JobID, 1)
	_, err := s.Delay("dlq-1", 5*time.Millisecond, func(ctx context.Context) error {
		return context.Canceled
	}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}), WithDeadLetter(func(ctx context.Context, id JobID, err error) {
		dead <- id
	}))
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	select {
	case id := <-dead:
		if id != "dlq-1" {
			t.Fatalf("unexpected job id: %s", id)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected dead letter callback")
	}
}

func TestReplaceExistingDelay(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	done := make(chan string, 2)
	_, err := s.Delay("replace-1", 20*time.Millisecond, func(ctx context.Context) error {
		done <- "old"
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	_, err = s.Delay("replace-1", 10*time.Millisecond, func(ctx context.Context) error {
		done <- "new"
		return nil
	}, ReplaceExisting())
	if err != nil {
		t.Fatalf("delay replace: %v", err)
	}

	select {
	case val := <-done:
		if val != "new" {
			t.Fatalf("expected replacement to run, got %s", val)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected replacement to run")
	}
}

func TestQueueBackpressureDrops(t *testing.T) {
	block := make(chan struct{})
	s := New(WithWorkers(1), WithQueueSize(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()
	defer close(block)

	_, err := s.Delay("blocker", 5*time.Millisecond, func(ctx context.Context) error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	_, err = s.Delay("queued", 5*time.Millisecond, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	_, err = s.Delay("dropper", 5*time.Millisecond, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	stats := s.Stats()
	if stats.Dropped == 0 {
		t.Fatalf("expected dropped tasks due to backpressure")
	}
}

func TestPanicHandler(t *testing.T) {
	called := make(chan JobID, 1)
	s := New(WithWorkers(1), WithPanicHandler(func(ctx context.Context, id JobID, recovered any) {
		called <- id
	}))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	_, err := s.Delay("panic-1", 5*time.Millisecond, func(ctx context.Context) error {
		panic("boom")
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	select {
	case id := <-called:
		if id != "panic-1" {
			t.Fatalf("unexpected job id: %s", id)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected panic handler to be called")
	}
}

func TestPersistenceReload(t *testing.T) {
	store := NewMemoryStore()
	done := make(chan struct{}, 1)

	s := New(WithWorkers(1), WithStore(store))
	if err := s.RegisterTask("persisted", func(ctx context.Context) error {
		done <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("register task: %v", err)
	}
	s.Start()
	_, err := s.Delay("persist-1", 10*time.Millisecond, func(ctx context.Context) error {
		done <- struct{}{}
		return nil
	}, WithTaskName("persisted"))
	if err != nil {
		t.Fatalf("delay: %v", err)
	}
	_ = s.Stop(t.Context())

	s2 := New(WithWorkers(1), WithStore(store))
	if err := s2.RegisterTask("persisted", func(ctx context.Context) error {
		done <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("register task: %v", err)
	}
	s2.Start()
	defer func() { _ = s2.Stop(t.Context()) }()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected persisted job to run")
	}
}

func TestMetricsSink(t *testing.T) {
	var sink metricsSink
	block := make(chan struct{})

	sched := New(WithWorkers(1), WithQueueSize(1), WithMetricsSink(&sink))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	_, err := sched.Delay("sink-2", 5*time.Millisecond, func(ctx context.Context) error {
		return context.Canceled
	}, WithRetryPolicy(RetryPolicy{MaxAttempts: 2, Backoff: func(_ int) time.Duration { return 5 * time.Millisecond }}))
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	_, err = sched.Delay("sink-1", 5*time.Millisecond, func(ctx context.Context) error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	_, err = sched.Delay("sink-3", 5*time.Millisecond, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	time.AfterFunc(20*time.Millisecond, func() { close(block) })
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&sink.run) > 0 && atomic.LoadInt32(&sink.retry) > 0 && atomic.LoadInt32(&sink.drop) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	if atomic.LoadInt32(&sink.run) == 0 {
		t.Fatalf("expected run metrics")
	}
	if atomic.LoadInt32(&sink.retry) == 0 {
		t.Fatalf("expected retry metrics")
	}
	if atomic.LoadInt32(&sink.drop) == 0 {
		t.Fatalf("expected drop metrics")
	}
}

func TestBackpressureDrop(t *testing.T) {
	// Test default drop behavior when queue is full
	var dropped atomic.Int32
	config := BackpressureConfig{
		Policy: BackpressureDrop,
		OnBackpressure: func(jobID JobID) {
			dropped.Add(1)
		},
	}

	sched := New(
		WithWorkers(1),
		WithQueueSize(1),
		WithBackpressure(config),
	)
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	// Block the worker
	block := make(chan struct{})
	_, err := sched.Delay("block", 5*time.Millisecond, func(ctx context.Context) error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	// Wait for job to be picked up by worker
	time.Sleep(20 * time.Millisecond)

	// Schedule jobs to fill the queue and trigger drops
	for i := 0; i < 10; i++ {
		_, _ = sched.Delay(JobID("job-"+string(rune(i))), 5*time.Millisecond, func(ctx context.Context) error {
			return nil
		})
	}

	close(block)
	time.Sleep(50 * time.Millisecond)

	// Verify drops occurred
	stats := sched.Stats()
	if stats.Dropped == 0 {
		t.Fatalf("expected dropped jobs, got 0")
	}
	if dropped.Load() == 0 {
		t.Fatalf("expected backpressure callback to be invoked")
	}
}

func TestBackpressureBlock(t *testing.T) {
	// Test blocking behavior - all jobs should be queued eventually
	config := BackpressureConfig{
		Policy: BackpressureBlock,
	}

	sched := New(
		WithWorkers(1),
		WithQueueSize(2),
		WithBackpressure(config),
	)
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	var executed atomic.Int32
	block := make(chan struct{})

	// Block the worker temporarily
	_, err := sched.Delay("block", 5*time.Millisecond, func(ctx context.Context) error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	// Wait for job to be picked up
	time.Sleep(20 * time.Millisecond)

	// Schedule multiple jobs - they should block but not drop
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			_, _ = sched.Delay(JobID("job-"+string(rune(i))), 5*time.Millisecond, func(ctx context.Context) error {
				executed.Add(1)
				return nil
			})
		}
		close(done)
	}()

	// Unblock after a delay
	time.Sleep(50 * time.Millisecond)
	close(block)

	// Wait for all scheduling to complete
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("scheduling timed out")
	}

	// Wait for execution
	time.Sleep(200 * time.Millisecond)

	stats := sched.Stats()
	if stats.Dropped > 0 {
		t.Fatalf("expected no drops with blocking policy, got %d", stats.Dropped)
	}
	if executed.Load() == 0 {
		t.Fatalf("expected jobs to execute")
	}
}

func TestBackpressureBlockTimeout(t *testing.T) {
	// Test timeout blocking behavior
	var dropped atomic.Int32
	config := BackpressureConfig{
		Policy:  BackpressureBlockTimeout,
		Timeout: 30 * time.Millisecond,
		OnBackpressure: func(jobID JobID) {
			dropped.Add(1)
		},
	}

	sched := New(
		WithWorkers(1),
		WithQueueSize(1),
		WithBackpressure(config),
	)
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	// Block the worker
	block := make(chan struct{})
	_, err := sched.Delay("block", 5*time.Millisecond, func(ctx context.Context) error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	// Wait for job to be picked up
	time.Sleep(20 * time.Millisecond)

	// Schedule jobs - some should timeout
	for i := 0; i < 5; i++ {
		_, _ = sched.Delay(JobID("job-"+string(rune(i))), 5*time.Millisecond, func(ctx context.Context) error {
			return nil
		})
	}

	// Don't unblock immediately to force timeouts
	time.Sleep(100 * time.Millisecond)
	close(block)
	time.Sleep(100 * time.Millisecond)

	stats := sched.Stats()
	if dropped.Load() == 0 {
		t.Fatalf("expected backpressure callback for timeouts")
	}
	if stats.Dropped == 0 {
		t.Fatalf("expected some jobs to be dropped due to timeout")
	}
}

func TestBatchOperationsByGroup(t *testing.T) {
	sched := New(WithWorkers(1))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	// Create jobs in different groups
	_, _ = sched.AddCron("group-a-1", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithGroup("group-a"))

	_, _ = sched.AddCron("group-a-2", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithGroup("group-a"))

	_, _ = sched.AddCron("group-b-1", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithGroup("group-b"))

	time.Sleep(20 * time.Millisecond)

	// Test PauseByGroup
	paused := sched.PauseByGroup("group-a")
	if paused != 2 {
		t.Fatalf("expected 2 jobs paused, got %d", paused)
	}

	// Verify jobs are paused
	status, ok := sched.Status("group-a-1")
	if !ok || !status.Paused {
		t.Fatalf("expected group-a-1 to be paused")
	}

	status, ok = sched.Status("group-b-1")
	if !ok || status.Paused {
		t.Fatalf("expected group-b-1 to not be paused")
	}

	// Test ResumeByGroup
	resumed := sched.ResumeByGroup("group-a")
	if resumed != 2 {
		t.Fatalf("expected 2 jobs resumed, got %d", resumed)
	}

	status, ok = sched.Status("group-a-1")
	if !ok || status.Paused {
		t.Fatalf("expected group-a-1 to be resumed")
	}

	// Test CancelByGroup
	canceled := sched.CancelByGroup("group-a")
	if canceled != 2 {
		t.Fatalf("expected 2 jobs canceled, got %d", canceled)
	}

	status, ok = sched.Status("group-a-1")
	if !ok || status.State != JobStateCanceled {
		t.Fatalf("expected group-a-1 to be canceled")
	}

	_, ok = sched.Status("group-b-1")
	if !ok {
		t.Fatalf("expected group-b-1 to still exist")
	}
}

func TestBatchOperationsByTags(t *testing.T) {
	sched := New(WithWorkers(1))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	// Create jobs with different tags
	_, _ = sched.AddCron("tagged-1", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithTags("urgent", "cleanup"))

	_, _ = sched.AddCron("tagged-2", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithTags("urgent", "cleanup"))

	_, _ = sched.AddCron("tagged-3", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithTags("urgent"))

	time.Sleep(20 * time.Millisecond)

	// Test PauseByTags - only jobs with ALL tags
	paused := sched.PauseByTags("urgent", "cleanup")
	if paused != 2 {
		t.Fatalf("expected 2 jobs paused, got %d", paused)
	}

	status, ok := sched.Status("tagged-1")
	if !ok || !status.Paused {
		t.Fatalf("expected tagged-1 to be paused")
	}

	status, ok = sched.Status("tagged-3")
	if !ok || status.Paused {
		t.Fatalf("expected tagged-3 to not be paused (missing 'cleanup' tag)")
	}

	// Test ResumeByTags
	resumed := sched.ResumeByTags("urgent", "cleanup")
	if resumed != 2 {
		t.Fatalf("expected 2 jobs resumed, got %d", resumed)
	}

	// Test CancelByTags
	canceled := sched.CancelByTags("urgent", "cleanup")
	if canceled != 2 {
		t.Fatalf("expected 2 jobs canceled, got %d", canceled)
	}

	status, ok = sched.Status("tagged-1")
	if !ok || status.State != JobStateCanceled {
		t.Fatalf("expected tagged-1 to be canceled")
	}

	_, ok = sched.Status("tagged-3")
	if !ok {
		t.Fatalf("expected tagged-3 to still exist")
	}
}

func TestQueryJobs(t *testing.T) {
	sched := New(WithWorkers(1))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	// Create diverse jobs for testing
	_, _ = sched.AddCron("cron-a-1", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithGroup("group-a"), WithTags("urgent"))

	_, _ = sched.AddCron("cron-a-2", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithGroup("group-a"), WithTags("urgent", "cleanup"))

	_, _ = sched.Delay("delay-b-1", 5*time.Second, func(ctx context.Context) error {
		return nil
	}, WithGroup("group-b"), WithTags("batch"))

	_, _ = sched.AddCron("cron-b-2", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithGroup("group-b"))

	time.Sleep(20 * time.Millisecond)

	// Pause one job for testing
	sched.Pause("cron-a-2")

	// Test 1: Filter by group
	result := sched.QueryJobs(JobQuery{Group: "group-a"})
	if result.Total != 2 {
		t.Fatalf("expected 2 jobs in group-a, got %d", result.Total)
	}

	// Test 2: Filter by tags
	result = sched.QueryJobs(JobQuery{Tags: []string{"urgent", "cleanup"}})
	if result.Total != 1 {
		t.Fatalf("expected 1 job with both tags, got %d", result.Total)
	}

	// Test 3: Filter by kind
	result = sched.QueryJobs(JobQuery{Kinds: []string{"cron"}})
	if result.Total != 3 {
		t.Fatalf("expected 3 cron jobs, got %d", result.Total)
	}

	result = sched.QueryJobs(JobQuery{Kinds: []string{"delay"}})
	if result.Total != 1 {
		t.Fatalf("expected 1 delay job, got %d", result.Total)
	}

	// Test 4: Filter by paused status
	paused := true
	result = sched.QueryJobs(JobQuery{Paused: &paused})
	if result.Total != 1 {
		t.Fatalf("expected 1 paused job, got %d", result.Total)
	}

	notPaused := false
	result = sched.QueryJobs(JobQuery{Paused: &notPaused})
	if result.Total != 3 {
		t.Fatalf("expected 3 not paused jobs, got %d", result.Total)
	}

	// Test 5: Sorting by ID
	result = sched.QueryJobs(JobQuery{OrderBy: "id", Ascending: true})
	if len(result.Jobs) < 2 {
		t.Fatalf("expected at least 2 jobs")
	}
	if result.Jobs[0].ID > result.Jobs[1].ID {
		t.Fatalf("expected jobs sorted by ID ascending")
	}

	// Test 6: Pagination
	result = sched.QueryJobs(JobQuery{Limit: 2, OrderBy: "id", Ascending: true})
	if len(result.Jobs) != 2 {
		t.Fatalf("expected 2 jobs with limit, got %d", len(result.Jobs))
	}
	if result.Total != 4 {
		t.Fatalf("expected total count of 4, got %d", result.Total)
	}

	result = sched.QueryJobs(JobQuery{Offset: 2, OrderBy: "id", Ascending: true})
	if len(result.Jobs) != 2 {
		t.Fatalf("expected 2 jobs with offset, got %d", len(result.Jobs))
	}

	// Test 7: Combined filters
	result = sched.QueryJobs(JobQuery{
		Group:  "group-a",
		Paused: &notPaused,
	})
	if result.Total != 1 {
		t.Fatalf("expected 1 job (group-a, not paused), got %d", result.Total)
	}

	// Test 8: Unknown orderBy should not alter result cardinality.
	result = sched.QueryJobs(JobQuery{OrderBy: "unknown"})
	if result.Total != 4 {
		t.Fatalf("expected total count 4 for unknown orderBy, got %d", result.Total)
	}

	// Test 9: Filter by state.
	if !sched.Cancel("cron-b-2") {
		t.Fatal("expected cancel cron-b-2 to succeed")
	}
	result = sched.QueryJobs(JobQuery{States: []JobState{JobStateCanceled}})
	if result.Total != 1 {
		t.Fatalf("expected 1 canceled job, got %d", result.Total)
	}
	if len(result.Jobs) != 1 || result.Jobs[0].ID != "cron-b-2" {
		t.Fatalf("expected canceled job cron-b-2, got %+v", result.Jobs)
	}
}

func TestJobDependenciesSuccess(t *testing.T) {
	sched := New(WithWorkers(2))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	var execOrder []string
	var mu sync.Mutex

	// Job A - no dependencies
	_, err := sched.Delay("job-a", 10*time.Millisecond, func(ctx context.Context) error {
		mu.Lock()
		execOrder = append(execOrder, "job-a")
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to add job-a: %v", err)
	}

	// Job B - depends on A
	_, err = sched.Delay("job-b", 10*time.Millisecond, func(ctx context.Context) error {
		mu.Lock()
		execOrder = append(execOrder, "job-b")
		mu.Unlock()
		return nil
	}, WithDependsOn(DependencyFailureSkip, "job-a"))
	if err != nil {
		t.Fatalf("failed to add job-b: %v", err)
	}

	// Job C - depends on A and B
	_, err = sched.Delay("job-c", 10*time.Millisecond, func(ctx context.Context) error {
		mu.Lock()
		execOrder = append(execOrder, "job-c")
		mu.Unlock()
		return nil
	}, WithDependsOn(DependencyFailureSkip, "job-a", "job-b"))
	if err != nil {
		t.Fatalf("failed to add job-c: %v", err)
	}

	// Wait for all jobs to complete
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(execOrder) != 3 {
		t.Fatalf("expected 3 jobs executed, got %d: %v", len(execOrder), execOrder)
	}

	// Verify execution order: A must come before B and C
	aIndex := -1
	bIndex := -1
	cIndex := -1
	for i, job := range execOrder {
		switch job {
		case "job-a":
			aIndex = i
		case "job-b":
			bIndex = i
		case "job-c":
			cIndex = i
		}
	}

	if aIndex == -1 || bIndex == -1 || cIndex == -1 {
		t.Fatalf("not all jobs executed: %v", execOrder)
	}

	if aIndex > bIndex {
		t.Fatalf("job-a must execute before job-b")
	}
	if bIndex > cIndex {
		t.Fatalf("job-b must execute before job-c")
	}
}

func TestJobDependencyFailureSkip(t *testing.T) {
	sched := New(WithWorkers(2))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	var executed atomic.Int32

	// Job A - will fail
	_, err := sched.Delay("job-a", 10*time.Millisecond, func(ctx context.Context) error {
		return fmt.Errorf("intentional failure")
	})
	if err != nil {
		t.Fatalf("failed to add job-a: %v", err)
	}

	// Job B - depends on A with Skip policy
	_, err = sched.Delay("job-b", 10*time.Millisecond, func(ctx context.Context) error {
		executed.Add(1)
		return nil
	}, WithDependsOn(DependencyFailureSkip, "job-a"))
	if err != nil {
		t.Fatalf("failed to add job-b: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if executed.Load() != 0 {
		t.Fatalf("expected job-b to be skipped, but it executed")
	}

	// Verify job-b still exists (not canceled)
	_, ok := sched.Status("job-b")
	if !ok {
		t.Fatalf("expected job-b to still exist after skip")
	}
}

func TestJobDependencyFailureCancel(t *testing.T) {
	sched := New(WithWorkers(2))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	var executed atomic.Int32

	// Job A - will fail
	_, err := sched.Delay("job-a", 10*time.Millisecond, func(ctx context.Context) error {
		return fmt.Errorf("intentional failure")
	})
	if err != nil {
		t.Fatalf("failed to add job-a: %v", err)
	}

	// Job B - depends on A with Cancel policy
	_, err = sched.Delay("job-b", 10*time.Millisecond, func(ctx context.Context) error {
		executed.Add(1)
		return nil
	}, WithDependsOn(DependencyFailureCancel, "job-a"))
	if err != nil {
		t.Fatalf("failed to add job-b: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if executed.Load() != 0 {
		t.Fatalf("expected job-b to not execute")
	}

	// Verify job-b is canceled
	status, ok := sched.Status("job-b")
	if !ok || status.State != JobStateCanceled {
		t.Fatalf("expected job-b to be canceled")
	}
}

func TestJobDependencyFailureContinue(t *testing.T) {
	sched := New(WithWorkers(2))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	var executed atomic.Int32

	// Job A - will fail
	_, err := sched.Delay("job-a", 10*time.Millisecond, func(ctx context.Context) error {
		return fmt.Errorf("intentional failure")
	})
	if err != nil {
		t.Fatalf("failed to add job-a: %v", err)
	}

	// Job B - depends on A with Continue policy
	_, err = sched.Delay("job-b", 10*time.Millisecond, func(ctx context.Context) error {
		executed.Add(1)
		return nil
	}, WithDependsOn(DependencyFailureContinue, "job-a"))
	if err != nil {
		t.Fatalf("failed to add job-b: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if executed.Load() != 1 {
		t.Fatalf("expected job-b to execute despite dependency failure")
	}
}

func TestJobDependencyValidation(t *testing.T) {
	sched := New(WithWorkers(1))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	// Test self-dependency
	_, err := sched.Delay("job-a", 10*time.Millisecond, func(ctx context.Context) error {
		return nil
	}, WithDependsOn(DependencyFailureSkip, "job-a"))
	if !errors.Is(err, ErrSelfDependency) {
		t.Fatalf("expected ErrSelfDependency, got: %v", err)
	}

	// Test non-existent dependency
	_, err = sched.Delay("job-b", 10*time.Millisecond, func(ctx context.Context) error {
		return nil
	}, WithDependsOn(DependencyFailureSkip, "non-existent"))
	if !errors.Is(err, ErrDependencyNotFound) {
		t.Fatalf("expected ErrDependencyNotFound, got: %v", err)
	}
}

func TestJobStateTransitions(t *testing.T) {
	sched := New(WithWorkers(1))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	block := make(chan struct{})
	_, err := sched.Delay("state-running", 0, func(ctx context.Context) error {
		<-block
		return nil
	})
	if err != nil {
		t.Fatalf("failed to add job: %v", err)
	}

	if !waitForState(t, sched, "state-running", JobStateRunning, 500*time.Millisecond) {
		t.Fatalf("expected state-running to reach running state")
	}

	close(block)

	if !waitForState(t, sched, "state-running", JobStateCompleted, 500*time.Millisecond) {
		t.Fatalf("expected state-running to reach completed state")
	}

	var attempts atomic.Int32
	_, err = sched.Delay("state-retry", 0, func(ctx context.Context) error {
		if attempts.Add(1) == 1 {
			return fmt.Errorf("fail once")
		}
		return nil
	}, WithRetryPolicy(RetryPolicy{
		MaxAttempts: 2,
		Backoff: func(attempt int) time.Duration {
			return 50 * time.Millisecond
		},
	}))
	if err != nil {
		t.Fatalf("failed to add retry job: %v", err)
	}

	if !waitForState(t, sched, "state-retry", JobStateRetrying, 200*time.Millisecond) {
		t.Fatalf("expected state-retry to enter retrying state")
	}

	_, err = sched.Delay("state-failed", 0, func(ctx context.Context) error {
		return fmt.Errorf("always fail")
	}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
	if err != nil {
		t.Fatalf("failed to add failed job: %v", err)
	}

	if !waitForState(t, sched, "state-failed", JobStateFailed, 500*time.Millisecond) {
		t.Fatalf("expected state-failed to reach failed state")
	}
}

func waitForState(t *testing.T, sched *Scheduler, id JobID, state JobState, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, ok := sched.Status(id)
		if ok && status.State == state {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestPruneTerminalJobs(t *testing.T) {
	s := New(WithWorkers(2))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	_, err := s.Delay("prune-done-1", 0, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("Delay prune-done-1: %v", err)
	}
	_, err = s.Delay("prune-done-2", 0, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("Delay prune-done-2: %v", err)
	}
	_, err = s.AddCron("prune-keep-cron", "@every 1h", func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("AddCron prune-keep-cron: %v", err)
	}

	if !waitForState(t, s, "prune-done-1", JobStateCompleted, 500*time.Millisecond) {
		t.Fatal("expected prune-done-1 to complete")
	}
	if !waitForState(t, s, "prune-done-2", JobStateCompleted, 500*time.Millisecond) {
		t.Fatal("expected prune-done-2 to complete")
	}

	if pruned := s.PruneTerminalJobs(1); pruned != 1 {
		t.Fatalf("expected first prune to remove 1 job, got %d", pruned)
	}
	if _, ok := s.Status("prune-keep-cron"); !ok {
		t.Fatal("expected prune-keep-cron to remain after prune")
	}

	if pruned := s.PruneTerminalJobs(0); pruned != 1 {
		t.Fatalf("expected second prune to remove remaining 1 terminal job, got %d", pruned)
	}
	if _, ok := s.Status("prune-done-1"); ok {
		t.Fatal("expected prune-done-1 to be pruned")
	}
	if _, ok := s.Status("prune-done-2"); ok {
		t.Fatal("expected prune-done-2 to be pruned")
	}
}

func TestPruneTerminalJobsSkipsJobsWithLiveDependents(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	_, err := s.Delay("dep-source", 0, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("Delay dep-source: %v", err)
	}
	_, err = s.Delay("dep-waiting", time.Hour, func(context.Context) error { return nil },
		Paused(),
		WithDependsOn(DependencyFailureSkip, "dep-source"),
	)
	if err != nil {
		t.Fatalf("Delay dep-waiting: %v", err)
	}

	if !waitForState(t, s, "dep-source", JobStateCompleted, 500*time.Millisecond) {
		t.Fatal("expected dep-source to complete")
	}

	if pruned := s.PruneTerminalJobs(0); pruned != 0 {
		t.Fatalf("expected no pruning for dep-source with live dependent, got %d", pruned)
	}
	if _, ok := s.Status("dep-source"); !ok {
		t.Fatal("expected dep-source to remain because dependent is still live")
	}
}

func TestCronDescriptors(t *testing.T) {
	tests := []struct {
		name       string
		descriptor string
		wantErr    bool
	}{
		{"hourly", "@hourly", false},
		{"daily", "@daily", false},
		{"midnight", "@midnight", false},
		{"weekly", "@weekly", false},
		{"monthly", "@monthly", false},
		{"yearly", "@yearly", false},
		{"annually", "@annually", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := ParseCronSpec(tt.descriptor)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseCronSpec(%q) error = %v, wantErr %v", tt.descriptor, err, tt.wantErr)
			}
			if err == nil {
				// Verify it produces valid next times
				now := time.Now()
				next := spec.Next(now)
				if next.IsZero() || !next.After(now) {
					t.Fatalf("invalid next time for %q", tt.descriptor)
				}
			}
		})
	}
}

func TestCronEveryDuration(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		wantErr  bool
		interval time.Duration
	}{
		{"5 minutes", "@every 5m", false, 5 * time.Minute},
		{"1 hour", "@every 1h", false, time.Hour},
		{"30 seconds", "@every 30s", false, 30 * time.Second},
		{"complex", "@every 1h30m", false, 90 * time.Minute},
		{"invalid", "@every invalid", true, 0},
		{"zero", "@every 0s", true, 0},
		{"negative", "@every -5m", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := ParseCronSpec(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseCronSpec(%q) error = %v, wantErr %v", tt.expr, err, tt.wantErr)
			}
			if err == nil {
				now := time.Now()
				next := spec.Next(now)
				expectedNext := now.Add(tt.interval)
				diff := next.Sub(expectedNext)
				if diff < 0 {
					diff = -diff
				}
				if diff > time.Second {
					t.Fatalf("@every %v: expected next ~%v, got %v", tt.interval, expectedNext, next)
				}
			}
		})
	}
}

func TestCronWithTimezone(t *testing.T) {
	// Create a timezone 5 hours ahead of UTC
	ahead := time.FixedZone("TEST+5", 5*3600)

	// Parse cron spec for 9 AM in the ahead timezone
	spec, err := ParseCronSpecWithLocation("0 9 * * *", ahead)
	if err != nil {
		t.Fatalf("ParseCronSpecWithLocation failed: %v", err)
	}

	// Start at 8 AM in the ahead timezone
	now := time.Date(2025, 1, 1, 8, 0, 0, 0, ahead)
	next := spec.Next(now)

	// Should be 9 AM in the ahead timezone
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Fatalf("expected 9:00, got %02d:%02d", next.Hour(), next.Minute())
	}

	// Verify it's in the correct timezone
	if next.Location().String() != ahead.String() {
		t.Fatalf("expected timezone %v, got %v", ahead, next.Location())
	}
}

func TestUpdateCronPreservesLocation(t *testing.T) {
	loc := time.FixedZone("TEST+8", 8*3600)
	sched := New(WithWorkers(1))

	_, err := sched.AddCronWithLocation("tz-cron", "0 9 * * *", func(ctx context.Context) error {
		return nil
	}, loc)
	if err != nil {
		t.Fatalf("AddCronWithLocation failed: %v", err)
	}

	before, ok := sched.Status("tz-cron")
	if !ok {
		t.Fatal("expected job status before update")
	}
	if before.NextRun.Location().String() != loc.String() {
		t.Fatalf("expected initial location %v, got %v", loc, before.NextRun.Location())
	}

	if err := sched.UpdateCron("tz-cron", "0 10 * * *"); err != nil {
		t.Fatalf("UpdateCron failed: %v", err)
	}

	after, ok := sched.Status("tz-cron")
	if !ok {
		t.Fatal("expected job status after update")
	}
	if after.NextRun.Location().String() != loc.String() {
		t.Fatalf("expected location to be preserved as %v, got %v", loc, after.NextRun.Location())
	}
}

func TestAddCronWithDescriptors(t *testing.T) {
	sched := New(WithWorkers(1))
	sched.Start()
	defer func() { _ = sched.Stop(t.Context()) }()

	var executed atomic.Int32

	// Test @every descriptor
	_, err := sched.AddCron("every-test", "@every 50ms", func(ctx context.Context) error {
		executed.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("AddCron with @every failed: %v", err)
	}

	// Test @hourly descriptor
	_, err = sched.AddCron("hourly-test", "@hourly", func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("AddCron with @hourly failed: %v", err)
	}

	// Wait for @every job to run
	time.Sleep(150 * time.Millisecond)

	if executed.Load() < 2 {
		t.Fatalf("expected at least 2 executions, got %d", executed.Load())
	}
}

func TestErrorHandlingStandardization(t *testing.T) {
	t.Run("ErrTaskNameEmpty", func(t *testing.T) {
		sched := New()
		err := sched.RegisterTask("", func(ctx context.Context) error { return nil })
		if !errors.Is(err, ErrTaskNameEmpty) {
			t.Fatalf("expected ErrTaskNameEmpty, got %v", err)
		}
	})

	t.Run("ErrTaskNil", func(t *testing.T) {
		sched := New()
		err := sched.RegisterTask("test", nil)
		if !errors.Is(err, ErrTaskNil) {
			t.Fatalf("expected ErrTaskNil, got %v", err)
		}
	})

	t.Run("ErrTaskNilInSchedule", func(t *testing.T) {
		sched := New()
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		_, err := sched.Schedule("test", time.Now().Add(time.Hour), nil)
		if !errors.Is(err, ErrTaskNil) {
			t.Fatalf("expected ErrTaskNil, got %v", err)
		}
	})

	t.Run("ErrRunAtRequired", func(t *testing.T) {
		sched := New()
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		task := func(ctx context.Context) error { return nil }
		_, err := sched.Schedule("test", time.Time{}, task)
		if !errors.Is(err, ErrRunAtRequired) {
			t.Fatalf("expected ErrRunAtRequired, got %v", err)
		}
	})

	t.Run("ErrJobExists", func(t *testing.T) {
		sched := New()
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		task := func(ctx context.Context) error { return nil }
		_, err := sched.AddCron("test", "* * * * *", task)
		if err != nil {
			t.Fatalf("first AddCron failed: %v", err)
		}

		_, err = sched.AddCron("test", "* * * * *", task)
		if !errors.Is(err, ErrJobExists) {
			t.Fatalf("expected ErrJobExists, got %v", err)
		}
	})

	t.Run("ErrSchedulerClosed", func(t *testing.T) {
		sched := New()
		sched.Start()
		_ = sched.Stop(t.Context())

		task := func(ctx context.Context) error { return nil }
		_, err := sched.AddCron("test", "* * * * *", task)
		if !errors.Is(err, ErrSchedulerClosed) {
			t.Fatalf("expected ErrSchedulerClosed, got %v", err)
		}
	})

	t.Run("ErrSelfDependency", func(t *testing.T) {
		sched := New()
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		task := func(ctx context.Context) error { return nil }
		_, err := sched.Delay("job1", time.Second, task,
			WithDependsOn(DependencyFailureSkip, "job1"),
		)
		if !errors.Is(err, ErrSelfDependency) {
			t.Fatalf("expected ErrSelfDependency, got %v", err)
		}
	})

	t.Run("ErrDependencyNotFound", func(t *testing.T) {
		sched := New()
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		task := func(ctx context.Context) error { return nil }
		_, err := sched.Delay("job1", time.Second, task,
			WithDependsOn(DependencyFailureSkip, "nonexistent"),
		)
		if !errors.Is(err, ErrDependencyNotFound) {
			t.Fatalf("expected ErrDependencyNotFound, got %v", err)
		}
	})

	t.Run("ErrInvalidCronExpr", func(t *testing.T) {
		tests := []struct {
			name string
			expr string
		}{
			{"too few fields", "* * *"},
			{"too many fields", "* * * * * * *"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := ParseCronSpec(tt.expr)
				if !errors.Is(err, ErrInvalidCronExpr) {
					t.Fatalf("expected ErrInvalidCronExpr for %q, got %v", tt.expr, err)
				}
			})
		}
	})

	t.Run("ErrInvalidCronField", func(t *testing.T) {
		tests := []struct {
			name string
			expr string
		}{
			{"invalid minute", "99 * * * *"},
			{"invalid hour", "* 25 * * *"},
			{"invalid day", "* * 32 * *"},
			{"invalid month", "* * * 13 *"},
			{"invalid dow", "* * * * 8"},
			{"invalid range", "5-2 * * * *"},
			{"invalid step", "*/0 * * * *"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := ParseCronSpec(tt.expr)
				if !errors.Is(err, ErrInvalidCronField) {
					t.Fatalf("expected ErrInvalidCronField for %q, got %v", tt.expr, err)
				}
			})
		}
	})

	t.Run("ErrInvalidEveryDuration", func(t *testing.T) {
		tests := []struct {
			name string
			expr string
		}{
			{"invalid format", "@every invalid"},
			{"zero duration", "@every 0s"},
			{"negative duration", "@every -5m"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := ParseCronSpec(tt.expr)
				if !errors.Is(err, ErrInvalidEveryDuration) {
					t.Fatalf("expected ErrInvalidEveryDuration for %q, got %v", tt.expr, err)
				}
			})
		}
	})
}

func TestDeadLetterQueue(t *testing.T) {
	t.Run("DLQ stores failed jobs", func(t *testing.T) {
		sched := New(
			WithWorkers(1),
			WithDeadLetterQueue(10),
		)
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		// Add a job that will fail
		_, err := sched.Delay("fail-job", 10*time.Millisecond, func(ctx context.Context) error {
			return fmt.Errorf("intentional failure")
		}, WithRetryPolicy(RetryPolicy{MaxAttempts: 2}))
		if err != nil {
			t.Fatalf("Delay failed: %v", err)
		}

		// Wait for job to fail and be added to DLQ
		time.Sleep(100 * time.Millisecond)

		// Check DLQ size
		size := sched.DeadLetterQueueSize()
		if size != 1 {
			t.Fatalf("expected DLQ size 1, got %d", size)
		}

		// Get the dead letter entry
		entry, exists := sched.GetDeadLetter("fail-job")
		if !exists {
			t.Fatal("expected dead letter entry to exist")
		}
		if entry.JobID != "fail-job" {
			t.Fatalf("expected job ID 'fail-job', got %s", entry.JobID)
		}
		if entry.Error == nil || entry.Error.Error() != "intentional failure" {
			t.Fatalf("expected error 'intentional failure', got %v", entry.Error)
		}
		if entry.Attempts != 2 {
			t.Fatalf("expected 2 attempts, got %d", entry.Attempts)
		}
	})

	t.Run("List all DLQ entries", func(t *testing.T) {
		sched := New(
			WithWorkers(1),
			WithDeadLetterQueue(10),
		)
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		// Add multiple failing jobs
		for i := 0; i < 3; i++ {
			jobID := fmt.Sprintf("fail-job-%d", i)
			_, err := sched.Delay(JobID(jobID), 10*time.Millisecond, func(ctx context.Context) error {
				return fmt.Errorf("failure %d", i)
			}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
			if err != nil {
				t.Fatalf("Delay failed: %v", err)
			}
		}

		// Wait for all jobs to fail
		time.Sleep(150 * time.Millisecond)

		// List all entries
		entries := sched.ListDeadLetters()
		if len(entries) != 3 {
			t.Fatalf("expected 3 DLQ entries, got %d", len(entries))
		}
	})

	t.Run("Delete DLQ entry", func(t *testing.T) {
		sched := New(
			WithWorkers(1),
			WithDeadLetterQueue(10),
		)
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		// Add a failing job
		_, err := sched.Delay("fail-job", 10*time.Millisecond, func(ctx context.Context) error {
			return fmt.Errorf("intentional failure")
		}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
		if err != nil {
			t.Fatalf("Delay failed: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Verify entry exists
		if sched.DeadLetterQueueSize() != 1 {
			t.Fatal("expected DLQ size 1")
		}

		// Delete the entry
		deleted := sched.DeleteDeadLetter("fail-job")
		if !deleted {
			t.Fatal("expected delete to return true")
		}

		// Verify entry is gone
		if sched.DeadLetterQueueSize() != 0 {
			t.Fatal("expected DLQ size 0 after delete")
		}
	})

	t.Run("Clear all DLQ entries", func(t *testing.T) {
		sched := New(
			WithWorkers(1),
			WithDeadLetterQueue(10),
		)
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		// Add multiple failing jobs
		for i := 0; i < 5; i++ {
			jobID := fmt.Sprintf("fail-job-%d", i)
			_, err := sched.Delay(JobID(jobID), 10*time.Millisecond, func(ctx context.Context) error {
				return fmt.Errorf("failure")
			}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
			if err != nil {
				t.Fatalf("Delay failed: %v", err)
			}
		}

		time.Sleep(150 * time.Millisecond)

		// Verify entries exist
		if sched.DeadLetterQueueSize() != 5 {
			t.Fatalf("expected DLQ size 5, got %d", sched.DeadLetterQueueSize())
		}

		// Clear all entries
		cleared := sched.ClearDeadLetters()
		if cleared != 5 {
			t.Fatalf("expected to clear 5 entries, got %d", cleared)
		}

		// Verify all entries are gone
		if sched.DeadLetterQueueSize() != 0 {
			t.Fatal("expected DLQ size 0 after clear")
		}
	})

	t.Run("Requeue from DLQ", func(t *testing.T) {
		sched := New(
			WithWorkers(1),
			WithDeadLetterQueue(10),
		)
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		var executed atomic.Int32

		// Add a failing job
		_, err := sched.Delay("fail-job", 10*time.Millisecond, func(ctx context.Context) error {
			return fmt.Errorf("intentional failure")
		}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
		if err != nil {
			t.Fatalf("Delay failed: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Verify entry in DLQ
		if sched.DeadLetterQueueSize() != 1 {
			t.Fatal("expected DLQ size 1")
		}

		// Requeue with a working task
		_, err = sched.RequeueDeadLetter("fail-job", func(ctx context.Context) error {
			executed.Add(1)
			return nil
		})
		if err != nil {
			t.Fatalf("RequeueDeadLetter failed: %v", err)
		}

		// Wait for requeued job to execute
		time.Sleep(100 * time.Millisecond)

		// Verify job executed
		if executed.Load() != 1 {
			t.Fatalf("expected 1 execution, got %d", executed.Load())
		}

		// Verify entry removed from DLQ
		if sched.DeadLetterQueueSize() != 0 {
			t.Fatalf("expected DLQ size 0 after requeue, got %d", sched.DeadLetterQueueSize())
		}
	})

	t.Run("Requeue keeps DLQ entry on registration failure", func(t *testing.T) {
		sched := New(
			WithWorkers(1),
			WithDeadLetterQueue(10),
		)
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		_, err := sched.Delay("fail-keep", 10*time.Millisecond, func(ctx context.Context) error {
			return fmt.Errorf("intentional failure")
		}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
		if err != nil {
			t.Fatalf("Delay failed: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		if sched.DeadLetterQueueSize() != 1 {
			t.Fatalf("expected DLQ size 1, got %d", sched.DeadLetterQueueSize())
		}

		_, err = sched.RequeueDeadLetter("fail-keep", nil)
		if !errors.Is(err, ErrTaskNil) {
			t.Fatalf("expected ErrTaskNil, got %v", err)
		}

		if sched.DeadLetterQueueSize() != 1 {
			t.Fatalf("expected DLQ entry preserved after failed requeue, got size %d", sched.DeadLetterQueueSize())
		}
		if _, ok := sched.GetDeadLetter("fail-keep"); !ok {
			t.Fatal("expected fail-keep to remain in DLQ")
		}
	})

	t.Run("DLQ max size eviction", func(t *testing.T) {
		sched := New(
			WithWorkers(1),
			WithDeadLetterQueue(3), // Max 3 entries
		)
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		// Add 5 failing jobs (should keep only 3)
		for i := 0; i < 5; i++ {
			jobID := fmt.Sprintf("fail-job-%d", i)
			_, err := sched.Delay(JobID(jobID), time.Duration(i*10)*time.Millisecond, func(ctx context.Context) error {
				return fmt.Errorf("failure")
			}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
			if err != nil {
				t.Fatalf("Delay failed: %v", err)
			}
		}

		time.Sleep(200 * time.Millisecond)

		// Verify max size is enforced
		size := sched.DeadLetterQueueSize()
		if size != 3 {
			t.Fatalf("expected DLQ size 3 (max), got %d", size)
		}
	})

	t.Run("DLQ disabled returns nil/false", func(t *testing.T) {
		sched := New(WithWorkers(1))
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		// All DLQ operations should return nil/false/0
		if sched.DeadLetterQueueSize() != 0 {
			t.Fatal("expected size 0 when DLQ disabled")
		}
		if sched.ListDeadLetters() != nil {
			t.Fatal("expected nil when DLQ disabled")
		}
		if _, ok := sched.GetDeadLetter("test"); ok {
			t.Fatal("expected false when DLQ disabled")
		}
		if sched.DeleteDeadLetter("test") {
			t.Fatal("expected false when DLQ disabled")
		}
		if sched.ClearDeadLetters() != 0 {
			t.Fatal("expected 0 when DLQ disabled")
		}
	})

	t.Run("Legacy callback still works with DLQ", func(t *testing.T) {
		sched := New(
			WithWorkers(1),
			WithDeadLetterQueue(10),
		)
		sched.Start()
		defer func() { _ = sched.Stop(t.Context()) }()

		var callbackCalled atomic.Bool

		// Add a failing job with legacy callback
		_, err := sched.Delay("fail-job", 10*time.Millisecond, func(ctx context.Context) error {
			return fmt.Errorf("intentional failure")
		},
			WithRetryPolicy(RetryPolicy{MaxAttempts: 1}),
			WithDeadLetter(func(ctx context.Context, id JobID, err error) {
				callbackCalled.Store(true)
			}),
		)
		if err != nil {
			t.Fatalf("Delay failed: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Verify both DLQ and callback were triggered
		if sched.DeadLetterQueueSize() != 1 {
			t.Fatal("expected DLQ size 1")
		}
		if !callbackCalled.Load() {
			t.Fatal("expected legacy callback to be called")
		}
	})
}

// TestReplaceExistingCleansUpDependencyMaps verifies that replacing a job via
// ReplaceExisting() fully cleans up the old job's dependency tracking (Bug #1).
func TestReplaceExistingCleansUpDependencyMaps(t *testing.T) {
	s := New(WithWorkers(2))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	triggered := make(chan struct{}, 4)

	// Register dep-a as a dependency.
	_, err := s.Delay("dep-a", 5*time.Second, func(ctx context.Context) error { return nil })
	if err != nil {
		t.Fatalf("dep-a: %v", err)
	}

	// Register dep-b to depend on dep-a.
	_, err = s.Delay("dep-b", 5*time.Second, func(ctx context.Context) error {
		triggered <- struct{}{}
		return nil
	}, WithDependsOn(DependencyFailureContinue, "dep-a"))
	if err != nil {
		t.Fatalf("dep-b: %v", err)
	}

	// Replace dep-b with a job that has NO dependencies.
	_, err = s.Delay("dep-b", 5*time.Second, func(ctx context.Context) error {
		return nil
	}, ReplaceExisting())
	if err != nil {
		t.Fatalf("replace dep-b: %v", err)
	}

	// dep-b's old dependents entry under dep-a must be gone.
	// After replacement, dep-b must NOT appear as a dependent of dep-a.
	s.mu.RLock()
	deps := s.dependents["dep-a"]
	s.mu.RUnlock()
	for _, d := range deps {
		if d == "dep-b" {
			t.Fatal("old dep-b still present in dependents[dep-a] after replacement")
		}
	}

	// dep-b's own dependencyStatus entry should also be removed.
	s.mu.RLock()
	_, ok := s.dependencyStatus["dep-b"]
	s.mu.RUnlock()
	if ok {
		t.Fatal("old dep-b still in dependencyStatus after replacement")
	}
}

// TestDependencyFailureContinueDoesNotCorruptStatus verifies that
// DependencyFailureContinue does not write true into dependencyStatus for
// the failed dependency (Bug #2).
func TestDependencyFailureContinueDoesNotCorruptStatus(t *testing.T) {
	s := New(WithWorkers(2))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	depErr := errors.New("dep failure")
	depRan := make(chan struct{}, 1)
	depBRan := make(chan struct{}, 1)

	// dep-a always fails.
	_, err := s.Delay("dep-a", 10*time.Millisecond, func(ctx context.Context) error {
		depRan <- struct{}{}
		return depErr
	}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
	if err != nil {
		t.Fatalf("dep-a: %v", err)
	}

	// dep-b continues even when dep-a fails.
	_, err = s.Delay("dep-b", 10*time.Millisecond, func(ctx context.Context) error {
		depBRan <- struct{}{}
		return nil
	}, WithDependsOn(DependencyFailureContinue, "dep-a"))
	if err != nil {
		t.Fatalf("dep-b: %v", err)
	}

	// Wait for dep-a to run.
	select {
	case <-depRan:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dep-a did not run")
	}
	// Wait for dep-b to run (Continue policy should allow it).
	select {
	case <-depBRan:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dep-b did not run after dep-a failed with DependencyFailureContinue")
	}

	// dep-a's status must remain false (failed), NOT been poisoned to true.
	time.Sleep(50 * time.Millisecond) // let execute() finish updating state
	s.mu.RLock()
	status, ok := s.dependencyStatus["dep-a"]
	s.mu.RUnlock()
	if ok && status {
		t.Fatal("DependencyFailureContinue incorrectly set dependencyStatus[dep-a] = true")
	}
}

// TestDependencyFailureSkipDelayJobMarkedFailed verifies that a delay job
// waiting on a dependency that fails with the Skip policy is marked as failed
// rather than left in limbo (Bug #3).
// ─── Fix regression tests (round 6) ──────────────────────────────────────────

// TestCronDOWOnlySchedule verifies that a cron expression with a restricted
// day-of-week and a wildcard day-of-month fires only on the specified weekday,
// not every day. ("0 0 * * 0" must produce Sundays only, not daily.)
func TestCronDOWOnlySchedule(t *testing.T) {
	// Start from a known Wednesday, 2025-01-08 00:00:00 UTC
	base := time.Date(2025, 1, 8, 0, 0, 0, 0, time.UTC) // Wednesday

	spec, err := ParseCronSpec("0 0 * * 0") // midnight every Sunday
	if err != nil {
		t.Fatalf("ParseCronSpec: %v", err)
	}

	next := spec.Next(base)
	// Next Sunday from Wed 8 Jan is 12 Jan
	want := time.Date(2025, 1, 12, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("expected next Sunday %v, got %v", want, next)
	}
	// Second call must jump to the following Sunday (19 Jan), not the next day.
	next2 := spec.Next(next)
	want2 := time.Date(2025, 1, 19, 0, 0, 0, 0, time.UTC)
	if !next2.Equal(want2) {
		t.Fatalf("expected second Sunday %v, got %v", want2, next2)
	}
}

// TestCronDOMOnlySchedule verifies that a restricted day-of-month with a
// wildcard day-of-week fires only on that calendar day, not every weekday.
// ("0 9 15 * *" must produce the 15th of each month only.)
func TestCronDOMOnlySchedule(t *testing.T) {
	// Start from 14 Jan 2025 (Tuesday).
	base := time.Date(2025, 1, 14, 9, 0, 0, 0, time.UTC)

	spec, err := ParseCronSpec("0 9 15 * *") // 9 AM on the 15th of every month
	if err != nil {
		t.Fatalf("ParseCronSpec: %v", err)
	}

	next := spec.Next(base)
	want := time.Date(2025, 1, 15, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("expected 15 Jan 09:00, got %v", next)
	}
	// Next after the 15th should be 15 Feb.
	next2 := spec.Next(next)
	want2 := time.Date(2025, 2, 15, 9, 0, 0, 0, time.UTC)
	if !next2.Equal(want2) {
		t.Fatalf("expected 15 Feb 09:00, got %v", next2)
	}
}

// TestCronBothDOMAndDOWRestricted verifies OR semantics when both DOM and DOW
// are restricted (Vixie cron: "30 4 1,15 * 5" fires on the 1st, 15th, and
// every Friday).
func TestCronBothDOMAndDOWRestricted(t *testing.T) {
	spec, err := ParseCronSpec("30 4 1,15 * 5") // 04:30 on 1st, 15th, or any Friday
	if err != nil {
		t.Fatalf("ParseCronSpec: %v", err)
	}

	// 2025-01-01 is a Wednesday — next match should be Friday 3 Jan (DOW=5).
	base := time.Date(2025, 1, 1, 5, 0, 0, 0, time.UTC)
	next := spec.Next(base)
	if next.Day() != 3 || next.Weekday() != time.Friday {
		t.Fatalf("expected Friday 3 Jan, got %v", next)
	}
}

// TestMaxRunsPreservesCompletedState verifies that when WithMaxRuns is reached
// the job ends up in JobStateCompleted (not JobStateCanceled), and that a
// dependent job is triggered as a successful completion.
func TestMaxRunsPreservesCompletedState(t *testing.T) {
	s := New(WithWorkers(2))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	var runs atomic.Int32
	_, err := s.AddCron("limited", "@every 20ms", func(ctx context.Context) error {
		runs.Add(1)
		return nil
	}, WithMaxRuns(2))
	if err != nil {
		t.Fatalf("AddCron: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	status, ok := s.Status("limited")
	if !ok {
		t.Fatal("job not found after MaxRuns")
	}
	if status.State != JobStateCompleted {
		t.Fatalf("expected state=completed after MaxRuns, got %s", status.State)
	}
	if runs.Load() < 2 {
		t.Fatalf("expected at least 2 runs, got %d", runs.Load())
	}
}

// TestMaxRunsTriggersDependents verifies that when a job hits MaxRuns its
// dependents are triggered as a SUCCESS, not as a cancellation failure.
func TestMaxRunsTriggersDependents(t *testing.T) {
	s := New(WithWorkers(2))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	_, _ = s.AddCron("producer", "@every 20ms", func(ctx context.Context) error {
		return nil
	}, WithMaxRuns(1))

	var consumerRan atomic.Int32
	_, _ = s.Delay("consumer", 0, func(ctx context.Context) error {
		consumerRan.Add(1)
		return nil
	}, WithDependsOn(DependencyFailureCancel, "producer"))

	time.Sleep(300 * time.Millisecond)

	if consumerRan.Load() != 1 {
		t.Fatalf("expected consumer to run once after producer hit MaxRuns, ran %d times", consumerRan.Load())
	}
}

// ─── Fix regression tests (round 5) ──────────────────────────────────────────

// TestCancelDependencyNotifiesDependents verifies that canceling a job that
// other jobs are waiting for applies DependencyFailurePolicy rather than
// leaving the dependents in limbo indefinitely.
func TestCancelDependencyNotifiesDependents(t *testing.T) {
	t.Run("policy=Cancel", func(t *testing.T) {
		s := New(WithWorkers(2))
		s.Start()
		defer func() { _ = s.Stop(t.Context()) }()

		block := make(chan struct{})
		_, _ = s.Delay("dep-a", 0, func(ctx context.Context) error { <-block; return nil })
		var bRan atomic.Int32
		_, _ = s.Delay("dep-b", 10*time.Millisecond, func(ctx context.Context) error {
			bRan.Add(1)
			return nil
		}, WithDependsOn(DependencyFailureCancel, "dep-a"))

		s.Cancel("dep-a")
		close(block)
		time.Sleep(150 * time.Millisecond)

		if bRan.Load() != 0 {
			t.Fatal("dep-b should not run when dependency is canceled (Cancel policy)")
		}
		status, ok := s.Status("dep-b")
		if !ok || status.State != JobStateCanceled {
			t.Fatalf("expected dep-b state=canceled, got state=%s ok=%v", status.State, ok)
		}
	})

	t.Run("policy=Skip_delay", func(t *testing.T) {
		s := New(WithWorkers(2))
		s.Start()
		defer func() { _ = s.Stop(t.Context()) }()

		block := make(chan struct{})
		_, _ = s.Delay("dep-a", 0, func(ctx context.Context) error { <-block; return nil })
		var bRan atomic.Int32
		_, _ = s.Delay("dep-b", 10*time.Millisecond, func(ctx context.Context) error {
			bRan.Add(1)
			return nil
		}, WithDependsOn(DependencyFailureSkip, "dep-a"))

		s.Cancel("dep-a")
		close(block)
		time.Sleep(150 * time.Millisecond)

		if bRan.Load() != 0 {
			t.Fatal("dep-b should not run when dependency is canceled (Skip policy)")
		}
		status, ok := s.Status("dep-b")
		if !ok {
			return // cleaned up from s.jobs, acceptable
		}
		if status.State != JobStateFailed {
			t.Fatalf("expected dep-b state=failed, got %s", status.State)
		}
	})

	t.Run("policy=Continue", func(t *testing.T) {
		s := New(WithWorkers(2))
		s.Start()
		defer func() { _ = s.Stop(t.Context()) }()

		block := make(chan struct{})
		_, _ = s.Delay("dep-a", 0, func(ctx context.Context) error { <-block; return nil })
		var bRan atomic.Int32
		_, _ = s.Delay("dep-b", 10*time.Millisecond, func(ctx context.Context) error {
			bRan.Add(1)
			return nil
		}, WithDependsOn(DependencyFailureContinue, "dep-a"))

		s.Cancel("dep-a")
		close(block)
		time.Sleep(150 * time.Millisecond)

		if bRan.Load() != 1 {
			t.Fatalf("expected dep-b to run once (Continue policy), ran %d times", bRan.Load())
		}
	})
}

// ─── Fix regression tests (round 4) ──────────────────────────────────────────

// TestTriggerNowCompletedDelayJob verifies that TriggerNow returns an error
// when called on a delay job that has already completed (one-shot semantics).
func TestTriggerNowCompletedDelayJob(t *testing.T) {
	s := New(WithWorkers(2))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	done := make(chan struct{})
	_, err := s.Delay("one-shot", 0, func(ctx context.Context) error {
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("Delay: %v", err)
	}

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("one-shot job did not complete in time")
	}
	// Allow state transition to propagate.
	time.Sleep(20 * time.Millisecond)

	if err := s.TriggerNow("one-shot"); err == nil {
		t.Fatal("expected error when calling TriggerNow on a completed delay job")
	}
}

// TestBackpressureReleasesRunningFlag verifies that when a job with SerialQueue
// overlap policy is dropped due to backpressure, the running flag is released
// so the job can be dispatched again on its next cycle.
func TestBackpressureReleasesRunningFlag(t *testing.T) {
	// Block the single worker so the queue fills immediately.
	block := make(chan struct{})
	s := New(WithWorkers(1), WithQueueSize(1), WithBackpressure(BackpressureConfig{Policy: BackpressureDrop}))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	blocker := make(chan struct{})
	_, err := s.Delay("blocker", 0, func(ctx context.Context) error {
		close(blocker)
		// Block until the test releases us or the scheduler shuts down.
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Delay blocker: %v", err)
	}

	// Wait until blocker is actually running, occupying the one worker slot.
	select {
	case <-blocker:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("blocker did not start")
	}

	// Now add a SerialQueue cron job whose first dispatch will hit backpressure.
	var runs atomic.Int32
	_, err = s.AddCron("serial-job", "@every 20ms", func(ctx context.Context) error {
		runs.Add(1)
		return nil
	}, WithOverlapPolicy(SerialQueue))
	if err != nil {
		t.Fatalf("AddCron: %v", err)
	}

	// Unblock the worker; serial-job should now be dispatchable.
	close(block)

	time.Sleep(200 * time.Millisecond)
	if runs.Load() == 0 {
		t.Fatal("serial-job never executed after backpressure was released; running flag may be stuck")
	}
}

// TestDLQDeleteRemovesFromOrderSlice verifies that Delete() keeps the order
// slice compact so that FIFO eviction remains efficient and memory is freed.
func TestDLQDeleteRemovesFromOrderSlice(t *testing.T) {
	q := NewDeadLetterQueue(10)

	for i := 0; i < 5; i++ {
		q.Add(DeadLetterEntry{
			JobID:       JobID(fmt.Sprintf("job-%d", i)),
			FirstFailed: time.Now(),
			LastFailed:  time.Now(),
		})
	}

	q.Delete("job-2")

	// The order slice should now have 4 elements, not 5.
	q.mu.RLock()
	orderLen := len(q.order)
	q.mu.RUnlock()
	if orderLen != 4 {
		t.Fatalf("expected order slice length 4 after Delete, got %d", orderLen)
	}
	if q.Size() != 4 {
		t.Fatalf("expected DLQ size 4, got %d", q.Size())
	}
}

func TestDLQListReturnsInsertionOrderAndCopies(t *testing.T) {
	q := NewDeadLetterQueue(10)
	now := time.Now()
	q.Add(DeadLetterEntry{
		JobID:        "job-1",
		ErrorMessage: "e1",
		Attempts:     1,
		FirstFailed:  now,
		LastFailed:   now,
		Tags:         []string{"tag-a"},
	})
	q.Add(DeadLetterEntry{
		JobID:        "job-2",
		ErrorMessage: "e2",
		Attempts:     1,
		FirstFailed:  now,
		LastFailed:   now,
		Tags:         []string{"tag-b"},
	})

	items := q.List()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].JobID != "job-1" || items[1].JobID != "job-2" {
		t.Fatalf("expected insertion order [job-1, job-2], got [%s, %s]", items[0].JobID, items[1].JobID)
	}

	// Mutating returned list must not affect internal entries.
	items[0].Tags[0] = "mutated-list"
	got, ok := q.Get("job-1")
	if !ok {
		t.Fatal("expected job-1 in DLQ")
	}
	if got.Tags[0] != "tag-a" {
		t.Fatalf("expected internal tag tag-a after list mutation, got %s", got.Tags[0])
	}

	// Mutating returned Get result must not affect subsequent reads.
	got.Tags[0] = "mutated-get"
	got2, ok := q.Get("job-1")
	if !ok {
		t.Fatal("expected job-1 in DLQ on second get")
	}
	if got2.Tags[0] != "tag-a" {
		t.Fatalf("expected internal tag tag-a after get mutation, got %s", got2.Tags[0])
	}
}

// TestLoadPersistedTwoPass verifies that jobs with dependencies are correctly
// restored from a persistent store even if they are listed before their
// dependency targets.
func TestLoadPersistedTwoPass(t *testing.T) {
	store := NewMemoryStore()

	// Persist dep-b (depends on dep-a) BEFORE dep-a so that a naive
	// single-pass loader would fail (dep-a not yet registered).
	_ = store.Save(StoredJob{
		ID:               "dep-b",
		TaskName:         "task-b",
		RunAt:            time.Now().Add(10 * time.Millisecond),
		Dependencies:     []JobID{"dep-a"},
		DependencyPolicy: DependencyFailureSkip,
	})
	_ = store.Save(StoredJob{
		ID:       "dep-a",
		TaskName: "task-a",
		RunAt:    time.Now().Add(10 * time.Millisecond),
	})

	var aRan, bRan atomic.Int32
	s := New(WithWorkers(2), WithStore(store))
	if err := s.RegisterTask("task-a", func(ctx context.Context) error { aRan.Add(1); return nil }); err != nil {
		t.Fatalf("RegisterTask task-a: %v", err)
	}
	if err := s.RegisterTask("task-b", func(ctx context.Context) error { bRan.Add(1); return nil }); err != nil {
		t.Fatalf("RegisterTask task-b: %v", err)
	}
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	time.Sleep(300 * time.Millisecond)

	if aRan.Load() == 0 {
		t.Fatal("dep-a (no dependencies) was not restored and executed")
	}
	if bRan.Load() == 0 {
		t.Fatal("dep-b (with dep-a dependency) was not restored and executed")
	}
}

func TestDependencyFailureSkipDelayJobMarkedFailed(t *testing.T) {
	s := New(WithWorkers(2))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	depRan := make(chan struct{}, 1)

	// dep-a always fails.
	_, err := s.Delay("dep-a", 10*time.Millisecond, func(ctx context.Context) error {
		depRan <- struct{}{}
		return errors.New("dep failure")
	}, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
	if err != nil {
		t.Fatalf("dep-a: %v", err)
	}

	// dep-b skips execution when dep-a fails.
	executed := make(chan struct{}, 1)
	_, err = s.Delay("dep-b", 10*time.Millisecond, func(ctx context.Context) error {
		executed <- struct{}{}
		return nil
	}, WithDependsOn(DependencyFailureSkip, "dep-a"))
	if err != nil {
		t.Fatalf("dep-b: %v", err)
	}

	// Wait for dep-a to run.
	select {
	case <-depRan:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dep-a did not run")
	}

	// dep-b must NOT execute.
	select {
	case <-executed:
		t.Fatal("dep-b should not have executed when DependencyFailureSkip")
	case <-time.After(200 * time.Millisecond):
	}

	// dep-b must be marked as failed (not limbo).
	time.Sleep(50 * time.Millisecond)
	status, ok := s.Status("dep-b")
	if !ok {
		// If the job was cleaned up from s.jobs that is also acceptable.
		return
	}
	if status.State != JobStateFailed {
		t.Fatalf("expected dep-b state=failed, got %s", status.State)
	}
}

// TestDependencyRunsAfterDependencyRetrySuccess verifies that a dependent job
// is NOT notified of failure on the first failed attempt of its dependency.
// If the dependency eventually succeeds (on a retry), the dependent must execute.
func TestDependencyRunsAfterDependencyRetrySuccess(t *testing.T) {
	s := New(WithWorkers(2))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	var attempt atomic.Int32

	// dep-a fails on attempt 1 and succeeds on attempt 2.
	depRan := make(chan struct{}, 1)
	_, err := s.Delay("dep-a", 0, func(ctx context.Context) error {
		n := attempt.Add(1)
		if n == 1 {
			return errors.New("transient error")
		}
		depRan <- struct{}{}
		return nil
	}, WithRetryPolicy(RetryPolicy{MaxAttempts: 2, Backoff: func(int) time.Duration { return 10 * time.Millisecond }}))
	if err != nil {
		t.Fatalf("dep-a: %v", err)
	}

	// dep-b depends on dep-a. It should run after dep-a succeeds on retry.
	executed := make(chan struct{}, 1)
	_, err = s.Delay("dep-b", 0, func(ctx context.Context) error {
		executed <- struct{}{}
		return nil
	}, WithDependsOn(DependencyFailureContinue, "dep-a"))
	if err != nil {
		t.Fatalf("dep-b: %v", err)
	}

	// Wait for dep-a to complete (including retry).
	select {
	case <-depRan:
	case <-time.After(2 * time.Second):
		t.Fatal("dep-a did not succeed within timeout")
	}

	// dep-b must execute because dep-a ultimately succeeded.
	select {
	case <-executed:
		// Good: dependent ran after dependency succeeded on retry.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("dep-b did not execute after dep-a succeeded on retry")
	}
}

func TestDelayTriggerNowInvalidatesPreviousSchedule(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	var runs atomic.Int32
	_, err := s.Delay("trigger-once", 120*time.Millisecond, func(ctx context.Context) error {
		runs.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("delay: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	if err := s.TriggerNow("trigger-once"); err != nil {
		t.Fatalf("trigger now: %v", err)
	}

	time.Sleep(220 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("expected exactly 1 execution after TriggerNow, got %d", got)
	}
}

func TestAddJobRejectsDependencyCycle(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	_, err := s.Delay("job-a", time.Hour, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("add job-a: %v", err)
	}

	_, err = s.Delay("job-b", time.Hour, func(context.Context) error { return nil }, WithDependsOn(DependencyFailureCancel, "job-a"))
	if err != nil {
		t.Fatalf("add job-b: %v", err)
	}

	_, err = s.Delay(
		"job-a",
		time.Hour,
		func(context.Context) error { return nil },
		ReplaceExisting(),
		WithDependsOn(DependencyFailureCancel, "job-b"),
	)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected ErrDependencyCycle, got %v", err)
	}
}

func TestCronTriggerNowKeepsNextScheduledRun(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	runs := make(chan time.Time, 4)
	_, err := s.AddCron("cron-trigger", "* * * * * *", func(ctx context.Context) error {
		runs <- time.Now()
		return nil
	})
	if err != nil {
		t.Fatalf("add cron: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if err := s.TriggerNow("cron-trigger"); err != nil {
		t.Fatalf("trigger now: %v", err)
	}

	select {
	case <-runs:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected immediate triggered cron run")
	}

	select {
	case <-runs:
		// Expected: normal cron schedule should still run.
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("expected next cron-scheduled run after TriggerNow")
	}
}

func TestReplaceExistingWithInvalidCycleKeepsOriginalJob(t *testing.T) {
	s := New(WithWorkers(1))
	s.Start()
	defer func() { _ = s.Stop(t.Context()) }()

	var oldRuns atomic.Int32
	_, err := s.AddCron("job-a", "* * * * * *", func(context.Context) error {
		oldRuns.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("add job-a: %v", err)
	}
	_, err = s.Delay("job-b", 2*time.Second, func(context.Context) error { return nil }, WithDependsOn(DependencyFailureCancel, "job-a"))
	if err != nil {
		t.Fatalf("add job-b: %v", err)
	}

	_, err = s.AddCron(
		"job-a",
		"* * * * * *",
		func(context.Context) error { return nil },
		ReplaceExisting(),
		WithDependsOn(DependencyFailureCancel, "job-b"),
	)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected ErrDependencyCycle, got %v", err)
	}

	<-time.After(1200 * time.Millisecond)
	if got := oldRuns.Load(); got == 0 {
		t.Fatal("expected original cron job to remain active after failed replacement")
	}
}

func TestWithRandomSeedProducesDeterministicJitterSequence(t *testing.T) {
	s1 := New(WithRandomSeed(42))
	s2 := New(WithRandomSeed(42))

	for i := 0; i < 8; i++ {
		j1 := s1.nextJitter(2 * time.Second)
		j2 := s2.nextJitter(2 * time.Second)
		if j1 != j2 {
			t.Fatalf("expected deterministic jitter at index %d, got %v and %v", i, j1, j2)
		}
		if j1 < 0 || j1 >= 2*time.Second {
			t.Fatalf("jitter out of expected bounds: %v", j1)
		}
	}
}

func TestWithRandomSeedSupportsDifferentSequences(t *testing.T) {
	s1 := New(WithRandomSeed(1))
	s2 := New(WithRandomSeed(2))

	different := false
	for i := 0; i < 8; i++ {
		if s1.nextJitter(time.Second) != s2.nextJitter(time.Second) {
			different = true
			break
		}
	}
	if !different {
		t.Fatal("expected different seeds to produce different jitter sequences")
	}
}
