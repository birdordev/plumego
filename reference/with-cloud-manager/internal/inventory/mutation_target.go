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
	OwnershipSource   string
	identity.AccountAccess
}

func ResolveInstanceMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, provider.Instance, true, identity.Execute, true)
}

func ResolveInstanceObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, provider.Instance, false, identity.Execute, true)
}

// ResolveInstancePowerObservation 锁定仍归属当前项目且库存状态明确的实例，供复核人只读对账。
func ResolveInstancePowerObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, provider.Instance, true, identity.View, true)
}

func ResolveVolumeMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, provider.Volume, true, identity.Execute, true)
}

func ResolveMutationPreview(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, kind provider.Kind) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, kind, true, identity.Execute, false)
}

func ResolveBareMetalMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, site, id string) (MutationTarget, error) {
	return resolveBareMetalMutation(ctx, tx, actor, account, site, id, true)
}

func PreviewBareMetalMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, site, id string) (MutationTarget, error) {
	return resolveBareMetalMutation(ctx, tx, actor, account, site, id, false)
}

func resolveBareMetalMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, site, id string, lock bool) (MutationTarget, error) {
	target, err := resolveMutation(ctx, tx, actor, account, site, id, provider.BareMetal, true, identity.ExecuteBareMetal, lock)
	if err != nil {
		return target, err
	}
	if lock {
		if err := lockBareMetalCustody(ctx, tx, id); err != nil {
			return target, err
		}
	}
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT s.credential_revision=$2 AND s.completed_at IS NOT NULL AND o.seen_generation=s.generation
		FROM cmp_inventory_scopes AS s JOIN cmp_inventory_objects AS o USING (scope_id)
		WHERE s.scope_id=$1 AND o.external_id=$3`, target.ScopeID, target.CredentialRevision, id).Scan(&current)
	if err == nil && !current {
		return target, identity.ErrAccountStale
	}
	return target, err
}

func ResolveBareMetalObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, site, id string) (MutationTarget, error) {
	target, err := resolveMutation(ctx, tx, actor, account, site, id, provider.BareMetal, true, identity.View, true)
	if err != nil {
		return target, err
	}
	return target, lockBareMetalCustody(ctx, tx, id)
}

func ReadBareMetalTarget(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, site, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, site, id, provider.BareMetal, true, identity.View, false)
}

func LockBareMetalScope(ctx context.Context, tx *sql.Tx, tenant, account, site string) (int64, error) {
	return lockInventoryScope(ctx, tx, tenant, account, site, provider.BareMetal)
}

func ResolveNetworkMutation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, kind provider.Kind) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, kind, true, identity.Execute, true)
}

func ResolveNetworkObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, kind provider.Kind) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, kind, true, identity.View, true)
}

func ResolveNetworkDeletion(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, kind provider.Kind, beforeWrite bool) (MutationTarget, error) {
	permission := identity.View
	if beforeWrite {
		permission = identity.Execute
	}
	return resolveMutation(ctx, tx, actor, account, region, id, kind, beforeWrite, permission, true)
}

func ResolveImageDeletion(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, beforeWrite bool) (MutationTarget, error) {
	permission := identity.View
	if beforeWrite {
		permission = identity.Execute
	}
	return resolveMutation(ctx, tx, actor, account, region, id, provider.Image, beforeWrite, permission, true)
}

// 调用方持有镜像行锁后再检查活动删除操作。
func imageDeletionActive(ctx context.Context, tx *sql.Tx, scopeID int64, id string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM cmp_instance_operations
		WHERE (scope_id,external_id)=($1,$2) AND action='delete-image'
		  AND status IN ('awaiting_approval','queued','applying','observing','needs_review'))`, scopeID, id).Scan(&active)
	return active, err
}

func ResolveElasticIPObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (MutationTarget, error) {
	return resolveMutation(ctx, tx, actor, account, region, id, provider.ElasticIP, false, identity.View, true)
}

func MarkResourceDeleted(ctx context.Context, tx *sql.Tx, scopeID int64, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_inventory_objects SET
		deletion_confirmed_at=statement_timestamp(),removed_at=COALESCE(removed_at,statement_timestamp())
		WHERE scope_id=$1 AND external_id=$2 AND deletion_confirmed_at IS NULL`, scopeID, id)
	return err
}

func LockInstanceScope(ctx context.Context, tx *sql.Tx, tenant, account, region string) (int64, error) {
	return lockInventoryScope(ctx, tx, tenant, account, region, provider.Instance)
}

func LockLoadBalancerScope(ctx context.Context, tx *sql.Tx, tenant, account, region string) (int64, error) {
	return lockInventoryScope(ctx, tx, tenant, account, region, provider.LoadBalancer)
}

func lockInventoryScope(ctx context.Context, tx *sql.Tx, tenant, account, region string, kind provider.Kind) (int64, error) {
	var scopeID int64
	err := tx.QueryRowContext(ctx, `SELECT scope_id FROM cmp_inventory_scopes
		WHERE (tenant_id,account_id,region,kind)=($1,$2,$3,$4) FOR NO KEY UPDATE`,
		tenant, account, region, kind).Scan(&scopeID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, identity.ErrForbidden
	}
	return scopeID, err
}

func RecordObservedAssetStatus(ctx context.Context, tx *sql.Tx, scopeID int64, instance, status string) error {
	var id string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_inventory_objects SET status=$3,observed_at=statement_timestamp()
		WHERE (scope_id,external_id)=($1,$2) AND removed_at IS NULL AND missing_generation IS NULL
		RETURNING external_id`, scopeID, instance, status).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrAccountStale
	}
	if err != nil {
		return err
	}
	return restartActiveInventoryScan(ctx, tx, scopeID)
}

func RecordObservedAssetName(ctx context.Context, tx *sql.Tx, scopeID int64, id, current, desired string) error {
	var recorded string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_inventory_objects SET name=$4
		WHERE (scope_id,external_id)=($1,$2) AND name IN ($3,$4) AND removed_at IS NULL AND missing_generation IS NULL
		RETURNING external_id`, scopeID, id, current, desired).Scan(&recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrAccountStale
	}
	if err != nil {
		return err
	}
	return restartActiveInventoryScan(ctx, tx, scopeID)
}

func restartActiveInventoryScan(ctx context.Context, tx *sql.Tx, scopeID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_inventory_scopes SET running=false,fence=fence+1,cursor='',pages=0
		WHERE scope_id=$1 AND running`, scopeID)
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
	var owner string
	err = tx.QueryRowContext(ctx, `SELECT `+projectedOwner+`,`+projectedRevision+", "+ownershipSource+`
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		LEFT JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=(s.scope_id,$2)
		LEFT JOIN cmp_asset_tag_assignments AS rule ON (rule.scope_id,rule.external_id)=(s.scope_id,$2)
		WHERE s.scope_id=$1`, target.ScopeID, id).Scan(&owner, &target.OwnershipRevision, &target.OwnershipSource)
	if err != nil {
		return MutationTarget{}, err
	}
	if owner != actor.Project || target.OwnershipSource == "pending" {
		return MutationTarget{}, identity.ErrForbidden
	}
	target.AccountAccess, err = identity.NewStore(tx).AuthorizeAccount(ctx, actor, account, permission)
	return target, err
}

// 调用方在变更实例前持有实例行锁。预览只读取当前预约。
func InstanceImageCreationActive(ctx context.Context, tx *sql.Tx, scopeID int64, id, exclude string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_instance_image_reservations
		WHERE (scope_id,instance_id)=($1,$2) AND operation_id<>$3)`, scopeID, id, exclude).Scan(&active)
	return active, err
}
