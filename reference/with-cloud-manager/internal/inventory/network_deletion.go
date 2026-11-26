package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

// 调用方持有资产行锁后检查忙碌状态。
func NetworkDeletionActive(ctx context.Context, tx *sql.Tx, scopeID int64, id string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM cmp_network_deletion_reservations WHERE (scope_id,external_id)=($1,$2))`, scopeID, id).Scan(&active)
	return active, err
}

func NetworkParentID(ctx context.Context, tx *sql.Tx, tenant, account, region, id string, kind provider.Kind) (string, error) {
	var parent string
	var matches int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(parent.id,''),parent.matches
		FROM cmp_inventory_scopes AS s JOIN cmp_inventory_objects AS o USING (scope_id)
		CROSS JOIN LATERAL (
			SELECT min(link->>'id') AS id,count(DISTINCT link->>'id') AS matches
			FROM jsonb_array_elements(o.links) AS link
			WHERE link->>'kind'='network' AND link->>'relation'='in_network'
		) AS parent
		WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,$4,$5)`,
		tenant, account, region, kind, id).Scan(&parent, &matches)
	if errors.Is(err, sql.ErrNoRows) {
		return "", identity.ErrForbidden
	}
	if err == nil && (matches > 1 || (kind == provider.Subnet && parent == "")) {
		return "", identity.ErrAccountStale
	}
	return parent, err
}

// 同步页会先取得同一父组的共享行锁。
func MarkSecurityGroupRulesDeleted(ctx context.Context, tx *sql.Tx, groupScope int64, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_inventory_objects AS rule SET
		deletion_confirmed_at=statement_timestamp(),removed_at=COALESCE(rule.removed_at,statement_timestamp())
		FROM cmp_inventory_scopes AS parent
		JOIN cmp_inventory_scopes AS rules USING (tenant_id,account_id,region)
		WHERE parent.scope_id=$1 AND rules.kind='security-group-rule' AND rule.scope_id=rules.scope_id
		  AND rule.deletion_confirmed_at IS NULL
		  AND rule.links @> jsonb_build_array(jsonb_build_object('kind','security-group','id',$2::text,'relation','in_security_group'))`,
		groupScope, id)
	return err
}
