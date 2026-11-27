package controller

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

const scheduleFields = "schedule_id::text,region,kind,interval_seconds,enabled,config_revision,next_at,failures,COALESCE(last_outcome,''),last_finished_at"

var regionIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

type Schedule struct {
	ID           string        `json:"id"`
	Region       string        `json:"region"`
	Kind         provider.Kind `json:"kind"`
	Interval     int           `json:"interval_seconds"`
	Enabled      bool          `json:"enabled"`
	Revision     int64         `json:"revision"`
	NextAt       time.Time     `json:"next_at"`
	Failures     int           `json:"failures"`
	LastOutcome  string        `json:"last_outcome,omitempty"`
	LastFinished *time.Time    `json:"last_finished_at,omitempty"`
}

type ScheduleSettings struct {
	Interval int   `json:"interval_seconds"`
	Enabled  *bool `json:"enabled"`
}

type RegisterSchedule struct {
	Region string        `json:"region"`
	Kind   provider.Kind `json:"kind"`
	ScheduleSettings
}

type ChangeSchedule struct {
	ScheduleSettings
	RunNow bool `json:"run_now,omitempty"`
}

func (s ScheduleSettings) valid() bool {
	return s.Interval >= 60 && s.Interval <= 604800 && s.Enabled != nil
}

func (s RegisterSchedule) valid() bool {
	if !s.ScheduleSettings.valid() || !s.Kind.Valid() || s.Kind == provider.BareMetal {
		return false
	}
	switch s.Kind {
	case provider.Region:
		return s.Region == ""
	case provider.DNSZone:
		return s.Region == provider.DNSGlobalScope
	case provider.ObjectBucket:
		return s.Region == provider.ObjectBucketGlobalScope
	default:
		return regionIdentifier.MatchString(s.Region) && s.Region != "global"
	}
}

func ParseScheduleID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, identity.ErrAccountInvalid
	}
	return id, nil
}

func (s Store) scheduleAccount(ctx context.Context, token string, scope identity.Scope, account string, write bool,
	work func(*sql.Tx, identity.Scope, bool) error) error {
	return dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: !write}, func(tx *sql.Tx) error {
		auth := identity.NewStore(tx)
		actor, err := auth.AuthenticateRequest(ctx, token)
		if err != nil {
			return err
		}
		if actor.Tenant != scope.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = scope.Project
		if _, err := auth.AuthorizeProject(ctx, actor, identity.Manage); err != nil {
			return err
		}
		query := `SELECT provider,mode FROM cmp_cloud_accounts WHERE (tenant_id,project_id,account_id)=($1,$2,$3)`
		if write {
			query += " FOR SHARE"
		}
		var name, mode string
		err = tx.QueryRowContext(ctx, query, actor.Tenant, actor.Project, account).Scan(&name, &mode)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		switch name {
		case provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
			return work(tx, actor, mode == "read_only" || mode == "write_enabled")
		default:
			return identity.ErrForbidden
		}
	})
}

func scanSchedule(row interface{ Scan(...any) error }) (Schedule, error) {
	var item Schedule
	err := row.Scan(&item.ID, &item.Region, &item.Kind, &item.Interval, &item.Enabled, &item.Revision,
		&item.NextAt, &item.Failures, &item.LastOutcome, &item.LastFinished)
	return item, err
}

func (s Store) ListSchedules(ctx context.Context, token string, scope identity.Scope, account string,
	after int64, limit int) (identity.AccountPage[Schedule], error) {
	if after < 0 || limit < 1 || limit > 100 {
		return identity.AccountPage[Schedule]{}, identity.ErrAccountInvalid
	}
	page := identity.AccountPage[Schedule]{Items: make([]Schedule, 0, limit)}
	err := s.scheduleAccount(ctx, token, scope, account, false, func(tx *sql.Tx, actor identity.Scope, _ bool) error {
		rows, err := tx.QueryContext(ctx, "SELECT "+scheduleFields+`
			FROM cmp_inventory_schedules AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			WHERE (s.tenant_id,a.project_id,s.account_id)=($1,$2,$3) AND s.target_id='' AND s.schedule_id>$4
			ORDER BY s.schedule_id LIMIT $5`, actor.Tenant, actor.Project, account, after, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			if len(page.Items) == limit {
				page.Next = page.Items[limit-1].ID
				break
			}
			item, err := scanSchedule(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	return page, err
}
