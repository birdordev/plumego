package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	"github.com/jackc/pgx/v5/pgconn"
)

type journalQueue struct {
	mq.TaskStore
	repo    repository
	metrics *schedulerMetrics
}

func (q *journalQueue) NextAvailable(ctx context.Context, topics []string, now time.Time) (time.Time, error) {
	return q.repo.queue.NextAvailable(ctx, topics, now)
}

func (q *journalQueue) ExtendLease(ctx context.Context, id, owner string, lease time.Duration, now time.Time) error {
	err := q.TaskStore.ExtendLease(ctx, id, owner, lease, now)
	if q.metrics != nil && ctx.Err() != context.Canceled && (err != nil || ctx.Err() == context.DeadlineExceeded) {
		q.metrics.renewalFailures.Add(1)
	}
	return err
}

func (q *journalQueue) Ack(ctx context.Context, id, owner string, now time.Time) error {
	return q.finalize(ctx, id, owner, "ack", "", time.Time{}, func() error {
		return q.TaskStore.Ack(ctx, id, owner, now)
	})
}

func (q *journalQueue) Release(ctx context.Context, id, owner string, opts mq.ReleaseOptions) error {
	return q.finalize(ctx, id, owner, "release", opts.Reason, opts.RetryAt, func() error {
		return q.TaskStore.Release(ctx, id, owner, opts)
	})
}

func (q *journalQueue) MoveToDLQ(ctx context.Context, id, owner, reason string, now time.Time) error {
	return q.finalize(ctx, id, owner, "dlq", reason, time.Time{}, func() error {
		return q.TaskStore.MoveToDLQ(ctx, id, owner, reason, now)
	})
}

func (q *journalQueue) finalize(ctx context.Context, id, owner, operation, reason string, retryAt time.Time, write func() error) (resultErr error) {
	defer func() {
		q.metrics.recordFinalizationFailure(ctx, resultErr)
	}()
	var attempt int
	var topic string
	var recordedAt time.Time
	err := q.repo.db.QueryRowContext(ctx, `WITH claim AS MATERIALIZED (
		SELECT id, attempts, topic FROM mq_tasks WHERE id = $1 AND status = 'leased' AND lease_owner = $2
			AND lease_until > clock_timestamp() FOR UPDATE
	)
	UPDATE mq_task_attempts a SET finalization_operation = $3, finalization_state = 'pending',
		finalization_reason = NULLIF($4, ''), finalization_retry_at = $5,
		finalization_recorded_at = COALESCE(a.finalization_recorded_at, clock_timestamp()),
		finalization_check_at = COALESCE(a.finalization_check_at, clock_timestamp() + interval '3 seconds'),
		execution_outcome = CASE WHEN a.execution_no IS NULL THEN 'not_started'
			WHEN $3 = 'ack' THEN 'succeeded'
			WHEN $4 IN ('task outcome unknown', 'worker shutdown') THEN 'unknown' ELSE 'failed' END
	FROM claim WHERE a.task_id = claim.id AND a.attempt_no = claim.attempts AND a.finished_at IS NULL
		AND (a.finalization_state IS NULL OR (a.finalization_state = 'pending'
			AND a.finalization_operation = $3 AND a.finalization_reason IS NOT DISTINCT FROM NULLIF($4, '')
			AND a.finalization_retry_at IS NOT DISTINCT FROM $5))
	RETURNING a.attempt_no, claim.topic, a.finalization_recorded_at`,
		id, owner, operation, reason, sql.NullTime{Time: retryAt, Valid: !retryAt.IsZero()}).
		Scan(&attempt, &topic, &recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return mq.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if err := write(); err != nil {
		// 请求取消后仍尝试记录分类，独立时间预算避免拖住 Worker。
		noteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer cancel()
		_, noteErr := q.repo.db.ExecContext(noteCtx, `UPDATE mq_task_attempts SET finalization_failure = $3
			WHERE task_id = $1 AND attempt_no = $2`, id, attempt, finalizationFailure(err))
		return errors.Join(err, noteErr)
	}
	q.metrics.finalization(topic, operation, recordedAt)
	return nil
}

func finalizationFailure(err error) string {
	if errors.Is(err, mq.ErrLeaseLost) || errors.Is(err, mq.ErrTaskNotFound) {
		return "lease_lost"
	}
	var pgErr *pgconn.PgError
	if pgconn.SafeToRetry(err) || (errors.As(err, &pgErr) && pgErr.Code != "40003" && pgErr.Code != "08007") {
		return "retryable"
	}
	return "commit_unknown"
}

func (r repository) reconcileFinalization(ctx context.Context) (string, error) {
	return r.reconcileMeasured(ctx, nil)
}

func (r repository) reconcileMeasured(ctx context.Context, metrics *schedulerMetrics) (string, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var id, operation, reason, topic string
	var attempt int
	var retryAt, recordedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT q.id, q.attempts, a.finalization_operation,
		COALESCE(a.finalization_reason, ''), a.finalization_retry_at, q.topic, a.finalization_recorded_at
	FROM mq_task_attempts a JOIN mq_tasks q ON q.id = a.task_id AND q.attempts = a.attempt_no
	WHERE a.finalization_state = 'pending' AND a.finalization_check_at <= statement_timestamp()
		AND a.finalization_recorded_at IS NOT NULL AND a.finalization_operation IS NOT NULL
		AND q.status = 'leased'
	ORDER BY a.finalization_check_at, a.task_id, a.attempt_no LIMIT 1 FOR UPDATE OF q SKIP LOCKED`).
		Scan(&id, &attempt, &operation, &reason, &retryAt, &topic, &recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	err = applyFinalization(ctx, tx, id, attempt, operation, reason, retryAt)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		metrics.recordFinalizationFailure(ctx, err)
		// 失败项后移，其他待确认项可以在下一轮前进。
		noteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer cancel()
		_, noteErr := r.db.ExecContext(noteCtx, `UPDATE mq_task_attempts
			SET finalization_failure = $3, finalization_check_at = CASE WHEN finalization_state = 'pending'
				THEN clock_timestamp() + interval '3 seconds' END
			WHERE task_id = $1 AND attempt_no = $2`, id, attempt, finalizationFailure(err))
		return "", errors.Join(err, noteErr)
	}
	metrics.finalization(topic, operation, recordedAt.Time)
	return id, nil
}

func applyFinalization(ctx context.Context, tx *sql.Tx, id string, attempt int, operation, reason string, retryAt sql.NullTime) error {
	var now time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return err
	}
	var status string
	switch operation {
	case "ack":
		status = "done"
	case "release":
		status = "queued"
	case "dlq":
		status = "dead"
		if _, err := tx.ExecContext(ctx, `INSERT INTO mq_task_dlq (task_id, topic, tenant_id, payload, meta, reason, failed_at)
			SELECT id, topic, tenant_id, payload, meta, $3, $4 FROM mq_tasks WHERE id = $1 AND attempts = $2 AND status = 'leased'`,
			id, attempt, reason, now); err != nil {
			return err
		}
	default:
		return errors.New("invalid finalization operation")
	}
	// 只应用持久化的既有执行结果。领取序号匹配，且从选中开始一直持有队列行锁。
	result, err := tx.ExecContext(ctx, `UPDATE mq_tasks SET status = $3, lease_owner = NULL, lease_until = NULL,
		updated_at = $4, available_at = COALESCE($5, available_at),
		last_error = CASE WHEN $3 = 'done' THEN last_error ELSE $6 END,
		last_error_at = CASE WHEN $3 = 'done' THEN last_error_at ELSE $4 END
	WHERE id = $1 AND attempts = $2 AND status = 'leased'`, id, attempt, status, now, retryAt, reason)
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
	return nil
}

func (r repository) expireLeasesWithoutFinalization(ctx context.Context, limit int, now time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `WITH candidates AS (
		SELECT q.id FROM mq_tasks q WHERE q.status = 'leased' AND q.lease_until < $1 AND q.expires_at <= $1
			AND NOT EXISTS (SELECT 1 FROM mq_task_attempts a WHERE a.task_id = q.id
				AND a.attempt_no = q.attempts AND a.finalization_state = 'pending')
		ORDER BY q.expires_at, q.id LIMIT $2 FOR UPDATE OF q SKIP LOCKED
	)
	UPDATE mq_tasks SET status = 'expired', lease_owner = NULL, lease_until = NULL, updated_at = $1
	WHERE id IN (SELECT id FROM candidates)`, now, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
