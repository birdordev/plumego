package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"
)

func (r repository) beginExecution(ctx context.Context, task mq.Task) (int, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var paused bool
	var scheduleID string
	err = tx.QueryRowContext(ctx, `SELECT s.claim_paused, s.id
		FROM scheduled_runs r JOIN schedules s ON s.tenant_id = r.tenant_id AND s.id = r.schedule_id
		WHERE r.tenant_id = $1 AND r.id = $2 FOR SHARE OF s`, task.TenantID, task.ID).Scan(&paused, &scheduleID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if paused {
		return 0, mq.ErrClaimPaused
	}
	budgets, err := lockExecutionBudgets(ctx, tx, task)
	if err != nil {
		return 0, err
	}

	var options taskOptions
	if raw := task.Meta["task_options"]; raw != "" {
		// 任务处理器沿用契约校验，将损坏的负载和选项隔离到死信。
		if json.Unmarshal([]byte(raw), &options) != nil || !options.Overlap.valid() {
			options.Overlap = nil
		}
	}
	overlap := options.Overlap
	var blocked bool
	if overlap != nil && overlap.Policy != "allow" {
		if overlap.Scope == "schedule" {
			if scheduleID == "" {
				return 0, errInvalidTask
			}
			overlap.Key = scheduleID
		}
		blocked, err = overlap.lockSlot(ctx, tx, task)
		if err != nil {
			return 0, err
		}
	}

	var claim, execution, maximum int
	var registered, fanoutChild bool
	err = tx.QueryRowContext(ctx, `SELECT q.attempts, q.execution_attempts, q.max_attempts, a.execution_no IS NOT NULL,
		EXISTS (SELECT 1 FROM scheduler_workflow_fanout_children c WHERE c.run_id=q.id)
		FROM mq_tasks q JOIN scheduled_runs r ON r.id = q.id AND r.tenant_id = q.tenant_id
		JOIN mq_task_attempts a ON a.task_id = q.id AND a.attempt_no = q.attempts
		WHERE q.id = $1 AND q.tenant_id = $2 AND q.status = 'leased' AND q.lease_owner = $3
		AND q.lease_until > clock_timestamp() FOR UPDATE OF q`, task.ID, task.TenantID, task.LeaseOwner).
		Scan(&claim, &execution, &maximum, &registered, &fanoutChild)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, mq.ErrLeaseLost
	}
	if err != nil {
		return 0, err
	}
	if registered {
		return 0, mq.ErrLeaseLost
	}
	if execution >= maximum {
		return 0, nil
	}
	if blocked {
		return 0, overlap.resolveConflict(ctx, tx, task)
	}
	if fanoutChild {
		var fanoutCanceled bool
		err = tx.QueryRowContext(ctx, `SELECT f.cancel_requested FROM scheduler_workflow_fanout_children c
			JOIN scheduler_workflow_fanouts f USING (tenant_id,workflow_id,node_id)
			WHERE c.tenant_id=$1 AND c.run_id=$2 FOR SHARE OF f`, task.TenantID, task.ID).Scan(&fanoutCanceled)
		if err != nil {
			return 0, err
		}
		if fanoutCanceled {
			return 0, mq.ErrClaimPaused
		}
	}
	retryAt, err := reserveExecutionBudgets(ctx, tx, budgets, task.ID, claim)
	if err != nil {
		return 0, err
	}
	if !retryAt.IsZero() {
		return 0, deferExecutionBudget(ctx, tx, task, retryAt)
	}
	if overlap != nil && overlap.Policy != "allow" {
		if err := overlap.occupy(ctx, tx, task); err != nil {
			return 0, err
		}
	}
	execution++
	// 每个领取代次只允许登记一次执行，计数和历史一起提交。
	result, err := tx.ExecContext(ctx, `WITH marker AS (
		UPDATE mq_task_attempts SET execution_no = $3, execution_started_at = clock_timestamp()
		WHERE task_id = $1 AND attempt_no = $2 AND execution_no IS NULL AND finished_at IS NULL
		RETURNING task_id
	)
	UPDATE mq_tasks q SET execution_attempts = $3 FROM marker
	WHERE q.id = marker.task_id AND q.lease_until > clock_timestamp()`, task.ID, claim, execution)
	if err != nil {
		return 0, err
	}
	marked, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if marked != 1 {
		return 0, mq.ErrLeaseLost
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return execution, nil
}
