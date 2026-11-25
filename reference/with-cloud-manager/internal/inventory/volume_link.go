package inventory

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

func LockVolumeScopes(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region string) error {
	_, err := tx.ExecContext(ctx, `SELECT scope_id FROM cmp_inventory_scopes
		WHERE (tenant_id,account_id,region)=($1,$2,$3) AND kind IN ('instance','volume')
		ORDER BY scope_id FOR NO KEY UPDATE`, actor.Tenant, account, region)
	return err
}

func RecordVolumeLink(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, instance, volume string, attached bool) error {
	if _, err := tx.ExecContext(ctx, `WITH expected(kind, id, other_kind, other_id, relation) AS (
		VALUES ('instance',$4::text,'volume',$5::text,'uses_volume'),('volume',$5,'instance',$4,'attached_to')
	) UPDATE cmp_inventory_objects AS o SET links=COALESCE((
		SELECT jsonb_agg(link.value ORDER BY link.ordinality) FROM jsonb_array_elements(o.links) WITH ORDINALITY AS link
		WHERE (link.value->>'kind',link.value->>'id') IS DISTINCT FROM (expected.other_kind,expected.other_id)
	),'[]'::jsonb) || CASE WHEN $6 THEN jsonb_build_array(jsonb_build_object(
		'kind',expected.other_kind,'id',expected.other_id,'relation',expected.relation)) ELSE '[]'::jsonb END,
	status=CASE WHEN expected.kind='volume' THEN CASE WHEN $6 THEN 'in-use' ELSE 'available' END ELSE o.status END
	FROM cmp_inventory_scopes AS s JOIN expected USING (kind)
	WHERE (s.tenant_id,s.account_id,s.region)=($1,$2,$3) AND (o.scope_id,o.external_id)=(s.scope_id,expected.id)`,
		actor.Tenant, account, region, instance, volume, attached); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE cmp_inventory_scopes SET running=false,fence=fence+1,cursor='',pages=0
		WHERE (tenant_id,account_id,region)=($1,$2,$3) AND kind IN ('instance','volume') AND running`, actor.Tenant, account, region)
	return err
}
