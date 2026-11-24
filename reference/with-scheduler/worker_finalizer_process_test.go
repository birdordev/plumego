//go:build unix

package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresFinalizerReplaysRecordedOutcome(t *testing.T) {
	for _, tc := range []struct {
		operation, status, outcome string
		failure                    mq.TaskFailureKind
		dead                       int
	}{
		{"ack", "done", "succeeded", "", 0},
		{"release", "queued", "failed", mq.TaskFailureTransient, 0},
		{"dlq", "dead", "failed", mq.TaskFailurePermanent, 1},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			_, contender, db := postgresTestQueues(t)
			ctx := t.Context()
			cfg := mqstore.DefaultSQLConfig()
			cfg.BlockedClaimsTable = "scheduler_blocked_claims"
			repo := repository{db, mqstore.NewSQL(db, cfg)}
			id, err := repo.submit(ctx, "tenant", "journal-failure", submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			// 目标状态是固定测试数据。约束同时阻断同步写入和独立核对写入。
			if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks ADD CONSTRAINT reject_final_state CHECK (status <> '"+tc.status+"') NOT VALID"); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			failed := make(chan struct{}, 1)
			queue := mq.NewTaskQueue(&journalQueue{TaskStore: repo.queue, repo: repo})
			w := mq.NewWorker(queue, mq.WorkerConfig{Concurrency: 1, PollInterval: 10 * time.Millisecond,
				BeginExecution: repo.beginExecution, RetryPolicy: mq.ExponentialBackoff{Base: time.Hour, Max: time.Hour},
				FinalizationErrorHook: func(context.Context, string, string, error) { failed <- struct{}{} }})
			w.Register(recordTaskType, func(ctx context.Context, task mq.Task) error {
				calls.Add(1)
				if tc.failure != "" {
					return &mq.TaskFailure{Kind: tc.failure}
				}
				return repo.recordResult(ctx, task)
			})
			w.Start(ctx)
			t.Cleanup(func() { _ = w.Stop(context.Background()) })
			select {
			case <-failed:
			case <-time.After(5 * time.Second):
				t.Fatal("did not observe injected write failure")
			}
			if err := w.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			history, err := repo.runAttempts(ctx, "tenant", id, 0)
			if err != nil || len(history.Items) != 1 {
				t.Fatalf("intent history %+v: %v", history, err)
			}
			intent := history.Items[0]
			if intent.ExecutionOutcome != tc.outcome || intent.Finalization != tc.operation ||
				intent.FinalizationState != "pending" || intent.ConfirmationError != "retryable" || intent.FinishedAt != nil {
				t.Fatalf("pending confirmation %+v", intent)
			}
			if _, err := db.ExecContext(ctx, `UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second'
				WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
			claims, err := contender.ReserveFenced(ctx, mq.ReserveOptions{Limit: 1, ConsumerID: "replacement"})
			if err != nil || len(claims) != 0 {
				t.Fatalf("attempt was offered for re-execution: %+v %v", claims, err)
			}
			var retryAt, due time.Time
			if tc.operation == "release" {
				if err := db.QueryRowContext(ctx, "SELECT finalization_retry_at FROM mq_task_attempts WHERE task_id = $1", id).Scan(&retryAt); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, "UPDATE mq_task_attempts SET finalization_check_at = clock_timestamp() WHERE task_id = $1", id); err != nil {
				t.Fatal(err)
			}
			if recovered, err := repo.reconcileFinalization(ctx); recovered != "" || err == nil {
				t.Fatalf("reconcile during outage = %q, %v", recovered, err)
			}
			if err := db.QueryRowContext(ctx, "SELECT finalization_check_at FROM mq_task_attempts WHERE task_id = $1", id).Scan(&due); err != nil || !due.After(time.Now()) {
				t.Fatalf("failed reconciliation was not deferred: %v %v", due, err)
			}
			if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks DROP CONSTRAINT reject_final_state"); err != nil {
				t.Fatal(err)
			}
			startSchedulerChild(t, db, "finalizer")
			startSchedulerChild(t, db, "finalizer")
			awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
				state, err := repo.runStatus(ctx, "tenant", id)
				return state.QueueStatus == tc.status, err
			})
			status, err := repo.runStatus(ctx, "tenant", id)
			if err != nil || status.ClaimAttempts != 1 || status.ExecutionAttempts != 1 || calls.Load() != 1 ||
				(tc.operation == "release" && (status.AvailableAt == nil || !status.AvailableAt.Equal(retryAt))) ||
				status.ResultReady != (tc.operation == "ack") {
				t.Fatalf("recovered status %+v, calls=%d: %v", status, calls.Load(), err)
			}
			history, err = repo.runAttempts(ctx, "tenant", id, 0)
			if err != nil || len(history.Items) != 1 || history.Items[0].FinalizationState != "confirmed" ||
				history.Items[0].ExecutionOutcome != tc.outcome || history.Items[0].ConfirmationError != "retryable" {
				t.Fatalf("confirmed history %+v: %v", history, err)
			}
			var dead int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM mq_task_dlq WHERE task_id = $1", id).Scan(&dead); err != nil || dead != tc.dead {
				t.Fatalf("DLQ entries=%d: %v", dead, err)
			}
		})
	}
}
