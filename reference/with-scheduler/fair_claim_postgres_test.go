package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresProtectedClaimsPreservePriorityFencingAndFallback(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	cfg := mqstore.DefaultSQLConfig()
	cfg.BlockedClaimsTable = "scheduler_blocked_claims"
	cfg.DeferredExpiration = true
	store := mqstore.NewSQL(db, cfg)
	repo := repository{db, store}
	enqueue := func(tenant, key, priority string) string {
		t.Helper()
		id, err := repo.submit(ctx, tenant, key, submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`),
			taskOptions: taskOptions{Priority: priority}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	for i := range 25 {
		enqueue("hot", fmt.Sprintf("hot-%d", i), "high")
	}
	low := enqueue("cold", "cold-normal", "normal")
	high := enqueue("cold", "cold-high", "high")
	newQueue := func() *mq.TaskQueue {
		return mq.NewTaskQueue(&fairClaimQueue{TaskStore: store, store: store, tenants: []string{"cold"}})
	}
	first, second := newQueue(), newQueue()
	claim := func(q *mq.TaskQueue, owner string) mq.Task {
		t.Helper()
		tasks, err := q.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: owner, Topics: []string{"demo.record"}, Limit: 4})
		if err != nil || len(tasks) == 0 {
			t.Fatalf("claim %s: %+v %v", owner, tasks, err)
		}
		return tasks[0]
	}
	a, b := claim(first, "first"), claim(second, "second")
	if a.ID != high || b.ID != low || a.LeaseOwner == b.LeaseOwner {
		t.Fatalf("cold priority or fencing: %+v %+v", a, b)
	}
	if next := claim(first, "first"); next.TenantID != "hot" {
		t.Fatalf("shared rotation lost hot traffic: %+v", next)
	}
	if next := claim(first, "first"); next.TenantID != "hot" {
		t.Fatalf("empty protected round did not use shared capacity: %+v", next)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	third := newQueue()
	current := claim(third, "replacement")
	if current.ID != a.ID || current.LeaseOwner == a.LeaseOwner {
		t.Fatalf("expired protected claim did not get a new lease: %+v", current)
	}
	if err := first.Ack(ctx, a.ID, a.LeaseOwner, time.Now()); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("old owner confirmed the replacement: %v", err)
	}
}
