package cloudtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type rolloutAPI struct {
	address, logPath string
	credential       string
	client           *http.Client
	stop             func()
}

func rolloutProcessEnv(dsn, address string) []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CLOUD_MANAGER_") && !strings.HasPrefix(entry, "PG") {
			env = append(env, entry)
		}
	}
	return append(env, "CLOUD_MANAGER_DATABASE_URL="+dsn, "CLOUD_MANAGER_LISTEN_ADDR="+address)
}

func rolloutMigrate(t *testing.T, binary, dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "migrate")
	cmd.Env = rolloutProcessEnv(dsn, "")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("migrate rollout fixture: %v: %s", err, output)
	}
}

func rolloutMigrateWithReads(t *testing.T, baseline rolloutAPI, binary, dsn, path string) int {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		reads int
		err   error
	}
	firstRead, migrated := make(chan struct{}), make(chan struct{})
	done := make(chan result, 1)
	go func() {
		reads := 0
		for {
			for _, target := range []string{"/readyz", path} {
				status, _, err := baseline.request(ctx, http.MethodGet, target, "", nil)
				if err != nil || status != http.StatusOK {
					done <- result{reads, fmt.Errorf("baseline read %s: status=%d error=%v", target, status, err)}
					return
				}
			}
			reads++
			if reads == 1 {
				close(firstRead)
			}
			select {
			case <-migrated:
				done <- result{reads, nil}
				return
			case <-ctx.Done():
				done <- result{reads, ctx.Err()}
				return
			default:
			}
		}
	}()
	select {
	case <-firstRead:
	case failed := <-done:
		t.Fatal(failed.err)
	}
	rolloutMigrate(t, binary, dsn)
	close(migrated)
	probes := <-done
	if probes.err != nil {
		t.Fatal(probes.err)
	}
	return probes.reads
}

func rolloutAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func startRolloutAPI(t *testing.T, binary, dsn, address string) rolloutAPI {
	t.Helper()
	stop, logPath := startRolloutProcess(t, binary, "api", rolloutProcessEnv(dsn, address))
	api := rolloutAPI{address: address, logPath: logPath, credential: pitrServiceToken,
		client: &http.Client{Timeout: 3 * time.Second}, stop: stop}
	api.waitProbe(t, "/healthz", http.StatusOK)
	t.Cleanup(api.client.CloseIdleConnections)
	return api
}

func startRolloutProcess(t *testing.T, binary, role string, env []string) (stop func(), logPath string) {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), role+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, role)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logFile, logFile
	startErr := cmd.Start()
	closeErr := logFile.Close()
	if startErr != nil {
		t.Fatalf("start rollout %s: %v (%v)", role, startErr, closeErr)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stop = sync.OnceFunc(func() {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("stop rollout %s: %v", role, err)
		}
		select {
		case err := <-done:
			if err != nil {
				log, logErr := os.ReadFile(logPath)
				t.Errorf("rollout %s failed to stop cleanly: %v; process log: %s (%v)", role, err, log, logErr)
			}
		case <-time.After(12 * time.Second):
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("kill stalled rollout %s: %v", role, err)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Errorf("rollout %s wait failed after forced stop", role)
			}
			t.Errorf("rollout %s did not stop after SIGTERM; process log: %s", role, logPath)
		}
	})
	t.Cleanup(stop)
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return stop, logPath
}

func (a rolloutAPI) waitProbe(t *testing.T, path string, want int) {
	t.Helper()
	pitrWait(t, path+" at "+a.address+" (process log "+a.logPath+")", func(ctx context.Context) (bool, error) {
		status, _, err := a.request(ctx, http.MethodGet, path, "", nil)
		return status == want, err
	})
}

func (a rolloutAPI) request(ctx context.Context, method, path, key string, payload []byte) (int, []byte, error) {
	revision := ""
	if key != "" {
		revision = `"1"`
	}
	return a.requestWithRevision(ctx, method, path, key, revision, payload)
}

func (a rolloutAPI) requestWithRevision(ctx context.Context, method, path, key, revision string, payload []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+a.address+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.credential)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if revision != "" {
		req.Header.Set("If-Match", revision)
	}
	if len(payload) != 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, errors.Join(readErr, resp.Body.Close())
}
