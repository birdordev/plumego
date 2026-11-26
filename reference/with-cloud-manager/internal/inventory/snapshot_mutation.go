package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

type SnapshotMutationTarget struct {
	MutationTarget
	VolumeScopeID int64
	Name, Class   string
}

func ResolveSnapshotMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, snapshotID, volumeID string,
	permission identity.Permission, present bool) (SnapshotMutationTarget, error) {
	source, err := resolveMutation(ctx, tx, actor, account, region, volumeID, provider.Volume, false, permission, true)
	if err != nil {
		return SnapshotMutationTarget{}, err
	}
	target := SnapshotMutationTarget{MutationTarget: source, VolumeScopeID: source.ScopeID}
	err = tx.QueryRowContext(ctx, `SELECT s.scope_id,o.name,COALESCE(o.snapshot->>'class','')
		FROM cmp_inventory_scopes AS s JOIN cmp_inventory_objects AS o USING(scope_id)
		WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,'snapshot',$4)
			AND (o.links->0->>'kind',o.links->0->>'relation',o.links->0->>'id')=('volume','snapshot_of',$5)
			AND o.deletion_confirmed_at IS NULL
			AND (NOT $6::bool OR (o.removed_at IS NULL AND o.missing_generation IS NULL AND s.credential_revision=$7))
		FOR UPDATE OF o`, actor.Tenant, account, region, snapshotID, volumeID, present, source.CredentialRevision).
		Scan(&target.ScopeID, &target.Name, &target.Class)
	if errors.Is(err, sql.ErrNoRows) {
		return SnapshotMutationTarget{}, identity.ErrForbidden
	}
	return target, err
}
