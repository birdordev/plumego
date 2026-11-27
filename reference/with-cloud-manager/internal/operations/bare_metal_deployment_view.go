package operations

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

type BareMetalDeploymentView struct {
	Deployment     *BareMetalDeployment `json:"deployment,omitempty"`
	StageOperation string               `json:"stage_operation,omitempty"`
}

func bareMetalDeploymentForBoot(ctx context.Context, tx *sql.Tx, actor identity.Scope, op Operation) (*BareMetalDeploymentView, error) {
	d, err := deploymentByBoot(ctx, tx, actor, op.ID)
	if err == nil {
		_, err := inventory.ReadBareMetalTarget(ctx, tx, actor, d.Account, d.Site, d.SystemID)
		if errors.Is(err, identity.ErrForbidden) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return &BareMetalDeploymentView{Deployment: &d.BareMetalDeployment}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if op.Status != "awaiting_approval" {
		return nil, nil
	}
	if _, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Manage); err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			return nil, nil
		}
		return nil, err
	}
	target, err := inventory.PreviewBareMetalMutation(ctx, tx, actor, op.Account, op.Region, op.AssetID)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return deploymentStageForBoot(ctx, tx, actor, op.ID, target)
}

func deploymentStageForBoot(ctx context.Context, tx *sql.Tx, actor identity.Scope, id string, target inventory.MutationTarget) (*BareMetalDeploymentView, error) {
	view := BareMetalDeploymentView{}
	err := tx.QueryRowContext(ctx, `SELECT p.operation_id`+bareMetalDeploymentPair+`
		AND (b.credential_revision,b.ownership_revision)=($4,$5) AND (`+latestBareMetalDeploymentStage+`)
		AND NOT EXISTS (SELECT 1 FROM cmp_bare_metal_deployments WHERE stage_operation=p.operation_id)
		ORDER BY p.updated_at DESC,p.operation_id DESC LIMIT 1`,
		actor.Tenant, actor.Project, id, target.CredentialRevision, target.OwnershipRevision).Scan(&view.StageOperation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// 审批和登记都持有同一开机操作行锁，确保审批绑定到实际部署。
func checkBareMetalApproval(ctx context.Context, tx *sql.Tx, actor identity.Scope, op Operation, id string) error {
	if op.Action != powerOnBareMetal {
		if id != "" {
			return ErrInvalid
		}
		return nil
	}
	d, err := deploymentByBoot(ctx, tx, actor, op.ID)
	if errors.Is(err, sql.ErrNoRows) {
		if id != "" {
			return ErrConflict
		}
		return nil
	}
	if err != nil {
		return err
	}
	if d.ID != id || (op.Status == "awaiting_approval" && d.State != "pending") {
		return ErrConflict
	}
	return nil
}
