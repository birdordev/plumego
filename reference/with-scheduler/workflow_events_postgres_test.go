//go:build unix

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresEventCursorOrdersDeduplicatesAndResumesReplay(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	t.Setenv("SCHEDULER_WORKFLOW_RELEASE_ACTOR", "publisher")
	api := func(tenant, actor string) http.Handler {
		t.Helper()
		app, err := newAppWithActor("", "token", tenant, actor, repo)
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	admin, producer, outsider := api("tenant", "publisher"), api("tenant", "producer"), api("other", "producer")
	request := func(app http.Handler, method, path, key, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	const graph = `{"nodes":[{"id":"record","task":{"topic":"demo.record","payload":{}}}]}`
	const source = "/event-sources/orders"
	request(admin, http.MethodPut, "/workflow-definitions/review/versions/1", "", graph, http.StatusNoContent)
	request(admin, http.MethodPut, "/workflow-definitions/review/versions/2", "", graph, http.StatusNoContent)
	request(admin, http.MethodPut, "/workflow-definitions/review/active", "activate", `{"version":2,"expected_version":0}`, http.StatusNoContent)
	const sourceConfig = `{"definition_name":"review","definition_version":1,"ingest_actor":"producer","max_lateness_seconds":60}`
	request(producer, http.MethodPut, source, "", sourceConfig, http.StatusForbidden)
	request(admin, http.MethodPut, source, "", sourceConfig, http.StatusNoContent)
	request(admin, http.MethodPut, source, "", sourceConfig, http.StatusNoContent)
	event := func(offset int64, id string, at time.Time) string {
		t.Helper()
		raw, err := json.Marshal(workflowEventInput{Offset: offset, ID: id, OccurredAt: at, Payload: json.RawMessage(`{"quantity":7,"currency":"CNY"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	at := time.Now().UTC()
	latest := event(3, "same", at)
	request(outsider, http.MethodPost, source+"/events", "", latest, http.StatusNotFound)
	request(admin, http.MethodPost, source+"/events", "", latest, http.StatusForbidden)
	request(producer, http.MethodPost, source+"/events", "", latest, http.StatusAccepted)
	request(producer, http.MethodPost, source+"/events", "", event(2, "late", at.Add(-time.Hour)), http.StatusAccepted)
	request(producer, http.MethodPost, source+"/events", "", event(3, "different", at), http.StatusConflict)
	request(producer, http.MethodPost, source+"/events", "", event(4, "same", at.Add(time.Second)), http.StatusConflict)
	sourceStatus := request(admin, http.MethodGet, source, "", "", http.StatusOK)
	if !strings.Contains(sourceStatus.Body.String(), `"highest_offset":3`) || !strings.Contains(sourceStatus.Body.String(), `"cursor":0`) {
		t.Fatalf("wrong gap status: %s", sourceStatus.Body.String())
	}
	if id, err := repo.consumeWorkflowEvent(ctx); err != nil || id != "" {
		t.Fatalf("consumer skipped a gap: %q %v", id, err)
	}
	request(admin, http.MethodPost, source+"/replays", "too-far", `{"from":1,"to":3}`, http.StatusConflict)
	first := strings.Replace(event(1, "same", at), `{"quantity":7,"currency":"CNY"}`, `{"currency":"CNY","quantity":7}`, 1)
	request(producer, http.MethodPost, source+"/events", "", first, http.StatusAccepted)
	request(producer, http.MethodPost, source+"/events", "", first, http.StatusAccepted)
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduler_source_events ADD CONSTRAINT force_consumer_rollback CHECK(outcome<>'triggered')"); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.consumeWorkflowEvent(ctx); err == nil || id != "" {
		t.Fatalf("injected consumption failure: %q %v", id, err)
	}
	var cursor, workflows int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT cursor_offset FROM scheduler_event_sources),
		(SELECT count(*) FROM scheduler_workflows)`).Scan(&cursor, &workflows); err != nil || cursor != 0 || workflows != 0 {
		t.Fatalf("workflow and cursor did not roll back together: %d %d %v", cursor, workflows, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduler_source_events DROP CONSTRAINT force_consumer_rollback"); err != nil {
		t.Fatal(err)
	}
	replica1 := startSchedulerChild(t, db, "events")
	replica2 := startSchedulerChild(t, db, "events")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var offset int
		err := db.QueryRowContext(ctx, "SELECT cursor_offset FROM scheduler_event_sources").Scan(&offset)
		return offset == 3, err
	})
	killSchedulerChild(t, replica1)
	killSchedulerChild(t, replica2)
	var id string
	if err := db.QueryRowContext(ctx, `SELECT workflow_id FROM scheduler_source_events
		WHERE source_offset=1 AND outcome='triggered'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	flow, err := repo.workflowStatus(ctx, "tenant", id)
	if err != nil || flow.DefinitionVersion != 1 || flow.EventSource != "orders" || flow.EventOffset == nil || *flow.EventOffset != 1 {
		t.Fatalf("pinned event workflow: %+v %v", flow, err)
	}
	for path, outcome := range map[string]string{"2": "late", "3": "duplicate"} {
		w := request(producer, http.MethodGet, source+"/events/"+path, "", "", http.StatusOK)
		if !strings.Contains(w.Body.String(), `"outcome":"`+outcome+`"`) {
			t.Fatalf("event %s: %s", path, w.Body.String())
		}
	}
	request(outsider, http.MethodGet, source+"/events/1", "", "", http.StatusNotFound)
	w := request(admin, http.MethodPost, source+"/replays", "replay", `{"from":1,"to":3}`, http.StatusAccepted)
	replayPath := w.Header().Get("Location")
	if request(admin, http.MethodPost, source+"/replays", "replay", `{"from":1,"to":3}`, http.StatusAccepted).Header().Get("Location") != replayPath {
		t.Fatal("replay key did not return original command")
	}
	replayID := strings.TrimPrefix(replayPath, source+"/replays/")
	if processed, err := repo.consumeEventReplay(ctx); err != nil || processed != replayID {
		t.Fatalf("first replay position: %q %v", processed, err)
	}
	if state, err := repo.eventReplayStatus(ctx, "tenant", "orders", replayID); err != nil || state.Next != 2 || state.Finished {
		t.Fatalf("replay checkpoint: %+v %v", state, err)
	}
	request(producer, http.MethodPost, source+"/events", "", event(4, "new", time.Now()), http.StatusAccepted)
	startSchedulerChild(t, db, "events")
	startSchedulerChild(t, db, "workflow")
	t.Setenv("SCHEDULER_WORKER_POOL", "short")
	startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var succeeded int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_workflows WHERE state='succeeded'").Scan(&succeeded); err != nil {
			return false, err
		}
		state, err := repo.eventReplayStatus(ctx, "tenant", "orders", replayID)
		return succeeded == 2 && state.Finished, err
	})
	if err := db.QueryRowContext(ctx, `SELECT (SELECT cursor_offset FROM scheduler_event_sources),
		(SELECT count(*) FROM scheduler_workflows)`).Scan(&cursor, &workflows); err != nil || cursor != 4 || workflows != 2 {
		t.Fatalf("replay changed live cursor or duplicated workflow: %d %d %v", cursor, workflows, err)
	}
}
