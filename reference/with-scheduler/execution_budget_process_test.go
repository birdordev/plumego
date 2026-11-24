//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresExecutionBudgetsLimitDownstreamPeaksAcrossWorkers(t *testing.T) {
	for _, tc := range []struct {
		scope, key string
		cap, rate  int
	}{
		{"tenant", "tenant-a", 1, 550},
		{"type", recordTaskType, 2, 100},
		{"downstream", recordTaskDownstream, 2, 100},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			_, _, db := postgresTestQueues(t)
			ctx := t.Context()
			repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
			if _, err := db.ExecContext(ctx, `INSERT INTO scheduler_execution_budgets (scope, budget_key, max_concurrent, min_start_interval_ms)
				VALUES ($1, $2, $3, $4)`, tc.scope, tc.key, tc.cap, tc.rate); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `CREATE TABLE budget_observed (
				id text PRIMARY KEY, tenant text NOT NULL, worker text NOT NULL,
				started_at timestamptz NOT NULL, ended_at timestamptz NOT NULL
			);
			CREATE FUNCTION probe_budget_downstream() RETURNS trigger LANGUAGE plpgsql AS $$
			DECLARE started timestamptz := clock_timestamp();
			BEGIN
				PERFORM pg_sleep(0.35);
				INSERT INTO budget_observed
				SELECT NEW.run_id, tenant_id, split_part(lease_owner, '/', 2), started, clock_timestamp()
				FROM mq_tasks WHERE id = NEW.run_id;
				RETURN NEW;
			END $$;
			CREATE TRIGGER probe_budget_downstream BEFORE INSERT ON scheduled_results
				FOR EACH ROW EXECUTE FUNCTION probe_budget_downstream()`); err != nil {
				t.Fatal(err)
			}
			const total = 12
			for i := range total {
				tenant := "tenant-a"
				if i%3 == 0 {
					tenant = "tenant-b"
				}
				version := i%2 + 1
				if _, err := repo.submit(ctx, tenant, fmt.Sprintf("budget-%d", i), submission{
					Topic: recordTaskType, Payload: json.RawMessage(`{"message":"budget"}`),
					taskOptions: taskOptions{TaskVersion: version},
				}); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "1")
			startSchedulerChild(t, db, "worker")
			t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "2")
			startSchedulerChild(t, db, "worker")
			awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
				var done int
				err := db.QueryRowContext(ctx, "SELECT count(*) FROM mq_tasks WHERE status = 'done'").Scan(&done)
				return done == total, err
			})
			var peak, workers int
			var minGap float64
			if err := db.QueryRowContext(ctx, `WITH effects AS (
				SELECT *, lag(started_at) OVER (ORDER BY started_at) AS previous
				FROM budget_observed WHERE $1 <> 'tenant' OR tenant = 'tenant-a'
			)
			SELECT (SELECT max((SELECT count(*) FROM effects y WHERE y.started_at <= x.started_at AND y.ended_at > x.started_at)) FROM effects x),
				count(DISTINCT worker), min(extract(epoch FROM (started_at - previous)) * 1000)
			FROM effects`, tc.scope).Scan(&peak, &workers, &minGap); err != nil {
				t.Fatal(err)
			}
			var allPeak, remaining int
			if err := db.QueryRowContext(ctx, `SELECT (SELECT max((
				SELECT count(*) FROM budget_observed y WHERE y.started_at <= x.started_at AND y.ended_at > x.started_at
				)) FROM budget_observed x), (SELECT count(*) FROM scheduler_execution_budget_slots)`).
				Scan(&allPeak, &remaining); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s actual downstream peak=%d (all tenants=%d), distinct workers=%d, shortest start gap=%.2f ms",
				tc.scope, peak, allPeak, workers, minGap)
			if peak != tc.cap || workers != 2 || minGap < float64(tc.rate-20) || remaining != 0 ||
				(tc.scope == "tenant" && allPeak <= peak) {
				t.Fatalf("invalid budget result: peak=%d workers=%d spacing=%.2f allPeak=%d remaining=%d", peak, workers, minGap, allPeak, remaining)
			}
		})
	}
}

func TestPostgresBudgetPermitRecoveredAfterWorkerKill(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	if _, err := db.ExecContext(ctx, `INSERT INTO scheduler_execution_budgets (scope, budget_key, max_concurrent)
		VALUES ('downstream', 'result-db', 1)`); err != nil {
		t.Fatal(err)
	}
	submit := func(key string, version int) string {
		t.Helper()
		id, err := repo.submit(ctx, "tenant", key, submission{Topic: recordTaskType, Payload: json.RawMessage(`{"message":"recover"}`),
			taskOptions: taskOptions{TaskVersion: version}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	holder := submit("held-budget", 1)
	barrier := newCrashBarrier(t, db, 1, "BEFORE INSERT ON scheduled_results FOR EACH ROW")
	t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "1")
	t.Setenv("SCHEDULER_WORKER_LEASE_DURATION", "3s")
	first := startSchedulerChild(t, db, "worker")
	barrier.wait(t, db)
	follower := submit("waiting-budget", 2)
	t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "2")
	startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		history, err := repo.runAttempts(ctx, "tenant", follower, 0)
		for _, item := range history.Items {
			if item.FailureCode == "budget_deferred" && item.ExecutionNo == nil {
				return true, nil
			}
		}
		return false, err
	})
	killedAt := time.Now()
	killSchedulerChild(t, first)
	barrier.abortWaiter(t, db)
	barrier.unlock(t)
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		result, err := repo.runStatus(ctx, "tenant", follower)
		return result.QueueStatus == "done" && result.ExecutionAttempts == 1, err
	})
	var expired, reclaimed bool
	if err := db.QueryRowContext(ctx, `SELECT lease_until <= clock_timestamp(),
		NOT EXISTS (SELECT 1 FROM scheduler_execution_budget_slots WHERE task_id = $1)
		FROM mq_tasks WHERE id = $1 AND status = 'leased'`, holder).Scan(&expired, &reclaimed); err != nil || !expired || !reclaimed {
		t.Fatalf("old claim must expire without holding a permit: expired=%v reclaimed=%v %v", expired, reclaimed, err)
	}
	t.Logf("follower finished using recovered permit after %v", time.Since(killedAt))
}
