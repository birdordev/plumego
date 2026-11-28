package operations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"regexp"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

const BareMetalDeploymentMigration = "0120_bare_metal_deployments"

var deploymentDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func MigrateBareMetalDeployments(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BareMetalDeploymentMigration)
}

type BareMetalDeploymentInput struct {
	StageOperation string `json:"stage_operation"`
	BootOperation  string `json:"boot_operation"`
	ImageSHA256    string `json:"image_sha256"`
	Installer      string `json:"installer_id"`
}

type BareMetalDeployment struct {
	BareMetalDeploymentInput
	ID             string     `json:"id"`
	Account        string     `json:"account"`
	Site           string     `json:"site"`
	SystemID       string     `json:"system_id"`
	State          string     `json:"state"`
	EvidenceSHA256 string     `json:"evidence_sha256,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RecordedAt     *time.Time `json:"recorded_at,omitempty"`
}

type deploymentRecord struct {
	BareMetalDeployment
	credential, ownership, membership, generation int64
}

const deploymentColumns = `deployment_id,account_id,site_id,system_id,stage_operation,boot_operation,
	image_sha256,installer_id,COALESCE(result,CASE
		WHEN EXISTS(SELECT 1 FROM cmp_instance_operations AS boot WHERE boot.operation_id=cmp_bare_metal_deployments.boot_operation AND boot.status='failed') THEN 'boot_failed'
		WHEN expires_at>statement_timestamp() THEN 'pending' ELSE 'expired' END),
	COALESCE(evidence_sha256,''),created_at,expires_at,recorded_at,credential_revision,ownership_revision,installer_membership,installer_generation`

func scanDeployment(row *sql.Row) (deploymentRecord, error) {
	var d deploymentRecord
	err := row.Scan(&d.ID, &d.Account, &d.Site, &d.SystemID, &d.StageOperation, &d.BootOperation, &d.ImageSHA256, &d.Installer,
		&d.State, &d.EvidenceSHA256, &d.CreatedAt, &d.ExpiresAt, &d.RecordedAt, &d.credential, &d.ownership, &d.membership, &d.generation)
	return d, err
}

func readDeployment(ctx context.Context, tx *sql.Tx, scope identity.Scope, id string, lock bool) (deploymentRecord, error) {
	query := `SELECT ` + deploymentColumns + ` FROM cmp_bare_metal_deployments WHERE (tenant_id,project_id,deployment_id)=($1,$2,$3)`
	if lock {
		query += " FOR UPDATE"
	}
	d, err := scanDeployment(tx.QueryRowContext(ctx, query, scope.Tenant, scope.Project, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, identity.ErrForbidden
	}
	return d, err
}

func (s Store) GetBareMetalDeployment(ctx context.Context, token string, scope identity.Scope, id string) (BareMetalDeployment, error) {
	if !operationKey.MatchString(id) {
		return BareMetalDeployment{}, ErrInvalid
	}
	var d deploymentRecord
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		d, err = readDeployment(ctx, tx, actor, id, false)
		if err != nil {
			return err
		}
		_, err = inventory.ReadBareMetalTarget(ctx, tx, actor, d.Account, d.Site, d.SystemID)
		return err
	})
	return d.BareMetalDeployment, err
}

func (s Store) RegisterBareMetalDeployment(ctx context.Context, token string, scope identity.Scope, input BareMetalDeploymentInput) (BareMetalDeployment, error) {
	if !operationKey.MatchString(input.StageOperation) || !operationKey.MatchString(input.BootOperation) ||
		!operationKey.MatchString(input.Installer) || !deploymentDigest.MatchString(input.ImageSHA256) || input.StageOperation == input.BootOperation {
		return BareMetalDeployment{}, ErrInvalid
	}
	var d deploymentRecord
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Manage)
		if err != nil {
			return err
		}
		if actor.Principal == input.Installer {
			return identity.ErrForbidden
		}
		d, err = registerDeployment(ctx, tx, actor, input)
		return err
	})
	return d.BareMetalDeployment, err
}

const bareMetalDeploymentPair = `
		FROM cmp_instance_operations AS b JOIN cmp_instance_operations AS p
		  ON (p.tenant_id,p.project_id,p.account_id,p.region,p.scope_id,p.external_id,p.credential_revision,p.ownership_revision)
		   = (b.tenant_id,b.project_id,b.account_id,b.region,b.scope_id,b.external_id,b.credential_revision,b.ownership_revision)
		WHERE (b.tenant_id,b.project_id,b.operation_id)=($1,$2,$3)
		  AND b.resource_kind='bare-metal' AND b.action='power-on-bare-metal'
		  AND p.resource_kind='bare-metal' AND p.action='stage-bare-metal-pxe' AND p.status='succeeded'
		  AND p.updated_at<=b.created_at`

const latestBareMetalDeploymentStage = `p.updated_at>statement_timestamp()-interval '2 hours'
		AND NOT EXISTS (SELECT 1 FROM cmp_instance_operations AS later
			WHERE later.resource_kind='bare-metal' AND later.external_id=b.external_id
			  AND later.operation_id<>b.operation_id AND later.created_at>=p.updated_at
			  AND later.status<>'failed')`

func deploymentByBoot(ctx context.Context, tx *sql.Tx, actor identity.Scope, id string) (deploymentRecord, error) {
	return scanDeployment(tx.QueryRowContext(ctx, `SELECT `+deploymentColumns+
		` FROM cmp_bare_metal_deployments WHERE (tenant_id,project_id,boot_operation)=($1,$2,$3)`, actor.Tenant, actor.Project, id))
}

func registerDeployment(ctx context.Context, tx *sql.Tx, actor identity.Scope, input BareMetalDeploymentInput) (deploymentRecord, error) {
	var d deploymentRecord
	var status string
	var latest bool
	err := tx.QueryRowContext(ctx, `SELECT b.account_id,b.region,b.external_id,b.credential_revision,b.ownership_revision,b.status,`+
		latestBareMetalDeploymentStage+bareMetalDeploymentPair+
		` AND p.operation_id=$4 FOR UPDATE OF b`, actor.Tenant, actor.Project, input.BootOperation, input.StageOperation).
		Scan(&d.Account, &d.Site, &d.SystemID, &d.credential, &d.ownership, &status, &latest)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrConflict
	}
	if err != nil {
		return d, err
	}
	target, err := inventory.ResolveBareMetalMutation(ctx, tx, actor, d.Account, d.Site, d.SystemID)
	if err != nil {
		return d, err
	}
	if target.OwnershipRevision != d.ownership || target.CredentialRevision != d.credential {
		return d, identity.ErrAccountStale
	}
	prior, err := deploymentByBoot(ctx, tx, actor, input.BootOperation)
	if err == nil {
		if prior.BareMetalDeploymentInput != input {
			return d, ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	if status != "awaiting_approval" || !latest {
		return d, ErrConflict
	}
	membership, generation, err := lockTicketVerifier(ctx, tx, actor, input.Installer)
	if err != nil {
		return d, err
	}
	d, err = scanDeployment(tx.QueryRowContext(ctx, `INSERT INTO cmp_bare_metal_deployments
		(deployment_id,tenant_id,project_id,account_id,site_id,system_id,stage_operation,boot_operation,
		credential_revision,ownership_revision,image_sha256,installer_id,installer_membership,installer_generation)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT DO NOTHING RETURNING `+deploymentColumns, rand.Text(), actor.Tenant, actor.Project, d.Account, d.Site, d.SystemID,
		input.StageOperation, input.BootOperation, d.credential, d.ownership, input.ImageSHA256, input.Installer, membership, generation))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrConflict
	}
	return d, err
}
