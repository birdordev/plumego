package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

type VolumeReleaseState struct {
	Name      string
	SizeGiB   int
	DataDisk  bool
	Attached  bool
	Snapshots bool
}

func ReadVolumeReleaseState(ctx context.Context, tx *sql.Tx, target MutationTarget, id string) (VolumeReleaseState, error) {
	var state VolumeReleaseState
	var status, usage, bootable string
	err := tx.QueryRowContext(ctx, `SELECT o.name,(o.volume_details->>'size_gib')::integer,o.status,
		COALESCE(o.volume_details->>'usage',''),COALESCE(o.volume_details->>'bootable',''),
		EXISTS(SELECT 1 FROM jsonb_array_elements(o.links) AS link WHERE link->>'kind'='instance') OR EXISTS(
			SELECT 1 FROM cmp_inventory_scopes AS other JOIN cmp_inventory_objects AS i USING(scope_id)
			WHERE (other.tenant_id,other.account_id,other.region)=(s.tenant_id,s.account_id,s.region)
			  AND other.kind='instance' AND i.removed_at IS NULL
			  AND i.links @> jsonb_build_array(jsonb_build_object('kind','volume','id',$2::text))),
		EXISTS(SELECT 1 FROM cmp_inventory_scopes AS other JOIN cmp_inventory_objects AS snap USING(scope_id)
			WHERE (other.tenant_id,other.account_id,other.region)=(s.tenant_id,s.account_id,s.region)
			  AND other.kind='snapshot' AND snap.removed_at IS NULL
			  AND snap.links @> jsonb_build_array(jsonb_build_object('kind','volume','id',$2::text,'relation','snapshot_of')))
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (o.scope_id,o.external_id)=($1,$2) AND s.credential_revision=$3
		  AND o.volume_details->>'size_gib' IS NOT NULL`,
		target.ScopeID, id, target.CredentialRevision).Scan(&state.Name, &state.SizeGiB, &status, &usage, &bootable, &state.Attached, &state.Snapshots)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	state.DataDisk = (target.Provider == provider.HuaweiECS && bootable == "false") ||
		(target.Provider != provider.HuaweiECS && usage == "data")
	state.Attached = state.Attached || (status != "UNATTACHED" && status != "Available" && status != "available")
	return state, err
}

func MarkVolumeReleased(ctx context.Context, tx *sql.Tx, scopeID int64, id string) error {
	var matched string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_inventory_objects
		SET removed_at=COALESCE(removed_at,statement_timestamp()),missing_generation=NULL
		WHERE (scope_id,external_id)=($1,$2) AND deletion_confirmed_at IS NULL
		RETURNING external_id`, scopeID, id).Scan(&matched)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrAccountStale
	}
	if err != nil {
		return err
	}
	return restartActiveInventoryScan(ctx, tx, scopeID)
}
