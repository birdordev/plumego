package main

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestRunListQueryValidation(t *testing.T) {
	cursor := (runCursor{time.Unix(1000, 0), "item-1"}).encode("queued")
	for _, raw := range []string{"limit=0", "limit=101", "limit=2&limit=3", "cursor=*",
		"cursor=" + cursor, "cursor=" + cursor + "&status=done", "status=unknown", "offset=1", "limit=%", "include_total=1"} {
		if _, err := parseRunListQuery(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	query, err := parseRunListQuery(url.Values{"cursor": {cursor}, "status": {"queued"}, "limit": {"2"}}.Encode())
	if err != nil || query.after == nil || query.after.id != "item-1" || query.limit != 2 {
		t.Fatalf("query = %+v, %v", query, err)
	}
}

func TestPostgresRunPagesRemainStableDuringInsert(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	ctx := t.Context()
	input := submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)}
	var expected []string
	for _, key := range []string{"a", "b", "c", "d"} {
		id, err := repo.submit(ctx, "tenant", key, input)
		if err != nil {
			t.Fatal(err)
		}
		expected = append(expected, id)
	}
	if _, err := repo.submit(ctx, "other", "visible-only-there", input); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE scheduled_runs SET created_at = statement_timestamp() - INTERVAL '1 hour'"); err != nil {
		t.Fatal(err)
	}
	slices.Sort(expected)
	slices.Reverse(expected)
	first, err := repo.listRuns(ctx, "tenant", runListQuery{limit: 2, status: "queued", includeTotal: true})
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" || first.Total == nil || *first.Total != 4 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	if _, err := repo.submit(ctx, "tenant", "concurrent-insert", input); err != nil {
		t.Fatal(err)
	}
	query, err := parseRunListQuery("limit=2&status=queued&cursor=" + first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	last, err := repo.listRuns(ctx, "tenant", query)
	if err != nil || len(last.Items) != 2 || last.NextCursor != "" || last.Total != nil {
		t.Fatalf("last page: %+v %v", last, err)
	}
	got := []string{first.Items[0].ID, first.Items[1].ID, last.Items[0].ID, last.Items[1].ID}
	if !slices.Equal(got, expected) {
		t.Fatalf("paginated = %v, want %v", got, expected)
	}
	if err := repo.cancelRun(ctx, "tenant", expected[0]); err != nil {
		t.Fatal(err)
	}
	canceled, err := repo.listRuns(ctx, "tenant", runListQuery{limit: 2, status: "canceled"})
	if err != nil || len(canceled.Items) != 1 || canceled.Items[0].ID != expected[0] || canceled.NextCursor != "" {
		t.Fatalf("filtered page: %+v %v", canceled, err)
	}
	empty, err := repo.listRuns(ctx, "missing", runListQuery{limit: 1})
	if err != nil || len(empty.Items) != 0 || empty.Items == nil {
		t.Fatalf("empty tenant: %+v %v", empty, err)
	}
}

func TestPostgresRunDeepPageUsesOrderedIndex(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO scheduler_tenant_usage (tenant_id, max_pending_runs) VALUES ('tenant', 25000);
		INSERT INTO mq_tasks
		(id, topic, tenant_id, priority, status, attempts, max_attempts, available_at, created_at, updated_at)
		SELECT 'run-' || i, 'demo.record', 'tenant', 0, 'queued', 0, 3, NOW(), NOW(), NOW()
		FROM generate_series(1, 20000) AS i;
		INSERT INTO scheduled_runs (id, tenant_id, scheduled_at, created_at)
		SELECT 'run-' || i, 'tenant', NOW(), '2025-01-01 UTC'::timestamptz + i * interval '1 second'
		FROM generate_series(1, 20000) AS i;
		ANALYZE mq_tasks; ANALYZE scheduled_runs;`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `EXPLAIN (ANALYZE, FORMAT TEXT)
		SELECT r.id, q.status FROM scheduled_runs r
		JOIN scheduler_queue_history q ON q.id = r.id AND q.tenant_id = r.tenant_id
		WHERE r.tenant_id = 'tenant' AND (r.created_at, r.id) < ('2025-01-01 00:03:00 UTC', 'run-180')
		ORDER BY r.created_at DESC, r.id DESC LIMIT 20`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "scheduled_runs_tenant_created_id") || !strings.Contains(plan.String(), "Index Cond:") {
		t.Fatalf("deep page did not use ordered index:\n%s", plan.String())
	}
}
