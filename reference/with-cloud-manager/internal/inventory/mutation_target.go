package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

type MutationTarget struct {
	ScopeID           int64
	OwnershipRevision int64
	identity.AccountAccess
}

func ResolveInstanceMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, provider.Instance, true, identity.Execute, true)
}

func ResolveInstanceObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, provider.Instance, false, identity.Execute, true)
}

func ResolveVolumeMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, provider.Volume, true, identity.Execute, true)
}

func ResolveMutationPreview(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, kind provider.Kind) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, kind, true, identity.Execute, false)
}

func ResolveNetworkMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, kind provider.Kind) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, kind, true, identity.Execute, true)
}

func ResolveNetworkObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, kind provider.Kind) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, kind, true, identity.View, true)
}

func MarkResourceDeleted(ctx context.Context, tx *sql.Tx, scopeID int64, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_inventory_objects SET
		deletion_confirmed_at=statement_timestamp(),removed_at=COALESCE(removed_at,statement_timestamp())
		WHERE scope_id=$1 AND external_id=$2 AND deletion_confirmed_at IS NULL`, scopeID, id)
	return err
}

func resolveMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, kind provider.Kind, present bool, permission identity.Permission, lock bool) (MutationTarget, error) {
	var target MutationTarget
	query := `SELECT s.scope_id FROM cmp_inventory_scopes AS s
		JOIN cmp_inventory_objects AS o USING (scope_id)
		JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,$4,$5)
		  AND (NOT $6::boolean OR (o.removed_at IS NULL AND o.missing_generation IS NULL))`
	if lock {
		query += " FOR SHARE OF a FOR UPDATE OF o"
	}
	err := tx.QueryRowContext(ctx, query, actor.Tenant, account, region, kind, id, present).Scan(&target.ScopeID)
	if errors.Is(err, sql.ErrNoRows) {
		return MutationTarget{}, identity.ErrForbidden
	}
	if err != nil {
		return MutationTarget{}, err
	}
	var owner, source string
	err = tx.QueryRowContext(ctx, `SELECT `+projectedOwner+`,`+projectedRevision+", "+ownershipSource+`
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		LEFT JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=(s.scope_id,$2)
		LEFT JOIN cmp_asset_tag_assignments AS rule ON (rule.scope_id,rule.external_id)=(s.scope_id,$2)
		WHERE s.scope_id=$1`, target.ScopeID, id).Scan(&owner, &target.OwnershipRevision, &source)
	if err != nil {
		return MutationTarget{}, err
	}
	if owner != actor.Project || source == "pending" {
		return MutationTarget{}, identity.ErrForbidden
	}
	target.AccountAccess, err = identity.NewStore(tx).AuthorizeAccount(ctx, actor, account, permission)
	return target, err
}
