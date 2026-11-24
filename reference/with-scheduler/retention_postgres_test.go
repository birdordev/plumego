package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresHistoryRetention(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	input := submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)}
	submit := func(key string) string {
		t.Helper()
		id, err := repo.submit(ctx, "tenant", key, input)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	complete := func(key string) string {
		t.Helper()
		id := submit(key)
		now := time.Now()
		tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{
			Topics: []string{recordTaskType}, Limit: 1, Lease: time.Minute, ConsumerID: "archive-test", Now: now,
		})
		if err != nil || len(tasks) != 1 || tasks[0].ID != id {
			t.Fatalf("claim %s: %v %v", id, tasks, err)
		}
		if err := repo.recordResult(ctx, tasks[0]); err != nil {
			t.Fatal(err)
		}
		if err := queue.Ack(ctx, id, tasks[0].LeaseOwner, now); err != nil {
			t.Fatal(err)
		}
		return id
	}

	eligible := complete("delivered")
	if id, err := repo.deliverNext(ctx, localSchedulerRegion); err != nil || id != eligible {
		t.Fatalf("deliver: %q %v", id, err)
	}
	undelivered := complete("undelivered")
	active := submit("active")
	canceled := submit("canceled")
	if err := repo.cancelRun(ctx, "tenant", canceled); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE mq_tasks SET updated_at = statement_timestamp() - interval '100 days'")
	exec("UPDATE mq_task_attempts SET finished_at = statement_timestamp() - interval '40 days' WHERE task_id = $1", eligible)
	exec("UPDATE scheduler_audit SET occurred_at = statement_timestamp() - interval '200 days' WHERE target_id = $1", eligible)

	policy := retentionPolicy{90 * 24 * time.Hour, 30 * 24 * time.Hour, 180 * 24 * time.Hour}
	if result, err := repo.archiveHistory(ctx, policy); err != nil || result == "" {
		t.Fatalf("archive: %q %v", result, err)
	}
	if result, err := repo.archiveHistory(ctx, policy); err != nil || result != "" {
		t.Fatalf("duplicate archive: %q %v", result, err)
	}
	for _, id := range []string{undelivered, active} {
		var hot bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM mq_tasks WHERE id = $1)", id).Scan(&hot); err != nil || !hot {
			t.Fatalf("protected task %s: hot=%t %v", id, hot, err)
		}
	}
	for _, id := range []string{eligible, canceled} {
		var hot bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM mq_tasks WHERE id = $1)", id).Scan(&hot); err != nil || hot {
			t.Fatalf("terminal task %s: hot=%t %v", id, hot, err)
		}
	}
	status, err := repo.runStatus(ctx, "tenant", eligible)
	if err != nil || status.QueueStatus != "done" || !status.ResultReady {
		t.Fatalf("archived status: %+v %v", status, err)
	}
	if id := submit("delivered"); id != eligible {
		t.Fatal("archive changed idempotent submission")
	}
	if err := repo.cancelRun(ctx, "tenant", canceled); err != nil {
		t.Fatalf("archive changed idempotent cancellation: %v", err)
	}
	page, err := repo.listRuns(ctx, "tenant", runListQuery{limit: 20, status: "done", includeTotal: true})
	if err != nil || len(page.Items) != 2 || page.Total == nil || *page.Total != 2 {
		t.Fatalf("mixed history page: %+v %v", page, err)
	}
	attempts, err := repo.runAttempts(ctx, "tenant", eligible, 0)
	if err != nil || len(attempts.Items) != 1 || attempts.Items[0].FinishedAt == nil {
		t.Fatalf("archived attempts: %+v %v", attempts, err)
	}
	var audits int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_archived_audit WHERE target_id = $1", eligible).Scan(&audits); err != nil || audits == 0 {
		t.Fatalf("archived audit: %d %v", audits, err)
	}
	slo, err := repo.readSLO(ctx)
	if err != nil || slo.archived != [4]int64{1, 0, 1, 0} {
		t.Fatalf("archive totals: %+v %v", slo.archived, err)
	}
}

func TestPostgresRetentionCapacity(t *testing.T) {
	if os.Getenv("PLUMEGO_TEST_RETENTION_CAPACITY") != "1" {
		t.Skip("enable the dedicated retention capacity fixture")
	}
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const history = 50000
	if _, err := db.ExecContext(ctx, `INSERT INTO mq_tasks
		(id, topic, tenant_id, payload, priority, status, attempts, max_attempts, available_at, created_at, updated_at)
		SELECT 'history-' || i, 'demo.record', 'tenant', convert_to(repeat('x', 1024), 'UTF8'),
		0, 'done', 1, 3, now() - interval '100 days', now() - interval '100 days', now() - interval '100 days'
		FROM generate_series(1, $1::integer) i`, history); err != nil {
		t.Fatal(err)
	}
	heapBytes := func() int64 {
		t.Helper()
		var size int64
		if err := db.QueryRowContext(ctx, "SELECT pg_relation_size('mq_tasks')").Scan(&size); err != nil {
			t.Fatal(err)
		}
		return size
	}
	before := heapBytes()
	measure := func() time.Duration {
		t.Helper()
		samples := make([]time.Duration, 100)
		for i := range samples {
			now := time.Now()
			id := fmt.Sprintf("probe-%d", now.UnixNano())
			if err := queue.Enqueue(ctx, mq.Task{ID: id, Topic: recordTaskType, TenantID: "tenant"}, mq.EnqueueOptions{AvailableAt: now}); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{
				Topics: []string{recordTaskType}, Limit: 1, Lease: time.Minute, ConsumerID: "capacity", Now: now,
			})
			samples[i] = time.Since(start)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("capacity claim: %v %v", tasks, err)
			}
			if err := queue.Ack(ctx, id, tasks[0].LeaseOwner, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		slices.Sort(samples)
		return samples[98]
	}
	baseline := measure()
	monitorCtx, stop := context.WithCancel(ctx)
	defer stop()
	monitor := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for monitorCtx.Err() == nil {
			if _, err := repo.readSLO(monitorCtx); err != nil && monitorCtx.Err() == nil {
				monitor <- err
				return
			}
			select {
			case <-ticker.C:
			case <-monitorCtx.Done():
			}
		}
		monitor <- nil
	}()
	scraped := measure()
	archive := make(chan error, 1)
	start := time.Now()
	go func() {
		policy := retentionPolicy{90 * 24 * time.Hour, 30 * 24 * time.Hour, 180 * 24 * time.Hour}
		for {
			moved, err := repo.archiveHistory(ctx, policy)
			if moved == "" || err != nil {
				archive <- err
				return
			}
		}
	}()
	competing := measure()
	if err := <-archive; err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	stop()
	if err := <-monitor; err != nil {
		t.Fatal(err)
	}
	slo, err := repo.readSLO(ctx)
	if err != nil || slo.archived[0] != history {
		t.Fatalf("archived %d instead of %d: %v", slo.archived[0], history, err)
	}
	deleted := heapBytes()
	if _, err := db.ExecContext(ctx, "VACUUM FULL mq_tasks"); err != nil {
		t.Fatal(err)
	}
	t.Logf("terminal rows=%d heap bytes before=%d after delete=%d after compact=%d archive=%s claim p99 baseline=%s with scraping=%s with scraping and archive=%s",
		history, before, deleted, heapBytes(), elapsed, baseline, scraped, competing)
}
