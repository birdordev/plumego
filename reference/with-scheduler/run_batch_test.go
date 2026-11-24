package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresBatchHTTPAtomicRetry(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("127.0.0.1:0", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	const batch = `{"runs":[{"idempotency_key":"new","topic":"demo.record","payload":{"data":1}},{"idempotency_key":"existing","topic":"demo.record","payload":{}}]}`
	existing, err := repo.submit(ctx, "tenant", "existing", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	request := func(body string, want int) []string {
		t.Helper()
		w := auditHTTPRequest(ctx, app, http.MethodPost, "/runs/batch", "", "", body)
		if w.Code != want {
			t.Fatalf("batch: status=%d body=%s, want %d", w.Code, w.Body, want)
		}
		if want != http.StatusAccepted {
			return nil
		}
		var decoded struct {
			Data struct {
				IDs []string `json:"ids"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded.Data.IDs
	}
	exec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	assertCounts := func(want int) {
		t.Helper()
		var runs, queued, auditRows, pending int
		err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM scheduled_runs),
			(SELECT count(*) FROM mq_tasks), (SELECT count(*) FROM scheduler_audit WHERE operation = 'run.submit'),
			(SELECT pending_runs FROM scheduler_tenant_usage WHERE tenant_id = 'tenant')`).Scan(&runs, &queued, &auditRows, &pending)
		if err != nil || runs != want || queued != want || auditRows != want || pending != want {
			t.Fatalf("counts runs=%d queued=%d audit=%d pending=%d err=%v want=%d", runs, queued, auditRows, pending, err, want)
		}
	}
	request(`{"runs":[{"idempotency_key":"x","topic":"demo.record","payload":{}},{"idempotency_key":"x","topic":"demo.record","payload":{}}]}`, 400)
	request(`{"runs":[{"idempotency_key":"different-new","topic":"demo.record","payload":{}},{"idempotency_key":"existing","topic":"demo.record","payload":{"different":true}}]}`, 409)
	assertCounts(1)
	exec("UPDATE scheduler_tenant_usage SET max_pending_runs = 2 WHERE tenant_id = 'tenant'")
	request(`{"runs":[{"idempotency_key":"new","topic":"demo.record","payload":{"data":1}},{"idempotency_key":"another-new","topic":"demo.record","payload":{}}]}`, 429)
	assertCounts(1)
	exec("UPDATE scheduler_tenant_usage SET max_pending_runs = 10 WHERE tenant_id = 'tenant'")
	exec("ALTER TABLE mq_tasks ADD CONSTRAINT injection CHECK (false) NOT VALID")
	request(batch, 503)
	assertCounts(1)
	exec("ALTER TABLE mq_tasks DROP CONSTRAINT injection")
	ids := request(batch, 202)
	if len(ids) != 2 || ids[0] == existing || ids[1] != existing {
		t.Fatalf("incorrect ordered IDs: %v", ids)
	}
	if repeated := request(batch, 202); !slices.Equal(ids, repeated) {
		t.Fatalf("replayed IDs %v, want %v", repeated, ids)
	}
	assertCounts(2)
}

func TestPostgresCoalescedAckRollbackAndPerClaimRetry(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	sqlQueue := mqstore.NewSQL(db, mqstore.DefaultSQLConfig())
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, id := range []string{"a", "b"} {
		if err := queue.Enqueue(ctx, mq.Task{ID: id, Topic: "demo.record", TenantID: "tenant"}, mq.EnqueueOptions{AvailableAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "worker", Now: now, Lease: time.Minute, Limit: 2})
	if err != nil || len(tasks) != 2 {
		t.Fatalf("claim count=%d err=%v", len(tasks), err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks ADD CONSTRAINT reject_ack CHECK (id <> 'b' OR status <> 'done') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	batchCtx, cancel := context.WithCancel(ctx)
	batched, done := startBatchedQueue(batchCtx, sqlQueue, time.Second, 2)
	defer func() {
		cancel()
		<-done
	}()
	ackPair := func(ownerA string) [2]error {
		var result [2]error
		var wg sync.WaitGroup
		wg.Go(func() { result[0] = batched.Ack(ctx, tasks[0].ID, ownerA, time.Now()) })
		wg.Go(func() { result[1] = batched.Ack(ctx, tasks[1].ID, tasks[1].LeaseOwner, time.Now()) })
		wg.Wait()
		return result
	}
	failed := ackPair(tasks[0].LeaseOwner)
	if failed[0] == nil || failed[1] == nil || errors.Is(failed[0], mq.ErrLeaseLost) {
		t.Fatalf("batch failed without group rollback: %v", failed)
	}
	stats, err := sqlQueue.Stats(ctx)
	if err != nil || stats.Leased != 2 {
		t.Fatalf("failed batch changed claim state: %+v %v", stats, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE mq_tasks DROP CONSTRAINT reject_ack"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second' WHERE id = 'a'"); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "new-worker", Lease: time.Minute, Limit: 1})
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != tasks[0].ID {
		t.Fatalf("reclaim count=%d err=%v", len(reclaimed), err)
	}
	partial := ackPair(tasks[0].LeaseOwner)
	if !errors.Is(partial[0], mq.ErrLeaseLost) || partial[1] != nil {
		t.Fatalf("results did not preserve claim fencing: %v", partial)
	}
	current := mqstore.TaskAcknowledgment{TaskID: reclaimed[0].ID, LeaseOwner: reclaimed[0].LeaseOwner}
	old := mqstore.TaskAcknowledgment{TaskID: tasks[0].ID, LeaseOwner: tasks[0].LeaseOwner}
	if results, err := sqlQueue.AckBatch(ctx, []mqstore.TaskAcknowledgment{old, current, current}, time.Now()); err != nil ||
		len(results) != 3 || !errors.Is(results[0], mq.ErrLeaseLost) || results[1] != nil || results[2] != nil {
		t.Fatalf("retry only unresolved claim: %v %v", results, err)
	}
}

func TestPostgresConcurrentBatchSubmissionConverges(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	input := submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}
	results := make(chan [2]string, 8)
	var wg sync.WaitGroup
	for i := range cap(results) {
		wg.Go(func() {
			items := []batchSubmission{{input, "key-a"}, {input, "key-b"}}
			if i%2 != 0 {
				slices.Reverse(items)
			}
			ids, err := repo.submitBatch(ctx, "tenant", items)
			if err != nil || len(ids) != 2 {
				t.Errorf("concurrent submit: count=%d err=%v", len(ids), err)
				return
			}
			if i%2 != 0 {
				slices.Reverse(ids)
			}
			results <- [2]string{ids[0], ids[1]}
		})
	}
	wg.Wait()
	if len(results) != cap(results) {
		t.Fatalf("completed %d competing submissions", len(results))
	}
	close(results)
	first := <-results
	for result := range results {
		if result != first {
			t.Fatalf("batch deduplication diverged: %v versus %v", result, first)
		}
	}
	var queued int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM mq_tasks WHERE tenant_id = 'tenant'").Scan(&queued); err != nil || queued != 2 {
		t.Fatalf("queue duplicates: count=%d err=%v", queued, err)
	}
}
