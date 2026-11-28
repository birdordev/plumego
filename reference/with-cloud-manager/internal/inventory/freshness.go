package inventory

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/migrations"
)

const DefaultFreshnessWindow = 900
const DefaultFreshnessLimit = 50

var freshnessFilterID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type FreshnessQuery struct {
	Tenant, Project                 string
	Provider, Account, State, After string
	DispatchState, Order            string
	Window, Limit                   int
	ScopeFingerprint                bool
}

type FreshnessSummary struct {
	Total   int64 `json:"total"`
	Fresh   int64 `json:"fresh"`
	Stale   int64 `json:"stale"`
	Pending int64 `json:"pending"`
}

type FreshnessDispatchSummary struct {
	Scheduled           int64 `json:"scheduled"`
	Ready               int64 `json:"ready"`
	AccountBusy         int64 `json:"account_busy"`
	Cooldown            int64 `json:"cooldown"`
	Leased              int64 `json:"leased"`
	LongestReadyOverdue int64 `json:"longest_ready_overdue_seconds"`
}

type FreshnessItem struct {
	Account              string     `json:"account"`
	Provider             string     `json:"provider"`
	Region               string     `json:"region"`
	Kind                 string     `json:"kind"`
	State                string     `json:"state"`
	Running              bool       `json:"running"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	NextAt               time.Time  `json:"next_at"`
	DispatchState        string     `json:"dispatch_state"`
	ScheduleLeaseUntil   *time.Time `json:"schedule_lease_until,omitempty"`
	AccountLeaseUntil    *time.Time `json:"account_lease_until,omitempty"`
	AccountCooldownUntil *time.Time `json:"account_cooldown_until,omitempty"`
}

type FreshnessReport struct {
	AsOf             time.Time                `json:"as_of"`
	Window           int                      `json:"window_seconds"`
	Summary          FreshnessSummary         `json:"summary"`
	DispatchSummary  FreshnessDispatchSummary `json:"dispatch_summary"`
	ScopeFingerprint string                   `json:"scope_fingerprint,omitempty"`
	Items            []FreshnessItem          `json:"items"`
	Next             string                   `json:"next,omitempty"`
}

func (q FreshnessQuery) position() (int64, *time.Time, error) {
	if q.Window < 60 || q.Window > 604800 || q.Limit < 1 || q.Limit > 100 ||
		(q.Provider != "" && !freshnessFilterID.MatchString(q.Provider)) || (q.Account != "" && !freshnessFilterID.MatchString(q.Account)) {
		return 0, nil, ErrInvalidQuery
	}
	switch q.State {
	case "", "fresh", "stale", "pending":
	default:
		return 0, nil, ErrInvalidQuery
	}
	switch q.DispatchState {
	case "", "scheduled", "ready", "account_busy", "cooldown", "leased":
	default:
		return 0, nil, ErrInvalidQuery
	}
	if q.Order != "" && q.Order != "next_at" {
		return 0, nil, ErrInvalidQuery
	}
	if q.After == "" {
		return 0, nil, nil
	}
	var nextAt *time.Time
	idText := q.After
	if q.Order == "next_at" {
		stamp, id, ok := strings.Cut(q.After, "/")
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if !ok || err != nil || at.UTC().Format(time.RFC3339Nano) != stamp || at.Nanosecond()%1000 != 0 {
			return 0, nil, ErrInvalidQuery
		}
		nextAt, idText = &at, id
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != idText {
		return 0, nil, ErrInvalidQuery
	}
	return id, nextAt, nil
}

func (s Store) Freshness(ctx context.Context, token string, q FreshnessQuery) (FreshnessReport, error) {
	after, afterTime, err := q.position()
	if err != nil {
		return FreshnessReport{}, err
	}
	report := FreshnessReport{Window: q.Window, Items: make([]FreshnessItem, 0, q.Limit)}
	err = s.readProject(ctx, token, Query{Tenant: q.Tenant, Project: q.Project}, func(tx *sql.Tx) error {
		args := []any{q.Tenant, q.Project, q.Provider, q.Account, q.Window}
		dispatch := &report.DispatchSummary
		err := tx.QueryRowContext(ctx, migrations.Freshness()+`
			SELECT transaction_timestamp(),count(*),count(*) FILTER (WHERE state='fresh'),
				count(*) FILTER (WHERE state='stale'),count(*) FILTER (WHERE state='pending'),
				count(*) FILTER (WHERE dispatch_state='scheduled'),count(*) FILTER (WHERE dispatch_state='ready'),
				count(*) FILTER (WHERE dispatch_state='account_busy'),count(*) FILTER (WHERE dispatch_state='cooldown'),
				count(*) FILTER (WHERE dispatch_state='leased'),
				COALESCE(FLOOR(EXTRACT(epoch FROM transaction_timestamp()-MIN(next_at) FILTER (WHERE dispatch_state='ready'))),0)::bigint
			FROM freshness`, args...).Scan(&report.AsOf, &report.Summary.Total, &report.Summary.Fresh, &report.Summary.Stale, &report.Summary.Pending,
			&dispatch.Scheduled, &dispatch.Ready, &dispatch.AccountBusy, &dispatch.Cooldown, &dispatch.Leased, &dispatch.LongestReadyOverdue)
		if err != nil {
			return err
		}
		if q.ScopeFingerprint {
			report.ScopeFingerprint, err = readFreshnessFingerprint(ctx, tx, args)
			if err != nil {
				return err
			}
		}
		pageArgs := append(args, q.State, q.DispatchState, q.Limit+1, after)
		pageOrder := "AND schedule_id>$9 ORDER BY schedule_id LIMIT $8"
		if q.Order == "next_at" {
			pageArgs = append(pageArgs, afterTime)
			pageOrder = "AND ($10::timestamptz IS NULL OR (next_at,schedule_id)>($10,$9)) ORDER BY next_at,schedule_id LIMIT $8"
		}
		rows, err := tx.QueryContext(ctx, migrations.Freshness()+`
			SELECT schedule_id,account_id,provider,region,kind,state,running,completed_at,next_at,
				dispatch_state,schedule_lease_until,account_lease_until,account_cooldown_until
			FROM freshness WHERE ($6='' OR state=$6) AND ($7='' OR dispatch_state=$7)
			`+pageOrder, pageArgs...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		var last int64
		for rows.Next() {
			if len(report.Items) == q.Limit {
				report.Next = strconv.FormatInt(last, 10)
				if q.Order == "next_at" {
					nextAt := report.Items[len(report.Items)-1].NextAt.UTC()
					report.Next = nextAt.Format(time.RFC3339Nano) + "/" + report.Next
				}
				break
			}
			var item FreshnessItem
			if err := rows.Scan(&last, &item.Account, &item.Provider, &item.Region, &item.Kind,
				&item.State, &item.Running, &item.CompletedAt, &item.NextAt, &item.DispatchState,
				&item.ScheduleLeaseUntil, &item.AccountLeaseUntil, &item.AccountCooldownUntil); err != nil {
				return err
			}
			report.Items = append(report.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return FreshnessReport{}, err
	}
	return report, nil
}
