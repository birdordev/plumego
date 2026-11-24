package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresScheduleCancelRunningPreservesOtherWork(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const tenant, schedule = "tenant", "nightly"
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: tenant, ID: schedule, Expression: "@every 1h",
		TimeZone: "UTC", Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	trigger := func(key string) string {
		t.Helper()
		id, err := repo.triggerSchedule(ctx, tenant, schedule, key)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	committed := trigger("committed")
	claims, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "committer", Limit: 1, Lease: time.Minute})
	if err != nil || len(claims) != 1 || claims[0].ID != committed {
		t.Fatalf("commit claim count=%d err=%v", len(claims), err)
	}
	if err := repo.recordResult(ctx, claims[0]); err != nil {
		t.Fatal(err)
	}
	active := trigger("active")
	started := make(chan mq.Task, 1)
	stopped := make(chan error, 1)
	worker := mq.NewWorker(queue, mq.WorkerConfig{
		Concurrency: 1, MaxInflight: 1, LeaseDuration: time.Second,
		LeaseExtendInterval: 25 * time.Millisecond, BeginExecution: repo.beginExecution,
	})
	worker.Register("demo.record", func(ctx context.Context, task mq.Task) error {
		started <- task
		<-ctx.Done()
		stopped <- ctx.Err()
		return ctx.Err()
	})
	worker.Start(ctx)
	defer func() {
		cancel()
		if err := worker.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	var running mq.Task
	select {
	case running = <-started:
		if running.ID != active {
			t.Fatalf("started wrong task: %s", running.ID)
		}
	case <-ctx.Done():
		t.Fatal("active handler did not start")
	}
	pending := trigger("pending")
	app, err := newApp("127.0.0.1:0", "test-token", tenant, repo)
	if err != nil {
		t.Fatal(err)
	}
	const route = "/schedules/nightly"
	w := auditHTTPRequest(ctx, app, http.MethodPatch, route, "", "", `{"version":1,"claim_paused":true,"cancel_running":true}`)
	var result struct {
		Data pauseResult `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Data.Version != 2 ||
		result.Data.Canceled == nil || *result.Data.Canceled != 1 {
		t.Fatalf("cancel running: %d %s", w.Code, w.Body)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("running task did not receive cancellation")
	}
	if err := repo.recordResult(ctx, running); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("revoked task committed: %v", err)
	}
	for _, want := range []struct {
		id, status string
		ready      bool
		attempts   int
	}{{active, "canceled", false, 1}, {committed, "leased", true, 1}, {pending, "queued", false, 0}} {
		status, err := repo.runStatus(ctx, tenant, want.id)
		if err != nil || status.QueueStatus != want.status || status.ResultReady != want.ready || status.ClaimAttempts != want.attempts {
			t.Fatalf("run status for %s = %+v, error %v", want.id, status, err)
		}
	}
	w = auditHTTPRequest(ctx, app, http.MethodPatch, route, "", "", `{"version":1,"cancel_running":true}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale cancellation %d %s", w.Code, w.Body)
	}
}

func TestPostgresPrefetchedClaimRespectsPauseAtExecution(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const tenant, schedule = "tenant", "prefetch"
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: tenant, ID: schedule, Expression: "@every 1h",
		TimeZone: "UTC", Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	id, err := repo.triggerSchedule(ctx, tenant, schedule, "prefetch")
	if err != nil {
		t.Fatal(err)
	}
	pauseResult := make(chan error, 1)
	worker := mq.NewWorker(queue, mq.WorkerConfig{
		Concurrency: 1, MaxInflight: 1, LeaseDuration: 2 * time.Second,
		BeginExecution: func(ctx context.Context, task mq.Task) (int, error) {
			paused := true
			_, err := repo.setSchedulePause(ctx, tenant, schedule, pauseCommand{Version: 1, ClaimPaused: &paused})
			if err != nil {
				return 0, err
			}
			n, err := repo.beginExecution(ctx, task)
			pauseResult <- err
			return n, err
		},
	})
	worker.Register("demo.record", func(context.Context, mq.Task) error {
		t.Error("paused prefetch reached its handler")
		return nil
	})
	worker.Start(ctx)
	defer func() {
		if err := worker.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	select {
	case err := <-pauseResult:
		if !errors.Is(err, mq.ErrClaimPaused) {
			t.Fatalf("prefetch start returned %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no prefetch start attempt")
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.runStatus(ctx, tenant, id)
		return status.QueueStatus == "queued" && status.ExecutionAttempts == 0 && status.ClaimAttempts == 1, err
	})
}
