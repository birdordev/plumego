package inventory

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func LoadBalancerBackendAttachTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, change *provider.LoadBalancerBackendAttach, beforeWrite bool) error {
	var err error
	change.Product, change.Secret, err = loadBalancerBackendIdentity(ctx, tx, target.ScopeID, change.ParentID)
	if err != nil || !beforeWrite {
		return err
	}
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM cmp_load_balancer_configurations AS c WHERE (c.scope_id,c.external_id,c.credential_revision)=($1,$2,$3)
    AND c.configuration->'target_groups' @> jsonb_build_array(jsonb_build_object('id',$4::text,'exclusive',true))
    AND jsonb_typeof(c.configuration->'backends')='object')`,
		target.ScopeID, change.ParentID, target.CredentialRevision, change.GroupID).Scan(&eligible)
	if err == nil && !eligible {
		return identity.ErrAccountStale
	}
	return err
}

func loadBalancerChangeActive(ctx context.Context, tx *sql.Tx, scopeID int64, id string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_load_balancer_change_reservations
		WHERE (scope_id,external_id)=($1,$2))`, scopeID, id).Scan(&active)
	return active, err
}

func InstanceBackendAttachmentActive(ctx context.Context, tx *sql.Tx, scopeID int64, id string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_load_balancer_change_reservations
		WHERE (source_scope_id,source_instance_id)=($1,$2))`, scopeID, id).Scan(&active)
	return active, err
}

func RecordLoadBalancerBackendAttach(ctx context.Context, tx *sql.Tx, scopeID, credentialRevision int64, change provider.LoadBalancerBackendAttach, key string) error {
	if err := fenceLoadBalancerBackendRefresh(ctx, tx, scopeID, change.ParentID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE cmp_load_balancer_configurations AS c
  SET configuration=jsonb_set(c.configuration,'{backends,members}',(
   SELECT jsonb_agg(item ORDER BY item->>'group_id',item->>'key',item->>'port')
   FROM (SELECT item FROM jsonb_array_elements(c.configuration#>'{backends,members}') AS m(item)
     UNION ALL SELECT jsonb_build_object('group_id',$4::text,'key',$5::text,'port',$6::int,'weight',0,'weight_editable',true)) AS projection
  ))
  WHERE (c.scope_id,c.external_id,c.credential_revision)=($1,$2,$3)
    AND c.configuration->'target_groups' @> jsonb_build_array(jsonb_build_object('id',$4::text,'exclusive',true))
    AND jsonb_typeof(c.configuration->'backends')='object'
    AND jsonb_array_length(c.configuration#>'{backends,members}')+jsonb_array_length(c.configuration#>'{backends,checks}')<$7
    AND NOT c.configuration#>'{backends,members}' @> jsonb_build_array(jsonb_build_object('group_id',$4::text,'key',$5::text))`,
		scopeID, change.ParentID, credentialRevision, change.GroupID, key, change.Port, provider.MaxLoadBalancerBackendRecords)
	return err
}
