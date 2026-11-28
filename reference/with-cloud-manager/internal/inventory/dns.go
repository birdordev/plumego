package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/provider"
)

var ErrDNSRecordsNotFound = errors.New("DNS zone records unavailable")

type DNSRecordsQuery struct {
	Tenant, Project, Account, ID string
	Offset, Limit                int
}

type DNSRecordsPage struct {
	ObservedAt time.Time            `json:"observed_at"`
	Records    []provider.DNSRecord `json:"records"`
	Total      int                  `json:"total"`
	NextOffset int                  `json:"next_offset,omitempty"`
}

const currentDNSZone = `s.kind='dns-zone' AND s.region='global'
	AND o.removed_at IS NULL AND o.missing_generation IS NULL
	AND a.mode IN ('read_only','write_enabled') AND a.credential_revision=s.credential_revision
	AND (a.remote_identity IS NOT NULL AND a.remote_identity<>'')`

const selectDNSZone = ` FROM cmp_inventory_objects AS o
	JOIN cmp_inventory_scopes AS s USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE (s.tenant_id,s.account_id,o.external_id,a.credential_revision)=($1,$2,$3,$4) AND ` + currentDNSZone

func (s Store) RefreshDNSRecords(ctx context.Context, key Key, id string, revision int64, adapter provider.DNSRecordReader) error {
	if key.Kind != provider.DNSZone || key.Region != provider.DNSGlobalScope || revision <= 0 ||
		!validConfigurationQueryValue(key.Tenant, 256) || !validConfigurationQueryValue(key.Account, 256) ||
		!validConfigurationQueryValue(id, 128) {
		return ErrInvalidQuery
	}
	scope := provider.Scope{Kind: key.Kind, Region: key.Region}
	zone := provider.DNSZoneRef{ID: id}
	var scopeID int64
	var parentObserved, started time.Time
	args := []any{key.Tenant, key.Account, id, revision}
	err := s.db.QueryRowContext(ctx, "SELECT s.scope_id,a.provider,a.remote_identity,o.name,o.observed_at,clock_timestamp()"+selectDNSZone, args...).
		Scan(&scopeID, &scope.Provider, &scope.RemoteIdentity, &zone.Name, &parentObserved, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	if err != nil {
		return err
	}
	records, err := adapter.ReadDNSRecords(ctx, scope, zone)
	if err != nil {
		return err
	}
	if err := provider.NormalizeDNSRecords(zone, records); err != nil {
		return err
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if len(encoded) > provider.MaxDNSProjectionBytes {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}

	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var found int64
		err := tx.QueryRowContext(ctx, "SELECT s.scope_id"+selectDNSZone+
			" AND s.scope_id=$5 AND o.observed_at=$6 AND a.remote_identity=$7 AND o.name=$8 FOR SHARE OF a,s,o",
			append(args, scopeID, parentObserved, scope.RemoteIdentity, zone.Name)...).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO cmp_dns_zone_records AS saved
			(scope_id,external_id,zone_name,credential_revision,refresh_started_at,observed_at,records)
			VALUES ($1,$2,$3,$4,$5,clock_timestamp(),$6::jsonb)
			ON CONFLICT (scope_id,external_id) DO UPDATE SET zone_name=EXCLUDED.zone_name,
				credential_revision=EXCLUDED.credential_revision, refresh_started_at=EXCLUDED.refresh_started_at,
				observed_at=EXCLUDED.observed_at, records=EXCLUDED.records, invalidated=false
			WHERE (saved.credential_revision,saved.refresh_started_at) < (EXCLUDED.credential_revision,EXCLUDED.refresh_started_at)`,
			scopeID, id, zone.Name, revision, started, string(encoded))
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err == nil && updated != 1 {
			return ErrStale
		}
		return err
	})
}

func (s Store) ReadDNSRecords(ctx context.Context, token string, q DNSRecordsQuery) (DNSRecordsPage, error) {
	if !validConfigurationQueryValue(q.Tenant, 256) || !validConfigurationQueryValue(q.Project, 256) ||
		!validConfigurationQueryValue(q.Account, 256) || !validConfigurationQueryValue(q.ID, 128) ||
		q.Offset < 0 || q.Offset > provider.MaxDNSRecords || q.Limit < 1 || q.Limit > 100 {
		return DNSRecordsPage{}, ErrInvalidQuery
	}
	var result DNSRecordsPage
	err := s.readProject(ctx, token, Query{Tenant: q.Tenant, Project: q.Project}, func(tx *sql.Tx) error {
		var records []byte
		err := tx.QueryRowContext(ctx, `SELECT snapshot.observed_at,jsonb_array_length(snapshot.records),
			COALESCE((SELECT jsonb_agg(item.value ORDER BY item.position)
				FROM jsonb_array_elements(snapshot.records) WITH ORDINALITY AS item(value,position)
				WHERE item.position > $5::integer AND item.position <= $5::integer+$6::integer),'[]'::jsonb)
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			JOIN cmp_inventory_objects AS o USING (scope_id)
			JOIN cmp_dns_zone_records AS snapshot ON (snapshot.scope_id,snapshot.external_id)=(s.scope_id,o.external_id)
				AND snapshot.credential_revision=a.credential_revision AND snapshot.zone_name=o.name AND NOT snapshot.invalidated`+inventoryOwnershipJoins+
			` WHERE `+assetVisibility+` AND `+currentDNSZone+` AND (s.account_id,o.external_id)=($3,$4)`,
			q.Tenant, q.Project, q.Account, q.ID, q.Offset, q.Limit).Scan(&result.ObservedAt, &result.Total, &records)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDNSRecordsNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(records, &result.Records)
	})
	if err != nil {
		return DNSRecordsPage{}, err
	}
	if end := q.Offset + len(result.Records); end < result.Total {
		result.NextOffset = end
	}
	return result, nil
}
