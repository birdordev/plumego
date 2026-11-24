package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresBoundedExpiryAndStableClaims(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	legacy := mqstore.NewSQL(db, mqstore.DefaultSQLConfig())
	cfg := mqstore.DefaultSQLConfig()
	cfg.DeferredExpiration = true
	queue := mqstore.NewSQL(db, cfg)
	_, err := db.ExecContext(ctx, `INSERT INTO mq_tasks
		(id, tenant_id, topic, priority, status, attempts, max_attempts, created_at, updated_at,
		 available_at, expires_at, lease_owner, lease_until)
	VALUES
		('expired-a', 't', 'ttl', 0, 'queued', 0, 3, $1::timestamptz, $1, $1, $1::timestamptz - interval '2 seconds', NULL, NULL),
		('expired-b', 't', 'ttl', 0, 'queued', 0, 3, $1, $1, $1, $1::timestamptz - interval '1 second', NULL, NULL),
		('expired-c', 't', 'ttl', 0, 'queued', 0, 3, $1, $1, $1, $1::timestamptz - interval '1 second', NULL, NULL),
		('leased-expired', 't', 'ttl', 0, 'leased', 1, 3, $1, $1, $1, $1, 'old', $1::timestamptz - interval '1 second'),
		('leased-live', 't', 'ttl', 0, 'leased', 1, 3, $1, $1, $1, $1, 'live', $1),
		('ready-b', 't', 'claim', 0, 'queued', 0, 3, $1, $1, $1, NULL, NULL, NULL),
		('ready-a', 't', 'claim', 0, 'queued', 0, 3, $1, $1, $1, NULL, NULL, NULL),
		('ready-c', 't', 'claim', 1, 'leased', 1, 3, $1, $1, $1, NULL, 'old', $1::timestamptz - interval '1 second')`, now)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.ExecContext(ctx, "SELECT id FROM mq_tasks WHERE id = 'expired-a' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	opts := mq.ReserveOptions{Topics: []string{"claim"}, ConsumerID: "worker", Limit: 2, Now: now}
	blockedCtx, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
	_, err = legacy.Reserve(blockedCtx, opts)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("legacy reservation did not wait on another topic: %v", err)
	}
	unblockedCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tasks, err := queue.Reserve(unblockedCtx, opts)
	if err != nil || len(tasks) != 2 || tasks[0].ID != "ready-c" || tasks[1].ID != "ready-a" {
		t.Fatalf("reservation order or lock isolation failed: %+v %v", tasks, err)
	}
	if _, err := lock.ExecContext(ctx, "SELECT id FROM mq_tasks WHERE id = 'ready-b' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	if got, err := queue.Reserve(unblockedCtx, opts); err != nil || len(got) != 0 {
		t.Fatalf("claim did not skip a locked eligible row: %+v %v", got, err)
	}
	opts.Topics = []string{"ttl"}
	if got, err := queue.Reserve(ctx, opts); err != nil || len(got) != 0 {
		t.Fatalf("reservation delivered an expired task: %+v %v", got, err)
	}
	expire := func(leased bool, expected int64) {
		t.Helper()
		n, err := queue.ExpireBatch(unblockedCtx, mqstore.ExpirationOptions{Leased: leased, Limit: 1, Now: now})
		if err != nil || n != expected {
			t.Fatalf("expired leased=%t: count=%d expected=%d err=%v", leased, n, expected, err)
		}
	}
	expire(false, 1)
	expire(true, 1)
	expire(true, 0)
	for id, expected := range map[string]string{"expired-a": "queued", "expired-b": "expired", "expired-c": "queued", "leased-expired": "expired", "leased-live": "leased"} {
		var status string
		if err := db.QueryRowContext(ctx, "SELECT status FROM mq_tasks WHERE id = $1", id).Scan(&status); err != nil || status != expected {
			t.Fatalf("state for %s: got %s want %s err=%v", id, status, expected, err)
		}
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	opts.Topics = []string{"claim"}
	if got, err := queue.Reserve(unblockedCtx, opts); err != nil || len(got) != 1 || got[0].ID != "ready-b" {
		t.Fatalf("unlocked eligible row was not claimed: %+v %v", got, err)
	}
	expire(false, 1)
	expire(false, 1)
	expire(false, 0)
}
