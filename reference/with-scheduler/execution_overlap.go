package main

import (
	"context"
	"database/sql"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"
)

type overlapOptions struct {
	Policy string `json:"policy"`
	Scope  string `json:"scope,omitempty"`
	Key    string `json:"key,omitempty"`
}

func (o *overlapOptions) valid() bool {
	if o == nil {
		return true
	}
	switch o.Policy {
	case "allow":
		return o.Scope == "" && o.Key == ""
	case "skip", "serial":
		switch o.Scope {
		case "schedule", "tenant":
			return o.Key == ""
		case "key":
			return runCursorID.MatchString(o.Key)
		}
	}
	return false
}

func (o overlapOptions) lockSlot(ctx context.Context, tx *sql.Tx, task mq.Task) (bool, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_execution_slots (tenant_id, scope, group_key)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, task.TenantID, o.Scope, o.Key); err != nil {
		return false, err
	}
	var holder, owner string
	if err := tx.QueryRowContext(ctx, `SELECT task_id, lease_owner FROM scheduler_execution_slots
		WHERE tenant_id = $1 AND scope = $2 AND group_key = $3 FOR UPDATE`, task.TenantID, o.Scope, o.Key).
		Scan(&holder, &owner); err != nil {
		return false, err
	}
	if holder == "" {
		return false, nil
	}
	if holder == task.ID {
		if owner == task.LeaseOwner {
			return false, mq.ErrLeaseLost
		}
		return false, nil
	}
	// 先锁住持有者，再用数据库时钟核对已提交的续租或终态。
	var active bool
	err := tx.QueryRowContext(ctx, `WITH holder AS MATERIALIZED (
		SELECT lease_until FROM mq_tasks WHERE id = $1 AND tenant_id = $2
			AND status = 'leased' AND lease_owner = $3 FOR UPDATE
	)
	SELECT lease_until > clock_timestamp() FROM holder`, holder, task.TenantID, owner).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active, err
}

func (o overlapOptions) occupy(ctx context.Context, tx *sql.Tx, task mq.Task) error {
	_, err := tx.ExecContext(ctx, `UPDATE scheduler_execution_slots SET task_id = $4, lease_owner = $5
		WHERE tenant_id = $1 AND scope = $2 AND group_key = $3`, task.TenantID, o.Scope, o.Key, task.ID, task.LeaseOwner)
	return err
}

func (o overlapOptions) resolveConflict(ctx context.Context, tx *sql.Tx, task mq.Task) error {
	status, reason := "queued", "execution overlap deferred"
	if o.Policy == "skip" {
		status, reason = "skipped", "execution overlap skipped"
	}
	result, err := tx.ExecContext(ctx, `WITH tick AS MATERIALIZED (SELECT clock_timestamp() AS now)
		UPDATE mq_tasks q SET status = $4, last_error = $5, last_error_at = tick.now,
			available_at = CASE WHEN $4 = 'queued' THEN tick.now + interval '500 milliseconds' ELSE q.available_at END,
			lease_owner = NULL, lease_until = NULL, updated_at = tick.now FROM tick
		WHERE q.id = $1 AND q.tenant_id = $2 AND q.status = 'leased' AND q.lease_owner = $3
			AND q.lease_until > tick.now`, task.ID, task.TenantID, task.LeaseOwner, status, reason)
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
