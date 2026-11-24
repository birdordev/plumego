package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/jackc/pgx/v5"
)

func queueListenerConfig(t *testing.T, db *sql.DB) *pgx.ConnConfig {
	t.Helper()
	cfg, err := pgx.ParseConfig(os.Getenv("PLUMEGO_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRowContext(t.Context(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize()
	cfg.RuntimeParams["application_name"] = schema
	return cfg
}

func startQueueTestListener(t *testing.T, cfg *pgx.ConnConfig) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		listenQueueNotifications(ctx, cfg, wake)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	awaitQueueWake(t, wake)
	return wake
}

func awaitQueueWake(t *testing.T, wake <-chan struct{}) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for PostgreSQL queue notification")
	}
}

func TestPostgresQueueNotificationsAfterCommitAndReconnect(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	cfg := queueListenerConfig(t, db)
	wake := startQueueTestListener(t, cfg)
	queue := mqstore.NewSQL(db, mqstore.DefaultSQLConfig())
	for _, shouldCommit := range []bool{false, true} {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		now := time.Now()
		task := mq.Task{ID: "tx-test", Topic: "demo.record", TenantID: "tenant", AvailableAt: now,
			CreatedAt: now, UpdatedAt: now, MaxAttempts: 3}
		if err := queue.InsertTx(ctx, tx, task); err != nil {
			t.Fatal(err)
		}
		select {
		case <-wake:
			t.Fatal("notification arrived ahead of commit")
		case <-time.After(60 * time.Millisecond):
		}
		if !shouldCommit {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		awaitQueueWake(t, wake)
	}
	var terminated bool
	if err := db.QueryRowContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE application_name = $1`, cfg.RuntimeParams["application_name"]).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate test listener: %t %v", terminated, err)
	}
	awaitQueueWake(t, wake)
	repo := repository{db, queue}
	if _, err := repo.submit(ctx, "tenant", "after-reconnect", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	awaitQueueWake(t, wake)
}

func TestPostgresQueueNextAvailability(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	queue := mqstore.NewSQL(db, mqstore.DefaultSQLConfig())
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.ExecContext(ctx, `INSERT INTO mq_tasks
		(id, topic, tenant_id, priority, status, attempts, max_attempts, available_at, expires_at, lease_owner, lease_until, created_at, updated_at)
		VALUES
		('future', 'demo.record', 't', 0, 'queued', 0, 3, $1::timestamptz + interval '4 seconds', NULL, NULL, NULL, $1, $1),
		('lease', 'demo.record', 't', 0, 'leased', 1, 3, $1, NULL, 'worker', $1::timestamptz + interval '2 seconds', $1, $1),
		('ttl', 'demo.record', 't', 0, 'queued', 0, 3, $1::timestamptz + interval '1 second', $1, NULL, NULL, $1, $1),
		('other', 'other', 't', 0, 'queued', 0, 3, $1::timestamptz + interval '0.5 seconds', NULL, NULL, NULL, $1, $1)`, now); err != nil {
		t.Fatal(err)
	}
	for _, start := range []struct {
		topic       string
		after, want time.Time
	}{
		{"demo.record", now, now.Add(2*time.Second + time.Microsecond)},
		{"demo.record", now.Add(3 * time.Second), now.Add(4 * time.Second)},
		{"other", now, now.Add(500 * time.Millisecond)},
		{"missing", now, time.Time{}},
	} {
		at, err := queue.NextAvailable(ctx, []string{start.topic}, start.after)
		if err != nil || !at.Equal(start.want) {
			t.Fatalf("topic %s: next=%s want=%s err=%v", start.topic, at, start.want, err)
		}
	}
}

func TestPostgresIdleWorkerFutureAndUnnotifiedTasks(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	cfg := mqstore.DefaultSQLConfig()
	cfg.DeferredExpiration = true
	queue := mqstore.NewSQL(db, cfg)
	repo := repository{db, queue}
	const idleMaximum = 600 * time.Millisecond
	handled := make(chan time.Time, 2)
	worker := mq.NewWorker(mq.NewTaskQueue(queue), mq.WorkerConfig{
		Concurrency: 1, PollInterval: 20 * time.Millisecond, MaxIdlePollInterval: idleMaximum,
	})
	worker.Register("demo.record", func(context.Context, mq.Task) error {
		handled <- time.Now()
		return nil
	})
	scheduled := time.Now().Add(450 * time.Millisecond)
	if _, err := repo.submit(ctx, "tenant", "future-wake", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`), RunAt: scheduled}); err != nil {
		t.Fatal(err)
	}
	worker.Start(ctx)
	defer func() {
		if err := worker.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	awaitTask := func() time.Time {
		t.Helper()
		select {
		case at := <-handled:
			return at
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not recover through periodic polling")
			return time.Time{}
		}
	}
	if lag := awaitTask().Sub(scheduled); lag < -time.Microsecond || lag > 250*time.Millisecond {
		t.Fatalf("future task lag outside polling hint budget: %s", lag)
	}
	select {
	case <-time.After(idleMaximum):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	committing := time.Now()
	if _, err := repo.submit(ctx, "tenant", "unnotified", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if lag := awaitTask().Sub(committing); lag > idleMaximum+250*time.Millisecond {
		t.Fatalf("periodic probe exceeded fallback interval: %s", lag)
	}
}
