package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresExecutionMarkerIsAtomicAndClaimFenced(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "atomic-execution", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	reserve := func() mq.Task {
		t.Helper()
		tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "worker", Lease: time.Minute, Limit: 1})
		if err != nil || len(tasks) != 1 || tasks[0].ID != id {
			t.Fatalf("claim count=%d err=%v", len(tasks), err)
		}
		return tasks[0]
	}
	exec := func(statement string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	old := reserve()
	exec("UPDATE mq_tasks SET max_attempts = 1, lease_until = clock_timestamp() - interval '1 second'")
	task := reserve()
	if _, err := repo.beginExecution(ctx, old); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("stale lease execution start = %v", err)
	}
	exec("ALTER TABLE mq_task_attempts ADD CONSTRAINT reject_start CHECK (execution_no IS NULL)")
	if _, err := repo.beginExecution(ctx, task); err == nil {
		t.Fatal("execution start succeeded after a write failure")
	}
	status, err := repo.runStatus(ctx, "tenant", id)
	if err != nil || status.ClaimAttempts != 2 || status.ExecutionAttempts != 0 {
		t.Fatalf("failed start mutated budget: %+v err=%v", status, err)
	}
	exec("ALTER TABLE mq_task_attempts DROP CONSTRAINT reject_start")
	if n, err := repo.beginExecution(ctx, task); err != nil || n != 1 {
		t.Fatalf("execution start seq=%d err=%v", n, err)
	}
	if _, err := repo.beginExecution(ctx, task); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("execution charge duplicated: %v", err)
	}
	attempts, err := repo.runAttempts(ctx, "tenant", id, 0)
	if err != nil || len(attempts.Items) != 2 || attempts.Items[0].ExecutionNo == nil ||
		*attempts.Items[0].ExecutionNo != 1 || attempts.Items[0].ExecutionStartedAt == nil || attempts.Items[1].ExecutionNo != nil {
		t.Fatalf("execution history: %+v err=%v", attempts, err)
	}
}
