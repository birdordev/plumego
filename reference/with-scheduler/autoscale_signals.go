package main

import (
	"context"
	"time"
)

type scaleSnapshot struct {
	ready, active        int
	oldest               float64
	arrivals, processed  float64
	downstreamSlots      int
	downstreamCap        int
	downstreamStarts     float64
	downstreamMinStartMS int
	databaseFree         int
}

func (r repository) readScaleSnapshot(ctx context.Context, topics []string, window time.Duration) (scaleSnapshot, error) {
	var snapshot scaleSnapshot
	err := r.db.QueryRowContext(ctx, `WITH runnable AS (
		SELECT CASE WHEN q.status = 'leased' THEN GREATEST(q.available_at, q.lease_until) ELSE q.available_at END AS ready_at
		FROM mq_tasks q WHERE q.topic = ANY($1::text[]) AND
			(q.status = 'queued' OR (q.status = 'leased' AND q.lease_until <= statement_timestamp()))
			AND q.available_at <= statement_timestamp()
			AND (q.expires_at IS NULL OR q.expires_at > statement_timestamp())
			AND NOT EXISTS (SELECT 1 FROM scheduler_blocked_claims b WHERE b.task_id = q.id)
	), backlog AS (
		SELECT ready_at FROM runnable ORDER BY ready_at LIMIT 10000
	), downstream AS (
		SELECT max_concurrent, min_start_interval_ms,
			(SELECT count(*) FROM scheduler_execution_budget_slots s
			 JOIN mq_tasks q ON q.id = s.task_id AND q.attempts = s.claim_no
			 WHERE s.scope = 'downstream' AND s.budget_key = $3 AND q.status = 'leased'
				AND q.lease_until > statement_timestamp()) AS used
		FROM scheduler_execution_budgets WHERE scope = 'downstream' AND budget_key = $3
	)
	SELECT (SELECT count(*) FROM backlog), COALESCE(EXTRACT(EPOCH FROM statement_timestamp() -
		(SELECT min(ready_at) FROM backlog)), 0),
		(SELECT count(*) FROM mq_tasks q JOIN mq_task_attempts a ON a.task_id = q.id AND a.attempt_no = q.attempts
			WHERE q.topic = ANY($1::text[]) AND q.status = 'leased' AND q.lease_until > statement_timestamp()
			AND a.execution_no IS NOT NULL AND a.finished_at IS NULL AND a.finalization_state IS DISTINCT FROM 'pending'),
		(SELECT count(*) FROM mq_tasks WHERE topic = ANY($1::text[]) AND created_at > statement_timestamp() - $2 * interval '1 second') / $2,
		(SELECT count(*) FROM mq_task_attempts a JOIN mq_tasks q ON q.id = a.task_id
			WHERE q.topic = ANY($1::text[]) AND a.execution_no IS NOT NULL AND a.finished_at IS NOT NULL
				AND a.finished_at > statement_timestamp() - $2 * interval '1 second') / $2,
		COALESCE((SELECT GREATEST(0, max_concurrent - used) FROM downstream WHERE max_concurrent > 0), -1),
		COALESCE((SELECT max_concurrent FROM downstream), 0),
		(SELECT count(*) FROM mq_task_attempts WHERE execution_started_at IS NOT NULL
			AND execution_started_at > statement_timestamp() - $2 * interval '1 second') / $2,
		COALESCE((SELECT min_start_interval_ms FROM downstream), 0),
		current_setting('max_connections')::int - current_setting('superuser_reserved_connections')::int
			- COALESCE(current_setting('reserved_connections', true)::int, 0)
			- (SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend')`,
		topics, window.Seconds(), recordTaskDownstream).Scan(&snapshot.ready, &snapshot.oldest, &snapshot.active,
		&snapshot.arrivals, &snapshot.processed, &snapshot.downstreamSlots, &snapshot.downstreamCap,
		&snapshot.downstreamStarts, &snapshot.downstreamMinStartMS, &snapshot.databaseFree)
	return snapshot, err
}
