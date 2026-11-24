package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresTaskContractAdmission(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("127.0.0.1:0", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	for i, sample := range []submission{
		{Topic: "unknown.type", Payload: json.RawMessage(`{}`)},
		{Topic: recordTaskType, Payload: json.RawMessage(`{}`), taskOptions: taskOptions{TaskVersion: 99}},
		{Topic: recordTaskType, Payload: json.RawMessage(`{}`), taskOptions: taskOptions{TaskVersion: 2}},
		{Topic: recordTaskType, Payload: json.RawMessage(`{"message":"ok","unexpected":1}`), taskOptions: taskOptions{TaskVersion: 2}},
		{Topic: recordTaskType, Payload: json.RawMessage(`"` + strings.Repeat("x", 64<<10) + `"`)},
		{Topic: recordTaskType, Payload: json.RawMessage(`{}`), taskOptions: taskOptions{Metadata: map[string]string{"label": strings.Repeat("x", 257)}}},
		{Topic: recordTaskType, Payload: json.RawMessage(`{}`), taskOptions: taskOptions{ResourceRefs: []string{"https://example.test/blob"}}},
		{Topic: recordTaskType, Payload: json.RawMessage(`{}`), taskOptions: taskOptions{ResourceRefs: strings.Fields("a b c d e f g h i")}},
		{Topic: recordTaskType, Payload: json.RawMessage(`{}`), taskOptions: taskOptions{ResourceRefs: []string{strings.Repeat("a", 129)}}},
		{Topic: recordTaskType, Payload: json.RawMessage(`{}`), taskOptions: taskOptions{Metadata: map[string]string{"invalid.key": "value"}}},
	} {
		raw, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		if w := auditHTTPRequest(ctx, app, http.MethodPost, "/runs", "invalid", "", string(raw)); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid case %d: %d %s", i, w.Code, w.Body)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM mq_tasks").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid tasks reached queue: count=%d err=%v", count, err)
	}
	first := auditHTTPRequest(ctx, app, http.MethodPost, "/runs", "compatible", "", `{"topic":"demo.record","payload":{"message":"same"}}`)
	explicit := auditHTTPRequest(ctx, app, http.MethodPost, "/runs", "compatible", "", `{"topic":"demo.record","task_version":1,"payload":{"message":"same"}}`)
	different := auditHTTPRequest(ctx, app, http.MethodPost, "/runs", "compatible", "", `{"topic":"demo.record","task_version":2,"payload":{"message":"same"}}`)
	if first.Code != 202 || explicit.Code != 202 || first.Header().Get("Location") == "" ||
		first.Header().Get("Location") != explicit.Header().Get("Location") || different.Code != 409 {
		t.Fatalf("version fingerprint v1=%d alias=%d v2=%d", first.Code, explicit.Code, different.Code)
	}
	valid := auditHTTPRequest(ctx, app, http.MethodPost, "/runs", "new-v2", "",
		`{"topic":"demo.record","task_version":2,"payload":{"message":"new"},"metadata":{"team":"infra"},"resource_refs":["blob_1"]}`)
	if valid.Code != http.StatusAccepted || valid.Header().Get("Location") == "" {
		t.Fatalf("v2 admission: %d %s", valid.Code, valid.Body)
	}
}

func TestLegacySubmissionFingerprintRemainsCompatible(t *testing.T) {
	const original = `{"topic":"demo.record","payload":{"message":"old"},"run_at":"0001-01-01T00:00:00Z"}`
	want := sha256.Sum256([]byte(original))
	for _, version := range []int{0, 1} {
		command, err := prepareSubmission("tenant", "key", submission{Topic: recordTaskType, Payload: json.RawMessage(`{"message":"old"}`),
			taskOptions: taskOptions{TaskVersion: version}})
		if err != nil || command.hash != want {
			t.Fatalf("v%d legacy fingerprint changed: %v", version, err)
		}
	}
}

func TestPostgresScheduledAndBatchTasksRetainContracts(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const tenant = "tenant"
	payload := json.RawMessage(`{"message":"versioned"}`)
	options := taskOptions{TaskVersion: 2, Metadata: map[string]string{"team": "infra"}, ResourceRefs: []string{"blob_123"}}
	definition := scheduleDefinition{Tenant: tenant, ID: "plan", Expression: "@every 1h",
		TimeZone: "UTC", Topic: recordTaskType, Payload: payload, taskOptions: options}
	if err := repo.addSchedule(ctx, definition); err != nil {
		t.Fatal(err)
	}
	manual, err := repo.triggerSchedule(ctx, tenant, definition.ID, "manual-v2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = clock_timestamp() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	automatic, err := repo.materializeNext(ctx)
	if err != nil || automatic == "" {
		t.Fatalf("automatic run=%q err=%v", automatic, err)
	}
	batch, err := repo.submitBatch(ctx, tenant, []batchSubmission{{Key: "batch-v2",
		submission: submission{Topic: recordTaskType, Payload: payload, taskOptions: options}}})
	if err != nil || len(batch) != 1 {
		t.Fatalf("batch length=%d err=%v", len(batch), err)
	}
	definition.taskOptions = taskOptions{}
	if version, err := repo.replaceSchedule(ctx, 1, definition); err != nil || version != 2 {
		t.Fatalf("replace version=%d err=%v", version, err)
	}
	newRun, err := repo.triggerSchedule(ctx, tenant, definition.ID, "manual-v1")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{manual, automatic, batch[0], newRun} {
		var topic, version, metadata, ref string
		err := db.QueryRowContext(ctx, `SELECT topic, meta->>'task_version',
			COALESCE((meta->>'task_options')::jsonb->'metadata'->>'team', ''),
			COALESCE((meta->>'task_options')::jsonb->'resource_refs'->>0, '')
			FROM mq_tasks WHERE id = $1`, id).Scan(&topic, &version, &metadata, &ref)
		if err != nil {
			t.Fatal(err)
		}
		if id == newRun {
			if topic != recordTaskType || version != "1" || metadata != "" || ref != "" {
				t.Fatalf("new contract: topic=%s version=%s metadata=%s ref=%s", topic, version, metadata, ref)
			}
		} else if topic != "demo.record.v2" || version != "2" || metadata != "infra" || ref != "blob_123" {
			t.Fatalf("task lost its stored contract: topic=%s version=%s metadata=%s ref=%s", topic, version, metadata, ref)
		}
	}
}

func TestTaskContractRejectsCorruptStoredEnvelope(t *testing.T) {
	c, _ := lookupTaskContract(recordTaskType, 2)
	job, err := queuedTask("run", "tenant", submission{Topic: recordTaskType, Payload: json.RawMessage(`{"message":"ok"}`),
		taskOptions: taskOptions{TaskVersion: 2}}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	handler := c.handle(func(context.Context, mq.Task) error {
		t.Fatal("invalid envelope reached the business handler")
		return nil
	})
	job.Meta["task_version"] = "99"
	if err := handler(t.Context(), job); err == nil {
		t.Fatal("unknown stored version reached the handler")
	}
}
