package operations

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

type BareMetalDeploymentReceipt struct {
	SystemID       string `json:"system_id"`
	ImageSHA256    string `json:"image_sha256"`
	Result         string `json:"result"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

func (s Store) RecordBareMetalDeployment(ctx context.Context, token string, scope identity.Scope, id string, receipt BareMetalDeploymentReceipt) (BareMetalDeployment, error) {
	if !operationKey.MatchString(id) || !bareMetalID.MatchString(receipt.SystemID) ||
		!deploymentDigest.MatchString(receipt.ImageSHA256) || !deploymentDigest.MatchString(receipt.EvidenceSHA256) ||
		(receipt.Result != "installed" && receipt.Result != "failed") {
		return BareMetalDeployment{}, ErrInvalid
	}
	var d deploymentRecord
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := identity.NewStore(tx).AuthenticateService(ctx, token)
		if err != nil {
			return err
		}
		if actor.Tenant != scope.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = scope.Project
		d, err = readDeployment(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		if d.Installer != actor.Principal {
			return identity.ErrForbidden
		}
		target, err := inventory.ResolveBareMetalObservation(ctx, tx, actor, d.Account, d.Site, d.SystemID)
		if err != nil {
			return err
		}
		membership, generation, err := lockTicketVerifier(ctx, tx, actor, d.Installer)
		if err != nil {
			return err
		}
		if membership != d.membership || generation != d.generation || d.credential != target.CredentialRevision || d.ownership != target.OwnershipRevision {
			return identity.ErrAccountStale
		}
		if receipt.SystemID != d.SystemID || receipt.ImageSHA256 != d.ImageSHA256 {
			return ErrConflict
		}
		if d.State != "pending" {
			if d.State == receipt.Result && d.EvidenceSHA256 == receipt.EvidenceSHA256 {
				return nil
			}
			return ErrConflict
		}
		var recorded deploymentRecord
		recorded, err = scanDeployment(tx.QueryRowContext(ctx, `UPDATE cmp_bare_metal_deployments
			SET result=$2,evidence_sha256=$3,recorded_at=statement_timestamp()
			WHERE deployment_id=$1 AND expires_at>statement_timestamp() AND result IS NULL
			AND EXISTS (SELECT 1 FROM cmp_instance_operations WHERE operation_id=cmp_bare_metal_deployments.boot_operation AND status='succeeded')
			RETURNING `+deploymentColumns, id, receipt.Result, receipt.EvidenceSHA256))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		d = recorded
		return nil
	})
	return d.BareMetalDeployment, err
}

func checkBareMetalDeploymentFence(ctx context.Context, tx *sql.Tx, system, op string) error {
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_bare_metal_deployments
		WHERE system_id=$1 AND (
			(boot_operation<>$2 AND result IS NULL AND expires_at>statement_timestamp()
				AND EXISTS(SELECT 1 FROM cmp_instance_operations AS boot
				WHERE boot.operation_id=cmp_bare_metal_deployments.boot_operation AND boot.status<>'failed')) OR
			(boot_operation=$2 AND (result IS NOT NULL OR expires_at<=statement_timestamp()))))
		OR EXISTS (SELECT 1 FROM cmp_bare_metal_reclaims
			WHERE system_id=$1 AND state IN ('requested','approved','claimed','verified'))`, system, op).Scan(&blocked)
	if err == nil && blocked {
		return ErrConflict
	}
	return err
}

func checkDeploymentInstaller(ctx context.Context, tx *sql.Tx, w *work) error {
	var id string
	var membership, generation int64
	err := tx.QueryRowContext(ctx, `SELECT installer_id,installer_membership,installer_generation
		FROM cmp_bare_metal_deployments WHERE boot_operation=$1`, w.ID).Scan(&id, &membership, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	currentMembership, currentGeneration, err := lockTicketVerifier(ctx, tx, w.actor, id)
	if err != nil {
		return err
	}
	if membership != currentMembership || generation != currentGeneration {
		return identity.ErrAccountStale
	}
	w.bareMetalDeployment = true
	return nil
}
