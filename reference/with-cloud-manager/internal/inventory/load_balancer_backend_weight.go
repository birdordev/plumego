package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func loadBalancerBackendIdentity(ctx context.Context, tx *sql.Tx, scopeID int64, parent string) (product string, secret []byte, err error) {
	err = tx.QueryRowContext(ctx, `SELECT o.load_balancer->>'product',k.secret FROM cmp_inventory_objects AS o
  JOIN cmp_load_balancer_backend_keys AS k USING(scope_id,external_id)
  WHERE (o.scope_id,o.external_id)=($1,$2) AND o.load_balancer->>'product' IS NOT NULL`, scopeID, parent).Scan(&product, &secret)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	return
}

func LoadBalancerBackendMutationTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, change *provider.LoadBalancerBackendTarget, currentWeight int, beforeWrite bool) error {
	var err error
	change.Product, change.Secret, err = loadBalancerBackendIdentity(ctx, tx, target.ScopeID, change.ParentID)
	if err != nil || !beforeWrite {
		return err
	}

	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM cmp_load_balancer_configurations AS c
  CROSS JOIN LATERAL jsonb_array_elements(c.configuration#>'{backends,members}') AS m(item)
  WHERE (c.scope_id,c.external_id,c.credential_revision)=($1,$2,$3)
    AND c.configuration->'target_groups' @> jsonb_build_array(jsonb_build_object('id',$4::text,'exclusive',true))
    AND m.item @> jsonb_build_object('group_id',$4::text,'key',$5::text,'weight',$6::int,'weight_editable',true))`,
		target.ScopeID, change.ParentID, target.CredentialRevision, change.GroupID, change.MemberKey, currentWeight).Scan(&eligible)
	if err == nil && !eligible {
		return identity.ErrAccountStale
	}
	return err
}

func RecordLoadBalancerBackendWeight(ctx context.Context, tx *sql.Tx, scopeID, credentialRevision int64, change provider.LoadBalancerBackendWeight) error {
	if err := fenceLoadBalancerBackendRefresh(ctx, tx, scopeID, change.ParentID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE cmp_load_balancer_configurations AS c
  SET configuration=jsonb_set(c.configuration,ARRAY['backends','members',(selected.position-1)::text,'weight'],to_jsonb($6::int))
  FROM (
   SELECT m.position FROM cmp_load_balancer_configurations AS saved
   CROSS JOIN LATERAL jsonb_array_elements(saved.configuration#>'{backends,members}') WITH ORDINALITY AS m(item,position)
   WHERE (saved.scope_id,saved.external_id,saved.credential_revision)=($1,$2,$7)
     AND saved.configuration->'target_groups' @> jsonb_build_array(jsonb_build_object('id',$3::text,'exclusive',true))
     AND m.item @> jsonb_build_object('group_id',$3::text,'key',$4::text,'weight_editable',true)
     AND m.item->'weight' IN (to_jsonb($5::int),to_jsonb($6::int))) AS selected
  WHERE (c.scope_id,c.external_id,c.credential_revision)=($1,$2,$7)`,
		scopeID, change.ParentID, change.GroupID, change.MemberKey, change.Current, change.Desired, credentialRevision)
	return err
}

func fenceLoadBalancerBackendRefresh(ctx context.Context, tx *sql.Tx, scopeID int64, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_load_balancer_backend_keys SET change_observed_at=clock_timestamp()
   WHERE (scope_id,external_id)=($1,$2)`, scopeID, id)
	return err
}

func RecordLoadBalancerBackendDetach(ctx context.Context, tx *sql.Tx, scopeID, credentialRevision int64, change provider.LoadBalancerBackendTarget) error {
	if err := fenceLoadBalancerBackendRefresh(ctx, tx, scopeID, change.ParentID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE cmp_load_balancer_configurations AS c
  SET configuration=jsonb_set(jsonb_set(c.configuration,'{backends,members}',COALESCE((
   SELECT jsonb_agg(item ORDER BY position) FROM jsonb_array_elements(c.configuration#>'{backends,members}') WITH ORDINALITY AS m(item,position)
   WHERE NOT item @> jsonb_build_object('group_id',$4::text,'key',$5::text)
  ),'[]'::jsonb)),'{backends,checks}',COALESCE((
   SELECT jsonb_agg(item ORDER BY position) FROM jsonb_array_elements(c.configuration#>'{backends,checks}') WITH ORDINALITY AS m(item,position)
   WHERE NOT item @> jsonb_build_object('group_id',$4::text,'key',$5::text)
  ),'[]'::jsonb))
  WHERE (c.scope_id,c.external_id,c.credential_revision)=($1,$2,$3)
   AND c.configuration->'target_groups' @> jsonb_build_array(jsonb_build_object('id',$4::text,'exclusive',true))
   AND jsonb_typeof(c.configuration->'backends')='object'`, scopeID, change.ParentID, credentialRevision, change.GroupID, change.MemberKey)
	return err
}
