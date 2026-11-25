package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

const recordTaskDownstream = "result-db"

type executionBudget struct {
	scope, key  string
	concurrency int
	intervalMS  int64
	nextStart   sql.NullTime
}

func lockExecutionBudgets(ctx context.Context, tx *sql.Tx, task mq.Task) ([]executionBudget, error) {
	var taskType, downstream string
	if contract, found := taskContractForTopic(task.Topic); found {
		taskType, downstream = contract.taskType, recordTaskDownstream
	}
	rows, err := tx.QueryContext(ctx, `SELECT scope, budget_key, max_concurrent, min_start_interval_ms, next_start_at
		FROM scheduler_execution_budgets WHERE (max_concurrent > 0 OR min_start_interval_ms > 0) AND (
			(scope = 'tenant' AND budget_key = $1) OR (scope = 'type' AND budget_key = $2)
			OR (scope = 'downstream' AND budget_key = $3) OR (scope = 'retry' AND budget_key = $2
				AND EXISTS (SELECT 1 FROM mq_tasks WHERE id=$4 AND execution_attempts>0)))
		ORDER BY scope, budget_key FOR UPDATE`, task.TenantID, taskType, downstream, task.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var budgets []executionBudget
	for rows.Next() {
		var budget executionBudget
		if err := rows.Scan(&budget.scope, &budget.key, &budget.concurrency, &budget.intervalMS, &budget.nextStart); err != nil {
			return nil, err
		}
		budgets = append(budgets, budget)
	}
	return budgets, rows.Err()
}

func reserveExecutionBudgets(ctx context.Context, tx *sql.Tx, budgets []executionBudget, taskID string, claim int) (time.Time, error) {
	if len(budgets) == 0 {
		return time.Time{}, nil
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return time.Time{}, err
	}
	var retryAt time.Time
	for _, budget := range budgets {
		if budget.intervalMS > 0 && budget.nextStart.Valid && budget.nextStart.Time.After(now) && budget.nextStart.Time.After(retryAt) {
			retryAt = budget.nextStart.Time
		}
		if budget.concurrency == 0 {
			continue
		}
		active, err := budget.activeSlots(ctx, tx)
		if err != nil {
			return time.Time{}, err
		}
		if active >= budget.concurrency {
			checkAt := now.Add(250 * time.Millisecond)
			if checkAt.After(retryAt) {
				retryAt = checkAt
			}
		}
	}
	if !retryAt.IsZero() {
		return retryAt, nil
	}
	for _, budget := range budgets {
		if budget.concurrency > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_execution_budget_slots (scope, budget_key, task_id, claim_no)
				VALUES ($1, $2, $3, $4)`, budget.scope, budget.key, taskID, claim); err != nil {
				return time.Time{}, err
			}
		}
		if budget.intervalMS > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE scheduler_execution_budgets
				SET next_start_at = clock_timestamp() + $3 * interval '1 millisecond'
				WHERE scope = $1 AND budget_key = $2`, budget.scope, budget.key, budget.intervalMS); err != nil {
				return time.Time{}, err
			}
		}
	}
	return time.Time{}, nil
}

func (b executionBudget) activeSlots(ctx context.Context, tx *sql.Tx) (int, error) {
	// 被其他事务锁住的过期领取仍保守计入额度。只回收当前事务已核对过的领取。
	if _, err := tx.ExecContext(ctx, `WITH expired AS MATERIALIZED (
		SELECT s.task_id, s.claim_no FROM scheduler_execution_budget_slots s JOIN mq_tasks q ON q.id = s.task_id
		WHERE s.scope = $1 AND s.budget_key = $2
			AND (q.status <> 'leased' OR q.attempts <> s.claim_no OR q.lease_until <= clock_timestamp())
		FOR UPDATE OF q SKIP LOCKED
	)
	DELETE FROM scheduler_execution_budget_slots s USING expired e
	WHERE s.scope = $1 AND s.budget_key = $2 AND s.task_id = e.task_id AND s.claim_no = e.claim_no`, b.scope, b.key); err != nil {
		return 0, err
	}
	var count int
	err := tx.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_execution_budget_slots WHERE scope = $1 AND budget_key = $2", b.scope, b.key).Scan(&count)
	return count, err
}

func deferExecutionBudget(ctx context.Context, tx *sql.Tx, task mq.Task, retryAt time.Time) error {
	result, err := tx.ExecContext(ctx, `WITH tick AS MATERIALIZED (SELECT clock_timestamp() AS now)
		UPDATE mq_tasks q SET status = 'queued', available_at = GREATEST($4, tick.now + interval '25 milliseconds'),
			lease_owner = NULL, lease_until = NULL, last_error = 'execution budget deferred', last_error_at = tick.now, updated_at = tick.now
		FROM tick WHERE q.id = $1 AND q.tenant_id = $2 AND q.status = 'leased' AND q.lease_owner = $3 AND q.lease_until > tick.now`,
		task.ID, task.TenantID, task.LeaseOwner, retryAt)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return mq.ErrLeaseLost
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return mq.ErrExecutionHandled
}
