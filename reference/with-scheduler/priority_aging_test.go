package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresPriorityAgingRanksEligibleTasks(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	config := mqstore.DefaultSQLConfig()
	config.PriorityAging = time.Second
	config.DeferredExpiration = true
	config.BlockedClaimsTable = "scheduler_blocked_claims"
	queue := mq.NewTaskQueue(mqstore.NewSQL(db, config))
	now := time.Now().UTC().Truncate(time.Microsecond)
	enqueue := func(id string, priority mq.MessagePriority, available time.Time) {
		t.Helper()
		if err := queue.Enqueue(ctx, mq.Task{ID: id, Topic: "jobs", TenantID: "tenant", Priority: priority,
			CreatedAt: available}, mq.EnqueueOptions{AvailableAt: available}); err != nil {
			t.Fatal(err)
		}
	}
	enqueue("recently-expired", mq.PriorityNormal, now.Add(-5*time.Second))
	first, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "first", Limit: 1,
		Now: now.Add(-time.Second), Lease: 900 * time.Millisecond})
	if err != nil || len(first) != 1 || first[0].ID != "recently-expired" {
		t.Fatalf("seed claim %+v: %v", first, err)
	}
	enqueue("old-low", mq.PriorityLow, now.Add(-3*time.Second))
	enqueue("new-high", mq.PriorityHigh, now.Add(-200*time.Millisecond))
	if err := queue.Enqueue(ctx, mq.Task{ID: "backdated-high", Topic: "jobs", TenantID: "tenant", Priority: mq.PriorityHigh,
		CreatedAt: now.Add(-50 * time.Millisecond)}, mq.EnqueueOptions{AvailableAt: now.Add(-10 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	enqueue("second-old-low", mq.PriorityLow, now.Add(-2*time.Second))
	enqueue("new-low", mq.PriorityLow, now.Add(-100*time.Millisecond))
	enqueue("future-high", mq.PriorityHigh, now.Add(time.Second))
	batch, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "second", Topics: []string{"jobs"}, Limit: 8, Now: now})
	want := []string{"old-low", "second-old-low", "new-high", "backdated-high", "recently-expired", "new-low"}
	if err != nil || len(batch) != len(want) {
		t.Fatalf("aged batch %+v: %v", batch, err)
	}
	for i, task := range batch {
		if task.ID != want[i] {
			t.Fatalf("index %d: %s, want %s", i, task.ID, want[i])
		}
	}
}

func TestPostgresPriorityAgingUnderContinuousHighPriorityWork(t *testing.T) {
	dbA, dbB := postgresTestDatabases(t)
	ctx := t.Context()
	if err := migrate(ctx, dbA, "../../x/messaging/mq/store/migrations/postgres/001_task_queue.sql"); err != nil {
		t.Fatal(err)
	}
	const aging = 250 * time.Millisecond
	config := mqstore.DefaultSQLConfig()
	config.PriorityAging = aging
	config.DeferredExpiration = true
	qa, qb := mq.NewTaskQueue(mqstore.NewSQL(dbA, config)), mq.NewTaskQueue(mqstore.NewSQL(dbB, config))
	var sequence, highsStarted atomic.Int64
	high := func(ctx context.Context, queue *mq.TaskQueue) error {
		return queue.Enqueue(ctx, mq.Task{ID: fmt.Sprintf("high-%d", sequence.Add(1)), Topic: "jobs", TenantID: "tenant", Priority: mq.PriorityHigh}, mq.EnqueueOptions{})
	}
	startAt := time.Now()
	if err := qa.Enqueue(ctx, mq.Task{ID: "low", Topic: "jobs", TenantID: "tenant", Priority: mq.PriorityLow},
		mq.EnqueueOptions{AvailableAt: startAt}); err != nil {
		t.Fatal(err)
	}
	const initialHighTasks = 24
	for range initialHighTasks {
		if err := high(ctx, qa); err != nil {
			t.Fatal(err)
		}
	}
	lowStarted := make(chan time.Time, 1)
	for _, queue := range []*mq.TaskQueue{qa, qb} {
		worker := mq.NewWorker(queue, mq.WorkerConfig{Concurrency: 1, MaxInflight: 1,
			PollInterval: 5 * time.Millisecond, LeaseDuration: 10 * time.Second})
		worker.Register("jobs", func(ctx context.Context, task mq.Task) error {
			if task.ID == "low" {
				lowStarted <- time.Now()
				return nil
			}
			highsStarted.Add(1)
			timer := time.NewTimer(15 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return high(ctx, queue)
			}
		})
		worker.Start(ctx)
		t.Cleanup(func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := worker.Stop(stopCtx); err != nil {
				t.Error(err)
			}
		})
	}
	select {
	case at := <-lowStarted:
		waited := at.Sub(startAt)
		if waited < aging || waited > aging+700*time.Millisecond || highsStarted.Load() < 4 {
			t.Fatalf("low waited %v with %d high starts", waited, highsStarted.Load())
		}
		t.Logf("low priority started after %v with %d high starts", waited, highsStarted.Load())
	case <-time.After(2 * time.Second):
		t.Fatal("high priority traffic starved the low priority task")
	}
	var pending int
	if err := dbA.QueryRowContext(ctx, "SELECT count(*) FROM mq_tasks WHERE priority = $1 AND status = 'queued'", mq.PriorityHigh).
		Scan(&pending); err != nil || pending < initialHighTasks-2 {
		t.Fatalf("high priority traffic was not sustained: pending=%d %v", pending, err)
	}
}

func TestPostgresTaskPrioritySubmissionPaths(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(key, body string, code int) string {
		t.Helper()
		response := auditHTTPRequest(ctx, app, "POST", "/runs", key, "", body)
		if response.Code != code {
			t.Fatalf("submit %s: %d %s", key, response.Code, response.Body)
		}
		return strings.TrimPrefix(response.Header().Get("Location"), "/runs/")
	}
	readPriority := func(runID string, expected mq.MessagePriority) {
		t.Helper()
		var value int
		var options string
		if err := db.QueryRowContext(ctx, "SELECT priority, meta->>'task_options' FROM mq_tasks WHERE id = $1", runID).
			Scan(&value, &options); err != nil || value != int(expected) || strings.Contains(options, `"priority"`) {
			t.Fatalf("priority=%d want=%d options=%s: %v", value, expected, options, err)
		}
	}
	base := `{"topic":"demo.record","payload":{}}`
	low := `{"topic":"demo.record","payload":{},"priority":"low"}`
	normal := `{"topic":"demo.record","payload":{},"priority":"normal"}`
	readPriority(submit("low", low, http.StatusAccepted), mq.PriorityLow)
	submit("low", normal, http.StatusConflict)
	legacy := submit("normal", base, http.StatusAccepted)
	if id := submit("normal", normal, http.StatusAccepted); id != legacy {
		t.Fatalf("normal priority altered the legacy command identity: %s, want %s", id, legacy)
	}
	submit("invalid", `{"topic":"demo.record","payload":{},"priority":"urgent"}`, http.StatusBadRequest)
	batch := auditHTTPRequest(ctx, app, "POST", "/runs/batch", "", "",
		`{"runs":[{"idempotency_key":"batch-priority","topic":"demo.record","payload":{},"priority":"high"}]}`)
	if batch.Code != http.StatusAccepted {
		t.Fatalf("batch: %d %s", batch.Code, batch.Body)
	}
	var batchID string
	if err := db.QueryRowContext(ctx, "SELECT id FROM scheduled_runs WHERE tenant_id = 'tenant' AND request_key = 'batch-priority'").Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	readPriority(batchID, mq.PriorityHigh)
	schedule := auditHTTPRequest(ctx, app, "POST", "/schedules", "", "",
		`{"id":"background","expression":"@every 1h","time_zone":"UTC","topic":"demo.record","payload":{},"priority":"low"}`)
	if schedule.Code != http.StatusCreated {
		t.Fatalf("schedule: %d %s", schedule.Code, schedule.Body)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = clock_timestamp() WHERE id = 'background'"); err != nil {
		t.Fatal(err)
	}
	id, err := repo.materializeNext(ctx)
	if err != nil || id == "" {
		t.Fatalf("materialize %q: %v", id, err)
	}
	readPriority(id, mq.PriorityLow)
}
