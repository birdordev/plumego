package inventory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func BucketVersioningMutationTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, id string) (string, provider.BucketVersioning, error) {
	var location string
	var state provider.BucketVersioning
	err := tx.QueryRowContext(ctx, `SELECT o.object_bucket->>'location',COALESCE(v.state,'')
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING (scope_id)
		LEFT JOIN cmp_bucket_versioning AS v ON (v.scope_id,v.external_id)=(o.scope_id,o.external_id)
			AND v.credential_revision=s.credential_revision AND v.location=o.object_bucket->>'location'
		WHERE (o.scope_id,o.external_id,s.credential_revision)=($1,$2,$3)
		AND s.kind='object-bucket' AND o.object_bucket IS NOT NULL`, target.ScopeID, id, target.CredentialRevision).Scan(&location, &state)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	return location, state, err
}

func RecordObservedBucketVersioning(ctx context.Context, tx *sql.Tx, target MutationTarget, change provider.BucketVersioningChange, started time.Time) error {
	var state provider.BucketVersioning
	err := tx.QueryRowContext(ctx, `INSERT INTO cmp_bucket_versioning AS previous
		(scope_id,external_id,credential_revision,location,state,refresh_started_at,observed_at)
		VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp())
		ON CONFLICT (scope_id,external_id) DO UPDATE SET credential_revision=EXCLUDED.credential_revision,
			location=EXCLUDED.location,state=EXCLUDED.state,refresh_started_at=EXCLUDED.refresh_started_at,observed_at=EXCLUDED.observed_at
		WHERE (previous.credential_revision,previous.refresh_started_at) < (EXCLUDED.credential_revision,EXCLUDED.refresh_started_at)
		RETURNING state`, target.ScopeID, change.ID, target.CredentialRevision, change.Location, change.Target, started).Scan(&state)
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	location, state, err := BucketVersioningMutationTarget(ctx, tx, target, change.ID)
	if err == nil && (location != change.Location || state != change.Target) {
		err = identity.ErrAccountStale
	}
	return err
}
