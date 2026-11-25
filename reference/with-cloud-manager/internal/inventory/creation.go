package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func CreationDependencies(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region string, spec provider.InstanceSpec, networkKind provider.Kind) ([3]int64, error) {
	var revisions [3]int64
	var network string
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
			AND (s.kind='security-group' OR lower(o.status) IN ('available','active')) FOR SHARE OF o`,
			actor.Tenant, account, region, dependency.kind, dependency.id).Scan(&scopeID, &parent)
		if errors.Is(err, sql.ErrNoRows) {
			return revisions, identity.ErrForbidden
		}
		if err != nil {
			return revisions, err
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
		if networkKind == provider.Subnet && dependency.kind != provider.Image {
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
	links, err := json.Marshal(object.Links)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_inventory_objects AS existing
		(scope_id,external_id,name,status,seen_generation,links)
		VALUES ($1,$2,$3,$4,$5,COALESCE(NULLIF($6::jsonb,'null'::jsonb),'[]'::jsonb))
		ON CONFLICT (scope_id,external_id) DO UPDATE SET name=EXCLUDED.name,status=EXCLUDED.status,
			seen_generation=EXCLUDED.seen_generation,links=EXCLUDED.links,observed_at=EXCLUDED.observed_at,
			missing_generation=NULL,removed_at=NULL
		WHERE existing.deletion_confirmed_at IS NULL AND (existing.removed_at IS NOT NULL OR existing.missing_generation IS NOT NULL)`,
		scope, object.ID, object.Name, object.Status, generation, string(links)); err != nil {
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
