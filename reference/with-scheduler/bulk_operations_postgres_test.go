//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresBulkCancellationSurvivesPauseAndExecutorReplacement(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	other, err := newApp("", "test-token", "other", repo)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(tenant, key string) string {
		t.Helper()
		id, err := repo.submit(ctx, tenant, key, submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	for i := range 19 {
		submit("tenant", fmt.Sprintf("initial-%d", i))
	}
	outside := submit("other", "outside")
	request := func(handler http.Handler, method, path, body, key string, expected int) bulkOperation {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != expected {
			t.Fatalf("%s %s returned %d: %s", method, path, w.Code, w.Body.String())
		}
		var response struct{ Data bulkOperation }
		if w.Code < 300 {
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return response.Data
	}
	const endpoint = "/bulk-cancellations"
	const filter = `{"topic":"demo.record","limit":19}`
	op := request(app, http.MethodPost, endpoint, filter, "batch", http.StatusCreated)
	path := endpoint + "/" + op.ID
	if op.State != "planned" || op.Total != 19 || op.Processed != 0 || len(op.Items) != 19 {
		t.Fatalf("invalid preview: %+v", op)
	}
	if id, err := repo.processBulkCancel(ctx); err != nil || id != "" {
		t.Fatalf("executed unapproved preview: %q %v", id, err)
	}
	late := submit("tenant", "after-preview")
	again := request(app, http.MethodPost, endpoint, filter, "batch", http.StatusCreated)
	if again.ID != op.ID || again.Total != 19 {
		t.Fatalf("idempotency did not preserve selection: %+v", again)
	}
	request(app, http.MethodPost, endpoint, `{"topic":"demo.record","limit":1}`, "batch", http.StatusConflict)
	request(other, http.MethodGet, path, "", "", http.StatusNotFound)
	request(other, http.MethodPatch, path, `{"paused":false}`, "", http.StatusNotFound)
	request(app, http.MethodPatch, path, `{"paused":false}`, "", http.StatusOK)
	if _, err := db.ExecContext(ctx, `ALTER TABLE scheduler_bulk_items ADD CONSTRAINT inject_bulk_failure
		CHECK (outcome <> 'canceled')`); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.processBulkCancel(ctx); err == nil || id != "" {
		t.Fatalf("injected batch failure: %q %v", id, err)
	}
	if current := request(app, http.MethodGet, path, "", "", http.StatusOK); current.Processed != 0 {
		t.Fatalf("failed batch persisted progress: %+v", current)
	}
	var queued int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_bulk_items i
		JOIN mq_tasks q ON q.id = i.run_id WHERE i.operation_id = $1 AND q.status = 'queued'`, op.ID).Scan(&queued); err != nil || queued != 19 {
		t.Fatalf("failed batch changed queue state: %d %v", queued, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduler_bulk_items DROP CONSTRAINT inject_bulk_failure"); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.processBulkCancel(ctx); err != nil || id != op.ID {
		t.Fatalf("first batch: %q %v", id, err)
	}
	paused := request(app, http.MethodPatch, path, `{"paused":true}`, "", http.StatusOK)
	if paused.State != "paused" || paused.Processed != 16 {
		t.Fatalf("pause did not preserve batch progress: %+v", paused)
	}
	changed := op.Items[18].RunID
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET updated_at = updated_at + interval '1 second' WHERE id = $1", changed); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.processBulkCancel(ctx); err != nil || id != "" {
		t.Fatalf("executor advanced paused plan: %q %v", id, err)
	}
	request(app, http.MethodPatch, path, `{"paused":false}`, "", http.StatusOK)
	startSchedulerChild(t, db, "bulk")
	var complete bulkOperation
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var err error
		complete, err = repo.readBulkOperation(ctx, "tenant", op.ID)
		return complete.State == "completed", err
	})
	if complete.Processed != 19 || complete.Items[18].Outcome != "changed" {
		t.Fatalf("version conflict was not recorded: %+v", complete)
	}
	for _, item := range complete.Items[:18] {
		if item.Outcome != "canceled" {
			t.Fatalf("unexpected item result: %+v", item)
		}
	}
	for _, id := range []string{outside, late, changed} {
		var status string
		if err := db.QueryRowContext(ctx, "SELECT status FROM mq_tasks WHERE id = $1", id).Scan(&status); err != nil || status != "queued" {
			t.Fatalf("unselected or changed task %s: %s %v", id, status, err)
		}
	}
}
