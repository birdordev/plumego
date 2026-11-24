package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func scrapeScheduler(t *testing.T, metrics *schedulerMetrics, expected ...string) string {
	t.Helper()
	w := httptest.NewRecorder()
	metrics.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()
	for _, line := range expected {
		if !strings.Contains(body, "\n"+line+"\n") {
			t.Errorf("missing metric %q in %s", line, body)
		}
	}
	return body
}

func TestPostgresSchedulerSLOIdleBacklogAndStorageFailure(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	metrics := &schedulerMetrics{readSLO: repo.readSLO}
	scrapeScheduler(t, metrics,
		"plumego_scheduler_storage_probe_success 1",
		"plumego_scheduler_oldest_due_schedule_seconds 0",
		"plumego_scheduler_oldest_runnable_seconds 0",
		"plumego_scheduler_dead_letters_5m 0")

	payload := json.RawMessage(`{}`)
	for _, id := range []string{"due", "claim-paused", "generation-paused"} {
		if err := repo.addSchedule(ctx, scheduleDefinition{ID: id, Tenant: "tenant", Topic: recordTaskType,
			Expression: "@hourly", TimeZone: "UTC", Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	blocked, err := repo.triggerSchedule(ctx, "tenant", "claim-paused", "paused-key")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE schedules SET next_fire_at = statement_timestamp() - interval '12 seconds' WHERE id = 'due'")
	exec("UPDATE schedules SET claim_paused = true WHERE id = 'claim-paused'")
	exec("UPDATE schedules SET paused = true, next_fire_at = statement_timestamp() - interval '1 hour' WHERE id = 'generation-paused'")
	exec("UPDATE mq_tasks SET available_at = statement_timestamp() - interval '1 hour' WHERE id = $1", blocked)

	submit := func(key string) string {
		t.Helper()
		id, err := repo.submit(ctx, "tenant", key, submission{Topic: recordTaskType, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	for i := range 11 {
		id, err := repo.submit(ctx, "tenant", fmt.Sprintf("dead-%d", i), submission{
			Topic: recordTaskType, Payload: json.RawMessage(`{"message":"sample"}`), taskOptions: taskOptions{TaskVersion: 2}})
		if err != nil {
			t.Fatal(err)
		}
		claims, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "failed", Limit: 1, Topics: []string{"demo.record.v2"}})
		if err != nil || len(claims) != 1 || claims[0].ID != id {
			t.Fatalf("claim %+v: %v", claims, err)
		}
		if err := queue.MoveToDLQ(ctx, id, claims[0].LeaseOwner, "permanent task failure", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	expired := submit("expired")
	exec("UPDATE mq_tasks SET available_at = statement_timestamp() + interval '1 hour' WHERE id = $1", expired)
	future := submit("future")
	exec("UPDATE mq_tasks SET available_at = statement_timestamp() + interval '1 hour' WHERE id = $1", future)

	reclaim := submit("reclaim")
	claims, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "lost-owner", Limit: 1, Lease: time.Minute})
	if err != nil || len(claims) != 1 || claims[0].ID != reclaim {
		t.Fatalf("reclaim seed %+v: %v", claims, err)
	}
	exec("UPDATE mq_tasks SET available_at = statement_timestamp() - interval '1 hour', lease_until = statement_timestamp() - interval '80 seconds' WHERE id = $1", reclaim)
	exec("UPDATE mq_tasks SET available_at = statement_timestamp() - interval '1 hour', expires_at = statement_timestamp() - interval '2 seconds' WHERE id = $1", expired)
	ready := submit("ready")
	exec("UPDATE mq_tasks SET available_at = statement_timestamp() - interval '25 seconds' WHERE id = $1", ready)

	state, err := repo.readSLO(ctx)
	if err != nil || state.overdue < 12 || state.overdue > 60 || state.runnable < 80 || state.runnable > 130 || state.recentDeadLetters != 11 {
		t.Fatalf("backlog state %+v: %v", state, err)
	}
	scrapeScheduler(t, metrics, "plumego_scheduler_storage_probe_success 1", "plumego_scheduler_dead_letters_5m 11")

	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.ExecContext(ctx, "LOCK TABLE scheduler_dead_letters IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	body := scrapeScheduler(t, metrics, "plumego_scheduler_storage_probe_success 0")
	if strings.Contains(body, "plumego_scheduler_oldest_runnable_seconds") || strings.Contains(body, "plumego_scheduler_dead_letters_5m") {
		t.Fatalf("failed storage scrape emitted backlog samples: %s", body)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	scrapeScheduler(t, metrics, "plumego_scheduler_storage_probe_success 1", "plumego_scheduler_dead_letters_5m 11")
}

func TestPostgresSchedulerSLOFailedWrites(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "slo-failures", submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "failing-worker", Limit: 1})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims %+v: %v", claims, err)
	}
	owner := claims[0].LeaseOwner
	metrics := &schedulerMetrics{}
	store := &journalQueue{TaskStore: repo.queue, repo: repo, metrics: metrics}
	if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks ADD CONSTRAINT reject_slo_writes CHECK (false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if err := store.ExtendLease(ctx, id, owner, time.Minute, time.Now()); err == nil {
		t.Fatal("lease write unexpectedly succeeded")
	}
	if err := store.Ack(ctx, id, owner, time.Now()); err == nil {
		t.Fatal("ack write unexpectedly succeeded")
	}
	scrapeScheduler(t, metrics,
		"plumego_scheduler_lease_renewal_failures_total 1",
		"plumego_scheduler_finalization_failures_total 1")
}
