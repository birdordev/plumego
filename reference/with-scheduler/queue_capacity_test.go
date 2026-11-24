package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresQueueClaimPlans(t *testing.T) {
	if os.Getenv("PLUMEGO_TEST_QUEUE_CAPACITY") != "1" {
		t.Skip("set PLUMEGO_TEST_QUEUE_CAPACITY=1 to run the capacity probe")
	}
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, group := range []struct {
		name, topic, status    string
		rows, available, lease int
	}{
		{"history", "demo.record", "done", 60000, -3600, 0},
		{"future", "demo.record", "queued", 20000, 1200, 0},
		{"queued-noise", "other", "queued", 10000, -5, 0},
		{"leased-noise", "other", "leased", 10000, -5, -1},
		{"live", "demo.record", "leased", 4000, -5, 1200},
		{"ready", "demo.record", "queued", 64, -5, 0},
		{"reclaim", "demo.record", "leased", 64, -5, -1},
	} {
		_, err := db.ExecContext(ctx, `INSERT INTO mq_tasks
			(id, tenant_id, topic, priority, status, attempts, max_attempts,
			 available_at, lease_owner, lease_until, created_at, updated_at)
		SELECT $1 || g, 'tenant-' || g % 256, $2, g % 3, $3, 0, 3,
			$5::timestamptz + $6::int * interval '1 second',
			CASE WHEN $3 = 'leased' THEN 'worker' END,
			CASE WHEN $3 = 'leased' THEN $5::timestamptz + $7::int * interval '1 second' END,
			$5, $5
		FROM generate_series(1, $4::int) g`, group.name, group.topic, group.status, group.rows, now, group.available, group.lease)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "ANALYZE mq_tasks"); err != nil {
		t.Fatal(err)
	}
	type planNode struct {
		Type     string     `json:"Node Type"`
		Index    string     `json:"Index Name"`
		Estimate int        `json:"Plan Rows"`
		Actual   float64    `json:"Actual Rows"`
		Filtered float64    `json:"Rows Removed by Filter"`
		Children []planNode `json:"Plans"`
	}
	for _, tc := range []struct {
		name, filter string
		indexes      []string
		maxScanned   float64
	}{
		{"queued", "status = 'queued'", []string{"mq_tasks_ready"}, 256},
		// 单独测量失效租约谓词时，优化器也可能选择全局租约索引。
		{"reclaim", "status = 'leased' AND lease_until < $1", nil, 10128},
		{"combined", "(status = 'queued' OR (status = 'leased' AND lease_until < $1))", []string{"mq_tasks_ready", "mq_tasks_reclaim_topic"}, 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			err := db.QueryRowContext(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
				SELECT id FROM mq_tasks WHERE `+tc.filter+` AND available_at <= $1 AND (expires_at IS NULL OR expires_at > $1)
				AND topic IN ($2) ORDER BY priority DESC, created_at, id LIMIT $3 FOR UPDATE SKIP LOCKED`,
				now, "demo.record", 32).Scan(&raw)
			if err != nil {
				t.Fatal(err)
			}
			var decoded []struct {
				Plan planNode `json:"Plan"`
			}
			if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded) != 1 {
				t.Fatalf("could not decode the PostgreSQL execution plan: %v", err)
			}
			var indexes []string
			var printPlan func(planNode)
			printPlan = func(node planNode) {
				t.Logf("%-18s %-25s estimated=%d actual=%.0f filtered=%.0f", node.Type, node.Index, node.Estimate, node.Actual, node.Filtered)
				if node.Index != "" {
					indexes = append(indexes, node.Index)
				}
				if node.Type == "Seq Scan" {
					t.Error("full heap scan in claim plan")
				}
				if node.Actual+node.Filtered > tc.maxScanned {
					t.Errorf("%s examined %.0f rows, want at most %.0f", node.Type, node.Actual+node.Filtered, tc.maxScanned)
				}
				for _, child := range node.Children {
					printPlan(child)
				}
			}
			printPlan(decoded[0].Plan)
			if tc.name == "reclaim" && !slices.Contains(indexes, "mq_tasks_reclaim_topic") &&
				!slices.Contains(indexes, "mq_tasks_lease_expiry") {
				t.Error("reclaim probe did not use a lease expiry index")
			}
			for _, index := range tc.indexes {
				if !slices.Contains(indexes, index) {
					t.Errorf("claim plan did not use %s", index)
				}
			}
		})
	}
}

func TestPostgresQueueExpiryLatency(t *testing.T) {
	if os.Getenv("PLUMEGO_TEST_QUEUE_CAPACITY") != "1" {
		t.Skip("set PLUMEGO_TEST_QUEUE_CAPACITY=1 to run the capacity probe")
	}
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	var version string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("PostgreSQL %s", version)
	const samples, backlog, batch = 100, 2000, 128
	var stats [2][]time.Duration
	cleanup := make([]time.Duration, 0, samples)
	for sample := range samples {
		// 交错两种模式，降低测量顺序对缓存和后台活动的影响。
		for offset := range 2 {
			mode := (sample + offset) % 2
			if _, err := db.ExecContext(ctx, "TRUNCATE mq_tasks, mq_task_attempts, scheduler_tenant_usage"); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			if _, err := db.ExecContext(ctx, `INSERT INTO mq_tasks
				(id, tenant_id, topic, priority, status, attempts, max_attempts, available_at, expires_at, created_at, updated_at)
				SELECT g::text, 'tenant-' || g % 8, CASE WHEN g = 0 THEN 'target' ELSE 'backlog' END,
					0, 'queued', 0, 3, $1, CASE WHEN g <> 0 THEN $1::timestamptz - interval '1 second' END, $1, $1
				FROM generate_series(0, $2::int) g`, now, backlog); err != nil {
				t.Fatal(err)
			}
			cfg := mqstore.DefaultSQLConfig()
			cfg.DeferredExpiration = mode == 1
			queue := mqstore.NewSQL(db, cfg)
			start := time.Now()
			got, err := queue.Reserve(ctx, mq.ReserveOptions{Topics: []string{"target"}, ConsumerID: "probe", Limit: 1, Now: now})
			stats[mode] = append(stats[mode], time.Since(start))
			if err != nil || len(got) != 1 || got[0].ID != "0" {
				t.Fatalf("mode %d reservation: count=%d err=%v", mode, len(got), err)
			}
			if mode == 1 {
				start = time.Now()
				n, err := queue.ExpireBatch(ctx, mqstore.ExpirationOptions{Now: now, Limit: batch})
				cleanup = append(cleanup, time.Since(start))
				if err != nil || n != batch {
					t.Fatalf("batch cleanup: count=%d err=%v", n, err)
				}
			}
		}
	}
	report := func(which string, durations []time.Duration) {
		t.Helper()
		slices.Sort(durations)
		t.Logf("%s (%d samples, %d expired rows): p50=%s p99=%s", which, samples, backlog,
			durations[samples/2-1], durations[samples*99/100-1])
	}
	report("inline expiry + claim", stats[0])
	report("claim with isolated expiry", stats[1])
	report(fmt.Sprintf("expiry transaction / %d rows", batch), cleanup)
}
