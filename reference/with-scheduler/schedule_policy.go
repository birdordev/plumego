package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var errScheduleVersion = errors.New("schedule version mismatch")

const misfireGrace = 2 * time.Second

func validMisfirePolicy(policy string, window int) bool {
	return (policy == "skip" || policy == "coalesce" || policy == "catch_up") && window >= 1 && window <= 86400
}

func fixedDelayOccurrence(d scheduleDefinition, at, dispatchAt, now time.Time) (fire, next time.Time, action string) {
	delay := time.Duration(d.delayMicros.Int64) * time.Microsecond
	overdue := now.Sub(dispatchAt)
	if overdue > time.Duration(d.CatchUpWindow)*time.Second ||
		(d.MisfirePolicy == "skip" && overdue > misfireGrace) {
		return time.Time{}, now.Truncate(time.Microsecond).Add(delay), "skip"
	}
	if d.MisfirePolicy == "coalesce" && overdue > misfireGrace {
		action = "coalesce"
	}
	return at, at.Add(delay), action
}

func advanceCompletedDelays(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, shardFilter string, params []any) error {
	_, err := db.ExecContext(ctx, `WITH ready AS (
		SELECT r.id, r.tenant_id, r.schedule_id, r.delay_completed_at
		FROM scheduled_runs r JOIN schedules s ON s.tenant_id = r.tenant_id AND s.id = r.schedule_id
		WHERE r.delay_completed_at IS NOT NULL AND NOT r.delay_applied`+shardFilter+`
		ORDER BY r.delay_completed_at, r.id LIMIT 32 FOR UPDATE OF s, r SKIP LOCKED
	), advanced AS (
		UPDATE schedules s SET next_fire_at = ready.delay_completed_at + s.fixed_delay_us * interval '1 microsecond',
			waiting_run_id = NULL FROM ready
			WHERE s.tenant_id = ready.tenant_id AND s.id = ready.schedule_id AND s.waiting_run_id = ready.id
	)
	UPDATE scheduled_runs r SET delay_applied = true FROM ready WHERE r.id = ready.id`, params...)
	return err
}

func scheduleOccurrence(d scheduleDefinition, spec scheduleRecurrence, at, now time.Time) (fire, next time.Time, action string) {
	afterNow := func() time.Time {
		return firstOccurrence(d.Expression, spec, at, now.Truncate(time.Microsecond).Add(time.Microsecond))
	}
	if d.MisfirePolicy == "skip" && now.Sub(at) > misfireGrace {
		return time.Time{}, afterNow(), "skip"
	}
	cutoff := now.Add(-time.Duration(d.CatchUpWindow) * time.Second).Truncate(time.Microsecond)
	fire = at
	if fire.Before(cutoff) {
		fire = firstOccurrence(d.Expression, spec, at, cutoff)
		action = "trim"
	}
	if fire.IsZero() || fire.After(now) {
		return time.Time{}, fire, action
	}
	if d.MisfirePolicy == "coalesce" && now.Sub(fire) > misfireGrace {
		return fire, afterNow(), "coalesce"
	}
	return fire, spec.Next(fire).Truncate(time.Microsecond), action
}

// firstOccurrence 在补跑窗口截断时保持 @every 的原始相位。
func firstOccurrence(expression string, spec scheduleRecurrence, anchor, cutoff time.Time) time.Time {
	if every, ok := strings.CutPrefix(expression, "@every "); ok {
		interval, err := time.ParseDuration(every)
		if err == nil && interval > 0 {
			phase := cutoff.Sub(anchor) % interval
			return cutoff.Add((interval - phase) % interval)
		}
	}
	return spec.Next(cutoff.Add(-time.Microsecond)).Truncate(time.Microsecond)
}

type pauseCommand struct {
	Version       int64 `json:"version"`
	Paused        *bool `json:"paused"`
	ClaimPaused   *bool `json:"claim_paused"`
	CancelRunning bool  `json:"cancel_running"`
}

type pauseResult struct {
	Version  int64  `json:"version"`
	Canceled *int64 `json:"canceled,omitempty"`
}

func (r repository) setSchedulePause(ctx context.Context, tenant, id string, cmd pauseCommand) (pauseResult, error) {
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return pauseResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if cmd.CancelRunning {
		if _, err := tx.ExecContext(ctx, "SELECT set_config('scheduler.cancel_running', 'true', true)"); err != nil {
			return pauseResult{}, err
		}
	}
	var result pauseResult
	err = tx.QueryRowContext(ctx, `UPDATE schedules
		SET paused = COALESCE($4, paused), claim_paused = COALESCE($5, claim_paused),
		    version = CASE WHEN $6 OR paused IS DISTINCT FROM COALESCE($4, paused)
		                   OR claim_paused IS DISTINCT FROM COALESCE($5, claim_paused)
		                   THEN version + 1 ELSE version END
		WHERE tenant_id = $1 AND id = $2 AND version = $3 RETURNING version`,
		tenant, id, cmd.Version, cmd.Paused, cmd.ClaimPaused, cmd.CancelRunning).Scan(&result.Version)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(ctx, "SELECT version FROM schedules WHERE tenant_id = $1 AND id = $2", tenant, id).
			Scan(&result.Version); err != nil {
			return pauseResult{}, err
		}
		return result, errScheduleVersion
	}
	if err != nil {
		return pauseResult{}, err
	}
	if cmd.CancelRunning {
		count, err := cancelLeasedSchedule(ctx, tx, tenant, id)
		if err != nil {
			return pauseResult{}, err
		}
		result.Canceled = &count
	}
	if err := tx.Commit(); err != nil {
		return pauseResult{}, err
	}
	return result, nil
}

func cancelLeasedSchedule(ctx context.Context, tx *sql.Tx, tenant, id string) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT q.id FROM scheduled_runs r
		JOIN mq_tasks q ON q.id = r.id AND q.tenant_id = r.tenant_id
		WHERE r.tenant_id = $1 AND r.schedule_id = $2 AND q.status = 'leased'
		ORDER BY q.id FOR UPDATE OF q`, tenant, id)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var locked []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return 0, err
		}
		locked = append(locked, taskID)
	}
	if err := rows.Err(); err != nil || len(locked) == 0 {
		return 0, err
	}
	// 队列行上锁后刷新查询快照，避开等待锁时已经提交的业务回执。
	result, err := tx.ExecContext(ctx, `UPDATE mq_tasks q
		SET status = 'canceled', lease_owner = NULL, lease_until = NULL, updated_at = clock_timestamp()
		WHERE q.id = ANY($1::text[]) AND q.tenant_id = $2
		  AND NOT EXISTS (SELECT 1 FROM scheduled_results s WHERE s.run_id = q.id)`, locked, tenant)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
