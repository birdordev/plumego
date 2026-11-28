package operations

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

const FirstLeaseMigration = "0150_operation_first_lease"
const LatencyLimit = 10_000

var ErrLatencyWindowTooLarge = errors.New("operation latency window contains too many operations")

type LatencyFilter struct {
	BacklogFilter
	From, Until time.Time
	Action      string
}

func (f LatencyFilter) Valid() bool {
	return f.BacklogFilter.Valid() && (f.Action == "" || operationKey.MatchString(f.Action)) &&
		!f.From.IsZero() && f.From.Before(f.Until) && f.Until.Sub(f.From) <= 31*24*time.Hour &&
		f.From.Nanosecond()%1000 == 0 && f.Until.Nanosecond()%1000 == 0
}

type LatencyDistribution struct {
	Samples int64  `json:"samples"`
	P50MS   *int64 `json:"p50_ms,omitempty"`
	P95MS   *int64 `json:"p95_ms,omitempty"`
	MaxMS   *int64 `json:"max_ms,omitempty"`
}

type LatencyTotals struct {
	Accepted    int64               `json:"accepted"`
	Enqueued    int64               `json:"enqueued"`
	Succeeded   int64               `json:"succeeded"`
	Failed      int64               `json:"failed"`
	NeedsReview int64               `json:"needs_review"`
	Pending     int64               `json:"pending"`
	QueueWait   LatencyDistribution `json:"enqueue_to_first_lease"`
	Convergence LatencyDistribution `json:"dispatch_to_observed_success"`
}

type ProviderLatency struct {
	Provider string `json:"provider"`
	LatencyTotals
}

type LatencyScope struct {
	Tenant  string `json:"tenant"`
	Project string `json:"project"`
	Account string `json:"account,omitempty"`
	Region  string `json:"region,omitempty"`
	Action  string `json:"action,omitempty"`
}

type LatencyReport struct {
	AsOf      time.Time         `json:"as_of"`
	From      time.Time         `json:"from"`
	Until     time.Time         `json:"until"`
	Scope     LatencyScope      `json:"scope"`
	Summary   LatencyTotals     `json:"summary"`
	Providers []ProviderLatency `json:"providers"`
}

func (s Store) Latency(ctx context.Context, token string, scope identity.Scope, filter LatencyFilter) (LatencyReport, error) {
	if !filter.Valid() {
		return LatencyReport{}, ErrInvalidQuery
	}
	report := LatencyReport{From: filter.From.UTC(), Until: filter.Until.UTC(), Providers: []ProviderLatency{}}
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		report.Scope = LatencyScope{Tenant: actor.Tenant, Project: actor.Project, Account: filter.Account, Region: filter.Region, Action: filter.Action}
		return readLatency(ctx, tx, actor, filter, &report)
	})
	if err != nil {
		return LatencyReport{}, err
	}
	return report, nil
}

const latencySQL = `WITH cohort AS MATERIALIZED (
	SELECT o.operation_id,o.account_id,o.status,o.outcome_reason
	FROM cmp_instance_operations AS o
	WHERE (o.tenant_id,o.project_id)=($1,$2) AND o.created_at >= $3 AND o.created_at < $4
	  AND ($5='' OR o.account_id=$5) AND ($6='' OR o.region=$6) AND ($7='' OR o.action=$7)
	ORDER BY o.created_at DESC,o.operation_id DESC LIMIT $8
), cohort_size AS (SELECT count(*) AS total FROM cohort), timings AS (
	SELECT a.provider,o.status,t.id IS NOT NULL AS enqueued,
		CASE WHEN t.first_lease_at BETWEEN t.created_at AND statement_timestamp()
			THEN floor(extract(epoch FROM (t.first_lease_at-t.created_at))*1000)::bigint END AS queue_ms,
		CASE WHEN e.dispatched_at<=e.succeeded_at AND e.succeeded_at<=statement_timestamp()
			THEN floor(extract(epoch FROM (e.succeeded_at-e.dispatched_at))*1000)::bigint END AS convergence_ms
	FROM cohort AS o
	JOIN cmp_cloud_accounts AS a ON (a.tenant_id,a.account_id)=($1,o.account_id)
	LEFT JOIN cmp_operation_tasks AS t ON (t.tenant_id,t.id,t.topic)=($1,o.operation_id,$9)
	LEFT JOIN LATERAL (
		SELECT first.recorded_at AS dispatched_at,
			(SELECT recorded_at FROM cmp_instance_operation_events
				WHERE operation_id=o.operation_id AND status='succeeded' AND event_id>first.event_id
				ORDER BY event_id DESC LIMIT 1) AS succeeded_at
		FROM (SELECT event_id,recorded_at FROM cmp_instance_operation_events
			WHERE operation_id=o.operation_id AND status='applying' ORDER BY event_id LIMIT 1) AS first
		WHERE o.status='succeeded' AND o.outcome_reason IS DISTINCT FROM 'manual_review'
		AND NOT EXISTS (SELECT FROM cmp_instance_operation_events WHERE operation_id=o.operation_id AND status='needs_review')
	) AS e ON true
	WHERE (SELECT total FROM cohort_size)<$8
)
SELECT statement_timestamp(),(SELECT total FROM cohort_size),GROUPING(provider),COALESCE(provider,''),
	count(*),count(*) FILTER (WHERE enqueued),count(*) FILTER (WHERE status='succeeded'),
	count(*) FILTER (WHERE status='failed'),count(*) FILTER (WHERE status='needs_review'),
	count(*) FILTER (WHERE status NOT IN ('succeeded','failed','needs_review')),
	count(queue_ms),percentile_disc(0.5) WITHIN GROUP (ORDER BY queue_ms),
	percentile_disc(0.95) WITHIN GROUP (ORDER BY queue_ms),max(queue_ms),
	count(convergence_ms),percentile_disc(0.5) WITHIN GROUP (ORDER BY convergence_ms),
	percentile_disc(0.95) WITHIN GROUP (ORDER BY convergence_ms),max(convergence_ms)
FROM timings GROUP BY GROUPING SETS ((),(provider)) ORDER BY GROUPING(provider) DESC,provider`

func readLatency(ctx context.Context, tx *sql.Tx, actor identity.Scope, filter LatencyFilter, report *LatencyReport) (retErr error) {
	rows, err := tx.QueryContext(ctx, latencySQL, actor.Tenant, actor.Project, filter.From, filter.Until,
		filter.Account, filter.Region, filter.Action, LatencyLimit+1, topic)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	for rows.Next() {
		var group, total int
		var row ProviderLatency
		queue, converge := &row.QueueWait, &row.Convergence
		if err := rows.Scan(&report.AsOf, &total, &group, &row.Provider, &row.Accepted, &row.Enqueued, &row.Succeeded,
			&row.Failed, &row.NeedsReview, &row.Pending, &queue.Samples, &queue.P50MS, &queue.P95MS, &queue.MaxMS,
			&converge.Samples, &converge.P50MS, &converge.P95MS, &converge.MaxMS); err != nil {
			return err
		}
		if total > LatencyLimit {
			return ErrLatencyWindowTooLarge
		}
		if group == 1 {
			report.Summary = row.LatencyTotals
		} else {
			report.Providers = append(report.Providers, row)
		}
	}
	return rows.Err()
}
