package inventory

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

const DefaultBucketProtectionWindow = 86400
const DefaultBucketProtectionLimit = 50

var bucketProtectionLocation = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type BucketProtectionQuery struct {
	Tenant, Project, Provider, Account, Location, Finding, After string
	Window, Limit                                                int
}

type BucketProtectionCount struct {
	Fresh      int64 `json:"fresh"`
	Stale      int64 `json:"stale"`
	Unobserved int64 `json:"unobserved"`
	Flagged    int64 `json:"flagged"`
}

type BucketProtectionSummary struct {
	Total                int64                 `json:"total"`
	InventoryUnavailable int64                 `json:"inventory_unavailable"`
	Encryption           BucketProtectionCount `json:"encryption"`
	Versioning           BucketProtectionCount `json:"versioning"`
}

type BucketProtectionObservation struct {
	Status     string     `json:"status"`
	State      string     `json:"state,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type BucketProtectionItem struct {
	ID                 string                      `json:"id"`
	Name               string                      `json:"name"`
	Provider           string                      `json:"provider"`
	Account            string                      `json:"account"`
	Location           string                      `json:"location"`
	InventoryAvailable bool                        `json:"inventory_available"`
	Encryption         BucketProtectionObservation `json:"encryption"`
	Versioning         BucketProtectionObservation `json:"versioning"`
}

type BucketProtectionReport struct {
	AsOf    time.Time               `json:"as_of"`
	Window  int                     `json:"window_seconds"`
	Summary BucketProtectionSummary `json:"summary"`
	Items   []BucketProtectionItem  `json:"items"`
	Next    string                  `json:"next,omitempty"`
}

func (q BucketProtectionQuery) position() (account, id string, err error) {
	if !validConfigurationQueryValue(q.Tenant, 256) || !validConfigurationQueryValue(q.Project, 256) ||
		q.Window < 60 || q.Window > 604800 || q.Limit < 1 || q.Limit > 100 ||
		(q.Account != "" && !freshnessFilterID.MatchString(q.Account)) ||
		(q.Location != "" && !bucketProtectionLocation.MatchString(q.Location)) {
		return "", "", ErrInvalidQuery
	}
	switch q.Provider {
	case "", provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
	default:
		return "", "", ErrInvalidQuery
	}
	switch q.Finding {
	case "", "inventory_unavailable", "encryption_unconfigured", "versioning_inactive", "encryption_needs_observation", "versioning_needs_observation":
	default:
		return "", "", ErrInvalidQuery
	}
	if q.After == "" {
		return "", "", nil
	}
	account, id, ok := strings.Cut(q.After, "/")
	if !ok || !freshnessFilterID.MatchString(account) || !provider.ValidBucketAssetID(id) || (q.Account != "" && q.Account != account) {
		return "", "", ErrInvalidQuery
	}
	return account, id, nil
}

const bucketProtectionSnapshotJoins = `
	LEFT JOIN cmp_bucket_encryption AS e ON (e.scope_id,e.external_id)=(s.scope_id,o.external_id)
		AND e.credential_revision=a.credential_revision AND e.location=o.object_bucket->>'location'
	LEFT JOIN cmp_bucket_versioning AS v ON (v.scope_id,v.external_id)=(s.scope_id,o.external_id)
		AND v.credential_revision=a.credential_revision AND v.location=o.object_bucket->>'location'`

const bucketProtectionBase = `WITH visible AS (
	SELECT o.external_id AS id,o.name,s.account_id AS account,a.provider,o.object_bucket->>'location' AS location,
		COALESCE((` + currentObjectBucket + `),false) AS inventory_available,
		e.state AS encryption_state,e.observed_at AS encryption_time,v.state AS versioning_state,v.observed_at AS versioning_time
	FROM cmp_inventory_scopes AS s
	JOIN cmp_inventory_objects AS o USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)` + bucketProtectionSnapshotJoins + directInventoryOwnershipJoins + `
	WHERE s.tenant_id=$1 AND s.kind='object-bucket' AND s.region='global'
		AND o.removed_at IS NULL AND o.object_bucket IS NOT NULL
		AND a.provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs') AND (` + directAssetVisibility + `)
		AND ($3='' OR a.provider=$3) AND ($4='' OR s.account_id=$4)
		AND ($5='' OR o.object_bucket->>'location'=$5)
)`

//go:embed bucket_protection.sql
var bucketProtectionSQL string

func (s Store) BucketProtection(ctx context.Context, token string, q BucketProtectionQuery) (BucketProtectionReport, error) {
	account, id, err := q.position()
	if err != nil {
		return BucketProtectionReport{}, err
	}
	report := BucketProtectionReport{Window: q.Window}
	var items []byte
	err = s.readProject(ctx, token, Query{Tenant: q.Tenant, Project: q.Project}, func(tx *sql.Tx) error {
		summary := &report.Summary
		return tx.QueryRowContext(ctx, bucketProtectionBase+bucketProtectionSQL,
			q.Tenant, q.Project, q.Provider, q.Account, q.Location, q.Window, q.Finding, account, id, q.Limit+1).Scan(
			&report.AsOf, &summary.Total, &summary.InventoryUnavailable,
			&summary.Encryption.Fresh, &summary.Encryption.Stale, &summary.Encryption.Unobserved, &summary.Encryption.Flagged,
			&summary.Versioning.Fresh, &summary.Versioning.Stale, &summary.Versioning.Unobserved, &summary.Versioning.Flagged, &items)
	})
	if err != nil {
		return BucketProtectionReport{}, err
	}
	if err := json.Unmarshal(items, &report.Items); err != nil {
		return BucketProtectionReport{}, err
	}
	if len(report.Items) > q.Limit {
		report.Items = report.Items[:q.Limit]
		last := report.Items[q.Limit-1]
		report.Next = last.Account + "/" + last.ID
	}
	return report, nil
}
