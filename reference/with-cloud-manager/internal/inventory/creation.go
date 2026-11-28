package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func CreationDependencies(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region string, spec provider.InstanceSpec, providerName string) ([3]int64, error) {
	var revisions [3]int64
	var network string
	networkKind := provider.Subnet
	if providerName == "openstack" {
		networkKind = provider.Network
	}
	regionalGroup := providerName == provider.TencentCVM || providerName == provider.HuaweiECS
	for i, dependency := range []struct {
		kind provider.Kind
		id   string
	}{
		{provider.Image, spec.ImageID},
		{provider.SecurityGroup, spec.SecurityGroup},
		{networkKind, spec.NetworkID},
	} {
		var scopeID int64
		var parent string
		err := tx.QueryRowContext(ctx, `SELECT s.scope_id,COALESCE((
			SELECT CASE WHEN count(DISTINCT link->>'id')=1 THEN min(link->>'id') END
			FROM jsonb_array_elements(o.links) AS link
			WHERE link->>'kind'='network' AND link->>'relation'='in_network'),'') FROM cmp_inventory_scopes AS s
			JOIN cmp_inventory_objects AS o USING (scope_id)
			WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,$4,$5)
			AND o.removed_at IS NULL AND o.missing_generation IS NULL
			AND (s.kind='security-group' OR lower(o.status) IN ('available','active') OR
			     ($6 AND ((s.kind='subnet' AND o.status='observed') OR (s.kind='image' AND o.status='NORMAL'))))
			FOR SHARE OF o`,
			actor.Tenant, account, region, dependency.kind, dependency.id, providerName == provider.TencentCVM).Scan(&scopeID, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			return revisions, identity.ErrForbidden
		}
		if err != nil {
			return revisions, err
		}
		if dependency.kind == provider.Image {
			deleting, err := imageDeletionActive(ctx, tx, scopeID, dependency.id)
			if err != nil {
				return revisions, err
			}
			if deleting {
				return revisions, identity.ErrForbidden
			}
		}
		if dependency.kind == provider.Subnet || dependency.kind == provider.Network || dependency.kind == provider.SecurityGroup {
			deleting, err := NetworkDeletionActive(ctx, tx, scopeID, dependency.id)
			if err != nil {
				return revisions, err
			}
			if deleting {
				return revisions, identity.ErrForbidden
			}
		}
		var project, source string
		err = tx.QueryRowContext(ctx, `SELECT `+projectedOwner+`,`+projectedRevision+`,`+ownershipSource+`
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			LEFT JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=(s.scope_id,$2)
			LEFT JOIN cmp_asset_tag_assignments AS rule ON (rule.scope_id,rule.external_id)=(s.scope_id,$2)
			WHERE s.scope_id=$1`, scopeID, dependency.id).Scan(&project, &revisions[i], &source)
		if err != nil {
			return revisions, err
		}
		if project != actor.Project || source == "pending" {
			return revisions, identity.ErrForbidden
		}
		if networkKind == provider.Subnet && dependency.kind != provider.Image &&
			(!regionalGroup || dependency.kind != provider.SecurityGroup) {
			if parent == "" || (network != "" && parent != network) {
				return revisions, identity.ErrForbidden
			}
			network = parent
		}
	}
	return revisions, nil
}

func RegisterCreatedAsset(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region string, kind provider.Kind, revision int64, object provider.Object) (int64, error) {
	var scope, generation int64
	err := tx.QueryRowContext(ctx, `INSERT INTO cmp_inventory_scopes AS s
		(tenant_id,account_id,region,kind,credential_revision,running,lease_until)
		VALUES ($1,$2,$3,$4,$5,false,clock_timestamp())
		ON CONFLICT (tenant_id,account_id,region,kind) DO UPDATE SET region=s.region
		RETURNING scope_id,generation`, actor.Tenant, account, region, kind, revision).Scan(&scope, &generation)
	if err != nil {
		return 0, err
	}
	if kind == provider.Snapshot {
		return scope, registerCreatedSnapshot(ctx, tx, scope, generation, object)
	}
	links, err := json.Marshal(object.Links)
	if err != nil {
		return 0, err
	}
	addresses, err := json.Marshal(object.PublicAddresses)
	if err != nil {
		return 0, err
	}
	if kind == provider.Volume {
		if err := registerProvisionedVolume(ctx, tx, scope, generation, object); err != nil {
			return 0, err
		}
	} else if kind == provider.Image {
		if err := registerImageOrigin(ctx, tx, scope, generation, object, string(links)); err != nil {
			return 0, err
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_inventory_objects AS existing
		(scope_id,external_id,name,status,seen_generation,links,public_addresses,instance_type)
		VALUES ($1,$2,$3,$4,$5,COALESCE(NULLIF($6::jsonb,'null'::jsonb),'[]'::jsonb),COALESCE(NULLIF($7::jsonb,'null'::jsonb),'[]'::jsonb),$8)
		ON CONFLICT (scope_id,external_id) DO UPDATE SET name=EXCLUDED.name,status=EXCLUDED.status,
			seen_generation=EXCLUDED.seen_generation,links=EXCLUDED.links,observed_at=EXCLUDED.observed_at,
			public_addresses=EXCLUDED.public_addresses,instance_type=EXCLUDED.instance_type,
			missing_generation=NULL,removed_at=NULL
		WHERE existing.deletion_confirmed_at IS NULL AND (existing.removed_at IS NOT NULL OR existing.missing_generation IS NOT NULL)`,
		scope, object.ID, object.Name, object.Status, generation, string(links), string(addresses), object.InstanceType); err != nil {
		return 0, err
	}
	var locked, parentID string
	err = tx.QueryRowContext(ctx, `SELECT external_id,COALESCE(links->0->>'id','') FROM cmp_inventory_objects
		WHERE (scope_id,external_id)=($1,$2) AND removed_at IS NULL AND deletion_confirmed_at IS NULL FOR UPDATE`, scope, object.ID).Scan(&locked, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, identity.ErrForbidden
	}
	if err != nil {
		return 0, err
	}
	if kind == provider.ElasticIP {
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_inventory_objects SET public_addresses=$3::jsonb,
			status=CASE WHEN status IN ('CREATING','PENDING_CREATE','NOTIFYING') THEN $4 ELSE status END,
			observed_at=statement_timestamp()
			WHERE (scope_id,external_id)=($1,$2) AND public_addresses='[]'::jsonb`,
			scope, object.ID, string(addresses), object.Status); err != nil {
			return 0, err
		}
	}
	if kind == provider.SecurityGroupRule {
		if expected, ok := provider.SecurityRuleParent(object); !ok || expected != parentID {
			return 0, identity.ErrForbidden
		}
		return scope, nil
	}
	var owner, source string
	var previousRevision int64
	err = tx.QueryRowContext(ctx, `SELECT `+projectedOwner+`,`+projectedRevision+`,`+ownershipSource+`
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_objects AS o USING (scope_id)
		LEFT JOIN cmp_asset_ownership AS own USING (scope_id,external_id)
		LEFT JOIN cmp_asset_tag_assignments AS rule USING (scope_id,external_id)
		WHERE (s.scope_id,o.external_id)=($1,$2)`, scope, object.ID).Scan(&owner, &previousRevision, &source)
	if err != nil {
		return 0, err
	}
	if source == "manual" && owner != actor.Project {
		return 0, identity.ErrForbidden
	}
	if source != "manual" {
		next := Ownership{Project: actor.Project, Revision: previousRevision + 1}
		if err := setOwnership(ctx, tx, actor, scope, object.ID, next, owner); err != nil {
			return 0, err
		}
	}
	return scope, nil
}

func registerProvisionedVolume(ctx context.Context, tx *sql.Tx, scope, generation int64, object provider.Object) error {
	if len(object.Links) == 0 || len(object.Links) > 2 || object.Links[0].Kind != provider.Zone || object.Links[0].Relation != "in_zone" {
		return identity.ErrForbidden
	}
	var snapshot string
	if len(object.Links) == 2 {
		if object.Links[1].Kind != provider.Snapshot || object.Links[1].Relation != "restored_from" {
			return identity.ErrForbidden
		}
		snapshot = object.Links[1].ID
	} else if object.Volume == nil || !object.Volume.Valid() {
		return identity.ErrForbidden
	}
	var conflict bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM cmp_inventory_objects AS o, LATERAL jsonb_array_elements(o.links) AS link
		WHERE (o.scope_id,o.external_id)=($1,$2) AND (link->>'relation'='attached_to'
		   OR (link->>'relation'='restored_from' AND (link->>'kind',link->>'id') IS DISTINCT FROM ('snapshot',$3::text))))`,
		scope, object.ID, snapshot).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return identity.ErrForbidden
	}
	if err := upsert(ctx, tx, scope, generation, provider.Volume, []provider.Object{object}); err != nil {
		return err
	}
	return restartActiveInventoryScan(ctx, tx, scope)
}

func registerCreatedSnapshot(ctx context.Context, tx *sql.Tx, scope, generation int64, object provider.Object) error {
	if !object.ValidSnapshot() || len(object.Links) == 0 || object.Links[0].Kind != provider.Volume {
		return identity.ErrForbidden
	}
	if err := upsert(ctx, tx, scope, generation, provider.Snapshot, []provider.Object{object}); err != nil {
		if provider.IsFailure(err, provider.InvalidResponse) {
			return identity.ErrForbidden
		}
		return err
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT external_id FROM cmp_inventory_objects
		WHERE (scope_id,external_id)=($1,$2) AND deletion_confirmed_at IS NULL`, scope, object.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrForbidden
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cmp_inventory_scopes SET running=false,fence=fence+1,cursor='',pages=0
		WHERE scope_id=$1 AND running`, scope)
	return err
}

func registerImageOrigin(ctx context.Context, tx *sql.Tx, scope, generation int64, object provider.Object, links string) error {
	switch len(object.Links) {
	case 0:
		links = "[]"
	case 1:
		if object.Links[0].Kind != provider.Instance || object.Links[0].Relation != "created_from" {
			return identity.ErrForbidden
		}
	default:
		return identity.ErrForbidden
	}
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO cmp_inventory_objects AS o (scope_id,external_id,name,status,seen_generation,links)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb)
		ON CONFLICT (scope_id,external_id) DO UPDATE SET name=EXCLUDED.name,status=EXCLUDED.status,
			seen_generation=EXCLUDED.seen_generation,links=EXCLUDED.links,observed_at=EXCLUDED.observed_at,
			removed_at=NULL,missing_generation=NULL
		WHERE o.deletion_confirmed_at IS NULL AND NOT EXISTS (
			SELECT 1 FROM jsonb_array_elements(o.links) AS edge
			WHERE edge->>'relation'='created_from' AND edge IS DISTINCT FROM (EXCLUDED.links->0)
		) RETURNING external_id`, scope, object.ID, object.Name, object.Status, generation, links).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrForbidden
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cmp_inventory_scopes SET running=false,fence=fence+1,cursor='',pages=0
		WHERE scope_id=$1 AND running`, scope)
	return err
}
