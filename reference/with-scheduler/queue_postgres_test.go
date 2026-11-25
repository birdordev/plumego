package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func postgresTestDatabases(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("PLUMEGO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PLUMEGO_TEST_POSTGRES_DSN is unset")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	schema := pgx.Identifier{"scheduler_test_" + rand.Text()}
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema.Sanitize() + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.RuntimeParams["search_path"] = schema.Sanitize()
	first, second := stdlib.OpenDB(*cfg), stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		if err := errors.Join(first.Close(), second.Close()); err != nil {
			t.Error(err)
		}
	})
	return first, second
}

func postgresTestQueues(t *testing.T) (*mq.TaskQueue, *mq.TaskQueue, *sql.DB) {
	t.Helper()
	first, second := postgresTestDatabases(t)
	if err := migrate(t.Context(), first, "../../x/messaging/mq/store/migrations/postgres/001_task_queue.sql"); err != nil {
		t.Fatal(err)
	}
	sqlCfg := mqstore.DefaultSQLConfig()
	sqlCfg.BlockedClaimsTable = "scheduler_blocked_claims"
	return mq.NewTaskQueue(mqstore.NewSQL(first, sqlCfg)), mq.NewTaskQueue(mqstore.NewSQL(second, sqlCfg)), first
}

func awaitSchedulerState(t *testing.T, check func(context.Context) (bool, error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := check(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("did not observe scheduler state: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPostgresCompetingClaimsAndExpiry(t *testing.T) {
	first, second, db := postgresTestQueues(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, id := range []string{"claim", "ttl"} {
		if err := first.Enqueue(ctx, mq.Task{ID: id, Topic: id, TenantID: "tenant", Payload: []byte(id), Meta: map[string]string{"k": "v"}},
			mq.EnqueueOptions{AvailableAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	opts := mq.ReserveOptions{Topics: []string{"claim"}, Limit: 1, Lease: time.Second, ConsumerID: "same-worker", Now: now}
	var wg sync.WaitGroup
	claims := make(chan mq.Task, 2)
	for _, q := range []*mq.TaskQueue{first, second} {
		wg.Go(func() {
			rows, err := q.ReserveFenced(ctx, opts)
			if err != nil {
				t.Error(err)
				return
			}
			for _, task := range rows {
				claims <- task
			}
		})
	}
	wg.Wait()
	if len(claims) != 1 {
		t.Fatalf("got %d competing claims, want 1", len(claims))
	}
	old := <-claims
	opts.Now = now.Add(2 * time.Second)
	rows, err := second.ReserveFenced(ctx, opts)
	if err != nil || len(rows) != 1 || rows[0].Attempts != 2 || rows[0].Meta["k"] != "v" {
		t.Fatalf("second claim = %+v, %v", rows, err)
	}
	if err := first.Ack(ctx, old.ID, old.LeaseOwner, opts.Now); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("stale ACK = %v", err)
	}
	if err := second.Ack(ctx, rows[0].ID, rows[0].LeaseOwner, opts.Now); err != nil {
		t.Fatal(err)
	}
	opts.Topics = []string{"ttl"}
	rows, err = first.ReserveFenced(ctx, opts)
	if err != nil || len(rows) != 1 {
		t.Fatalf("TTL claim = %+v, %v", rows, err)
	}
	opts.Now = now.Add(2 * time.Hour)
	rows, err = second.ReserveFenced(ctx, opts)
	if err != nil || len(rows) != 0 {
		t.Fatalf("expired TTL claim = %+v, %v", rows, err)
	}
	var status string
	var owner, until sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT status, lease_owner, lease_until::text FROM mq_tasks WHERE id='ttl'").Scan(&status, &owner, &until); err != nil {
		t.Fatal(err)
	}
	if status != mq.TaskStatusExpired || owner.Valid || until.Valid {
		t.Fatalf("expired row state = %s %v %v", status, owner, until)
	}
}

func TestPostgresAtomicTaskSubmission(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	input := submission{Topic: "demo.record", Payload: json.RawMessage(`{"message":"sample"}`)}
	ids := make(chan string, 8)
	var wg sync.WaitGroup
	for range cap(ids) {
		wg.Go(func() {
			id, err := repo.submit(ctx, "tenant-a", "request", input)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id
		})
	}
	wg.Wait()
	close(ids)
	var id string
	for got := range ids {
		if id != "" && id != got {
			t.Errorf("multiple submissions: %s and %s", id, got)
		}
		id = got
	}
	if id == "" {
		t.Fatal("no submission committed")
	}
	input.Payload = json.RawMessage(`{"message":"different"}`)
	if _, err := repo.submit(ctx, "tenant-a", "request", input); !errors.Is(err, errRequestConflict) {
		t.Fatalf("fingerprint conflict: %v", err)
	}
	var queueCount, runCount int
	if err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM mq_tasks), (SELECT COUNT(*) FROM scheduled_runs)").Scan(&queueCount, &runCount); err != nil {
		t.Fatal(err)
	}
	if queueCount != 1 || runCount != 1 {
		t.Fatalf("queue=%d runs=%d", queueCount, runCount)
	}
}

func TestPostgresCronCursorRollbackAndCompetition(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if err := repo.addSchedule(ctx, scheduleDefinition{
		Tenant: "tenant-a", ID: "hourly", Expression: "@hourly", TimeZone: "UTC", Topic: "demo.record", Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	var due time.Time
	if err := db.QueryRowContext(ctx, `UPDATE schedules SET next_fire_at = clock_timestamp() - INTERVAL '1 second'
		WHERE tenant_id = 'tenant-a' AND id = 'hourly' RETURNING next_fire_at`).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks ADD CONSTRAINT inject_failure CHECK (topic <> 'demo.record')"); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.materializeNext(ctx); err == nil || id != "" {
		t.Fatalf("failed enqueue returned %q, %v", id, err)
	}
	var runs int
	var cursor time.Time
	if err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM scheduled_runs), next_fire_at FROM schedules").Scan(&runs, &cursor); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || !cursor.Equal(due) {
		t.Fatalf("failed transaction persisted run or cursor: %d %v", runs, cursor)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks DROP CONSTRAINT inject_failure"); err != nil {
		t.Fatal(err)
	}
	ids := make(chan string, 2)
	var wg sync.WaitGroup
	for range cap(ids) {
		wg.Go(func() {
			id, err := repo.materializeNext(ctx)
			if err != nil {
				t.Error(err)
			}
			if id != "" {
				ids <- id
			}
		})
	}
	wg.Wait()
	if len(ids) != 1 {
		t.Fatalf("created %d occurrences", len(ids))
	}
	var queued int
	if err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM mq_tasks), next_fire_at FROM schedules").Scan(&queued, &cursor); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || !cursor.After(due) {
		t.Fatalf("committed queue count or cursor: %d %v", queued, cursor)
	}
}

func TestPostgresResultRequiresCurrentClaimAndIsIdempotent(t *testing.T) {
	first, second, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	id, err := repo.submit(ctx, "tenant", "command", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	opts := mq.ReserveOptions{ConsumerID: "worker", Limit: 1, Lease: time.Minute}
	old, err := first.ReserveFenced(ctx, opts)
	if err != nil || len(old) != 1 {
		t.Fatalf("initial claim = %+v %v", old, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until = clock_timestamp() - INTERVAL '1 second' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	current, err := second.ReserveFenced(ctx, opts)
	if err != nil || len(current) != 1 {
		t.Fatalf("replacement claim = %+v %v", current, err)
	}
	if err := repo.recordResult(ctx, old[0]); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("stale claim commit = %v", err)
	}
	wrongTenant := current[0]
	wrongTenant.TenantID = "other"
	if err := repo.recordResult(ctx, wrongTenant); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("cross-tenant claim commit = %v", err)
	}
	for range 2 {
		if err := repo.recordResult(ctx, current[0]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.runStatus(ctx, "other", id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant query = %v", err)
	}
	status, err := repo.runStatus(ctx, "tenant", id)
	if err != nil || !status.ResultReady || status.QueueStatus != mq.TaskStatusLeased {
		t.Fatalf("result before ACK = %+v %v", status, err)
	}
	if err := second.Ack(ctx, id, current[0].LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	status, err = repo.runStatus(ctx, "tenant", id)
	if err != nil || !status.ResultReady || status.QueueStatus != mq.TaskStatusDone {
		t.Fatalf("result after ACK = %+v %v", status, err)
	}
}

func TestPostgresFailoverRejectsFormerLease(t *testing.T) {
	dsn := os.Getenv("PLUMEGO_TEST_FAILOVER_DATABASE_URL")
	if dsn == "" {
		t.Skip("run by the disposable PostgreSQL failover drill")
	}
	task := mq.Task{ID: os.Getenv("PLUMEGO_TEST_FAILOVER_RUN_ID"), TenantID: "failover-drill",
		LeaseOwner: os.Getenv("PLUMEGO_TEST_FAILOVER_LEASE_OWNER")}
	if !runCursorID.MatchString(task.ID) || task.LeaseOwner == "" {
		t.Fatal("failover probe requires a run ID and the previous lease")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("could not open the promoted database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()

	var active, priorResult bool
	err = db.QueryRowContext(ctx, `SELECT NOT pg_is_in_recovery() AND status='leased'
		AND attempts=2 AND execution_attempts=2 AND lease_owner<>$2 AND lease_until>clock_timestamp(),
		EXISTS (SELECT 1 FROM scheduled_results s WHERE s.run_id=q.id)
		FROM mq_tasks q WHERE id=$1 AND tenant_id=$3`, task.ID, task.LeaseOwner, task.TenantID).Scan(&active, &priorResult)
	if err != nil || !active || priorResult {
		t.Fatal("the promoted writer must have an active replacement claim before returning a result")
	}
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if err := repo.recordResult(ctx, task); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatal("the production result transaction did not fence the former lease")
	}
}

func TestPostgresCancellationSerializesWithResult(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	for _, stage := range []string{"queued", "leased", "committed", "race"} {
		t.Run(stage, func(t *testing.T) {
			id, err := repo.submit(ctx, "tenant", stage, submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.cancelRun(ctx, "other", id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("cross-tenant cancellation = %v", err)
			}
			if stage == "queued" {
				if err := repo.cancelRun(ctx, "tenant", id); err != nil {
					t.Fatal(err)
				}
			}
			claims, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "worker", Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if stage == "queued" {
				if len(claims) != 0 {
					t.Fatal("canceled queued task was claimed")
				}
				return
			}
			if len(claims) != 1 || claims[0].ID != id {
				t.Fatalf("unexpected claims: %+v", claims)
			}
			claim := claims[0]
			if stage == "committed" {
				if err := repo.recordResult(ctx, claim); err != nil {
					t.Fatal(err)
				}
				if err := repo.cancelRun(ctx, "tenant", id); !errors.Is(err, errRunFinalized) {
					t.Fatalf("cancel after business commit = %v", err)
				}
				if err := queue.Ack(ctx, id, claim.LeaseOwner, time.Now()); err != nil {
					t.Fatal(err)
				}
				return
			}
			var cancelErr, resultErr error
			if stage == "leased" {
				cancelErr = repo.cancelRun(ctx, "tenant", id)
				resultErr = repo.recordResult(ctx, claim)
			} else {
				var wg sync.WaitGroup
				wg.Go(func() { cancelErr = repo.cancelRun(ctx, "tenant", id) })
				wg.Go(func() { resultErr = repo.recordResult(ctx, claim) })
				wg.Wait()
			}
			switch {
			case cancelErr == nil && errors.Is(resultErr, mq.ErrLeaseLost):
				if err := repo.cancelRun(ctx, "tenant", id); err != nil {
					t.Fatalf("repeat cancel = %v", err)
				}
				if err := queue.ExtendLease(ctx, id, claim.LeaseOwner, time.Minute, time.Now()); !errors.Is(err, mq.ErrLeaseLost) {
					t.Fatalf("canceled claim renewal = %v", err)
				}
			case stage == "race" && errors.Is(cancelErr, errRunFinalized) && resultErr == nil:
			default:
				t.Fatalf("%s cancel = %v, result = %v", stage, cancelErr, resultErr)
			}
		})
	}
}

func TestPostgresAtomicOutboxAndRecovery(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	id, err := repo.submit(ctx, "tenant", "event", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "worker", Limit: 1})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim = %+v %v", claims, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduled_outbox ADD CONSTRAINT inject_failure CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	if err := repo.recordResult(ctx, claims[0]); err == nil {
		t.Fatal("result commit succeeded despite outbox failure")
	}
	status, err := repo.runStatus(ctx, "tenant", id)
	if err != nil || status.ResultReady {
		t.Fatalf("rolled-back result = %+v %v", status, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduled_outbox DROP CONSTRAINT inject_failure"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repo.recordResult(ctx, claims[0]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduled_deliveries ADD CONSTRAINT inject_failure CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.deliverNext(ctx, localSchedulerRegion); err == nil {
		t.Fatal("failure did not abort delivery")
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduled_deliveries DROP CONSTRAINT inject_failure"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	delivered := make(chan string, 2)
	for range cap(delivered) {
		wg.Go(func() {
			got, err := repo.deliverNext(ctx, localSchedulerRegion)
			if err != nil {
				t.Error(err)
			}
			if got != "" {
				delivered <- got
			}
		})
	}
	wg.Wait()
	if len(delivered) != 1 || <-delivered != id {
		t.Fatal("expected one recovered delivery")
	}
	var recorded, pending int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM scheduled_deliveries),
		(SELECT COUNT(*) FROM scheduled_outbox WHERE delivered_at IS NULL)`).Scan(&recorded, &pending); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 || pending != 0 {
		t.Fatalf("recorded = %d, pending = %d", recorded, pending)
	}
}

func TestPostgresWorkerRecoversMissingAckAfterAttemptLimit(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	id, err := repo.submit(ctx, "tenant", "lost-ack", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET max_attempts = 1 WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "stopped", Limit: 1})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("first claim: %+v %v", tasks, err)
	}
	if err := repo.recordResult(ctx, tasks[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	worker := mq.NewWorker(queue, mq.WorkerConfig{Deduper: resultDeduper{db}, DedupeKeyFunc: func(task mq.Task) string { return task.ID }})
	worker.Register("demo.record", func(context.Context, mq.Task) error {
		t.Error("a committed result should not execute again")
		return errors.New("unexpected execution")
	})
	worker.Start(ctx)
	defer func() {
		if err := worker.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := repo.runStatus(deadline, "tenant", id)
		if err != nil {
			t.Fatal(err)
		}
		if status.QueueStatus == mq.TaskStatusDone {
			return
		}
		select {
		case <-deadline.Done():
			t.Fatalf("last state: %+v", status)
		case <-ticker.C:
		}
	}
}

func TestPostgresSubmissionRollbackAndRetry(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	input := submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}
	if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks ADD CONSTRAINT reject_enqueue CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.submit(ctx, "tenant", "key", input); id != "" || err == nil {
		t.Fatalf("failed write accepted: id=%s error=%v", id, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduled_runs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned runs: %d, %v", count, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks DROP CONSTRAINT reject_enqueue"); err != nil {
		t.Fatal(err)
	}
	// 丢弃第一份成功结果，模拟响应未到客户端。
	if _, err := repo.submit(ctx, "tenant", "key", input); err != nil {
		t.Fatal(err)
	}
	id, err := repo.submit(ctx, "tenant", "key", input)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := repo.runStatus(ctx, "tenant", id); err != nil || status.QueueStatus != "queued" {
		t.Fatalf("recovered submission: %+v %v", status, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM mq_tasks").Scan(&count); err != nil || count != 1 {
		t.Fatalf("queued count: %d, %v", count, err)
	}
}

func TestPostgresAcceptedBacklogSurvivesSaturatedWorker(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	started := make(chan struct{})
	continueWork := make(chan struct{})
	var once sync.Once
	w := mq.NewWorker(queue, mq.WorkerConfig{Concurrency: 1, MaxInflight: 1})
	w.Register("demo.record", func(ctx context.Context, task mq.Task) error {
		once.Do(func() { close(started) })
		select {
		case <-continueWork:
			return repo.recordResult(ctx, task)
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	w.Start(ctx)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := w.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	}()
	const runs = 24
	for i := range runs {
		if _, err := repo.submit(ctx, "tenant", fmt.Sprintf("load-%d", i), submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not claim task")
	}
	var queued int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM mq_tasks WHERE status = 'queued'").Scan(&queued); err != nil || queued != runs-1 {
		t.Fatalf("queued under saturation = %d: %v", queued, err)
	}
	close(continueWork)
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var done int
		if err := db.QueryRowContext(deadline, "SELECT COUNT(*) FROM mq_tasks WHERE status = 'done'").Scan(&done); err != nil {
			t.Fatal(err)
		}
		if done == runs {
			return
		}
		select {
		case <-deadline.Done():
			t.Fatalf("completed %d of %d accepted jobs", done, runs)
		case <-ticker.C:
		}
	}
}

func TestPostgresCancellationReachesRunningHandler(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	id, err := repo.submit(ctx, "tenant", "running", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}), make(chan error, 1)
	worker := mq.NewWorker(queue, mq.WorkerConfig{LeaseDuration: time.Second, LeaseExtendInterval: 20 * time.Millisecond})
	worker.Register("demo.record", func(ctx context.Context, _ mq.Task) error {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return ctx.Err()
	})
	worker.Start(context.WithoutCancel(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := worker.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("worker did not begin execution")
	}
	if err := repo.cancelRun(ctx, "tenant", id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("worker did not receive cancellation")
	}
	status, err := repo.runStatus(ctx, "tenant", id)
	if err != nil || status.QueueStatus != "canceled" || status.ResultReady {
		t.Fatalf("canceled run: %+v %v", status, err)
	}
}

func TestPostgresPauseResumeAndMisfirePolicies(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	for _, policy := range []string{"skip", "coalesce", "catch_up"} {
		t.Run(policy, func(t *testing.T) {
			d := scheduleDefinition{Tenant: "tenant", ID: policy, Expression: "@every 1m", TimeZone: "UTC",
				Topic: "demo.record", Payload: json.RawMessage(`{}`), MisfirePolicy: policy, CatchUpWindow: 120}
			if err := repo.addSchedule(ctx, d); err != nil {
				t.Fatal(err)
			}
			paused, running := true, false
			result, err := repo.setSchedulePause(ctx, d.Tenant, d.ID, pauseCommand{Version: 1, Paused: &paused})
			if err != nil || result.Version != 2 {
				t.Fatalf("pause version=%d err=%v", result.Version, err)
			}
			if _, err := repo.setSchedulePause(ctx, d.Tenant, d.ID, pauseCommand{Version: 1, Paused: &running}); !errors.Is(err, errScheduleVersion) {
				t.Fatalf("stale resume: %v", err)
			}
			if _, err := repo.setSchedulePause(ctx, "other", d.ID, pauseCommand{Version: 2, Paused: &running}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("cross-tenant resume: %v", err)
			}
			var anchor time.Time
			if err := db.QueryRowContext(ctx, `UPDATE schedules SET next_fire_at = clock_timestamp() - interval '5 minutes 10 seconds'
				WHERE id = $1 RETURNING next_fire_at`, d.ID).Scan(&anchor); err != nil {
				t.Fatal(err)
			}
			restarted := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
			if id, err := restarted.materializeNext(ctx); id != "" || err != nil {
				t.Fatalf("paused schedule was selected: %q %v", id, err)
			}
			result, err = restarted.setSchedulePause(ctx, d.Tenant, d.ID, pauseCommand{Version: 2, Paused: &running})
			if err != nil || result.Version != 3 {
				t.Fatalf("resume version=%d err=%v", result.Version, err)
			}
			id, err := restarted.materializeNext(ctx)
			if err != nil || (id == "") != (policy == "skip") {
				t.Fatalf("materialized %q, %v", id, err)
			}
			status, err := restarted.scheduleStatus(ctx, d.Tenant, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			action := policy
			if action == "catch_up" {
				action = "trim"
			}
			if status.Paused || status.Version != 3 || status.LastMisfireAction == nil || *status.LastMisfireAction != action ||
				status.NextFireAt.Sub(anchor)%time.Minute != 0 {
				t.Fatalf("unexpected status: %+v", status)
			}
			if policy == "catch_up" {
				if status.LastFiredAt == nil || status.LastFiredAt.Sub(anchor) != 4*time.Minute ||
					status.NextFireAt.Sub(anchor) != 5*time.Minute {
					t.Fatalf("bounded catch-up lost phase: %+v", status)
				}
			} else if status.NextFireAt.Sub(anchor) != 6*time.Minute {
				t.Fatalf("next fire should advance beyond missed window: %+v", status)
			}
			if _, err := repo.setSchedulePause(ctx, d.Tenant, d.ID, pauseCommand{Version: 3, Paused: &paused}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresVersionedScheduleAndIdempotentTrigger(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	d := scheduleDefinition{Tenant: "tenant", ID: "change", Expression: "@every 1m", TimeZone: "UTC", Topic: "demo.record",
		Payload: json.RawMessage(`{"revision":1}`)}
	if err := repo.addSchedule(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.triggerSchedule(ctx, "other", d.ID, "foreign"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant trigger = %v", err)
	}
	oldID, err := repo.triggerSchedule(ctx, d.Tenant, d.ID, "trigger")
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.scheduleStatus(ctx, d.Tenant, d.ID)
	if err != nil || before.LastFiredAt != nil {
		t.Fatalf("manual trigger moved periodic status: %+v %v", before, err)
	}
	d.Payload = json.RawMessage(`{"revision":2}`)
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for range cap(outcomes) {
		wg.Go(func() {
			_, err := repo.replaceSchedule(ctx, 1, d)
			outcomes <- err
		})
	}
	wg.Wait()
	close(outcomes)
	wins, conflicts := 0, 0
	for err := range outcomes {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, errScheduleVersion):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	after, err := repo.scheduleStatus(ctx, d.Tenant, d.ID)
	if err != nil || after.Version != 2 || after.NextFireAt == nil || before.NextFireAt == nil || !after.NextFireAt.Equal(*before.NextFireAt) {
		t.Fatalf("payload-only replacement moved cursor: %+v %v", after, err)
	}

	if retried, err := repo.triggerSchedule(ctx, d.Tenant, d.ID, "trigger"); err != nil || retried != oldID {
		t.Fatalf("trigger replay = %q %v, want %q", retried, err, oldID)
	}
	if _, err := repo.submit(ctx, d.Tenant, "trigger", submission{Topic: d.Topic, Payload: d.Payload}); !errors.Is(err, errRequestConflict) {
		t.Fatalf("cross-command idempotency conflict = %v", err)
	}

	triggers := make(chan string, 4)
	for range cap(triggers) {
		wg.Go(func() {
			id, err := repo.triggerSchedule(ctx, d.Tenant, d.ID, "new-trigger")
			if err != nil {
				t.Error(err)
			}
			triggers <- id
		})
	}
	wg.Wait()
	close(triggers)
	newID := <-triggers
	if newID == "" || newID == oldID {
		t.Fatalf("new trigger ID = %q", newID)
	}
	for id := range triggers {
		if id != newID {
			t.Fatalf("duplicate trigger = %q, want %q", id, newID)
		}
	}
	for i, id := range []string{oldID, newID} {
		var payload []byte
		if err := db.QueryRowContext(ctx, "SELECT payload FROM mq_tasks WHERE id = $1", id).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		status, err := repo.runStatus(ctx, d.Tenant, id)
		if err != nil || !status.ManualTrigger || status.ScheduleID == nil || *status.ScheduleID != d.ID ||
			status.ScheduleVersion == nil || *status.ScheduleVersion != int64(i+1) || string(payload) != fmt.Sprintf(`{"revision":%d}`, i+1) {
			t.Fatalf("run snapshot: %+v payload=%s err=%v", status, payload, err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE schedules SET next_fire_at = (SELECT scheduled_at FROM scheduled_runs WHERE id = $2)
		WHERE id = $1`, d.ID, oldID); err != nil {
		t.Fatal(err)
	}
	automaticID, err := repo.materializeNext(ctx)
	if err != nil || automaticID == "" {
		t.Fatalf("automatic run = %q %v", automaticID, err)
	}
	status, err := repo.runStatus(ctx, d.Tenant, automaticID)
	if err != nil || status.ManualTrigger || status.ScheduleVersion == nil || *status.ScheduleVersion != 2 {
		t.Fatalf("automatic snapshot: %+v %v", status, err)
	}
}

func TestPostgresV3UpgradePreservesQueuedRuns(t *testing.T) {
	t.Setenv("SCHEDULER_TZDATA_ZIP", zoneArchive(t, 3600))
	db, _ := postgresTestDatabases(t)
	ctx := t.Context()
	base, err := os.ReadFile("../../x/messaging/mq/store/migrations/postgres/001_task_queue.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range []string{string(base), schedulerSchema, schedulerSchemaV2, schedulerSchemaV3,
		"CREATE TABLE scheduler_schema (version integer PRIMARY KEY); INSERT INTO scheduler_schema VALUES (3)"} {
		if _, err := db.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	legacyCursor := time.Date(2032, time.February, 29, 9, 15, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `INSERT INTO schedules
		(tenant_id, id, expression, time_zone, topic, payload, next_fire_at)
		VALUES ('tenant', 'legacy-clock', '15 10 29 2 *', $1, 'demo.record', $2, $3)`,
		exampleZone, []byte("{}"), legacyCursor); err != nil {
		t.Fatal(err)
	}
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	input := submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}
	command, err := prepareSubmission("tenant", "before-upgrade", input)
	if err != nil {
		t.Fatal(err)
	}
	// 直接构造 v3 提交事务，避免夹具依赖升级后的入口限速表。
	old := rand.Text()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_runs (id,tenant_id,request_key,request_hash,scheduled_at)
		VALUES ($1,'tenant','before-upgrade',$2,clock_timestamp())`, old, command.hash[:]); err != nil {
		t.Fatal(err)
	}
	if err := repo.insertTask(ctx, tx, old, "tenant", input, time.Now(), traceSource{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	legacyConfig := mqstore.DefaultSQLConfig()
	legacyConfig.EnableAttemptLog = true
	legacy := mq.NewTaskQueue(mqstore.NewSQL(db, legacyConfig))
	claimed, err := legacy.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "before-upgrade", Limit: 1})
	if err != nil || len(claimed) != 1 || claimed[0].ID != old {
		t.Fatalf("legacy claim: count=%d err=%v", len(claimed), err)
	}
	if err := legacy.Release(ctx, old, claimed[0].LeaseOwner, mq.ReleaseOptions{Reason: "task rate limited"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrate(ctx, db, "../../x/messaging/mq/store/migrations/postgres/001_task_queue.sql"); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	clock, err := repo.scheduleStatus(ctx, "tenant", "legacy-clock")
	if err != nil || clock.TimeZone != exampleZone || len(clock.ZoneRulesDigest) != 64 ||
		clock.NextFireAt == nil || !clock.NextFireAt.Equal(legacyCursor) {
		t.Fatalf("legacy zone migration %+v: %v", clock, err)
	}
	var pending, bytes int
	if err := db.QueryRowContext(ctx, "SELECT pending_runs, pending_bytes FROM scheduler_tenant_usage WHERE tenant_id = 'tenant'").Scan(&pending, &bytes); err != nil || pending != 1 || bytes != 2 {
		t.Fatalf("legacy usage = %d/%d, %v", pending, bytes, err)
	}
	kept, err := repo.runStatus(ctx, "tenant", old)
	if err != nil || kept.QueueStatus != mq.TaskStatusQueued || kept.ClaimAttempts != 1 || kept.ExecutionAttempts != 1 {
		t.Fatalf("pending v3 run: %+v %v", kept, err)
	}
	replayed, err := repo.submit(ctx, "tenant", "before-upgrade", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil || replayed != old {
		t.Fatalf("replayed v3 run = %q %v", replayed, err)
	}
	history, err := repo.runAttempts(ctx, "tenant", old, 0)
	if err != nil || len(history.Items) != 1 || history.Items[0].Number != 1 || history.Items[0].FailureCode != "rate_limited" {
		t.Fatalf("upgraded legacy attempt: %+v %v", history, err)
	}
	newQueue := mq.NewTaskQueue(repo.queue)
	claimed, err = newQueue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "after-upgrade", Limit: 1})
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("upgraded claim: count=%d err=%v", len(claimed), err)
	}
	if n, err := repo.beginExecution(ctx, claimed[0]); err != nil || n != 2 {
		t.Fatalf("upgraded execution budget did not preserve the legacy count: n=%d err=%v", n, err)
	}
	history, err = repo.runAttempts(ctx, "tenant", old, 0)
	if err != nil || len(history.Items) != 2 || history.Items[0].Number != 2 ||
		history.Items[0].ExecutionNo == nil || *history.Items[0].ExecutionNo != 2 ||
		history.Items[1].ExecutionNo != nil || history.Items[1].FailureCode != "rate_limited" {
		t.Fatalf("upgraded history continuity: %+v %v", history, err)
	}
}
