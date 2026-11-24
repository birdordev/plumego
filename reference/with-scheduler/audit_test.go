package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func auditHTTPRequest(ctx context.Context, handler http.Handler, method, path, key, reason, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(contract.RequestIDHeader, "untrusted-request")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-Change-Reason", reason)
	handler.ServeHTTP(w, req)
	return w
}

func TestPostgresAuditCapturesActorVersionsAndSafeReason(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	db.SetMaxOpenConns(1)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newAppWithActor("127.0.0.1:0", "test-token", "tenant", "operator-01", repo)
	if err != nil {
		t.Fatal(err)
	}
	const plan = `{"id":"today","expression":"@every 1h","time_zone":"UTC","topic":"demo.record","payload":{"private":"hidden-value"}}`
	created := auditHTTPRequest(ctx, app, "POST", "/schedules", "", "maintenance", plan)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	paused := auditHTTPRequest(ctx, app, "PATCH", "/schedules/today", "", "incident_recovery", `{"paused":true,"version":1}`)
	if paused.Code != 200 {
		t.Fatalf("pause: %d %s", paused.Code, paused.Body)
	}
	stale := auditHTTPRequest(ctx, app, "PATCH", "/schedules/today", "", "operator_request", `{"paused":false,"version":1}`)
	if stale.Code != 409 {
		t.Fatalf("stale: %d %s", stale.Code, stale.Body)
	}
	invalid := auditHTTPRequest(ctx, app, "PATCH", "/schedules/today", "", "free-form secret", `{"paused":false,"version":2}`)
	if invalid.Code != 400 {
		t.Fatalf("reason boundary: %d %s", invalid.Code, invalid.Body)
	}
	running := false
	if _, err := repo.setSchedulePause(ctx, "tenant", "today", pauseCommand{Version: 2, Paused: &running}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT actor, request_id, operation, reason_code, before_state::text, after_state::text
		FROM scheduler_audit WHERE tenant_id = 'tenant' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	expected := []struct {
		op, reason string
		response   *httptest.ResponseRecorder
	}{
		{"schedule.create", "maintenance", created},
		{"schedule.pause", "incident_recovery", paused},
		{"schedule.resume", "schedule.resume", nil},
	}
	for i, want := range expected {
		if !rows.Next() {
			t.Fatalf("missing audit %d: %v", i, rows.Err())
		}
		var actor, operation, reason, before, after string
		var requestID sql.NullString
		if err := rows.Scan(&actor, &requestID, &operation, &reason, &before, &after); err != nil {
			t.Fatal(err)
		}
		if operation != want.op || reason != want.reason || strings.Contains(before+after, "hidden-value") {
			t.Fatalf("audit %d: %q %q %q %q", i, operation, reason, before, after)
		}
		if want.response == nil {
			if actor != "scheduler-system" || requestID.Valid {
				t.Fatalf("transaction settings leaked after commit: %s %v", actor, requestID)
			}
			continue
		}
		if actor != "operator-01" || !requestID.Valid || requestID.String != want.response.Header().Get(contract.RequestIDHeader) ||
			requestID.String == "untrusted-request" {
			t.Fatalf("actor=%s request=%v", actor, requestID)
		}
		if i == 1 && (!strings.Contains(before, `"version": 1`) || !strings.Contains(after, `"version": 2`)) {
			t.Fatalf("version transition %s => %s", before, after)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra audit or error: %v", rows.Err())
	}
}

func TestPostgresAuditFailureRollsBackCancellationAndReplay(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("127.0.0.1:0", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	id, err := repo.submit(ctx, "tenant", "cancel", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE scheduler_audit ADD CONSTRAINT inject_failure CHECK (operation NOT IN ('run.cancel','run.replay'))`); err != nil {
		t.Fatal(err)
	}
	if w := auditHTTPRequest(ctx, app, "POST", "/runs/"+id+"/cancel", "", "maintenance", ""); w.Code != 503 {
		t.Fatalf("cancel failure: %d %s", w.Code, w.Body)
	}
	if status, err := repo.runStatus(ctx, "tenant", id); err != nil || status.QueueStatus != "queued" {
		t.Fatalf("cancellation committed without audit: %+v %v", status, err)
	}
	failNextTask(t, queue, "task failed")
	input := struct {
		replaySpec
		Preview bool `json:"preview"`
	}{replaySpec{Limit: 1, FailedFrom: time.Now().Add(-time.Hour), FailedTo: time.Now().Add(time.Hour)}, false}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	replay := func() *httptest.ResponseRecorder {
		return auditHTTPRequest(ctx, app, "POST", "/dead-letters/replay", "replay-sensitive-key", "correction", string(raw))
	}
	if w := replay(); w.Code != 503 {
		t.Fatalf("replay failure: %d %s", w.Code, w.Body)
	}
	var status string
	var replays, commands, usage int
	if err := db.QueryRowContext(ctx, `SELECT q.status, r.dead_letter_replays,
		(SELECT count(*) FROM scheduler_replay_commands), u.pending_runs
		FROM mq_tasks q JOIN scheduled_runs r ON r.id = q.id
		JOIN scheduler_tenant_usage u ON u.tenant_id = r.tenant_id WHERE q.id = $1`, id).
		Scan(&status, &replays, &commands, &usage); err != nil || status != "dead" || replays != 0 || commands != 0 || usage != 0 {
		t.Fatalf("replay side effects: %s replays=%d commands=%d usage=%d err=%v", status, replays, commands, usage, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduler_audit DROP CONSTRAINT inject_failure"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if w := replay(); w.Code != 202 {
			t.Fatalf("replay: %d %s", w.Code, w.Body)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM scheduler_audit
		WHERE target_id = $1 AND operation = 'run.replay' AND reason_code = 'correction'
		AND after_state->>'replays' = '1'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replay audit=%d err=%v", count, err)
	}
}
