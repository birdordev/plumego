package main

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

type progressReporter struct {
	lastAt time.Time
	last   int
	write  func(context.Context, int) error
}

func (p *progressReporter) report(ctx context.Context, now time.Time, percent int) error {
	if percent < 0 || percent > 99 || (!p.lastAt.IsZero() &&
		(percent-p.last < 5 || now.Sub(p.lastAt) < 250*time.Millisecond)) {
		return nil
	}
	if err := p.write(ctx, percent); err != nil {
		return err
	}
	p.lastAt, p.last = now, percent
	return nil
}

func (r repository) storeProgress(ctx context.Context, task mq.Task, percent int) error {
	result, err := r.db.ExecContext(ctx, `WITH claim AS MATERIALIZED (
		SELECT id, attempts FROM mq_tasks WHERE id = $1 AND tenant_id = $2 AND status = 'leased'
			AND lease_owner = $3 AND lease_until > clock_timestamp() FOR UPDATE
	)
	INSERT INTO scheduler_run_progress (run_id, claim_no, percent)
	SELECT id, attempts, $4 FROM claim
	ON CONFLICT (run_id) DO UPDATE SET claim_no = EXCLUDED.claim_no,
		percent = CASE WHEN scheduler_run_progress.claim_no = EXCLUDED.claim_no
			THEN GREATEST(scheduler_run_progress.percent, EXCLUDED.percent) ELSE EXCLUDED.percent END`,
		task.ID, task.TenantID, task.LeaseOwner, percent)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows == 0 {
		return mq.ErrLeaseLost
	}
	return err
}
