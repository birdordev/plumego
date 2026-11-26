package inventory

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"strings"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

//go:embed 0008_asset_ownership.sql
var ownershipSchema string

// 子规则和快照沿用父资源的归属。列表、历史和拓扑使用相同的授权条件。
const inventoryOwnershipJoins = `
	LEFT JOIN cmp_inventory_scopes AS parent_scope
		ON s.kind IN ('security-group-rule','snapshot') AND (parent_scope.tenant_id,parent_scope.account_id,parent_scope.region,parent_scope.kind)=
		(s.tenant_id,s.account_id,s.region,CASE WHEN s.kind='snapshot' THEN 'volume' ELSE 'security-group' END)
	LEFT JOIN cmp_inventory_objects AS parent
		ON (parent.scope_id,parent.external_id)=(parent_scope.scope_id,o.links->0->>'id')
		AND ((s.kind='security-group-rule' AND o.links->0->>'kind'='security-group' AND o.links->0->>'relation'='in_security_group'
			AND parent.removed_at IS NULL AND parent.missing_generation IS NULL)
			OR (s.kind='snapshot' AND o.links->0->>'kind'='volume' AND o.links->0->>'relation'='snapshot_of'))
	LEFT JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=
		(COALESCE(parent.scope_id,o.scope_id),COALESCE(parent.external_id,o.external_id))
	LEFT JOIN cmp_asset_tag_assignments AS rule ON (rule.scope_id,rule.external_id)=
		(COALESCE(parent.scope_id,o.scope_id),COALESCE(parent.external_id,o.external_id))`

const projectCanReadAccount = `a.project_id=$2 OR EXISTS (
	SELECT 1 FROM cmp_cloud_account_grants AS g
	WHERE (g.tenant_id,g.account_id,g.project_id)=(s.tenant_id,s.account_id,$2))`

const directInventoryOwnershipJoins = `
	LEFT JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=(o.scope_id,o.external_id)
	LEFT JOIN cmp_asset_tag_assignments AS rule ON (rule.scope_id,rule.external_id)=(o.scope_id,o.external_id)`

const directAssetVisibility = `(
	(own.project_id IS NOT NULL AND own.project_id=$2)
	OR (own.project_id IS NULL AND (
		(COALESCE(rule.state,'inherited')='inherited' AND (` + projectCanReadAccount + `))
		OR (rule.state IN ('mapped','conflict') AND (` + projectedOwner + `)=$2)
	)))`

const assetVisibility = `s.tenant_id=$1 AND CASE WHEN s.kind IN ('security-group-rule','snapshot') THEN
	parent.scope_id IS NOT NULL AND (` + projectCanReadAccount + `)
	AND (` + projectedOwner + `)=$2 AND (` + ownershipSource + `)<>'pending'
	ELSE ` + directAssetVisibility + ` END`

type OwnershipTransfer struct {
	Account       string        `json:"account"`
	Region        string        `json:"region"`
	Kind          provider.Kind `json:"kind"`
	AssetID       string        `json:"asset_id"`
	TargetProject string        `json:"target_project"`
}

type Ownership struct {
	Project  string `json:"project"`
	Revision int64  `json:"revision"`
}

func MigrateOwnership(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0008_asset_ownership", ownershipSchema)
}

func (s Store) TransferOwnership(ctx context.Context, token string, source identity.Scope, expected int64, input OwnershipTransfer) (Ownership, error) {
	if input.Account == "" || len(input.Account) > 256 || len(input.Region) > 256 ||
		!input.Kind.Valid() || input.Kind == provider.SecurityGroupRule || input.Kind == provider.Snapshot ||
		(input.Region == "") != (input.Kind == provider.Region) ||
		input.AssetID == "" || len(input.AssetID) > provider.MaxObjectIDSize ||
		input.TargetProject == "" || len(input.TargetProject) > 256 || input.TargetProject == source.Project ||
		strings.ContainsRune(input.Account+input.Region+input.AssetID+input.TargetProject, 0) {
		return Ownership{}, ErrInvalidQuery
	}
	var value Ownership
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		auth := identity.NewStore(tx)
		actor, err := auth.AuthenticateRequest(ctx, token)
		if err != nil {
			return err
		}
		if actor.Tenant != source.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = source.Project
		if _, err := auth.AuthorizeProject(ctx, actor, identity.Manage); err != nil {
			return err
		}
		target := actor
		target.Project = input.TargetProject
		if _, err := auth.AuthorizeProject(ctx, target, identity.Manage); err != nil {
			return err
		}
		var scopeID int64
		var before Ownership
		err = tx.QueryRowContext(ctx, `SELECT s.scope_id,a.project_id,a.credential_revision
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			JOIN cmp_inventory_objects AS o USING (scope_id)
			WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,$4,$5)
			  AND o.removed_at IS NULL FOR SHARE OF a FOR UPDATE OF o`,
			actor.Tenant, input.Account, input.Region, input.Kind, input.AssetID).Scan(&scopeID, &before.Project, &before.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT `+projectedOwner+`,`+projectedRevision+`
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			LEFT JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=(s.scope_id,$2)
			LEFT JOIN cmp_asset_tag_assignments AS rule ON (rule.scope_id,rule.external_id)=(s.scope_id,$2)
			WHERE s.scope_id=$1`, scopeID, input.AssetID).Scan(&before.Project, &before.Revision)
		if err != nil {
			return err
		}
		if before.Project != actor.Project {
			return identity.ErrForbidden
		}
		if before.Revision != expected {
			return identity.ErrAccountStale
		}
		if input.Kind == provider.Network || input.Kind == provider.Subnet || input.Kind == provider.SecurityGroup {
			active, err := NetworkDeletionActive(ctx, tx, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		if input.Kind == provider.LoadBalancer {
			active, err := loadBalancerChangeActive(ctx, tx, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		if input.Kind == provider.KubernetesCluster {
			active, err := workloadChangeActive(ctx, tx, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		if input.Kind == provider.DatabaseInstance {
			active, err := databaseRenameActive(ctx, tx, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		if input.Kind == provider.CacheInstance {
			active, err := cacheRenameActive(ctx, tx, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		if input.Kind == provider.DNSZone {
			active, err := dnsZoneChangeActive(ctx, tx, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		if input.Kind == provider.Image {
			active, err := imageDeletionActive(ctx, tx, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_image_copy_reservations
				WHERE (scope_id,image_id)=($1,$2))`, scopeID, input.AssetID).Scan(&active); err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		if input.Kind == provider.Instance || input.Kind == provider.Volume {
			active, err := instanceRetirementActive(ctx, tx, input.Kind, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		if input.Kind == provider.Instance {
			active, err := InstanceImageCreationActive(ctx, tx, scopeID, input.AssetID, "")
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
			active, err = InstanceBackendAttachmentActive(ctx, tx, scopeID, input.AssetID)
			if err != nil {
				return err
			}
			if active {
				return identity.ErrAccountConflict
			}
		}
		value = Ownership{Project: target.Project, Revision: before.Revision + 1}
		return setOwnership(ctx, tx, actor, scopeID, input.AssetID, value, actor.Project)
	})
	if err != nil {
		return Ownership{}, err
	}
	return value, nil
}

func setOwnership(ctx context.Context, tx *sql.Tx, actor identity.Scope, scopeID int64, asset string, value Ownership, previousProject string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_asset_ownership (tenant_id,scope_id,external_id,project_id,revision)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (scope_id,external_id) DO UPDATE SET project_id=EXCLUDED.project_id,revision=EXCLUDED.revision`,
		actor.Tenant, scopeID, asset, value.Project, value.Revision); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_asset_ownership_events
		(scope_id,external_id,tenant_id,actor_id,from_project,to_project,revision)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, scopeID, asset, actor.Tenant, actor.Principal, previousProject, value.Project, value.Revision)
	return err
}
