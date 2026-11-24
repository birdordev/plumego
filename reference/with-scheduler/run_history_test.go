package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresScheduleRunAndAttemptHistory(t *testing.T) {
	first, second, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const tenant, schedule = "tenant", "nightly"
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: tenant, ID: schedule, Expression: "@every 1m",
		TimeZone: "UTC", Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = $1 WHERE tenant_id = $2 AND id = $3", at, tenant, schedule); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 2)
	for i := range ids {
		id, err := repo.materializeNext(ctx)
		if err != nil || id == "" {
			t.Fatalf("materialize %d: id=%q err=%v", i, id, err)
		}
		ids[i] = id
	}
	reserve := func(q *mq.TaskQueue, id, worker string) mq.Task {
		t.Helper()
		rows, err := q.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: worker, Limit: 1})
		if err != nil || len(rows) != 1 || rows[0].ID != id {
			t.Fatalf("claim %s: count=%d error=%v", id, len(rows), err)
		}
		return rows[0]
	}
	a := reserve(first, ids[0], "worker-a")
	b := reserve(second, ids[1], "worker-b")

	app, err := newApp("127.0.0.1:0", "test-token", tenant, repo)
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string, dest any) string {
		t.Helper()
		w := auditHTTPRequest(ctx, app, http.MethodGet, path, "", "", "")
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(envelope.Data, dest); err != nil {
			t.Fatal(err)
		}
		return w.Body.String()
	}
	lookup := "/schedules/" + schedule + "/occurrence"
	for i, id := range ids {
		scheduled := at.Add(time.Duration(i) * time.Minute)
		var occurrence plannedOccurrence
		path := lookup + "?scheduled_at=" + url.QueryEscape(scheduled.In(time.FixedZone("offset", 3600)).Format(time.RFC3339))
		read(path, &occurrence)
		if occurrence.RunID != id || occurrence.ScheduleID != schedule || occurrence.ScheduleVersion != 1 || !occurrence.ScheduledAt.Equal(scheduled) {
			t.Fatalf("occurrence %d: %+v", i, occurrence)
		}
		status, err := repo.runStatus(ctx, tenant, id)
		if err != nil || status.QueueStatus != mq.TaskStatusLeased {
			t.Fatalf("run %d must be leased concurrently: %+v %v", i, status, err)
		}
	}
	for _, suffix := range []string{"", "?scheduled_at=2026-01-01T00:00:00.000000123Z", "?scheduled_at=invalid&scheduled_at=invalid"} {
		if w := auditHTTPRequest(ctx, app, http.MethodGet, lookup+suffix, "", "", ""); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid schedule time: %d %s", w.Code, w.Body)
		}
	}
	pathA := "/runs/" + a.ID + "/attempts"
	var page attemptPage
	read(pathA, &page)
	if len(page.Items) != 1 || page.Items[0].Number != 1 || page.Items[0].ClaimedAt.IsZero() ||
		page.Items[0].FinishedAt != nil || page.Items[0].Success != nil {
		t.Fatalf("active attempt: %+v", page)
	}
	if err := first.Release(ctx, a.ID, a.LeaseOwner, mq.ReleaseOptions{Reason: "private-error-marker"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.recordResult(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := second.Ack(ctx, b.ID, b.LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	retry := reserve(first, a.ID, "worker-a")
	if err := first.MoveToDLQ(ctx, retry.ID, retry.LeaseOwner, "permanent task failure", time.Now()); err != nil {
		t.Fatal(err)
	}
	page = attemptPage{}
	raw := read(pathA, &page)
	if len(page.Items) != 2 || page.Items[0].Number != 2 || page.Items[0].FailureCode != "permanent" ||
		page.Items[1].Number != 1 || page.Items[1].FailureCode != "task_failed" || strings.Contains(raw, "private-error-marker") {
		t.Fatalf("failed run attempts: %s", raw)
	}
	page = attemptPage{}
	read("/runs/"+b.ID+"/attempts", &page)
	if len(page.Items) != 1 || page.Items[0].Number != 1 || page.Items[0].Success == nil || !*page.Items[0].Success ||
		page.Items[0].FailureCode != "" {
		t.Fatalf("successful run attempts: %+v", page)
	}
	for id, expected := range map[string]string{a.ID: mq.TaskStatusDead, b.ID: mq.TaskStatusDone} {
		status, err := repo.runStatus(ctx, tenant, id)
		if err != nil || status.QueueStatus != expected {
			t.Fatalf("run %s status = %+v %v", id, status, err)
		}
	}
	other, err := newApp("127.0.0.1:0", "test-token", "other-tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{pathA, lookup + "?scheduled_at=" + at.Format(time.RFC3339)} {
		if w := auditHTTPRequest(ctx, other, http.MethodGet, path, "", "", ""); w.Code != http.StatusNotFound {
			t.Fatalf("foreign history: %d %s", w.Code, w.Body)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO mq_task_attempts (task_id, attempt_no, started_at)
		SELECT $1, n, clock_timestamp() FROM generate_series(3, 52) n`, a.ID); err != nil {
		t.Fatal(err)
	}
	page = attemptPage{}
	read(pathA, &page)
	if len(page.Items) != attemptPageSize || page.NextBefore != 3 {
		t.Fatalf("unexpected history cursor: size=%d next=%d", len(page.Items), page.NextBefore)
	}
	before := page.NextBefore
	page = attemptPage{}
	read(pathA+"?before="+strconv.Itoa(before), &page)
	if len(page.Items) != 2 || page.Items[0].Number != 2 || page.Items[1].Number != 1 || page.NextBefore != 0 {
		t.Fatalf("unexpected final history page: %+v", page)
	}
}

func TestPostgresAttemptHistoryIsAtomicWithQueueState(t *testing.T) {
	first, second, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "attempt-atomicity", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	assertConstraint := func(err error) {
		t.Helper()
		var sqlErr *pgconn.PgError
		if !errors.As(err, &sqlErr) || sqlErr.Code != "23514" {
			t.Fatalf("expected attempt write constraint, got %v", err)
		}
	}
	exec("ALTER TABLE mq_task_attempts ADD CONSTRAINT reject_history CHECK (false) NOT VALID")
	opts := mq.ReserveOptions{ConsumerID: "worker", Limit: 1}
	_, err = first.ReserveFenced(ctx, opts)
	assertConstraint(err)
	var status string
	var count int
	if err := db.QueryRowContext(ctx, "SELECT status, attempts FROM mq_tasks WHERE id = $1", id).Scan(&status, &count); err != nil ||
		status != mq.TaskStatusQueued || count != 0 {
		t.Fatalf("failed claim did not roll back: status=%s attempts=%d err=%v", status, count, err)
	}
	exec("ALTER TABLE mq_task_attempts DROP CONSTRAINT reject_history")
	if claimed, err := first.ReserveFenced(ctx, opts); err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 {
		t.Fatalf("initial claim: count=%d err=%v", len(claimed), err)
	}
	exec("UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", id)
	claimed, err := second.ReserveFenced(ctx, opts)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("takeover claim: count=%d err=%v", len(claimed), err)
	}
	exec("ALTER TABLE mq_task_attempts ADD CONSTRAINT reject_history CHECK (finished_at IS NULL) NOT VALID")
	assertConstraint(second.Ack(ctx, id, claimed[0].LeaseOwner, time.Now()))
	run, err := repo.runStatus(ctx, "tenant", id)
	if err != nil || run.QueueStatus != mq.TaskStatusLeased {
		t.Fatalf("failed ACK did not roll back: %+v %v", run, err)
	}
	exec("ALTER TABLE mq_task_attempts DROP CONSTRAINT reject_history")
	if err := second.Ack(ctx, id, claimed[0].LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, err := repo.runAttempts(ctx, "tenant", id, 0)
	if err != nil || len(page.Items) != 2 || page.Items[0].Success == nil || !*page.Items[0].Success ||
		page.Items[1].Number != 1 || page.Items[1].FinishedAt == nil || page.Items[1].FailureCode != "lease_expired" {
		t.Fatalf("takeover history: %+v %v", page, err)
	}
}
