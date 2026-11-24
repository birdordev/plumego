//go:build unix

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresSchedulerLatencyAcrossProcesses(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	options := taskOptions{TaskVersion: 2}
	payload := json.RawMessage(`{"message":"private-text"}`)
	if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: "test-tenant-id", ID: "test-schedule-id",
		Topic: recordTaskType, Expression: "@hourly", TimeZone: "UTC", Payload: payload, taskOptions: options}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = clock_timestamp() - interval '10 seconds'"); err != nil {
		t.Fatal(err)
	}
	start := func(role string) string {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SCHEDULER_METRICS_ADDR", address)
		startSchedulerChild(t, db, role)
		return "http://" + address + "/metrics"
	}
	client := &http.Client{Timeout: time.Second}
	scrape := func(url string, lines ...string) string {
		t.Helper()
		var result string
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return false, err
			}
			resp, err := client.Do(req)
			if err != nil {
				return false, nil
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return false, err
			}
			result = string(body)
			for _, line := range lines {
				if !strings.Contains(result, line+"\n") {
					return false, nil
				}
			}
			return resp.StatusCode == http.StatusOK, nil
		})
		return result
	}
	const labels = `queue="demo.record.v2",task_type="demo.record",result=`
	const triggerLabels = labels + `"created"`
	const queueLabels = labels + `"started"`
	const workerLabels = labels + `"succeeded"`
	const ackLabels = labels + `"ack"`
	schedulerURL := start("scheduler")
	triggerOutput := scrape(schedulerURL,
		`plumego_scheduler_trigger_delay_seconds_count{`+triggerLabels+`} 1`,
		`plumego_scheduler_trigger_delay_seconds_bucket{`+triggerLabels+`,le="5"} 0`)
	workerURL := start("worker")
	scrape(workerURL,
		`plumego_scheduler_queue_delay_seconds_count{`+queueLabels+`} 1`,
		`plumego_scheduler_queue_delay_seconds_bucket{`+queueLabels+`,le="5"} 0`,
		`plumego_scheduler_execution_duration_seconds_count{`+workerLabels+`} 1`,
		`plumego_scheduler_execution_duration_seconds_bucket{`+workerLabels+`,le="5"} 1`,
		`plumego_scheduler_finalization_delay_seconds_count{`+ackLabels+`} 1`)
	if _, err := db.ExecContext(ctx, `CREATE SEQUENCE rejected_metric_acks;
		CREATE FUNCTION reject_metric_ack() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM nextval('rejected_metric_acks');
			RAISE EXCEPTION 'injected ack failure';
		END $$;
		CREATE TRIGGER reject_metric_ack BEFORE UPDATE OF status ON mq_tasks
			FOR EACH ROW WHEN (OLD.status = 'leased' AND NEW.status = 'done') EXECUTE FUNCTION reject_metric_ack()`); err != nil {
		t.Fatal(err)
	}
	id, err := repo.submit(ctx, "test-tenant-id", "metric-recovery", submission{
		Topic: recordTaskType, Payload: payload, taskOptions: options})
	if err != nil {
		t.Fatal(err)
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		history, err := repo.runAttempts(ctx, "test-tenant-id", id, 0)
		if err != nil || len(history.Items) != 1 || history.Items[0].ConfirmationError != "retryable" {
			return false, err
		}
		var attempts int
		err = db.QueryRowContext(ctx, "SELECT last_value FROM rejected_metric_acks").Scan(&attempts)
		return attempts >= 2, err
	})
	if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_metric_ack ON mq_tasks"); err != nil {
		t.Fatal(err)
	}
	finalizerURL := start("finalizer")
	finalizerOutput := scrape(finalizerURL,
		`plumego_scheduler_finalization_delay_seconds_count{`+ackLabels+`} 1`,
		`plumego_scheduler_finalization_delay_seconds_bucket{`+ackLabels+`,le="2"} 0`)
	workerOutput := scrape(workerURL,
		`plumego_scheduler_finalization_delay_seconds_count{`+ackLabels+`} 1`,
		`plumego_scheduler_execution_duration_seconds_count{`+workerLabels+`} 2`)
	for _, value := range []string{id, "test-tenant-id", "test-schedule-id", "private-text", "metric-recovery"} {
		if strings.Contains(triggerOutput+workerOutput+finalizerOutput, value) {
			t.Errorf("unexpected data in metrics: %q", value)
		}
	}
}
