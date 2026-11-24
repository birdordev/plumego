//go:build unix

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestSandboxTaskRequiresExplicitAPIAdmission(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	body := `{"topic":"sandbox.shell","payload":{"script":"exit 0"}}`
	t.Setenv("SCHEDULER_SANDBOX_TASKS", "")
	disabled, err := newApp("", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	if response := auditHTTPRequest(t.Context(), disabled, "POST", "/runs", "sandbox-submit", "", body); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled runtime returned %d: %s", response.Code, response.Body)
	}
	t.Setenv("SCHEDULER_SANDBOX_TASKS", "1")
	enabled, err := newApp("", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	if response := auditHTTPRequest(t.Context(), enabled, "POST", "/runs", "sandbox-submit", "", body); response.Code != http.StatusAccepted {
		t.Fatalf("enabled runtime returned %d: %s", response.Code, response.Body)
	}
	if response := auditHTTPRequest(t.Context(), enabled, "POST", "/runs", "sandbox-invalid", "",
		`{"topic":"sandbox.shell","payload":{"script":"exit 0","image":"other"}}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field returned %d: %s", response.Code, response.Body)
	}
}

func sandboxDockerTest(t *testing.T) *sandboxExecutor {
	t.Helper()
	if os.Getenv("PLUMEGO_TEST_SANDBOX") != "1" {
		t.Skip("PLUMEGO_TEST_SANDBOX is unset")
	}
	t.Setenv("SCHEDULER_SANDBOX_SCOPE", "test-"+rand.Text())
	sandbox, err := loadSandboxExecutor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := sandbox.reap(ctx, time.Now().Add(time.Minute)); err != nil {
			t.Error(err)
		}
	})
	return sandbox
}

func TestSandboxDockerLimitsAndCleanup(t *testing.T) {
	sandbox := sandboxDockerTest(t)
	t.Setenv("SCHEDULER_DATABASE_URL", "sandbox-test-credential")
	const verify = `set -eu
test -z "${SCHEDULER_DATABASE_URL:-}"
test "$(id -u)" = 65534
test ! -S /var/run/docker.sock
test ! -e /host
test ! -e /sys/class/net/eth0
test "$(cat /sys/fs/cgroup/memory.max)" = 67108864
test "$(cat /sys/fs/cgroup/memory.swap.max)" = 0
test "$(cat /sys/fs/cgroup/cpu.max)" = "50000 100000"
test "$(cat /sys/fs/cgroup/pids.max)" = 16
grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/self/status
grep -Eq '^CapEff:[[:space:]]+0+$' /proc/self/status
grep -Eq '^Seccomp:[[:space:]]+2$' /proc/self/status
if touch /usr/sandbox-probe 2>/dev/null; then exit 1; fi
touch /tmp/allowed
if dd if=/dev/zero of=/tmp/quota-test bs=1M count=9 2>/dev/null; then exit 1; fi
`
	if err := sandbox.execute(t.Context(), verify); err != nil {
		t.Fatalf("isolation assertions: %v", err)
	}
	if err := sandbox.execute(t.Context(), `awk 'BEGIN { s="x"; for (i=0;i<28;i++) s=s s; print length(s) }'`); !errors.Is(err, errSandboxExecution) {
		t.Fatalf("memory overrun: %v", err)
	}
	start := time.Now()
	if err := sandbox.execute(t.Context(), "while :; do :; done"); !errors.Is(err, errSandboxExecution) {
		t.Fatalf("CPU-bound timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 7*time.Second || elapsed > 12*time.Second {
		t.Fatalf("CPU-bound timeout took %s", elapsed)
	}
	testContainer := "plumego-gc-test-" + rand.Text()
	if err := sandbox.command(t.Context(), "create", "--name", testContainer, "--label", sandboxLabel+"="+sandbox.scope, sandbox.image).Run(); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.reap(t.Context(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	raw, err := sandbox.command(t.Context(), "ps", "--all", "--quiet", "--filter", "label="+sandboxLabel+"="+sandbox.scope).Output()
	if err != nil || len(strings.TrimSpace(string(raw))) != 0 {
		t.Fatalf("retained sandbox containers = %q: %v", raw, err)
	}
}

func TestPostgresSandboxWorkerCrashReclaimsContainer(t *testing.T) {
	sandbox := sandboxDockerTest(t)
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	t.Setenv("SCHEDULER_WORKER_POOL", "sandbox")
	t.Setenv("SCHEDULER_WORKER_LEASE_DURATION", "3s")
	startWorker := func() {
		t.Helper()
		startSchedulerChild(t, db, "worker")
	}
	worker := startSchedulerChild(t, db, "worker")
	ctx := t.Context()
	submit := func(key, script string) string {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"script": script})
		if err != nil {
			t.Fatal(err)
		}
		id, err := repo.submit(ctx, "tenant", key, submission{Topic: sandboxTaskType, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	success := submit("sandbox-success", "exit 0")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		result, err := repo.runStatus(ctx, "tenant", success)
		return result.QueueStatus == "done" && result.ResultReady, err
	})
	crashed := submit("sandbox-crash", "sleep 60")
	scan := func(ctx context.Context) (bool, error) {
		raw, err := sandbox.command(ctx, "ps", "--quiet", "--filter", "label="+sandboxLabel+"="+sandbox.scope).Output()
		return len(strings.TrimSpace(string(raw))) != 0, err
	}
	awaitSchedulerState(t, scan)
	killSchedulerChild(t, worker)
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		running, err := scan(ctx)
		return !running, err
	})
	startWorker()
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		result, err := repo.runStatus(ctx, "tenant", crashed)
		return result.QueueStatus == "dead" && !result.ResultReady && result.ExecutionAttempts == 2, err
	})
}
