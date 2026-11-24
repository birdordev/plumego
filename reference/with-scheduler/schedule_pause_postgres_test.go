//go:build unix

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresSchedulePauseSurvivesBothRoleRestarts(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const tenant, id = "tenant", "restart-pause"
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: tenant, ID: id, Expression: "@every 1m",
		TimeZone: "UTC", Topic: "demo.record", Payload: json.RawMessage(`{}`), MisfirePolicy: "coalesce"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE schedules SET next_fire_at = clock_timestamp() - interval '4 minutes' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	paused, running := true, false
	patch := func(cmd pauseCommand, want int64) {
		t.Helper()
		result, err := repo.setSchedulePause(ctx, tenant, id, cmd)
		if err != nil || result.Version != want {
			t.Fatalf("pause result=%+v err=%v, want version %d", result, err, want)
		}
	}
	patch(pauseCommand{Version: 1, Paused: &paused, ClaimPaused: &paused}, 2)
	var backlog []string
	for _, key := range []string{"first", "second"} {
		runID, err := repo.triggerSchedule(ctx, tenant, id, key)
		if err != nil {
			t.Fatal(err)
		}
		backlog = append(backlog, runID)
	}
	waitDone := func(runID string) {
		t.Helper()
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			run, err := repo.runStatus(ctx, tenant, runID)
			return run.QueueStatus == "done", err
		})
	}
	assertUnclaimed := func(ids []string) {
		t.Helper()
		for _, runID := range ids {
			run, err := repo.runStatus(ctx, tenant, runID)
			if err != nil || run.QueueStatus != "queued" || run.ClaimAttempts != 0 {
				t.Fatalf("paused backlog %s: %+v %v", runID, run, err)
			}
		}
	}
	for _, control := range []string{"before-restart", "after-restart"} {
		scheduler := startSchedulerChild(t, db, "scheduler")
		worker := startSchedulerChild(t, db, "worker")
		waitDone(submitShutdownTask(t, repo, control))
		assertUnclaimed(backlog)
		status, err := repo.scheduleStatus(ctx, tenant, id)
		if err != nil || !status.Paused || !status.ClaimPaused || status.Version != 2 || status.LastFiredAt != nil {
			t.Fatalf("paused schedule status: %+v %v", status, err)
		}
		killSchedulerChild(t, worker)
		killSchedulerChild(t, scheduler)
	}
	scheduler := startSchedulerChild(t, db, "scheduler")
	worker := startSchedulerChild(t, db, "worker")
	patch(pauseCommand{Version: 2, Paused: &running}, 3)
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.scheduleStatus(ctx, tenant, id)
		return !status.Paused && status.ClaimPaused && status.LastFiredAt != nil &&
			status.LastMisfireAction != nil && *status.LastMisfireAction == "coalesce", err
	})
	var automatic string
	if err := db.QueryRowContext(ctx, `SELECT id FROM scheduled_runs
		WHERE tenant_id = $1 AND schedule_id = $2 AND request_key IS NULL`, tenant, id).Scan(&automatic); err != nil {
		t.Fatal(err)
	}
	backlog = append(backlog, automatic)
	assertUnclaimed(backlog)
	patch(pauseCommand{Version: 3, ClaimPaused: &running}, 4)
	for _, runID := range backlog {
		waitDone(runID)
	}
	killSchedulerChild(t, worker)
	killSchedulerChild(t, scheduler)
}

func TestPostgresScheduleCancellationRechecksReceiptAfterRowLock(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const tenant, id = "tenant", "receipt-race"
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: tenant, ID: id, Expression: "@every 1h",
		TimeZone: "UTC", Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	runID, err := repo.triggerSchedule(ctx, tenant, id, "once")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "writer", Limit: 1})
	if err != nil || len(claims) != 1 || claims[0].ID != runID {
		t.Fatalf("claim count=%d err=%v", len(claims), err)
	}
	barrier := newCrashBarrier(t, db, 1, "BEFORE INSERT ON scheduled_results FOR EACH ROW")
	committed := make(chan error, 1)
	go func() { committed <- repo.recordResult(ctx, claims[0]) }()
	barrier.wait(t, db)
	type completion struct {
		result pauseResult
		err    error
	}
	canceled := make(chan completion, 1)
	go func() {
		result, err := repo.setSchedulePause(ctx, tenant, id, pauseCommand{Version: 1, CancelRunning: true})
		canceled <- completion{result, err}
	}()
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE 'SELECT q.id FROM scheduled_runs r%')`).Scan(&waiting)
		return waiting, err
	})
	barrier.unlock(t)
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	got := <-canceled
	if got.err != nil || got.result.Canceled == nil || *got.result.Canceled != 0 {
		t.Fatalf("committed receipt did not win cancellation race: %+v", got)
	}
	status, err := repo.runStatus(ctx, tenant, runID)
	if err != nil || status.QueueStatus != "leased" || !status.ResultReady {
		t.Fatalf("committed run changed: %+v %v", status, err)
	}
}
