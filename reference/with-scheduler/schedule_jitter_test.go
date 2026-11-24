package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresScheduleJitterPreservesOccurrenceAndPolicy(t *testing.T) {
	for _, mode := range []string{"fixed_rate", "fixed_delay"} {
		t.Run(mode, func(t *testing.T) {
			_, _, db := postgresTestQueues(t)
			ctx := t.Context()
			repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
			app, err := newApp("", "test-token", "tenant", repo)
			if err != nil {
				t.Fatal(err)
			}
			invalid := auditHTTPRequest(ctx, app, "POST", "/schedules", "", "",
				`{"id":"invalid","expression":"@hourly","time_zone":"UTC","jitter_window_ms":60001,"topic":"demo.record","payload":{}}`)
			if invalid.Code != http.StatusBadRequest {
				t.Fatalf("jitter boundary: %d %s", invalid.Code, invalid.Body)
			}
			const window = 60000
			var definitionID string
			if err := db.QueryRowContext(ctx, `SELECT id FROM (SELECT 'jitter-' || i::text id FROM generate_series(1, 64) i) candidates
				WHERE scheduler_jitter_milliseconds('tenant', id, $1) > 8000 LIMIT 1`, window).Scan(&definitionID); err != nil {
				t.Fatal(err)
			}
			plan := scheduleDefinition{Tenant: "tenant", ID: definitionID, Expression: "@every 1h",
				TimeZone: "UTC", Topic: recordTaskType, Payload: json.RawMessage(`{}`), Mode: mode,
				MisfirePolicy: "skip", JitterWindow: window}
			if err := repo.addSchedule(ctx, plan); err != nil {
				t.Fatal(err)
			}
			var nominal, due time.Time
			if err := db.QueryRowContext(ctx, `UPDATE schedules SET next_fire_at = clock_timestamp() - interval '4 seconds'
				WHERE id = $1 RETURNING next_fire_at, next_dispatch_at`, definitionID).Scan(&nominal, &due); err != nil {
				t.Fatal(err)
			}
			offset := due.Sub(nominal)
			if offset < 8*time.Second || offset > time.Duration(window)*time.Millisecond {
				t.Fatalf("offset outside designated window: %s", offset)
			}
			if id, err := repo.materializeNext(ctx); err != nil || id != "" {
				t.Fatalf("future dispatch produced an occurrence: %q %v", id, err)
			}
			if signal, err := repo.readSLO(ctx); err != nil || signal.overdue != 0 {
				t.Fatalf("intentional jitter counted as overdue: %+v %v", signal, err)
			}
			manual, err := repo.triggerSchedule(ctx, "tenant", definitionID, "manual")
			if err != nil {
				t.Fatal(err)
			}
			var immediate bool
			if err := db.QueryRowContext(ctx, `SELECT q.available_at = r.scheduled_at FROM mq_tasks q
				JOIN scheduled_runs r USING(id) WHERE q.id = $1`, manual).Scan(&immediate); err != nil || !immediate {
				t.Fatalf("manual trigger was dispersed: %v", err)
			}
			if err := db.QueryRowContext(ctx, `UPDATE schedules
				SET next_fire_at = clock_timestamp() - $2 * interval '1 millisecond' - interval '50 milliseconds'
				WHERE id = $1 RETURNING next_fire_at, next_dispatch_at`, definitionID, offset.Milliseconds()).Scan(&nominal, &due); err != nil {
				t.Fatal(err)
			}
			metrics := &schedulerMetrics{}
			id, err := repo.materializeOwned(ctx, metrics, shardClaim{})
			if err != nil || id == "" {
				t.Fatalf("eligible occurrence was skipped: %q %v", id, err)
			}
			occurrence, err := repo.plannedOccurrence(ctx, "tenant", definitionID, nominal)
			if err != nil || occurrence.RunID != id {
				t.Fatalf("nominal occurrence changed: %+v %v", occurrence, err)
			}
			triggerSample := metrics.series[latencyLabels{triggerLatency, recordTaskType, recordTaskType, "created"}]
			dispatchSample := metrics.series[latencyLabels{dispatchLatency, recordTaskType, recordTaskType, "created"}]
			if triggerSample.sum < 8 || dispatchSample.sum < 0.05 || dispatchSample.sum > 2 {
				t.Fatalf("intentional delay appears in dispatch SLO: trigger=%v dispatch=%v", triggerSample.sum, dispatchSample.sum)
			}
			var available time.Time
			if err := db.QueryRowContext(ctx, "SELECT available_at FROM mq_tasks WHERE id = $1", id).Scan(&available); err != nil || !available.Equal(due) {
				t.Fatalf("queue availability=%s wanted=%s: %v", available, due, err)
			}
			status, err := repo.scheduleStatus(ctx, "tenant", definitionID)
			if err != nil || status.LastMisfireAction != nil || status.JitterWindow != window {
				t.Fatalf("unexpected status %+v: %v", status, err)
			}
			if mode == "fixed_rate" {
				if status.NextFireAt == nil || !status.NextFireAt.Equal(nominal.Add(time.Hour)) ||
					status.NextDispatchAt == nil || status.NextDispatchAt.Sub(*status.NextFireAt) != offset {
					t.Fatalf("cadence drift: %+v", status)
				}
				before := *status.NextFireAt
				plan.JitterWindow = 0
				if _, err := repo.replaceSchedule(ctx, status.Version, plan); err != nil {
					t.Fatal(err)
				}
				status, err = repo.scheduleStatus(ctx, "tenant", definitionID)
				if err != nil || status.NextFireAt == nil || !status.NextFireAt.Equal(before) ||
					status.NextDispatchAt == nil || !status.NextDispatchAt.Equal(before) {
					t.Fatalf("window change moved nominal cursor: %+v %v", status, err)
				}
			} else if status.WaitingRunID == nil || *status.WaitingRunID != id || status.NextDispatchAt != nil {
				t.Fatalf("fixed delay should wait for completion: %+v", status)
			}
		})
	}
}
