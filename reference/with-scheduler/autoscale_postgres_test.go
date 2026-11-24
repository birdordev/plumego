//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresAutoscaleBurstBudgetAndDrain(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx, stop := context.WithCancel(t.Context())
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if _, err := db.ExecContext(ctx, `INSERT INTO scheduler_execution_budgets (scope,budget_key,max_concurrent)
		VALUES ('downstream','result-db',1)`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCHEDULER_WORKER_POOL", "io")
	t.Setenv("SCHEDULER_WORKER_CONCURRENCY", "1")
	t.Setenv("SCHEDULER_WORKER_DRAIN_TIMEOUT", "4s")
	t.Setenv("SCHEDULER_RESULT_DIR", "")
	t.Setenv("SCHEDULER_METRICS_ADDR", "")
	t.Setenv("SCHEDULER_WORKER_PROBE_ADDR", "")
	pool, err := loadWorkerPool("io", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := autoscaleConfig{pool: pool, minWorkers: 1, maxWorkers: 3, interval: 60 * time.Millisecond,
		window: time.Second, age: 250 * time.Millisecond, settle: 650 * time.Millisecond, drain: 4 * time.Second}
	commands := make(chan *exec.Cmd, 8)
	for range cap(commands) {
		cmd, _ := schedulerChildCommand(t, db, "worker")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		commands <- cmd
	}
	starts := make(chan *os.Process, cap(commands))
	done := make(chan error, 1)
	go func() {
		done <- superviseWorkers(ctx, repo, cfg, func() (*managedWorker, error) {
			select {
			case cmd := <-commands:
				w, err := startManagedWorker(cmd)
				if err == nil {
					starts <- w.process
				}
				return w, err
			default:
				return nil, fmt.Errorf("test exhausted child processes")
			}
		})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Error("autoscaler did not drain its workers")
		}
	})
	nextWorker := func() *os.Process {
		t.Helper()
		select {
		case process := <-starts:
			return process
		case <-time.After(6 * time.Second):
			t.Fatal("expected worker did not start")
			return nil
		}
	}
	first := nextWorker()
	submit := func(key string) string {
		t.Helper()
		id, err := repo.submit(ctx, "tenant", key, submission{Topic: ioTaskType, Payload: json.RawMessage(`{"delay_ms":850}`)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	for i := range 12 {
		submit(fmt.Sprintf("burst-%d", i))
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		s, err := repo.readScaleSnapshot(ctx, []string{ioTaskType}, cfg.window)
		return s.ready > 0 && s.active == 1 && s.downstreamSlots == 0 && s.arrivals > s.processed, err
	})
	select {
	case p := <-starts:
		t.Fatalf("added worker %d with exhausted downstream capacity", p.Pid)
	case <-time.After(400 * time.Millisecond):
	}
	obstruction, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = obstruction.Rollback() }()
	if _, err := obstruction.ExecContext(ctx, "LOCK TABLE scheduler_execution_budgets IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-starts:
		t.Fatalf("added worker %d while telemetry was unavailable", p.Pid)
	case <-time.After(1200 * time.Millisecond):
	}
	if err := obstruction.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE scheduler_execution_budgets SET max_concurrent = 3 WHERE scope = 'downstream'"); err != nil {
		t.Fatal(err)
	}
	second, third := nextWorker(), nextWorker()
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		s, err := repo.readScaleSnapshot(ctx, []string{ioTaskType}, cfg.window)
		return s.active == 3, err
	})
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var count int
		err := db.QueryRowContext(ctx, "SELECT count(*) FROM mq_tasks WHERE topic = $1 AND status = 'done'", ioTaskType).Scan(&count)
		return count == 12, err
	})
	awaitSchedulerState(t, func(context.Context) (bool, error) {
		return second.Signal(syscall.Signal(0)) != nil && third.Signal(syscall.Signal(0)) != nil, nil
	})
	if err := first.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("base worker stopped during scale-in: %v", err)
	}
	after := submit("after-drain")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		state, err := repo.runStatus(ctx, "tenant", after)
		return state.QueueStatus == "done" && state.ClaimAttempts == 1, err
	})
	t.Log("downstream cap held at one process, raised budget activated three, drained back to one")
}
