package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

type schedulerSLO struct {
	overdue, runnable float64
	short, cpu, io    float64
	recentDeadLetters int
	archived          [4]int64
}

func (m *schedulerMetrics) recordFinalizationFailure(ctx context.Context, err error) {
	if m != nil && err != nil && ctx.Err() != context.Canceled &&
		!errors.Is(err, mq.ErrLeaseLost) && !errors.Is(err, mq.ErrTaskNotFound) {
		m.finalizationFailures.Add(1)
	}
}

func (r repository) readSLO(ctx context.Context) (schedulerSLO, error) {
	var result schedulerSLO
	err := r.db.QueryRowContext(ctx, `WITH oldest AS (
		SELECT topics.pool, MIN(t.ready_at) AS ready_at
		FROM (VALUES ('short', 'demo.record'), ('short', 'demo.record.v2'), ('short', 'demo.reserve'), ('short', 'demo.release'), ('cpu', 'demo.compute'), ('cpu', 'demo.checkpoint'), ('io', 'demo.io'), ('io', 'demo.export')) AS topics(pool, topic)
		CROSS JOIN LATERAL (
			(SELECT q.available_at AS ready_at FROM mq_tasks q
			 WHERE q.topic = topics.topic AND q.status = 'queued' AND q.available_at <= statement_timestamp()
				AND (q.expires_at IS NULL OR q.expires_at > statement_timestamp())
				AND NOT EXISTS (SELECT 1 FROM scheduler_blocked_claims b WHERE b.task_id = q.id)
			 ORDER BY q.available_at LIMIT 1)
			UNION ALL
			(SELECT GREATEST(q.available_at, q.lease_until) AS ready_at FROM mq_tasks q
			 WHERE q.topic = topics.topic AND q.status = 'leased' AND q.lease_until < statement_timestamp()
				AND q.available_at <= statement_timestamp()
				AND (q.expires_at IS NULL OR q.expires_at > statement_timestamp())
				AND NOT EXISTS (SELECT 1 FROM scheduler_blocked_claims b WHERE b.task_id = q.id)
			 ORDER BY q.lease_until LIMIT 1)
		) t GROUP BY topics.pool
	)
	SELECT COALESCE(EXTRACT(EPOCH FROM statement_timestamp() -
		(SELECT next_dispatch_at FROM schedules WHERE NOT paused AND next_dispatch_at <= statement_timestamp()
		 ORDER BY next_dispatch_at LIMIT 1)), 0),
		COALESCE(EXTRACT(EPOCH FROM statement_timestamp() - MIN(oldest.ready_at)), 0),
		COALESCE(EXTRACT(EPOCH FROM statement_timestamp() - MIN(oldest.ready_at) FILTER (WHERE pool = 'short')), 0),
		COALESCE(EXTRACT(EPOCH FROM statement_timestamp() - MIN(oldest.ready_at) FILTER (WHERE pool = 'cpu')), 0),
		COALESCE(EXTRACT(EPOCH FROM statement_timestamp() - MIN(oldest.ready_at) FILTER (WHERE pool = 'io')), 0),
		(SELECT COUNT(*) FROM (SELECT 1 FROM scheduler_dead_letters
		 WHERE failed_at > statement_timestamp() - interval '5 minutes' LIMIT 10000) recent),
		(SELECT total FROM scheduler_archive_totals WHERE status = 'done'),
		(SELECT total FROM scheduler_archive_totals WHERE status = 'dead'),
		(SELECT total FROM scheduler_archive_totals WHERE status = 'canceled'),
		(SELECT total FROM scheduler_archive_totals WHERE status = 'expired')
	FROM oldest`).Scan(&result.overdue, &result.runnable, &result.short, &result.cpu, &result.io, &result.recentDeadLetters,
		&result.archived[0], &result.archived[1], &result.archived[2], &result.archived[3])
	return result, err
}

func (m *schedulerMetrics) writeSLO(ctx context.Context, w io.Writer) {
	fmt.Fprintf(w, "# TYPE plumego_scheduler_lease_renewal_failures_total counter\nplumego_scheduler_lease_renewal_failures_total %d\n", m.renewalFailures.Load())
	fmt.Fprintf(w, "# TYPE plumego_scheduler_finalization_failures_total counter\nplumego_scheduler_finalization_failures_total %d\n", m.finalizationFailures.Load())
	if m.readSLO == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := m.readSLO(ctx)
	fmt.Fprintln(w, "# TYPE plumego_scheduler_storage_probe_success gauge")
	if err != nil {
		fmt.Fprintln(w, "plumego_scheduler_storage_probe_success 0")
		return
	}
	fmt.Fprintln(w, "plumego_scheduler_storage_probe_success 1")
	fmt.Fprintf(w, "# TYPE plumego_scheduler_oldest_due_schedule_seconds gauge\nplumego_scheduler_oldest_due_schedule_seconds %g\n", result.overdue)
	fmt.Fprintf(w, "# TYPE plumego_scheduler_oldest_runnable_seconds gauge\nplumego_scheduler_oldest_runnable_seconds %g\n", result.runnable)
	fmt.Fprintf(w, "# TYPE plumego_scheduler_pool_oldest_runnable_seconds gauge\nplumego_scheduler_pool_oldest_runnable_seconds{pool=\"short\"} %g\nplumego_scheduler_pool_oldest_runnable_seconds{pool=\"cpu\"} %g\nplumego_scheduler_pool_oldest_runnable_seconds{pool=\"io\"} %g\n",
		result.short, result.cpu, result.io)
	fmt.Fprintf(w, "# TYPE plumego_scheduler_dead_letters_5m gauge\nplumego_scheduler_dead_letters_5m %d\n", result.recentDeadLetters)
	fmt.Fprintln(w, "# TYPE plumego_scheduler_archived_runs_total counter")
	for i, status := range [4]string{"done", "dead", "canceled", "expired"} {
		fmt.Fprintf(w, "plumego_scheduler_archived_runs_total{status=%q} %d\n", status, result.archived[i])
	}
}
