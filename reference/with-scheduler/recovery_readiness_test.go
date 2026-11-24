package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresRecoveryReadiness(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	check := func(status int, reason string) {
		t.Helper()
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		body := response.Body.String()
		if response.Code != status || !strings.Contains(body, reason) || strings.Contains(body, "private-topic") {
			t.Fatalf("readiness = %d %s, want %d %q", response.Code, body, status, reason)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	submit := func() string {
		t.Helper()
		id, err := repo.submit(ctx, "tenant", "recovery-check", submission{
			Topic: recordTaskType, Payload: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	check(http.StatusOK, "ready")
	id := submit()
	exec("UPDATE mq_tasks SET topic = 'private-topic' WHERE id = $1", id)
	check(http.StatusServiceUnavailable, "scheduler task handler is unregistered")
	exec("UPDATE mq_tasks SET topic = $2 WHERE id = $1", id, recordTaskType)
	check(http.StatusOK, "ready")

	now := time.Now()
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{
		Topics: []string{recordTaskType}, Limit: 1, Lease: time.Minute, ConsumerID: "test", Now: now,
	})
	if err != nil || len(tasks) != 1 || tasks[0].ID != id {
		t.Fatalf("reserve: %v %v", tasks, err)
	}
	journal := journalQueue{TaskStore: repo.queue, repo: repo}
	injected := errors.New("injected unavailable finalization write")
	err = journal.finalize(ctx, id, tasks[0].LeaseOwner, "release", "", now, func() error { return injected })
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	check(http.StatusOK, "ready")
	exec(`UPDATE mq_task_attempts SET finalization_recorded_at = NULL,
		finalization_check_at = statement_timestamp() WHERE task_id = $1`, id)
	check(http.StatusServiceUnavailable, "scheduler recovery record requires isolation")
	if got, err := repo.reconcileFinalization(ctx); err != nil || got != "" {
		t.Fatalf("invalid intent was not isolated: %q %v", got, err)
	}
	exec(`UPDATE mq_task_attempts SET finalization_recorded_at = statement_timestamp() - interval '1 minute',
		finalization_check_at = statement_timestamp() WHERE task_id = $1`, id)
	check(http.StatusServiceUnavailable, "scheduler recovery deadline exceeded")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SELECT id FROM mq_tasks WHERE id = $1 FOR UPDATE", id); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.reconcileFinalization(ctx); err != nil || got != "" {
		t.Fatalf("locked recovery = %q %v", got, err)
	}
	check(http.StatusServiceUnavailable, "scheduler recovery deadline exceeded")
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.reconcileFinalization(ctx); err != nil || got != id {
		t.Fatalf("recovery = %q %v", got, err)
	}
	check(http.StatusOK, "ready")

	exec("ALTER TABLE mq_task_attempts RENAME TO unavailable_attempts")
	check(http.StatusServiceUnavailable, "scheduler recovery storage unavailable")
	exec("ALTER TABLE unavailable_attempts RENAME TO mq_task_attempts")
	t.Run("bounded storage lock", func(t *testing.T) {
		lock, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lock.Rollback() }()
		if _, err := lock.ExecContext(ctx, "LOCK TABLE mq_task_attempts IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		check(http.StatusServiceUnavailable, "scheduler recovery storage unavailable")
		if elapsed := time.Since(start); elapsed > 3*readinessTimeout {
			t.Fatalf("probe exceeded its deadline: %v", elapsed)
		}
	})
	check(http.StatusOK, "ready")
	exec("UPDATE scheduler_schema SET version = version - 1")
	check(http.StatusServiceUnavailable, "scheduler schema or storage unavailable")
	exec("UPDATE scheduler_schema SET version = version + 1")
	check(http.StatusOK, "ready")
	exec("INSERT INTO scheduler_schema (version) VALUES ($1)", schemaVersion+1)
	check(http.StatusServiceUnavailable, "scheduler schema or storage unavailable")
}
