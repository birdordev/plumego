package operations

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

const QueueSnapshotMigration = "0151_operation_queue_snapshot"

const QueueFindingsMigration = "0153_operation_queue_findings"

type BacklogFilter struct{ Account, Region string }

func (f BacklogFilter) Valid() bool {
	return (f.Account == "" || operationKey.MatchString(f.Account)) && (f.Region == "" || operationKey.MatchString(f.Region))
}

type BacklogCounts struct {
	AwaitingApproval   int64      `json:"awaiting_approval"`
	Ready              int64      `json:"ready"`
	Deferred           int64      `json:"deferred"`
	Leased             int64      `json:"leased"`
	Recoverable        int64      `json:"recoverable"`
	NeedsReview        int64      `json:"needs_review"`
	QueueAnomalies     int64      `json:"queue_anomalies"`
	EarliestReadyAt    *time.Time `json:"earliest_ready_at,omitempty"`
	EarliestRecoveryAt *time.Time `json:"earliest_recovery_at,omitempty"`
	NextDeferredAt     *time.Time `json:"next_deferred_at,omitempty"`
}

type ProviderBacklog struct {
	Provider string `json:"provider"`
	BacklogCounts
}

type BacklogReport struct {
	AsOf      time.Time         `json:"as_of"`
	Summary   BacklogCounts     `json:"summary"`
	Providers []ProviderBacklog `json:"providers"`
}

func (s Store) Backlog(ctx context.Context, token string, scope identity.Scope, filter BacklogFilter) (BacklogReport, error) {
	if !filter.Valid() {
		return BacklogReport{}, ErrInvalidQuery
	}
	report := BacklogReport{Providers: []ProviderBacklog{}}
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		return readBacklog(ctx, tx, actor, filter, &report)
	})
	if err != nil {
		return BacklogReport{}, err
	}
	return report, nil
}

const backlogSQL = `WITH classified AS (
	SELECT provider,available_at,created_at,lease_until,state
	FROM cmp_operation_queue_snapshot WHERE (tenant_id,project_id)=($1,$2)
	  AND ($3='' OR account_id=$3) AND ($4='' OR region=$4)
)
SELECT statement_timestamp(),GROUPING(provider),COALESCE(provider,''),
	count(*) FILTER (WHERE state='awaiting_approval'),count(*) FILTER (WHERE state='ready'),
	count(*) FILTER (WHERE state='deferred'),count(*) FILTER (WHERE state='leased'),
	count(*) FILTER (WHERE state='recoverable'),count(*) FILTER (WHERE state='needs_review'),
	count(*) FILTER (WHERE state='anomaly'),min(GREATEST(available_at,created_at)) FILTER (WHERE state='ready'),
	min(GREATEST(available_at,created_at,lease_until)) FILTER (WHERE state='recoverable'),
	min(available_at) FILTER (WHERE state='deferred')
FROM classified GROUP BY GROUPING SETS ((),(provider)) ORDER BY GROUPING(provider) DESC,provider`

func readBacklog(ctx context.Context, tx *sql.Tx, actor identity.Scope, filter BacklogFilter, report *BacklogReport) (retErr error) {
	rows, err := tx.QueryContext(ctx, backlogSQL, actor.Tenant, actor.Project, filter.Account, filter.Region)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	for rows.Next() {
		var group int
		var row ProviderBacklog
		counts := &row.BacklogCounts
		if err := rows.Scan(&report.AsOf, &group, &row.Provider, &counts.AwaitingApproval, &counts.Ready,
			&counts.Deferred, &counts.Leased, &counts.Recoverable, &counts.NeedsReview, &counts.QueueAnomalies,
			&counts.EarliestReadyAt, &counts.EarliestRecoveryAt, &counts.NextDeferredAt); err != nil {
			return err
		}
		if group == 1 {
			report.Summary = row.BacklogCounts
		} else {
			report.Providers = append(report.Providers, row)
		}
	}
	return rows.Err()
}
