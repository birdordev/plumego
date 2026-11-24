//go:build unix

package main

import (
	"context"
	"encoding/json"
	"testing"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresVersionedWorkersOverlapSafely(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	const tenant = "tenant"
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	submit := func(key string, version int) string {
		t.Helper()
		id, err := repo.submit(ctx, tenant, key, submission{Topic: recordTaskType, Payload: json.RawMessage(`{"message":"ok"}`),
			taskOptions: taskOptions{TaskVersion: version}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	waitState := func(id, status string) {
		t.Helper()
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			run, err := repo.runStatus(ctx, tenant, id)
			if err == nil && run.QueueStatus == status {
				if run.ClaimAttempts != 1 || run.ResultReady != (status == "done") {
					t.Fatalf("unexpected terminal run: %+v", run)
				}
				return true, nil
			}
			return false, err
		})
	}
	v2 := submit("waits-for-v2", 2)
	v1 := submit("legacy", 1)
	t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "1")
	oldWorker := startSchedulerChild(t, db, "worker")
	waitState(v1, "done")
	pending, err := repo.runStatus(ctx, tenant, v2)
	if err != nil || pending.QueueStatus != "queued" || pending.ClaimAttempts != 0 {
		t.Fatalf("v1 worker claimed v2: %+v %v", pending, err)
	}
	corrupt := submit("corrupt-v2", 2)
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET payload = '{}'::bytea WHERE id = $1", corrupt); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "2")
	newWorker := startSchedulerChild(t, db, "worker")
	waitState(submit("legacy-during-rollout", 1), "done")
	waitState(v2, "done")
	waitState(corrupt, "dead")
	history, err := repo.deadLetters(ctx, tenant, corrupt, 0)
	if err != nil || len(history.Items) != 1 || history.Items[0].Topic != recordTaskType ||
		history.Items[0].TaskVersion != "2" || history.Items[0].FailureCode != "permanent" {
		t.Fatalf("invalid stored task diagnosis: %+v %v", history, err)
	}
	killSchedulerChild(t, oldWorker)
	killSchedulerChild(t, newWorker)
}
