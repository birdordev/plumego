package operations

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

const BareMetalReclaimRecoveriesMigration = "0122_bare_metal_reclaim_recoveries"

func MigrateBareMetalReclaimRecoveries(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BareMetalReclaimRecoveriesMigration)
}

type BareMetalReclaimRecoveryInput struct {
	ID              string `json:"recovery_id"`
	SystemID        string `json:"system_id"`
	IsolationSHA256 string `json:"isolation_sha256"`
	EvidenceSHA256  string `json:"evidence_sha256"`
}

type BareMetalReclaimRecoveryReview struct {
	ID       string `json:"recovery_id"`
	Decision string `json:"decision"`
}

type BareMetalReclaimRecovery struct {
	ID                            string     `json:"id"`
	IsolationSHA256               string     `json:"isolation_sha256"`
	EvidenceSHA256                string     `json:"evidence_sha256"`
	State                         string     `json:"state"`
	Requester                     string     `json:"requester_id"`
	Reviewer                      string     `json:"reviewer_id,omitempty"`
	RequestedAt                   time.Time  `json:"requested_at"`
	ReviewedAt                    *time.Time `json:"reviewed_at,omitempty"`
	credential, requesterRevision int64
}

const recoveryColumns = `recovery_id,isolation_sha256,evidence_sha256,state,requester_id,COALESCE(reviewer_id,''),
	requested_at,reviewed_at,credential_revision,requester_revision`

const reclaimRecoveryQuery = `SELECT ` + recoveryColumns +
	` FROM cmp_bare_metal_reclaim_recoveries WHERE (tenant_id,project_id,reclaim_id)=($1,$2,$3)`

func scanReclaimRecovery(row *sql.Row) (*BareMetalReclaimRecovery, error) {
	var v BareMetalReclaimRecovery
	if err := row.Scan(&v.ID, &v.IsolationSHA256, &v.EvidenceSHA256, &v.State, &v.Requester, &v.Reviewer,
		&v.RequestedAt, &v.ReviewedAt, &v.credential, &v.requesterRevision); err != nil {
		return nil, err
	}
	return &v, nil
}

func latestReclaimRecovery(ctx context.Context, tx *sql.Tx, actor identity.Scope, reclaimID string) (*BareMetalReclaimRecovery, error) {
	v, err := scanReclaimRecovery(tx.QueryRowContext(ctx, reclaimRecoveryQuery+` ORDER BY requested_at DESC,recovery_id DESC LIMIT 1`,
		actor.Tenant, actor.Project, reclaimID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

func (s Store) RequestBareMetalReclaimRecovery(ctx context.Context, token string, scope identity.Scope, id string, input BareMetalReclaimRecoveryInput) (BareMetalReclaim, error) {
	if !operationKey.MatchString(input.ID) || !bareMetalID.MatchString(input.SystemID) ||
		!deploymentDigest.MatchString(input.IsolationSHA256) || !deploymentDigest.MatchString(input.EvidenceSHA256) {
		return BareMetalReclaim{}, ErrInvalid
	}
	return s.withReclaimRecovery(ctx, token, scope, id, identity.Manage, func(ctx context.Context, tx *sql.Tx, actor identity.Scope, r *reclaimRecord, target inventory.MutationTarget) error {
		if input.SystemID != r.SystemID {
			return ErrConflict
		}
		member, err := lockReclaimMember(ctx, tx, actor, actor.Principal, "admin")
		if err != nil {
			return err
		}
		prior, err := scanReclaimRecovery(tx.QueryRowContext(ctx, reclaimRecoveryQuery+` AND recovery_id=$4`, actor.Tenant, actor.Project, id, input.ID))
		if err == nil {
			if prior.Requester != actor.Principal || prior.EvidenceSHA256 != input.EvidenceSHA256 || prior.IsolationSHA256 != input.IsolationSHA256 {
				return ErrConflict
			}
			r.Recovery = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if r.State != "claimed" {
			return ErrConflict
		}
		if err := requireReclaimSanitizerRevoked(ctx, tx, actor, *r); err != nil {
			return err
		}
		r.Recovery, err = scanReclaimRecovery(tx.QueryRowContext(ctx, `INSERT INTO cmp_bare_metal_reclaim_recoveries
			(tenant_id,project_id,reclaim_id,recovery_id,requester_id,requester_revision,credential_revision,isolation_sha256,evidence_sha256)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING RETURNING `+recoveryColumns,
			actor.Tenant, actor.Project, id, input.ID, actor.Principal, member, target.CredentialRevision, input.IsolationSHA256, input.EvidenceSHA256))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		return recordReclaimAudit(ctx, tx, actor, *r, "bare_metal_reclaim_recovery_requested", input.ID)
	})
}

type reclaimRecoveryChange func(context.Context, *sql.Tx, identity.Scope, *reclaimRecord, inventory.MutationTarget) error

func (s Store) withReclaimRecovery(ctx context.Context, token string, scope identity.Scope, id string, permission identity.Permission, change reclaimRecoveryChange) (BareMetalReclaim, error) {
	if !operationKey.MatchString(id) {
		return BareMetalReclaim{}, ErrInvalid
	}
	var r reclaimRecord
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, permission)
		if err != nil {
			return err
		}
		r, err = readReclaim(ctx, tx, actor, id)
		if err != nil {
			return err
		}
		if r.Sanitizer == actor.Principal {
			return identity.ErrForbidden
		}
		var target inventory.MutationTarget
		r, target, err = lockReclaimTarget(ctx, tx, actor, r)
		if err != nil {
			return err
		}
		return change(ctx, tx, actor, &r, target)
	})
	return r.BareMetalReclaim, err
}
