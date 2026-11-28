package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/migrations"
)

const DatabaseProtectionMigration = "0171_database_protection"
const DefaultDatabaseProtectionWindow = 86400
const DefaultDatabaseProtectionLimit = 50

func MigrateDatabaseProtection(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, DatabaseProtectionMigration)
}

type DatabaseProtectionQuery struct {
	Tenant, Project, Provider, Account, Region, Finding, After string
	Window, MaxAge, Limit                                      int
}

type DatabaseProtectionCounts struct {
	Ready       int64 `json:"ready"`
	Outdated    int64 `json:"outdated"`
	Missing     int64 `json:"missing"`
	Stale       int64 `json:"stale"`
	Unobserved  int64 `json:"unobserved"`
	Unsupported int64 `json:"unsupported"`
}

type DatabaseProtectionSummary struct {
	Total                int64                    `json:"total"`
	InventoryUnavailable int64                    `json:"inventory_unavailable"`
	RefreshFailed        int64                    `json:"refresh_failed"`
	Backups              DatabaseProtectionCounts `json:"backups"`
	RestoreWindows       DatabaseProtectionCounts `json:"restore_windows"`
}

type DatabaseProtectionEvidence struct {
	Status             string     `json:"status"`
	ObservedAt         *time.Time `json:"observed_at,omitempty"`
	LatestEvidenceAt   *time.Time `json:"latest_evidence_at,omitempty"`
	Scope              string     `json:"scope,omitempty"`
	ScheduleEnabled    *bool      `json:"schedule_enabled,omitempty"`
	LastRefreshOutcome string     `json:"last_refresh_outcome,omitempty"`
	LastRefreshAt      *time.Time `json:"last_refresh_at,omitempty"`
}

type DatabaseProtectionItem struct {
	ID                 string                     `json:"id"`
	Name               string                     `json:"name"`
	Account            string                     `json:"account"`
	Region             string                     `json:"region"`
	Provider           string                     `json:"provider"`
	Product            string                     `json:"product"`
	Engine             string                     `json:"engine"`
	InventoryAvailable bool                       `json:"inventory_available"`
	Backups            DatabaseProtectionEvidence `json:"backups"`
	RestoreWindows     DatabaseProtectionEvidence `json:"restore_windows"`
}

type DatabaseProtectionReport struct {
	AsOf    time.Time                 `json:"as_of"`
	Window  int                       `json:"window_seconds"`
	MaxAge  int                       `json:"max_age_seconds"`
	Summary DatabaseProtectionSummary `json:"summary"`
	Items   []DatabaseProtectionItem  `json:"items"`
	Next    string                    `json:"next,omitempty"`
}

func (q DatabaseProtectionQuery) position() (account, region, id string, err error) {
	if !validConfigurationQueryValue(q.Tenant, 256) || !validConfigurationQueryValue(q.Project, 256) ||
		q.Window < 60 || q.Window > 604800 || q.MaxAge < 60 || q.MaxAge > 2592000 || q.Limit < 1 || q.Limit > 100 ||
		(q.Account != "" && !freshnessFilterID.MatchString(q.Account)) ||
		(q.Region != "" && !freshnessFilterID.MatchString(q.Region)) {
		return "", "", "", ErrInvalidQuery
	}
	switch q.Provider {
	case "", provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
	default:
		return "", "", "", ErrInvalidQuery
	}
	switch q.Finding {
	case "", "inventory_unavailable", "backup_attention", "recovery_attention", "refresh_failed":
	default:
		return "", "", "", ErrInvalidQuery
	}
	if q.After == "" {
		return "", "", "", nil
	}
	account, rest, ok := strings.Cut(q.After, "/")
	region, id, hasID := strings.Cut(rest, "/")
	if !ok || !hasID || !freshnessFilterID.MatchString(account) || !freshnessFilterID.MatchString(region) ||
		strings.Contains(id, "/") || !validConfigurationQueryValue(id, 128) ||
		(q.Account != "" && account != q.Account) || (q.Region != "" && region != q.Region) {
		return "", "", "", ErrInvalidQuery
	}
	return account, region, id, nil
}

const databaseProtectionBase = `WITH visible AS (
	SELECT o.external_id AS id,o.name,s.account_id AS account,s.region,a.provider,
		o.database_instance->>'product' AS product,o.database_instance->>'engine' AS engine,
		COALESCE((` + currentObservedDatabase + `),false) AS inventory_available,
		b.observed_at AS backup_observed_at,b.latest_ready_started_at,
		w.observed_at AS restore_observed_at,w.latest_window_end_at,w.recovery_scope,
		bs.enabled AS backup_schedule,bs.last_outcome AS backup_outcome,bs.last_finished_at AS backup_finished_at,
		ws.enabled AS restore_schedule,ws.last_outcome AS restore_outcome,ws.last_finished_at AS restore_finished_at
	FROM cmp_inventory_scopes AS s
	JOIN cmp_inventory_objects AS o USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	LEFT JOIN cmp_database_backups AS b ON (b.scope_id,b.external_id)=(s.scope_id,o.external_id)
		AND b.credential_revision=a.credential_revision AND b.product=o.database_instance->>'product'
		AND b.engine=o.database_instance->>'engine'
	LEFT JOIN cmp_database_restore_windows AS w ON (w.scope_id,w.external_id)=(s.scope_id,o.external_id)
		AND w.credential_revision=a.credential_revision AND w.product=o.database_instance->>'product'
		AND w.engine=o.database_instance->>'engine'
	LEFT JOIN cmp_inventory_schedules AS bs ON
		(bs.tenant_id,bs.account_id,bs.region,bs.kind,bs.target_id)=(s.tenant_id,s.account_id,s.region,'database-backups',o.external_id)
		AND a.project_id=$2
	LEFT JOIN cmp_inventory_schedules AS ws ON
		(ws.tenant_id,ws.account_id,ws.region,ws.kind,ws.target_id)=(s.tenant_id,s.account_id,s.region,'database-restore-windows',o.external_id)
		AND a.project_id=$2` + directInventoryOwnershipJoins + `
	WHERE s.tenant_id=$1 AND s.kind='database-instance' AND o.removed_at IS NULL AND o.database_instance IS NOT NULL
		AND a.provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs') AND (` + directAssetVisibility + `)
		AND ($3='' OR a.provider=$3) AND ($4='' OR s.account_id=$4) AND ($5='' OR s.region=$5)
)`

func (s Store) DatabaseProtection(ctx context.Context, token string, q DatabaseProtectionQuery) (DatabaseProtectionReport, error) {
	account, region, id, err := q.position()
	if err != nil {
		return DatabaseProtectionReport{}, err
	}
	report := DatabaseProtectionReport{Window: q.Window, MaxAge: q.MaxAge}
	var summary, items []byte
	err = s.readProject(ctx, token, Query{Tenant: q.Tenant, Project: q.Project}, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, databaseProtectionBase+migrations.DatabaseProtection(),
			q.Tenant, q.Project, q.Provider, q.Account, q.Region, q.Window, q.MaxAge, q.Finding, account, region, id, q.Limit+1).
			Scan(&report.AsOf, &summary, &items)
	})
	if err != nil {
		return DatabaseProtectionReport{}, err
	}
	if err := json.Unmarshal(summary, &report.Summary); err != nil {
		return DatabaseProtectionReport{}, err
	}
	if err := json.Unmarshal(items, &report.Items); err != nil {
		return DatabaseProtectionReport{}, err
	}
	if len(report.Items) > q.Limit {
		report.Items = report.Items[:q.Limit]
		last := report.Items[q.Limit-1]
		report.Next = last.Account + "/" + last.Region + "/" + last.ID
	}
	return report, nil
}
