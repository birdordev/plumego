package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func failNextTask(t *testing.T, queue *mq.TaskQueue, reason string) mq.Task {
	t.Helper()
	ctx := t.Context()
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "dead-letter-test", Limit: 1})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("reserve: %+v %v", tasks, err)
	}
	task := tasks[0]
	if err := queue.MoveToDLQ(ctx, task.ID, task.LeaseOwner, reason, time.Now()); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPostgresDeadLetterHistoryKeepsScheduleAndAttempt(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: "tenant", ID: "nightly", Expression: "@every 1h", TimeZone: "UTC",
		Topic: "demo.record", Payload: json.RawMessage(`{"private":"payload"}`)}); err != nil {
		t.Fatal(err)
	}
	first, err := repo.triggerSchedule(ctx, "tenant", "nightly", "first")
	if err != nil {
		t.Fatal(err)
	}
	if task := failNextTask(t, queue, "source raw secret text"); task.ID != first {
		t.Fatal("wrong task")
	}
	second, err := repo.triggerSchedule(ctx, "tenant", "nightly", "second")
	if err != nil {
		t.Fatal(err)
	}
	if task := failNextTask(t, queue, "permanent task failure"); task.ID != second {
		t.Fatal("wrong task")
	}
	for _, id := range []string{first, second} {
		page, err := repo.deadLetters(ctx, "tenant", id, 0)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("history: %+v %v", page, err)
		}
		entry := page.Items[0]
		if entry.RunID != id || entry.ScheduleID == nil || *entry.ScheduleID != "nightly" ||
			entry.ScheduleVersion == nil || *entry.ScheduleVersion != 1 || entry.Attempt == nil || *entry.Attempt != 1 || entry.TaskVersion != "1" {
			t.Fatalf("snapshot: %+v", entry)
		}
		serialized, err := json.Marshal(entry)
		if err != nil || strings.Contains(string(serialized), "secret") || strings.Contains(string(serialized), "payload") {
			t.Fatalf("unsafe history: %s %v", serialized, err)
		}
	}
	if _, err := repo.deadLetters(ctx, "other", first, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant history: %v", err)
	}
}

func TestPostgresReplayPreservesAttemptsAndEnforcesBudget(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	input := submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}
	id, err := repo.submit(ctx, "tenant", "failed", input)
	if err != nil {
		t.Fatal(err)
	}
	task := failNextTask(t, queue, "permanent task failure")
	if task.ID != id {
		t.Fatal("wrong task")
	}
	if _, err := repo.submit(ctx, "other", "failed", input); err != nil {
		t.Fatal(err)
	}
	failNextTask(t, queue, "permanent task failure")
	if _, err := repo.submit(ctx, "tenant", "unknown", input); err != nil {
		t.Fatal(err)
	}
	failNextTask(t, queue, "task outcome unknown")

	spec := replaySpec{FailedFrom: time.Now().Add(-time.Hour), FailedTo: time.Now().Add(time.Hour), Limit: 25}
	items, err := selectReplay(ctx, db, "tenant", spec, false)
	if err != nil || len(items) != 1 || items[0].RunID != id {
		t.Fatalf("preview: %+v %v", items, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			replayed, err := repo.replayDeadLetters(ctx, "tenant", "command-1", spec)
			if err != nil || len(replayed) != 1 || replayed[0] != items[0] {
				t.Errorf("concurrent replay: %+v %v", replayed, err)
			}
		})
	}
	wg.Wait()
	mismatch := spec
	mismatch.Limit--
	if _, err := repo.replayDeadLetters(ctx, "tenant", "command-1", mismatch); !errors.Is(err, errRequestConflict) {
		t.Fatalf("different request under same key: %v", err)
	}
	if task := failNextTask(t, queue, "permanent task failure"); task.ID != id || task.Attempts != 2 {
		t.Fatalf("first replay reset lineage: %+v", task)
	}
	if _, err := db.ExecContext(ctx, `UPDATE scheduler_replay_budget SET window_start = date_trunc('minute',clock_timestamp()), used = 25`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.replayDeadLetters(ctx, "tenant", "budget-check", spec); !errors.Is(err, errTenantCapacity) {
		t.Fatalf("budget result: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE scheduler_replay_budget SET used = 0"); err != nil {
		t.Fatal(err)
	}
	if items, err := repo.replayDeadLetters(ctx, "tenant", "command-2", spec); err != nil || len(items) != 1 {
		t.Fatalf("second replay: %+v %v", items, err)
	}
	if task := failNextTask(t, queue, "permanent task failure"); task.ID != id || task.Attempts != 3 {
		t.Fatalf("second replay reset lineage: %+v", task)
	}
	if items, err := repo.replayDeadLetters(ctx, "tenant", "command-3", spec); err != nil || len(items) != 0 {
		t.Fatalf("exhausted task replayed again: %+v %v", items, err)
	}
	page, err := repo.deadLetters(ctx, "tenant", id, 0)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("history: %+v %v", page, err)
	}
	for i, item := range page.Items {
		if item.Attempt == nil || *item.Attempt != 3-i || (item.ReplayedAt != nil) != (i > 0) {
			t.Fatalf("unexpected attempt history: %+v", item)
		}
	}
}

func TestPostgresDeadLetterHTTPPreviewAndCommit(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(t.Context(), "tenant", "failed", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	failNextTask(t, queue, "task age limit exceeded")
	app, err := newApp("127.0.0.1:0", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, key, body string, auth bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if auth {
			r.Header.Set("Authorization", "Bearer test-token")
		}
		r.Header.Set("Idempotency-Key", key)
		app.ServeHTTP(w, r)
		return w
	}
	input := struct {
		replaySpec
		Preview bool `json:"preview"`
	}{replaySpec{Failure: "age_limit", Limit: 1, FailedFrom: time.Now().Add(-time.Hour), FailedTo: time.Now().Add(time.Hour)}, true}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/dead-letters/replay", "", string(raw), false); w.Code != 401 {
		t.Fatalf("unauthorized preview: %d", w.Code)
	}
	preview := request("POST", "/dead-letters/replay", "", string(raw), true)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), id) {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body)
	}
	if got, err := repo.runStatus(t.Context(), "tenant", id); err != nil || got.QueueStatus != "dead" {
		t.Fatalf("preview mutated run: %+v %v", got, err)
	}
	input.Preview = false
	raw, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w := request("POST", "/dead-letters/replay", "command", string(raw), true)
		if w.Code != 202 || !strings.Contains(w.Body.String(), id) {
			t.Fatalf("commit or replay: %d %s", w.Code, w.Body)
		}
	}
	w := request("GET", "/runs/"+id+"/dead-letters", "", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "replayed_at") {
		t.Fatalf("history: %d %s", w.Code, w.Body)
	}
}
