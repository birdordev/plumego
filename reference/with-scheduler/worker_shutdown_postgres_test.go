//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func workerProbeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCHEDULER_WORKER_PROBE_ADDR", address)
	return address
}

func awaitWorkerProbe(t *testing.T, address string, expected int) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/readyz", nil)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, nil
		}
		return resp.StatusCode == expected, resp.Body.Close()
	})
}

func submitShutdownTask(t *testing.T, repo repository, key string) string {
	t.Helper()
	id, err := repo.submit(t.Context(), "tenant", key, submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgresWorkerSIGTERMDrainsWithoutClaimingNewWork(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	address := workerProbeAddress(t)
	t.Setenv("SCHEDULER_WORKER_DRAIN_TIMEOUT", "6s")
	first := submitShutdownTask(t, repo, "drain")
	barrier := newCrashBarrier(t, db, 1, "BEFORE INSERT ON scheduled_results FOR EACH ROW")
	process := startSchedulerChild(t, db, "worker")
	awaitWorkerProbe(t, address, http.StatusOK)
	barrier.wait(t, db)
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	awaitWorkerProbe(t, address, http.StatusServiceUnavailable)
	late := submitShutdownTask(t, repo, "after-drain")
	barrier.unlock(t)
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	done, err := repo.runStatus(ctx, "tenant", first)
	if err != nil || done.QueueStatus != mq.TaskStatusDone || !done.ResultReady {
		t.Fatalf("in-flight run did not drain: %+v %v", done, err)
	}
	pending, err := repo.runStatus(ctx, "tenant", late)
	if err != nil || pending.QueueStatus != mq.TaskStatusQueued || pending.ClaimAttempts != 0 {
		t.Fatalf("worker claimed new work during drain: %+v %v", pending, err)
	}
}

func TestPostgresWorkerTimedKillRetainsLiveLeaseForTakeover(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	address := workerProbeAddress(t)
	t.Setenv("SCHEDULER_WORKER_DRAIN_TIMEOUT", "6s")
	t.Setenv("SCHEDULER_WORKER_LEASE_DURATION", "2500ms")
	id := submitShutdownTask(t, repo, "timed-kill")
	barrier := newCrashBarrier(t, db, 1, "BEFORE INSERT ON scheduled_results FOR EACH ROW")
	process := startSchedulerChild(t, db, "worker")
	awaitWorkerProbe(t, address, http.StatusOK)
	barrier.wait(t, db)
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	awaitWorkerProbe(t, address, http.StatusServiceUnavailable)
	// 模拟进程卡住，外部监督器超时后仍能强制终止。
	if err := process.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-time.After(250 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	killSchedulerChild(t, process)
	barrier.abortWaiter(t, db)
	barrier.unlock(t)
	old := mq.Task{ID: id, TenantID: "tenant"}
	if err := db.QueryRowContext(ctx, "SELECT lease_owner, lease_until FROM mq_tasks WHERE id = $1", id).Scan(&old.LeaseOwner, &old.LeaseUntil); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !old.LeaseUntil.After(now) {
		t.Fatal("original lease was already expired before the takeover check")
	}
	opts := mq.ReserveOptions{ConsumerID: "early-replacement", Limit: 1, Now: now}
	if rows, err := queue.ReserveFenced(ctx, opts); err != nil || len(rows) != 0 {
		t.Fatalf("claimed before lease expiry: count=%d err=%v", len(rows), err)
	}
	t.Setenv("SCHEDULER_WORKER_PROBE_ADDR", "")
	replacement := startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.runStatus(ctx, "tenant", id)
		return status.QueueStatus == mq.TaskStatusDone, err
	})
	killSchedulerChild(t, replacement)
	history, err := repo.runAttempts(ctx, "tenant", id, 0)
	if err != nil || len(history.Items) != 2 || history.Items[0].ClaimedAt.Before(old.LeaseUntil) {
		t.Fatalf("unexpected takeover history: %+v %v", history, err)
	}
	if err := repo.recordResult(ctx, old); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("old worker wrote after takeover: %v", err)
	}
	var receipts, events int
	err = db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM scheduled_results WHERE run_id = $1),
		(SELECT count(*) FROM scheduled_outbox WHERE run_id = $1)`, id).Scan(&receipts, &events)
	if err != nil || receipts != 1 || events != 1 {
		t.Fatalf("duplicate effect: receipts=%d events=%d err=%v", receipts, events, err)
	}
}

func TestPostgresWorkerDrainTimeoutCancelsBeforeHandoff(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	address := workerProbeAddress(t)
	t.Setenv("SCHEDULER_WORKER_DRAIN_TIMEOUT", "2s")
	id := submitShutdownTask(t, repo, "cooperative-timeout")
	barrier := newCrashBarrier(t, db, 1, "BEFORE INSERT ON scheduled_results FOR EACH ROW")
	process := startSchedulerChild(t, db, "worker")
	awaitWorkerProbe(t, address, http.StatusOK)
	barrier.wait(t, db)
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	awaitWorkerProbe(t, address, http.StatusServiceUnavailable)
	var exitErr *exec.ExitError
	if err := process.Wait(); !errors.As(err, &exitErr) || !exitErr.Exited() || exitErr.ExitCode() == 0 {
		t.Fatalf("worker did not report a drain timeout: %v", err)
	}
	var waiting bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory'
		AND classid = $1 AND objid = $2 AND objsubid = 2 AND NOT granted)`, barrier.key, barrier.phase).Scan(&waiting)
	if err != nil || waiting {
		t.Fatalf("canceled handler still has a blocked transaction: waiting=%t err=%v", waiting, err)
	}
	status, err := repo.runStatus(ctx, "tenant", id)
	if err != nil || status.QueueStatus != mq.TaskStatusQueued || status.ResultReady || status.ClaimAttempts != 1 {
		t.Fatalf("canceled claim not safe to hand off: %+v %v", status, err)
	}
	history, err := repo.runAttempts(ctx, "tenant", id, 0)
	if err != nil || len(history.Items) != 1 || history.Items[0].FailureCode != "worker_shutdown" {
		t.Fatalf("handoff history: %+v %v", history, err)
	}
	barrier.unlock(t)
	t.Setenv("SCHEDULER_WORKER_PROBE_ADDR", "")
	replacement := startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.runStatus(ctx, "tenant", id)
		return status.QueueStatus == mq.TaskStatusDone && status.ClaimAttempts == 2 && status.ResultReady, err
	})
	killSchedulerChild(t, replacement)
}
