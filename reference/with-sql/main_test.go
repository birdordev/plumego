package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestItemLifecycleAndTransactionalOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixtures.db")
	if err := run(t.Context(), "", path, "", true); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), "", path, "", true); err != nil {
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
	send := func(method, path, body, version string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "case-1")
		req.Header.Set("If-Match", version)
		if authenticated {
			req.Header.Set("Authorization", "Bearer test-key")
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	if got := send("POST", "/items", `{"name":"one"}`, "", false); got.Code != 401 {
		t.Fatalf("unauthorized status = %d", got.Code)
	}

	created := send("POST", "/items", `{"name":"one"}`, "", true)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	location, originalTag := created.Header().Get("Location"), created.Header().Get("ETag")
	updated := send("PUT", location, `{"name":"two"}`, originalTag, true)
	if updated.Code != 200 || updated.Header().Get("ETag") == originalTag {
		t.Fatalf("update: %d %s", updated.Code, updated.Body)
	}
	stale := send("PUT", location, `{"name":"stale"}`, originalTag, true)
	if stale.Code != 412 {
		t.Fatalf("stale write: %d %s", stale.Code, stale.Body)
	}
	replay := send("POST", "/items", `{"name":"one"}`, "", true)
	if replay.Code != 201 || replay.Header().Get("Idempotency-Replayed") != "true" ||
		replay.Header().Get("Location") != location || replay.Body.String() != created.Body.String() {
		t.Fatalf("replay did not preserve the original response: %d %s", replay.Code, replay.Body)
	}
	if got := send("POST", "/items", `{"name":"different"}`, "", true); got.Code != 409 {
		t.Fatalf("conflicting key = %d", got.Code)
	}
	read := send("GET", location, "", "", true)
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

	deleted := send("DELETE", location, "", read.Header().Get("ETag"), true)
	if deleted.Code != 204 || deleted.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body)
	}
	if got := send("GET", location, "", "", true); got.Code != 404 {
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
