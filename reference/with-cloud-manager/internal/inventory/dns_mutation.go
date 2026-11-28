package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func DNSRecordTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, zoneID, recordID string, observed time.Time) (provider.DNSZoneRef, provider.DNSRecord, error) {
	zone := provider.DNSZoneRef{ID: zoneID}
	var encoded []byte
	var record provider.DNSRecord
	err := tx.QueryRowContext(ctx, `SELECT snapshot.zone_name,item.value
		FROM cmp_dns_zone_records AS snapshot
		JOIN cmp_inventory_objects AS o USING(scope_id,external_id)
		CROSS JOIN LATERAL jsonb_array_elements(snapshot.records) AS item(value)
		WHERE (snapshot.scope_id,snapshot.external_id,snapshot.credential_revision)=($1,$2,$3)
		  AND NOT snapshot.invalidated AND snapshot.zone_name=o.name AND item.value->>'id'=$4
		  AND ($5::timestamptz IS NULL OR snapshot.observed_at=$5)`, target.ScopeID, zoneID, target.CredentialRevision,
		recordID, sql.NullTime{Time: observed, Valid: !observed.IsZero()}).Scan(&zone.Name, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	if err != nil {
		return zone, record, err
	}
	err = json.Unmarshal(encoded, &record)
	return zone, record, err
}

func DNSCreationTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, zoneID, name string, observed time.Time) (provider.DNSZoneRef, []provider.DNSRecord, error) {
	zone := provider.DNSZoneRef{ID: zoneID}
	var encoded []byte
	err := tx.QueryRowContext(ctx, `SELECT snapshot.zone_name,COALESCE((
			SELECT jsonb_agg(item.value)
			FROM jsonb_array_elements(snapshot.records) AS item(value)
			WHERE item.value->>'name'=$5 OR (item.value->>'type' IN ('NS','DNAME') AND right($5::text,length(item.value->>'name')+1)='.'||(item.value->>'name'))
		),'[]'::jsonb)
		FROM cmp_dns_zone_records AS snapshot JOIN cmp_inventory_objects AS o USING(scope_id,external_id)
		WHERE (snapshot.scope_id,snapshot.external_id,snapshot.credential_revision)=($1,$2,$3)
		  AND NOT snapshot.invalidated AND snapshot.zone_name=o.name
		  AND ($4::timestamptz IS NULL OR snapshot.observed_at=$4)`, target.ScopeID, zoneID, target.CredentialRevision,
		sql.NullTime{Time: observed, Valid: !observed.IsZero()}, name).Scan(&zone.Name, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	if err != nil {
		return zone, nil, err
	}
	var records []provider.DNSRecord
	err = json.Unmarshal(encoded, &records)
	return zone, records, err
}

func CheckDNSZoneName(ctx context.Context, tx *sql.Tx, scopeID int64, zone provider.DNSZoneRef) error {
	var found bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_inventory_objects
		WHERE (scope_id,external_id,name)=($1,$2,$3) AND removed_at IS NULL AND missing_generation IS NULL)`,
		scopeID, zone.ID, zone.Name).Scan(&found); err != nil {
		return err
	}
	if !found {
		return identity.ErrAccountStale
	}
	return nil
}

func InvalidateDNSRecords(ctx context.Context, tx *sql.Tx, scopeID int64, zoneID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_dns_zone_records SET invalidated=true,refresh_started_at=clock_timestamp()
		WHERE (scope_id,external_id)=($1,$2)`, scopeID, zoneID)
	return err
}

func dnsZoneChangeActive(ctx context.Context, tx *sql.Tx, scopeID int64, id string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_dns_zone_change_reservations
		WHERE (scope_id,external_id)=($1,$2))`, scopeID, id).Scan(&active)
	return active, err
}
