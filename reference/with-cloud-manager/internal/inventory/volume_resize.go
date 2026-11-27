package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func LockVolumeScope(ctx context.Context, tx *sql.Tx, tenant, account, region string) (int64, error) {
	return lockInventoryScope(ctx, tx, tenant, account, region, provider.Volume)
}

func VolumeResizeTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, id string) (size int, detached bool, err error) {
	var state string
	var linked bool
	err = tx.QueryRowContext(ctx, `SELECT (o.volume_details->>'size_gib')::integer,o.status,
		EXISTS(SELECT 1 FROM jsonb_array_elements(o.links) AS item WHERE item->>'kind'='instance')
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (o.scope_id,o.external_id)=($1,$2) AND s.credential_revision=$3
		  AND o.volume_details->>'size_gib' IS NOT NULL`,
		target.ScopeID, id, target.CredentialRevision).Scan(&size, &state, &linked)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	return size, !linked && (state == "UNATTACHED" || state == "Available" || state == "available"), err
}

func RecordObservedVolumeSize(ctx context.Context, tx *sql.Tx, scopeID int64, change provider.VolumeResize) error {
	var id string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_inventory_objects
		SET volume_details=jsonb_set(volume_details,'{size_gib}',to_jsonb($4::integer)),observed_at=statement_timestamp()
		WHERE (scope_id,external_id)=($1,$2) AND (volume_details->>'size_gib')::integer IN ($3,$4)
		  AND removed_at IS NULL AND missing_generation IS NULL RETURNING external_id`,
		scopeID, change.ID, change.CurrentGiB, change.TargetGiB).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrAccountStale
	}
	if err != nil {
		return err
	}
	return restartActiveInventoryScan(ctx, tx, scopeID)
}
