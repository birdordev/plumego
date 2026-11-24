package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresBudgetAdmissionIsAtomicAndReclaimsExpiredPermit(t *testing.T) {
	first, second, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	apply := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	apply(`INSERT INTO scheduler_execution_budgets (scope, budget_key, max_concurrent, min_start_interval_ms)
		VALUES ('downstream', 'result-db', 1, 0), ('tenant', 'second', 1, 50)`)
	submit := func(tenant, key string) string {
		t.Helper()
		id, err := repo.submit(ctx, tenant, key, submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	claim := func(q *mq.TaskQueue, id string) mq.Task {
		t.Helper()
		tasks, err := q.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "worker", Limit: 1})
		if err != nil || len(tasks) != 1 || tasks[0].ID != id {
			t.Fatalf("reserve %s got %+v: %v", id, tasks, err)
		}
		return tasks[0]
	}
	holderID := submit("first", "holder")
	holder := claim(first, holderID)
	if n, err := repo.beginExecution(ctx, holder); n != 1 || err != nil {
		t.Fatalf("start holder: %d %v", n, err)
	}
	id := submit("second", "contender")
	contender := claim(second, id)
	if n, err := repo.beginExecution(ctx, contender); n != 0 || !errors.Is(err, mq.ErrExecutionHandled) {
		t.Fatalf("cap bypassed: %d %v", n, err)
	}
	var untouched bool
	if err := db.QueryRowContext(ctx, `SELECT next_start_at IS NULL AND
		NOT EXISTS (SELECT 1 FROM scheduler_execution_budget_slots WHERE task_id = $1)
		FROM scheduler_execution_budgets WHERE scope = 'tenant' AND budget_key = 'second'`, id).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("denial consumed an unrelated budget: %v %v", untouched, err)
	}
	history, err := repo.runAttempts(ctx, "second", id, 0)
	if err != nil || len(history.Items) != 1 || history.Items[0].FailureCode != "budget_deferred" || history.Items[0].ExecutionNo != nil {
		t.Fatalf("denied attempt history %+v: %v", history, err)
	}
	apply("UPDATE mq_tasks SET available_at = clock_timestamp() WHERE id = $1", id)
	contender = claim(second, id)
	apply("UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", holderID)
	if err := first.ExtendLease(ctx, holderID, holder.LeaseOwner, time.Minute, time.Now().Add(-2*time.Second)); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("expired lease resurrected: %v", err)
	}
	if n, err := repo.beginExecution(ctx, contender); n != 1 || err != nil {
		t.Fatalf("expired permit remained occupied: %d %v", n, err)
	}
	if n, err := repo.beginExecution(ctx, contender); n != 0 || !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("same claim was not fenced: %d %v", n, err)
	}
	if err := repo.recordResult(ctx, holder); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("old owner wrote after permit reclamation: %v", err)
	}
	if err := second.Ack(ctx, id, contender.LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_execution_budget_slots").Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("completed claims retained permits: %d %v", retained, err)
	}
}
