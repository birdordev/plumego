package main

import (
	"context"
	"errors"
	"time"
)

type retentionPolicy struct {
	terminal, attempts, audit time.Duration
}

func loadRetentionPolicy() (retentionPolicy, error) {
	terminal, terminalErr := workerDurationEnv("SCHEDULER_TERMINAL_RETENTION", 90*24*time.Hour)
	attempts, attemptErr := workerDurationEnv("SCHEDULER_ATTEMPT_RETENTION", 30*24*time.Hour)
	audit, auditErr := workerDurationEnv("SCHEDULER_AUDIT_RETENTION", 180*24*time.Hour)
	return retentionPolicy{terminal, attempts, audit}, errors.Join(terminalErr, attemptErr, auditErr)
}

func (r repository) archiveHistory(ctx context.Context, policy retentionPolicy) (string, error) {
	var moved int64
	for _, batch := range []struct {
		statement string
		age       time.Duration
	}{
		{archiveTerminalBatch, policy.terminal},
		{archiveAttemptBatch, policy.attempts},
		{archiveAuditBatch, policy.audit},
	} {
		result, err := r.db.ExecContext(ctx, batch.statement, batch.age.Seconds())
		if err != nil {
			return "", err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return "", err
		}
		moved += count
	}
	if moved == 0 {
		return "", nil
	}
	return "archived", nil
}

// 先锁定终态队列行，和死信重放、取消及结果写入使用相同的所有权边界。
const archiveTerminalBatch = `
	WITH picked AS MATERIALIZED (
		SELECT q.id, q.tenant_id, q.status, q.attempts, q.execution_attempts, q.updated_at
		FROM mq_tasks q
		WHERE q.status IN ('done','dead','canceled','expired')
			AND q.updated_at < statement_timestamp() - $1 * interval '1 second'
			AND NOT EXISTS (SELECT 1 FROM scheduled_outbox o WHERE o.run_id = q.id AND o.delivered_at IS NULL)
			AND NOT EXISTS (SELECT 1 FROM mq_task_attempts a WHERE a.task_id = q.id AND a.finalization_state = 'pending')
		ORDER BY q.updated_at, q.id LIMIT 128 FOR UPDATE OF q SKIP LOCKED
	), copied AS (
		INSERT INTO scheduler_archived_queue (id, tenant_id, status, attempts, execution_attempts, updated_at)
		SELECT id, tenant_id, status, attempts, execution_attempts, updated_at FROM picked
		RETURNING id, status
	), removed AS (
		DELETE FROM mq_tasks q USING copied c WHERE q.id = c.id RETURNING c.status
	)
	UPDATE scheduler_archive_totals t SET total = t.total + moved.n
	FROM (SELECT status, count(*) AS n FROM removed GROUP BY status) moved WHERE t.status = moved.status`

// 只迁出已归档或不可重放的终态实例的旧尝试，保留运行中和可重放实例的恢复记录。
const archiveAttemptBatch = `
	WITH picked AS MATERIALIZED (
		SELECT a.task_id, a.attempt_no FROM mq_task_attempts a
		WHERE a.finalization_state IS DISTINCT FROM 'pending'
			AND COALESCE(a.finished_at, a.started_at) < statement_timestamp() - $1 * interval '1 second'
			AND (EXISTS (SELECT 1 FROM scheduler_archived_queue q WHERE q.id = a.task_id)
				OR EXISTS (SELECT 1 FROM mq_tasks q WHERE q.id = a.task_id AND q.status IN ('done','canceled','expired')))
		ORDER BY COALESCE(a.finished_at, a.started_at), a.task_id, a.attempt_no
		LIMIT 128 FOR UPDATE OF a SKIP LOCKED
	), removed AS (
		DELETE FROM mq_task_attempts a USING picked p
		WHERE a.task_id = p.task_id AND a.attempt_no = p.attempt_no RETURNING a.*
	)
	INSERT INTO scheduler_archived_attempts SELECT * FROM removed`

const archiveAuditBatch = `
	WITH picked AS MATERIALIZED (
		SELECT id FROM scheduler_audit
		WHERE occurred_at < statement_timestamp() - $1 * interval '1 second'
		ORDER BY occurred_at, id LIMIT 128 FOR UPDATE SKIP LOCKED
	), removed AS (
		DELETE FROM scheduler_audit a USING picked p WHERE a.id = p.id RETURNING a.*
	)
	INSERT INTO scheduler_archived_audit SELECT * FROM removed`
