//go:build unix

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

const queueMigrationFile = "../../x/messaging/mq/store/migrations/postgres/001_task_queue.sql"

func TestSchedulerChildProcess(t *testing.T) {
	role := os.Getenv("PLUMEGO_TEST_SCHEDULER_CHILD_ROLE")
	if role == "" {
		t.Skip("subprocess entrypoint")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, role, "", queueMigrationFile); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresFixedDelaySurvivesSchedulerRestart(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: "tenant", ID: "restart", Expression: "@every 100ms",
		TimeZone: "UTC", Mode: "fixed_delay", Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	process := startSchedulerChild(t, db, "scheduler")
	var first string
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.scheduleStatus(ctx, "tenant", "restart")
		if err != nil || status.WaitingRunID == nil {
			return false, err
		}
		first = *status.WaitingRunID
		return true, nil
	})
	killSchedulerChild(t, process)
	claimed, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "worker", Limit: 1})
	if err != nil || len(claimed) != 1 || claimed[0].ID != first {
		t.Fatalf("first claim: count=%d err=%v", len(claimed), err)
	}
	if err := queue.Ack(ctx, first, claimed[0].LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	var completed time.Time
	if err := db.QueryRowContext(ctx, "SELECT delay_completed_at FROM scheduled_runs WHERE id = $1", first).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	restarted := startSchedulerChild(t, db, "scheduler")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.scheduleStatus(ctx, "tenant", "restart")
		if err != nil || status.WaitingRunID == nil || *status.WaitingRunID == first {
			return false, err
		}
		run, err := repo.runStatus(ctx, "tenant", *status.WaitingRunID)
		if err == nil && !run.ScheduledAt.Equal(completed.Add(100*time.Millisecond)) {
			return false, fmt.Errorf("recovered occurrence used a different completion anchor: %s", run.ScheduledAt)
		}
		return err == nil, err
	})
	if err := restarted.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresExpiryRoleClearsPendingQuota(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "expire-process", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET expires_at = clock_timestamp() - interval '1 second' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	process := startSchedulerChild(t, db, "expiry")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var state string
		var pending int
		err := db.QueryRowContext(ctx, `SELECT q.status, u.pending_runs FROM mq_tasks q
			JOIN scheduler_tenant_usage u ON u.tenant_id = q.tenant_id WHERE q.id = $1`, id).Scan(&state, &pending)
		return state == mq.TaskStatusExpired && pending == 0, err
	})
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
}

func schedulerChildCommand(t *testing.T, db *sql.DB, role string) (*exec.Cmd, string) {
	t.Helper()
	var schema string
	if err := db.QueryRowContext(t.Context(), `SELECT quote_ident(current_schema())`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	application := "scheduler-test-" + rand.Text()
	dsn := os.Getenv("PLUMEGO_TEST_POSTGRES_DSN")
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("invalid test DSN")
		}
		query := u.Query()
		query.Set("search_path", schema)
		query.Set("application_name", application)
		u.RawQuery = query.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path='" + schema + "' application_name=" + application
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSchedulerChildProcess$")
	cmd.Env = append(os.Environ(), "SCHEDULER_DATABASE_URL="+dsn, "PLUMEGO_TEST_SCHEDULER_CHILD_ROLE="+role)
	return cmd, application
}

func startSchedulerChild(t *testing.T, db *sql.DB, role string) *exec.Cmd {
	t.Helper()
	cmd, application := schedulerChildCommand(t, db, role)
	logFile, err := os.CreateTemp(t.TempDir(), "scheduler-child-*.log")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		_ = logFile.Close()
		if t.Failed() {
			contents, err := os.ReadFile(logFile.Name())
			if err == nil {
				t.Logf("%s child output: %s", role, contents)
			}
		}
	})
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var connected bool
		err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name = $1)", application).Scan(&connected)
		return connected, err
	})
	return cmd
}

func killSchedulerChild(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	err := cmd.Wait()
	if !errors.As(err, &exit) {
		t.Fatalf("expected a killed child, got %v", err)
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("expected SIGKILL, got %v", exit)
	}
}

type crashBarrier struct {
	key, phase int
	conn       *sql.Conn
}

func newCrashBarrier(t *testing.T, db *sql.DB, phase int, trigger string) crashBarrier {
	t.Helper()
	var key int
	if err := db.QueryRowContext(t.Context(), "SELECT hashtext(current_schema()) & 2147483647").Scan(&key); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1, $2)", key, phase)
		_ = conn.Close()
	})
	if _, err := conn.ExecContext(t.Context(), "SELECT pg_advisory_lock($1, $2)", key, phase); err != nil {
		t.Fatal(err)
	}
	ddl := fmt.Sprintf(`CREATE FUNCTION test_crash_barrier() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		PERFORM pg_advisory_xact_lock(%d, %d);
		RETURN NEW;
	END;
	$$; CREATE TRIGGER test_crash_barrier %s EXECUTE FUNCTION test_crash_barrier()`, key, phase, trigger)
	if _, err := db.ExecContext(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	return crashBarrier{key, phase, conn}
}

func (b crashBarrier) wait(t *testing.T, db *sql.DB) {
	t.Helper()
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks
			WHERE locktype = 'advisory' AND classid = $1 AND objid = $2 AND objsubid = 2 AND NOT granted)`, b.key, b.phase).Scan(&waiting)
		return waiting, err
	})
}

func (b crashBarrier) unlock(t *testing.T) {
	t.Helper()
	if _, err := b.conn.ExecContext(t.Context(), "SELECT pg_advisory_unlock($1, $2)", b.key, b.phase); err != nil {
		t.Fatal(err)
	}
}

func (b crashBarrier) abortWaiter(t *testing.T, db *sql.DB) {
	t.Helper()
	// PostgreSQL may continue the statement while blocked after its client dies.
	// Abort the backend before releasing the barrier to model a pre-commit crash.
	if _, err := db.ExecContext(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND classid = $1 AND objid = $2 AND objsubid = 2 AND NOT granted`, b.key, b.phase); err != nil {
		t.Fatal(err)
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var exited bool
		err := db.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM pg_locks
			WHERE locktype = 'advisory' AND classid = $1 AND objid = $2 AND objsubid = 2 AND NOT granted)`, b.key, b.phase).Scan(&exited)
		return exited, err
	})
}

func TestPostgresWorkerProcessCrashRecovery(t *testing.T) {
	for _, cut := range []struct {
		name, trigger string
		receipts      int
	}{
		{"before-result-commit", "BEFORE INSERT ON scheduled_results FOR EACH ROW", 0},
		{"after-result-before-ack", "BEFORE UPDATE OF status ON mq_tasks FOR EACH ROW WHEN (OLD.status = 'leased' AND NEW.status = 'done')", 1},
		{"after-ack", "", 1},
	} {
		t.Run(cut.name, func(t *testing.T) {
			_, _, db := postgresTestQueues(t)
			ctx := t.Context()
			repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
			id, err := repo.submit(ctx, "tenant", "crash", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET max_attempts = 2 WHERE id = $1", id); err != nil {
				t.Fatal(err)
			}
			var barrier crashBarrier
			if cut.trigger != "" {
				barrier = newCrashBarrier(t, db, 1, cut.trigger)
			}
			worker := startSchedulerChild(t, db, "worker")
			if cut.trigger != "" {
				barrier.wait(t, db)
			} else {
				awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
					s, err := repo.runStatus(ctx, "tenant", id)
					return s.QueueStatus == "done", err
				})
			}
			var receipts, events int
			checkEffects := func(want int) {
				t.Helper()
				if err := db.QueryRowContext(ctx, `SELECT
					(SELECT count(*) FROM scheduled_results WHERE run_id = $1),
					(SELECT count(*) FROM scheduled_outbox WHERE run_id = $1)`, id).Scan(&receipts, &events); err != nil {
					t.Fatal(err)
				}
				if receipts != want || events != want {
					t.Fatalf("effects receipt=%d outbox=%d, want %d", receipts, events, want)
				}
			}
			checkEffects(cut.receipts)
			killSchedulerChild(t, worker)
			wantAttempts := 1
			if cut.trigger != "" {
				barrier.abortWaiter(t, db)
				barrier.unlock(t)
				if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1 AND status = 'leased'", id); err != nil {
					t.Fatal(err)
				}
				wantAttempts = 2
			}
			if cut.name == "after-result-before-ack" {
				startSchedulerChild(t, db, "finalizer")
				wantAttempts = 1
			}
			replacement := startSchedulerChild(t, db, "worker")
			awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
				s, err := repo.runStatus(ctx, "tenant", id)
				return s.QueueStatus == "done", err
			})
			if err := replacement.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if err := replacement.Wait(); err != nil {
				t.Fatal(err)
			}
			checkEffects(1)
			var attempts int
			if err := db.QueryRowContext(ctx, "SELECT attempts FROM mq_tasks WHERE id = $1", id).Scan(&attempts); err != nil || attempts != wantAttempts {
				t.Fatalf("attempts=%d, want=%d, err=%v", attempts, wantAttempts, err)
			}
		})
	}
}

func TestPostgresSchedulerProcessCrashRecovery(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: "tenant", ID: "process", Expression: "@every 1h", TimeZone: "UTC",
		Topic: "demo.record", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	var due time.Time
	if err := db.QueryRowContext(ctx, `UPDATE schedules SET next_fire_at = clock_timestamp() - interval '1 second' RETURNING next_fire_at`).Scan(&due); err != nil {
		t.Fatal(err)
	}
	barrier := newCrashBarrier(t, db, 2, "BEFORE UPDATE OF next_fire_at ON schedules FOR EACH ROW")
	first := startSchedulerChild(t, db, "scheduler")
	barrier.wait(t, db)
	second := startSchedulerChild(t, db, "scheduler")
	var queued, runs int
	var cursor time.Time
	read := func(ctx context.Context) error {
		return db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM mq_tasks),
			(SELECT count(*) FROM scheduled_runs), next_fire_at FROM schedules`).Scan(&queued, &runs, &cursor)
	}
	if err := read(ctx); err != nil || queued != 0 || runs != 0 || !due.Equal(cursor) {
		t.Fatalf("uncommitted result visible: %d %d %v %v", queued, runs, cursor, err)
	}
	killSchedulerChild(t, first)
	barrier.unlock(t)
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		err := read(ctx)
		return queued == 1 && runs == 1 && cursor.Equal(due.Add(time.Hour)), err
	})
	third := startSchedulerChild(t, db, "scheduler")
	killSchedulerChild(t, second)
	if err := third.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := third.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := read(ctx); err != nil || queued != 1 || runs != 1 || !cursor.Equal(due.Add(time.Hour)) {
		t.Fatalf("committed result lost or duplicated: %d %d %v %v", queued, runs, cursor, err)
	}
}

func TestPostgresWorkerProcessPersistsDeadLetter(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "dead-letter", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduled_results ADD CONSTRAINT inject_process_failure CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET max_attempts = 1 WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	worker := startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.runStatus(ctx, "tenant", id)
		return status.QueueStatus == "dead" && !status.ResultReady, err
	})
	killSchedulerChild(t, worker)
	var dead, failedAttempts int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM mq_task_dlq WHERE task_id = $1 AND reason = 'task failed'),
		(SELECT count(*) FROM mq_task_attempts WHERE task_id = $1 AND finished_at IS NOT NULL AND NOT success)`, id).
		Scan(&dead, &failedAttempts); err != nil || dead != 1 || failedAttempts != 1 {
		t.Fatalf("dead letters=%d failed attempts=%d err=%v", dead, failedAttempts, err)
	}
	history, err := repo.deadLetters(ctx, "tenant", id, 0)
	if err != nil || len(history.Items) != 1 || history.Items[0].Attempt == nil ||
		*history.Items[0].Attempt != 1 || history.Items[0].TaskVersion != "1" {
		t.Fatalf("persisted history after process exit: %+v %v", history, err)
	}
}

func TestPostgresWorkerRestartPreservesExecutionBackoff(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "durable-retry", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduled_results ADD CONSTRAINT reject_handler_result CHECK (false)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET max_attempts = 3 WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	// 只领取后失联的 Worker 不应消耗执行预算。
	for range 2 {
		tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "prefetch-only", Limit: 1})
		if err != nil || len(tasks) != 1 || tasks[0].ID != id {
			t.Fatalf("prefetch count=%d err=%v", len(tasks), err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
	}
	waitRetry := func(execution int) time.Time {
		t.Helper()
		var retryAt time.Time
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			status, err := repo.runStatus(ctx, "tenant", id)
			if err != nil || status.QueueStatus != "queued" || status.ExecutionAttempts != execution || status.AvailableAt == nil {
				return false, err
			}
			if status.ClaimAttempts != execution+2 {
				return false, fmt.Errorf("claim attempts=%d, executions=%d", status.ClaimAttempts, status.ExecutionAttempts)
			}
			retryAt = *status.AvailableAt
			return true, nil
		})
		return retryAt
	}
	worker := startSchedulerChild(t, db, "worker")
	firstRetry := waitRetry(1)
	killSchedulerChild(t, worker)
	worker = startSchedulerChild(t, db, "worker")
	secondRetry := waitRetry(2)
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.runStatus(ctx, "tenant", id)
		return status.QueueStatus == "dead" && status.ExecutionAttempts == 3 && status.ClaimAttempts == 5, err
	})
	killSchedulerChild(t, worker)
	page, err := repo.runAttempts(ctx, "tenant", id, 0)
	if err != nil || len(page.Items) != 5 {
		t.Fatalf("retry history count=%d err=%v", len(page.Items), err)
	}
	for i, item := range page.Items {
		want := 3 - i
		if want > 0 && (item.ExecutionNo == nil || *item.ExecutionNo != want || item.ExecutionStartedAt == nil) ||
			want <= 0 && (item.ExecutionNo != nil || item.ExecutionStartedAt != nil) {
			t.Fatalf("attempt %d: %+v", i, item)
		}
	}
	first, second, third := page.Items[2], page.Items[1], page.Items[0]
	if second.ClaimedAt.Before(firstRetry) || third.ClaimedAt.Before(secondRetry) {
		t.Fatalf("retry reclaimed ahead of persisted time: %v %v", second.ClaimedAt, third.ClaimedAt)
	}
	if first.FinishedAt == nil || second.FinishedAt == nil {
		t.Fatal("missing completion timestamp for released claim")
	}
	for _, step := range []struct {
		at, completed time.Time
		min, max      time.Duration
	}{
		{firstRetry, *first.FinishedAt, 700 * time.Millisecond, 1300 * time.Millisecond},
		{secondRetry, *second.FinishedAt, 1500 * time.Millisecond, 2500 * time.Millisecond},
	} {
		if interval := step.at.Sub(step.completed); interval < step.min || interval > step.max {
			t.Fatalf("persisted exponential backoff interval %s outside [%s, %s]", interval, step.min, step.max)
		}
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduled_results DROP CONSTRAINT reject_handler_result"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	items, err := repo.replayDeadLetters(ctx, "tenant", "retry-replay", replaySpec{FailedFrom: now.Add(-time.Hour), FailedTo: now.Add(time.Hour), Limit: 1})
	if err != nil || len(items) != 1 || items[0].RunID != id {
		t.Fatalf("replay count=%d err=%v", len(items), err)
	}
	worker = startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.runStatus(ctx, "tenant", id)
		return status.QueueStatus == "done" && status.ExecutionAttempts == 4 && status.ClaimAttempts == 6 && status.ResultReady, err
	})
	killSchedulerChild(t, worker)
}
