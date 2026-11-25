package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresTenantIngressPreservesIdempotencyAndDedicatedBudget(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	apply := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	apply(`INSERT INTO scheduler_tenant_ingress (tenant_id,regular_per_second,urgent_per_second,urgent_actor)
		VALUES ('hot',1,1,'urgent-operator')`)
	run := submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)}
	fast := run
	fast.Priority = "high"
	if ids, err := repo.submitBatch(ctx, "hot", []batchSubmission{{run, "a"}, {run, "b"}}); ids != nil || !errors.Is(err, errTenantCapacity) {
		t.Fatalf("overlimit batch: %v %v", ids, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_runs WHERE tenant_id='hot'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("overlimit batch left partial admissions: %d %v", count, err)
	}
	id, err := repo.submit(ctx, "hot", "original", run)
	if err != nil {
		t.Fatal(err)
	}
	apply("UPDATE scheduler_tenant_ingress SET regular_per_second=0 WHERE tenant_id='hot'")
	if again, err := repo.submit(ctx, "hot", "original", run); err != nil || again != id {
		t.Fatalf("idempotent replay was charged: %q %v", again, err)
	}
	if ids, err := repo.submitBatch(ctx, "hot", []batchSubmission{{run, "original"}}); err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("batch replay was charged: %+v %v", ids, err)
	}
	if _, err := repo.submit(ctx, "hot", "unauthorized", fast); !errors.Is(err, errTenantCapacity) {
		t.Fatalf("unprivileged user consumed emergency budget: %v", err)
	}
	urgent := context.WithValue(ctx, auditRequestKey{}, auditIdentity{actor: "urgent-operator", requestID: "test-request"})
	if _, err := repo.submit(urgent, "hot", "urgent", fast); err != nil {
		t.Fatalf("reserved admission did not survive regular saturation: %v", err)
	}
	if _, err := repo.submit(ctx, "cold", "independent", run); err != nil {
		t.Fatalf("cold tenant shared hot gate: %v", err)
	}
}

func TestPostgresRetryBudgetDoesNotDelayInitialExecution(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if _, err := db.ExecContext(ctx, `INSERT INTO scheduler_execution_budgets
		(scope,budget_key,min_start_interval_ms,next_start_at)
		VALUES ('retry','demo.record',1000,clock_timestamp()+interval '1 minute')`); err != nil {
		t.Fatal(err)
	}
	id, err := repo.submit(ctx, "test", "initial", submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 2; want++ {
		claims, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "retry-test", Limit: 1})
		if err != nil || len(claims) != 1 || claims[0].ID != id {
			t.Fatalf("claim: %+v %v", claims, err)
		}
		n, err := repo.beginExecution(ctx, claims[0])
		if want == 1 {
			if n != 1 || err != nil {
				t.Fatalf("initial attempt was throttled: %d %v", n, err)
			}
			if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
		} else if n != 0 || !errors.Is(err, mq.ErrExecutionHandled) {
			t.Fatalf("retry ignored its scoped rate: %d %v", n, err)
		}
	}
}
