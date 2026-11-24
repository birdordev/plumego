package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresTenantCapacityUnderConcurrentSubmission(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if _, err := db.ExecContext(ctx, `INSERT INTO scheduler_tenant_usage
		(tenant_id, max_pending_runs, max_pending_bytes, max_schedules)
		VALUES ('test', 3, 4, 1)`); err != nil {
		t.Fatal(err)
	}
	input := submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}
	type result struct {
		key, id string
		err     error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for n := range cap(results) {
		wg.Go(func() {
			key := fmt.Sprint(n)
			id, err := repo.submit(ctx, "test", key, input)
			results <- result{key, id, err}
		})
	}
	wg.Wait()
	close(results)
	var accepted []result
	for result := range results {
		if result.err == nil {
			accepted = append(accepted, result)
		} else if !errors.Is(result.err, errTenantCapacity) || result.id != "" {
			t.Fatalf("unhandled result: %+v", result)
		}
	}
	if len(accepted) != 2 {
		t.Fatalf("accepted = %+v, want two two-byte tasks", accepted)
	}
	replayed, err := repo.submit(ctx, "test", accepted[0].key, input)
	if err != nil || replayed != accepted[0].id {
		t.Fatalf("replay beyond limit = %q %v", replayed, err)
	}
	if err := repo.cancelRun(ctx, "test", accepted[0].id); err != nil {
		t.Fatal(err)
	}
	third, err := repo.submit(ctx, "test", "after-cancel", input)
	if err != nil || third == "" {
		t.Fatalf("cancel did not release capacity: %q %v", third, err)
	}
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "worker", Limit: 1})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("reserve: %+v %v", tasks, err)
	}
	if err := queue.Ack(ctx, tasks[0].ID, tasks[0].LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	id, err := repo.submit(ctx, "test", "after-ack", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET expires_at = clock_timestamp() - interval '1s' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "worker", Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.submit(ctx, "test", "after-expiry", input); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.submit(ctx, "other", "own-capacity", input); err != nil {
		t.Fatal(err)
	}

	d := scheduleDefinition{Tenant: "test", ID: "plan", Expression: "@every 1m", TimeZone: "UTC", Topic: input.Topic, Payload: input.Payload}
	if err := repo.addSchedule(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.triggerSchedule(ctx, d.Tenant, d.ID, "trigger-above-limit"); !errors.Is(err, errTenantCapacity) {
		t.Fatalf("manual trigger exceeded limit: %v", err)
	}
	d.ID = "extra"
	if err := repo.addSchedule(ctx, d); !errors.Is(err, errTenantCapacity) {
		t.Fatalf("schedule exceeded limit: %v", err)
	}
	var runs, bytes, schedules, actualRuns, actualBytes, totalAccepted int
	if err := db.QueryRowContext(ctx, `SELECT pending_runs, pending_bytes, schedule_count,
		(SELECT count(*) FROM mq_tasks WHERE tenant_id = 'test' AND status IN ('queued','leased')),
		(SELECT sum(octet_length(payload)) FROM mq_tasks WHERE tenant_id = 'test' AND status IN ('queued','leased')),
		(SELECT count(*) FROM scheduled_runs WHERE tenant_id = 'test')
		FROM scheduler_tenant_usage WHERE tenant_id = 'test'`).
		Scan(&runs, &bytes, &schedules, &actualRuns, &actualBytes, &totalAccepted); err != nil {
		t.Fatal(err)
	}
	if runs != actualRuns || bytes != actualBytes || schedules != 1 || totalAccepted != 5 {
		t.Fatalf("usage %d/%d, actual %d/%d, schedules=%d accepted=%d", runs, bytes, actualRuns, actualBytes, schedules, totalAccepted)
	}
}

func TestPostgresFullTenantDoesNotBlockOtherSchedules(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if _, err := db.ExecContext(ctx, "INSERT INTO scheduler_tenant_usage (tenant_id, max_pending_runs) VALUES ('full', 0)"); err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"full", "available"} {
		if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: tenant, ID: "schedule", Expression: "@every 1m", TimeZone: "UTC",
			Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE schedules SET next_fire_at = statement_timestamp() -
		CASE WHEN tenant_id = 'full' THEN interval '2s' ELSE interval '1s' END`); err != nil {
		t.Fatal(err)
	}
	id, err := repo.materializeNext(ctx)
	if err != nil || id == "" {
		t.Fatalf("available tenant blocked: %q %v", id, err)
	}
	if found, err := repo.runStatus(ctx, "available", id); err != nil || found.ID != id {
		t.Fatalf("materialized wrong tenant: %+v %v", found, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE scheduler_tenant_usage SET max_pending_runs = 1 WHERE tenant_id = 'full'"); err != nil {
		t.Fatal(err)
	}
	id, err = repo.materializeNext(ctx)
	if err != nil || id == "" {
		t.Fatalf("unblocked tenant did not resume: %q %v", id, err)
	}
	if found, err := repo.runStatus(ctx, "full", id); err != nil || found.ID != id {
		t.Fatalf("materialized wrong tenant: %+v %v", found, err)
	}
}

func TestPostgresHTTPAdmissionRejectsOnlyNewCommands(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	if _, err := db.ExecContext(t.Context(), "INSERT INTO scheduler_tenant_usage (tenant_id, max_pending_runs) VALUES ('tenant', 1)"); err != nil {
		t.Fatal(err)
	}
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("127.0.0.1:0", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	post := func(key string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), "POST", "/runs",
			strings.NewReader(`{"topic":"demo.record","payload":{}}`))
		r.Header.Set("Authorization", "Bearer test-token")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		app.ServeHTTP(w, r)
		return w
	}
	first := post("first")
	if first.Code != 202 || first.Header().Get("Location") == "" {
		t.Fatalf("first submission: %d %s", first.Code, first.Body)
	}
	rejected := post("fresh")
	if rejected.Code != 429 || rejected.Header().Get("Retry-After") == "" || rejected.Header().Get("Location") != "" {
		t.Fatalf("capacity response: %d %s %v", rejected.Code, rejected.Body, rejected.Header())
	}
	replay := post("first")
	if replay.Code != 202 || replay.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatalf("replay at limit: %d %s", replay.Code, replay.Body)
	}
}
