package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var errRunFinalized = errors.New("run already finalized")

// cancelRun 使用与业务结果提交相同的队列行锁确定先后顺序。
func (r repository) cancelRun(ctx context.Context, tenant, id string) error {
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var status string
	err = tx.QueryRowContext(ctx, `SELECT q.status
		FROM mq_tasks q JOIN scheduled_runs r ON r.id = q.id AND r.tenant_id = q.tenant_id
		WHERE q.tenant_id = $1 AND q.id = $2 FOR UPDATE OF q`, tenant, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, "SELECT status FROM scheduler_archived_queue WHERE tenant_id = $1 AND id = $2", tenant, id).Scan(&status)
	}
	if err != nil {
		return err
	}
	switch status {
	case "canceled":
		return tx.Commit()
	case "queued", "leased":
	default:
		return errRunFinalized
	}

	var committed bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM scheduled_results WHERE run_id = $1)", id).Scan(&committed); err != nil {
		return err
	}
	if committed {
		return errRunFinalized
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mq_tasks
		SET status = 'canceled', lease_owner = NULL, lease_until = NULL, updated_at = clock_timestamp()
		WHERE id = $1 AND tenant_id = $2`, id, tenant); err != nil {
		return err
	}
	return tx.Commit()
}

// deliverNext 在独立投递事务中写入参考接收方的持久回执。
func (r repository) deliverNext(ctx context.Context, region string) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT o.run_id FROM scheduled_outbox o`+outboxShardJoin+`
		WHERE s.region=$1 AND o.delivered_at IS NULL AND o.lease_token IS NULL
		ORDER BY o.created_at,o.run_id LIMIT 1 FOR UPDATE OF o SKIP LOCKED FOR SHARE OF s SKIP LOCKED`, region).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO scheduled_deliveries (run_id) VALUES ($1) ON CONFLICT DO NOTHING", id); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE scheduled_outbox SET delivered_at = clock_timestamp() WHERE run_id = $1", id); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// resultDeduper 使用已经提交的业务回执恢复 ACK。
type resultDeduper struct{ db *sql.DB }

func (d resultDeduper) IsCompleted(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := d.db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM scheduled_results WHERE run_id = $1)", id).Scan(&exists)
	return exists, err
}

func (d resultDeduper) MarkCompleted(ctx context.Context, id string, _ time.Duration) error {
	exists, err := d.IsCompleted(ctx, id)
	if err == nil && !exists {
		return sql.ErrNoRows
	}
	return err
}
