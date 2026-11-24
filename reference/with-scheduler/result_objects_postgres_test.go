//go:build unix

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

type resultTestReader func([]byte) (int, error)

func (r resultTestReader) Read(p []byte) (int, error) {
	return r(p)
}

func TestPostgresResultObjectsAndProgress(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	objects := &resultObjects{root: root, ttl: time.Hour}
	tracing, err := newSchedulerTracing(false)
	if err != nil {
		t.Fatal(err)
	}
	makeAPI := func(tenant string) http.Handler {
		t.Helper()
		app, err := newTracedApp("", "test-token", tenant, "", repo, tracing, objects)
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	api, other := makeAPI("tenant"), makeAPI("other")
	get := func(handler http.Handler, id, rangeHeader string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/runs/"+id+"/result", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Range", rangeHeader)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != want || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("result response: status=%d bytes=%d headers=%v", w.Code, w.Body.Len(), w.Header())
		}
		return w
	}
	submit := func(key string) string {
		t.Helper()
		id, err := repo.submit(ctx, "tenant", key, submission{Topic: exportTaskType, Payload: json.RawMessage(`{"size_bytes":2097152}`)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	inProgressID := submit("progress")
	at := time.Now()
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{
		Topics: []string{exportTaskType}, Limit: 1, Lease: time.Minute, ConsumerID: "test", Now: at,
	})
	if err != nil || len(tasks) != 1 || tasks[0].ID != inProgressID {
		t.Fatalf("reserve progress run: %v %v", tasks, err)
	}
	task := tasks[0]
	writes := 0
	reporter := progressReporter{write: func(ctx context.Context, percent int) error {
		writes++
		return repo.storeProgress(ctx, task, percent)
	}}
	start := time.Now()
	for i := range 10000 {
		if err := reporter.report(ctx, at.Add(time.Duration(i)*time.Millisecond), i/100); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 20 {
		t.Fatalf("10,000 progress callbacks produced %d writes", writes)
	}
	t.Logf("10,000 progress callbacks: %d PostgreSQL writes in %s", writes, time.Since(start))
	status, err := repo.runStatus(ctx, "tenant", task.ID)
	if err != nil || status.ProgressPercent == nil || *status.ProgressPercent != 95 {
		t.Fatalf("run progress: %+v %v", status, err)
	}
	if err := repo.publishResultObject(ctx, objects, task, 50, strings.NewReader("truncated")); err == nil {
		t.Fatal("truncated upload was published")
	}
	if _, err := repo.runResult(ctx, "tenant", task.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed upload left a visible result: %v", err)
	}
	interrupted := resultTestReader(func(p []byte) (int, error) {
		err := repo.cancelRun(ctx, "tenant", task.ID)
		return copy(p, "old"), err
	})
	if err := repo.publishResultObject(ctx, objects, task, 3, interrupted); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("claim canceled during streaming published a result: %v", err)
	}
	if err := repo.storeProgress(ctx, task, 99); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("canceled claim wrote progress: %v", err)
	}
	if err := repo.publishResultObject(ctx, objects, task, 3, strings.NewReader("old")); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("canceled claim published a result: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE scheduler_result_objects SET expires_at = statement_timestamp() WHERE state = 'staging'"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if id, err := repo.expireResultObject(ctx, objects); err != nil || id == "" {
			t.Fatalf("abandoned object cleanup: %q %v", id, err)
		}
	}
	if id, err := repo.expireResultObject(ctx, objects); err != nil || id != "" {
		t.Fatalf("canceled claim left another object: %q %v", id, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("abandoned files: %v %v", files, err)
	}

	t.Setenv("SCHEDULER_WORKER_POOL", "io")
	t.Setenv("SCHEDULER_RESULT_DIR", dir)
	t.Setenv("SCHEDULER_RESULT_RETENTION", "1h")
	post := func(handler http.Handler, expected int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/runs",
			strings.NewReader(`{"topic":"demo.export","payload":{"size_bytes":2097152}}`))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "download")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != expected {
			t.Fatalf("submit export: %d %s", w.Code, w.Body.String())
		}
		return w
	}
	disabled, err := newApp("", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	post(disabled, http.StatusServiceUnavailable)
	exportID := strings.TrimPrefix(post(api, http.StatusAccepted).Header().Get("Location"), "/runs/")
	startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		state, err := repo.runStatus(ctx, "tenant", exportID)
		return state.QueueStatus == "done" && state.ProgressPercent != nil && *state.ProgressPercent == 100, err
	})
	full := get(api, exportID, "", http.StatusOK)
	if full.Body.Len() != 2<<20 || full.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download: length=%d headers=%v", full.Body.Len(), full.Header())
	}
	if partial := get(api, exportID, "bytes=0-7", http.StatusPartialContent); partial.Body.Len() != 8 {
		t.Fatal("unexpected range result")
	}
	get(other, exportID, "", http.StatusNotFound)
	if _, err := db.ExecContext(ctx, "UPDATE scheduler_result_objects SET expires_at = statement_timestamp() WHERE run_id = $1", exportID); err != nil {
		t.Fatal(err)
	}
	get(api, exportID, "", http.StatusGone)
	startSchedulerChild(t, db, "result-gc")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var state string
		err := db.QueryRowContext(ctx, "SELECT state FROM scheduler_result_objects WHERE run_id = $1", exportID).Scan(&state)
		return state == "expired", err
	})
	files, err = os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("expired files: %v %v", files, err)
	}
	get(other, exportID, "", http.StatusNotFound)
}
