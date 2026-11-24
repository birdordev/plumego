//go:build unix

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/birdordev/plumego/x/observability/tracer"
)

func TestPostgresRequestTraceSurvivesWorkerRetryAndReplay(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	tracing, err := newSchedulerTracing(false)
	if err != nil {
		t.Fatal(err)
	}
	requestSpans := make(map[string]*tracer.Span)
	finish := tracing.finish
	tracing.finish = func(span *tracer.Span) {
		finish(span)
		requestSpans[span.Attributes["request_id"].(string)] = span
	}
	app, err := newTracedApp("", "trace-test-credential", "tenant", "", repo, tracing, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, key, body string, status int) (*httptest.ResponseRecorder, traceSource) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer trace-test-credential")
		req.Header.Set("Cookie", "session=trace-test-cookie")
		req.Header.Set("Baggage", "secret=trace-test-baggage")
		req.Header.Set("Traceparent", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
		req.Header.Set(contract.RequestIDHeader, "trace-test-request")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		requestID := w.Header().Get(contract.RequestIDHeader)
		span := requestSpans[requestID]
		if span == nil {
			t.Fatalf("missing request span for %s", requestID)
		}
		return w, traceSource{requestID, string(span.TraceID), string(span.ID)}
	}
	const taskPayload = `{"topic":"demo.record","payload":{}}`
	first, origin := request(http.MethodPost, "/runs", "trace", taskPayload, http.StatusAccepted)
	runID := strings.TrimPrefix(first.Header().Get("Location"), "/runs/")
	duplicate, _ := request(http.MethodPost, "/runs", "trace", taskPayload, http.StatusAccepted)
	if !origin.valid() || duplicate.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatalf("invalid origin or idempotency replay: %+v", origin)
	}
	exec := func(query string, parameters ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, parameters...); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE mq_tasks SET max_attempts = 2 WHERE id = $1", runID)
	exec("ALTER TABLE scheduled_results ADD CONSTRAINT reject_trace_result CHECK (false)")
	t.Setenv("SCHEDULER_TRACE_LOG", "1")
	worker := startSchedulerChild(t, db, "worker")
	checkStatus := func(want string, executions int) {
		t.Helper()
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			result, err := repo.runStatus(ctx, "tenant", runID)
			return result.QueueStatus == want && result.ExecutionAttempts == executions, err
		})
	}
	checkStatus("dead", 2)
	killSchedulerChild(t, worker)
	exec("ALTER TABLE scheduled_results DROP CONSTRAINT reject_trace_result")
	replayBody, err := json.Marshal(struct {
		replaySpec
		Preview bool `json:"preview"`
	}{replaySpec{FailedFrom: time.Now().Add(-time.Hour), FailedTo: time.Now().Add(time.Hour), Limit: 1}, false})
	if err != nil {
		t.Fatal(err)
	}
	_, replay := request(http.MethodPost, "/dead-letters/replay", "replay-trace", string(replayBody), http.StatusAccepted)
	replacement := startSchedulerChild(t, db, "worker")
	checkStatus("done", 3)

	var raw string
	if err := db.QueryRowContext(ctx, "SELECT meta::text FROM mq_tasks WHERE id = $1", runID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var meta map[string]string
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatal(err)
	}
	if sourceFromTask(meta, "origin") != origin || sourceFromTask(meta, "replay") != replay {
		t.Fatalf("persisted lineage mismatch: %s", raw)
	}
	workerLog, err := os.ReadFile(worker.Stdout.(*os.File).Name())
	if err != nil {
		t.Fatal(err)
	}
	replacementLog, err := os.ReadFile(replacement.Stdout.(*os.File).Name())
	if err != nil {
		t.Fatal(err)
	}
	workerLog = append(workerLog, replacementLog...)
	workerTraces := make(map[tracer.TraceID]bool)
	for _, line := range strings.Split(string(workerLog), "\n") {
		_, record, ok := strings.Cut(line, "scheduler span ")
		if !ok {
			continue
		}
		var span tracer.Span
		if err := json.Unmarshal([]byte(record), &span); err != nil {
			t.Fatal(err)
		}
		if span.Attributes["run_id"] != runID {
			continue
		}
		number, _ := span.Attributes["execution_attempt"].(float64)
		if span.TraceID == tracer.TraceID(origin.traceID) || workerTraces[span.TraceID] || span.EndTime == nil ||
			len(span.Links) == 0 || !spanLinksSource(span.Links[0], origin, "origin") {
			t.Fatalf("invalid worker span %+v", span)
		}
		if (number == 3 && (len(span.Links) != 2 || !spanLinksSource(span.Links[1], replay, "replay"))) ||
			(number < 3 && len(span.Links) != 1) {
			t.Fatalf("replay lineage %+v", span)
		}
		workerTraces[span.TraceID] = true
	}
	if len(workerTraces) != 3 {
		t.Fatalf("got %d worker traces: %s", len(workerTraces), workerLog)
	}
	const scheduleBody = `{"id":"traceplan","expression":"@hourly","time_zone":"UTC","topic":"demo.record","payload":{}}`
	_, schedule := request(http.MethodPost, "/schedules", "", scheduleBody, http.StatusCreated)
	exec("UPDATE schedules SET next_fire_at = clock_timestamp() WHERE id = 'traceplan'")
	automatic, err := repo.materializeNext(ctx)
	if err != nil || automatic == "" {
		t.Fatalf("materialize %q: %v", automatic, err)
	}
	assertSource := func(id string, expected traceSource) {
		t.Helper()
		var data []byte
		if err := db.QueryRowContext(ctx, "SELECT meta FROM mq_tasks WHERE id = $1", id).Scan(&data); err != nil {
			t.Fatal(err)
		}
		var fields map[string]string
		if err := json.Unmarshal(data, &fields); err != nil || sourceFromTask(fields, "origin") != expected {
			t.Fatalf("submission lineage %s: %v", data, err)
		}
	}
	assertSource(automatic, schedule)
	_, updatedSchedule := request(http.MethodPut, "/schedules/traceplan", "",
		`{"version":1,"expression":"@hourly","time_zone":"UTC","topic":"demo.record","payload":{}}`, http.StatusOK)
	exec("UPDATE schedules SET next_fire_at = clock_timestamp() WHERE id = 'traceplan'")
	subsequent, err := repo.materializeNext(ctx)
	if err != nil || subsequent == "" {
		t.Fatalf("materialize updated schedule %q: %v", subsequent, err)
	}
	assertSource(automatic, schedule)
	assertSource(subsequent, updatedSchedule)
	manual, manualSource := request(http.MethodPost, "/schedules/traceplan/trigger", "manual", "", http.StatusAccepted)
	assertSource(strings.TrimPrefix(manual.Header().Get("Location"), "/runs/"), manualSource)
	_, batchSource := request(http.MethodPost, "/runs/batch", "", `{"runs":[{"idempotency_key":"batch-trace","topic":"demo.record","payload":{}}]}`, http.StatusAccepted)
	var batch string
	if err := db.QueryRowContext(ctx, "SELECT id FROM scheduled_runs WHERE tenant_id = 'tenant' AND request_key = 'batch-trace'").Scan(&batch); err != nil {
		t.Fatal(err)
	}
	assertSource(batch, batchSource)
	for _, secret := range []string{"trace-test-credential", "trace-test-cookie", "trace-test-baggage", "trace-test-request", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if strings.Contains(raw+string(workerLog), secret) {
			t.Fatalf("unexpected request data: %s", secret)
		}
	}
}

func spanLinksSource(link tracer.SpanLink, source traceSource, kind string) bool {
	return string(link.TraceID) == source.traceID && string(link.SpanID) == source.spanID &&
		link.Attributes["request_id"] == source.requestID && link.Attributes["kind"] == kind
}

func TestPostgresTracedWorkerBoundsExecutionContext(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "bounded-execution", submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	barrier := newCrashBarrier(t, db, 1, "BEFORE INSERT ON scheduled_results FOR EACH ROW")
	t.Setenv("SCHEDULER_WORKER_TASK_TIMEOUT", "500ms")
	worker := startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		history, err := repo.runAttempts(ctx, "tenant", id, 0)
		for _, item := range history.Items {
			if item.Number == 1 && item.FailureCode == "execution_timeout" && item.FinishedAt != nil {
				return true, nil
			}
		}
		return false, err
	})
	killSchedulerChild(t, worker)
	barrier.unlock(t)
	state, err := repo.runStatus(ctx, "tenant", id)
	if err != nil || state.ResultReady {
		t.Fatalf("canceled database transaction persisted a result: %+v %v", state, err)
	}
}
