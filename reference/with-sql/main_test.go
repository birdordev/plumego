package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestItemLifecycleAndTransactionalOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixtures.db")
	if err := run(t.Context(), "", path, "", modeMigrate); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), "", path, "", modeMigrate); err != nil {
		t.Fatalf("migration did not converge: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	repo := repository{db: db}
	app, err := newApp("127.0.0.1:0", "test-key", repo)
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, path, body, version, credentials string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "case-1")
		req.Header.Set("If-Match", version)
		if credentials != "" {
			req.Header.Set("Authorization", credentials)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	for _, credentials := range []string{"", "Bearer invalid-token"} {
		w := send("POST", "/items", `{"name":"one"}`, "", credentials)
		if got := w.Header().Get("WWW-Authenticate"); w.Code != http.StatusUnauthorized || got != `Bearer realm="sql-api"` {
			t.Fatalf("credentials supplied=%t: status=%d challenge=%q", credentials != "", w.Code, got)
		}
	}

	created := send("POST", "/items", `{"name":"one"}`, "", "Bearer test-key")
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	location, originalTag := created.Header().Get("Location"), created.Header().Get("ETag")
	updated := send("PUT", location, `{"name":"two"}`, originalTag, "Bearer test-key")
	if updated.Code != 200 || updated.Header().Get("ETag") == originalTag {
		t.Fatalf("update: %d %s", updated.Code, updated.Body)
	}
	stale := send("PUT", location, `{"name":"stale"}`, originalTag, "Bearer test-key")
	if stale.Code != 412 {
		t.Fatalf("stale write: %d %s", stale.Code, stale.Body)
	}
	replay := send("POST", "/items", `{"name":"one"}`, "", "Bearer test-key")
	if replay.Code != 201 || replay.Header().Get("Idempotency-Replayed") != "true" ||
		replay.Header().Get("Location") != location || replay.Body.String() != created.Body.String() {
		t.Fatalf("replay did not preserve the original response: %d %s", replay.Code, replay.Body)
	}
	if got := send("POST", "/items", `{"name":"different"}`, "", "Bearer test-key"); got.Code != 409 {
		t.Fatalf("conflicting key = %d", got.Code)
	}
	read := send("GET", location, "", "", "Bearer test-key")
	var current struct{ Data item }
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &current) != nil || current.Data.Name != "two" {
		t.Fatalf("read after update: %d %s", read.Code, read.Body)
	}

	var attempted int64
	sinkErr := errors.New("fixture sink unavailable")
	if _, err := repo.sendNext(t.Context(), func(_ context.Context, event outboxEvent) error {
		attempted = event.ID
		return sinkErr
	}); !errors.Is(err, sinkErr) {
		t.Fatalf("first publish error = %v", err)
	}
	if sent, err := repo.sendNext(t.Context(), func(_ context.Context, event outboxEvent) error {
		if event.ID != attempted || event.ItemID != current.Data.ID {
			t.Fatalf("retry changed event identity: %+v", event)
		}
		return nil
	}); !sent || err != nil {
		t.Fatalf("retry: sent=%v err=%v", sent, err)
	}
	if sent, err := repo.sendNext(t.Context(), func(_ context.Context, _ outboxEvent) error {
		t.Fatal("duplicate event from replay")
		return nil
	}); sent || err != nil {
		t.Fatalf("extra event: sent=%v err=%v", sent, err)
	}

	deleted := send("DELETE", location, "", read.Header().Get("ETag"), "Bearer test-key")
	if deleted.Code != 204 || deleted.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body)
	}
	if got := send("GET", location, "", "", "Bearer test-key"); got.Code != 404 {
		t.Fatalf("read deleted item = %d", got.Code)
	}
}

func TestConcurrentCreatesCommitOneRecordAndOneEvent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	repo := repository{db: db}
	ctx := t.Context()
	if err := repo.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result createdItem
		err    error
	}
	const requests = 16
	outcomes := make(chan outcome, requests)
	start := make(chan struct{})
	now := time.Now()
	for range requests {
		go func() {
			<-start
			result, err := repo.create(ctx, "concurrent-key", "same-fingerprint", "one", now)
			outcomes <- outcome{result, err}
		}()
	}
	close(start)
	var id int64
	creations := 0
	for range requests {
		got := <-outcomes
		if got.err != nil {
			t.Fatal(got.err)
		}
		if id == 0 {
			id = got.result.Item.ID
		}
		if got.result.Item.ID != id {
			t.Fatalf("same key produced multiple item IDs: %d and %d", id, got.result.Item.ID)
		}
		if !got.result.Replay {
			creations++
		}
	}
	var events int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM outbox").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if creations != 1 || events != 1 {
		t.Fatalf("creations=%d, events=%d", creations, events)
	}
}

type droppedResponseWriter struct {
	header    http.Header
	status    int
	attempted bool
}

func (w *droppedResponseWriter) Header() http.Header    { return w.header }
func (w *droppedResponseWriter) WriteHeader(status int) { w.status = status }
func (w *droppedResponseWriter) Write([]byte) (int, error) {
	w.attempted = true
	return 0, errors.New("fixture connection closed before response body")
}

func TestReplayAfterLostResponseAndDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.db")
	if err := run(t.Context(), "", path, "", modeMigrate); err != nil {
		t.Fatal(err)
	}
	openApp := func() (http.Handler, *sql.DB) {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		db.SetMaxOpenConns(1)
		app, err := newApp("127.0.0.1:0", "test-key", repository{db: db})
		if err != nil {
			t.Fatal(err)
		}
		return app, db
	}
	send := func(app http.Handler, w http.ResponseWriter) {
		req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"name":"replayed"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "reopen-after-loss")
		req.Header.Set("Authorization", "Bearer test-key")
		app.ServeHTTP(w, req)
	}

	app, db := openApp()
	lost := &droppedResponseWriter{header: make(http.Header)}
	send(app, lost)
	if !lost.attempted || lost.status != http.StatusCreated {
		t.Fatalf("did not reach the response write: status=%d attempted=%v", lost.status, lost.attempted)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, db := openApp()
	replay := httptest.NewRecorder()
	send(restarted, replay)
	var result struct{ Data item }
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotency-Replayed") != "true" ||
		replay.Header().Get("Location") != lost.header.Get("Location") ||
		replay.Header().Get("ETag") != lost.header.Get("ETag") ||
		json.Unmarshal(replay.Body.Bytes(), &result) != nil || result.Data.Name != "replayed" {
		t.Fatalf("invalid replay after reopening: %d %v %s", replay.Code, replay.Header(), replay.Body)
	}
	var rows, events int
	if err := db.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM items), (SELECT count(*) FROM outbox)").
		Scan(&rows, &events); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || events != 1 {
		t.Fatalf("unexpected side effects after retry: items=%d outbox=%d", rows, events)
	}
}

func TestStandaloneWorkerRecoversDeliveryAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "worker.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, stmt := range []string{
		"CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL, version INTEGER NOT NULL)",
		"INSERT INTO items VALUES (5, 'persisted', 1)",
		"CREATE TABLE create_receipts (key TEXT PRIMARY KEY, expires_at INTEGER NOT NULL)",
		"CREATE TABLE outbox (id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL, kind TEXT NOT NULL)",
		"INSERT INTO outbox VALUES (17, 5, 'item.created')",
		"PRAGMA user_version = 1",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(ctx, "", path, "", modeWorker); err == nil || !strings.Contains(err.Error(), "schema version 1") {
		t.Fatalf("worker accepted stale schema: %v", err)
	}
	if err := run(ctx, "", path, "", modeMigrate); err != nil {
		t.Fatal(err)
	}
	repo := repository{db: db}
	if version, err := repo.schemaVersion(ctx); err != nil || version != currentSchemaVersion {
		t.Fatalf("upgraded schema version = %d, err = %v", version, err)
	}
	interrupted := errors.New("fixture interrupted before acknowledgment")
	if _, err := repo.sendNext(ctx, func(ctx context.Context, event outboxEvent) error {
		if err := repo.recordDelivery(ctx, event); err != nil {
			return err
		}
		return interrupted
	}); !errors.Is(err, interrupted) {
		t.Fatalf("expected interruption after recording the delivery, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	observer, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	// 传入无效的 HTTP 地址，确保独立 worker 不会创建 HTTP 监听器。
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- run(workerCtx, "not an address", path, "", modeWorker) }()
	defer func() {
		stopWorker()
		if err := <-workerDone; err != nil {
			t.Errorf("standalone worker exited with %v", err)
		}
	}()
	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
	for {
		var pending, delivered int
		var name string
		if err := observer.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM outbox),
			(SELECT count(*) FROM delivered_events WHERE event_id = 17 AND item_id = 5 AND kind = 'item.created'),
			(SELECT name FROM items WHERE id = 5)`).Scan(&pending, &delivered, &name); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			if delivered != 1 || name != "persisted" {
				t.Fatalf("migration or retry lost data: delivered=%d item=%q", delivered, name)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker did not acknowledge the existing event")
		case <-timer.C:
		}
	}
}
