package operations

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

func (s Store) ReviewBareMetalReclaimRecovery(ctx context.Context, token string, scope identity.Scope, id string, input BareMetalReclaimRecoveryReview) (BareMetalReclaim, error) {
	if !operationKey.MatchString(input.ID) || (input.Decision != "approve" && input.Decision != "reject") {
		return BareMetalReclaim{}, ErrInvalid
	}
	return s.withReclaimRecovery(ctx, token, scope, id, identity.Approve, func(ctx context.Context, tx *sql.Tx, actor identity.Scope, r *reclaimRecord, target inventory.MutationTarget) error {
		recovery, err := scanReclaimRecovery(tx.QueryRowContext(ctx, reclaimRecoveryQuery+` AND recovery_id=$4`, actor.Tenant, actor.Project, id, input.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		if actor.Principal == recovery.Requester || actor.Principal == r.Requester {
			return identity.ErrForbidden
		}
		if _, err := lockReclaimMember(ctx, tx, actor, actor.Principal, "approver"); err != nil {
			return err
		}
		next := "rejected"
		if input.Decision == "approve" {
			next = "verified"
		}
		if recovery.State == next && recovery.Reviewer == actor.Principal {
			r.Recovery = recovery
			return nil
		}
		if r.State != "claimed" || recovery.State != "requested" {
			return ErrConflict
		}
		if next == "verified" {
			if err := verifyReclaimRecovery(ctx, tx, actor, r, recovery, target); err != nil {
				return err
			}
		}
		r.Recovery, err = scanReclaimRecovery(tx.QueryRowContext(ctx, `UPDATE cmp_bare_metal_reclaim_recoveries
			SET state=$3,reviewer_id=$4,reviewed_at=statement_timestamp() WHERE (reclaim_id,recovery_id)=($1,$2)
			RETURNING `+recoveryColumns, id, input.ID, next, actor.Principal))
		if err != nil {
			return err
		}
		return recordReclaimAudit(ctx, tx, actor, *r, "bare_metal_reclaim_recovery_"+next, input.ID)
	})
}

func verifyReclaimRecovery(ctx context.Context, tx *sql.Tx, actor identity.Scope, r *reclaimRecord, recovery *BareMetalReclaimRecovery, target inventory.MutationTarget) error {
	if recovery.credential != target.CredentialRevision {
		return identity.ErrAccountStale
	}
	member, err := lockReclaimMember(ctx, tx, actor, recovery.Requester, "admin")
	if err != nil {
		return err
	}
	if member != recovery.requesterRevision {
		return identity.ErrAccountStale
	}
	if err := requireReclaimSanitizerRevoked(ctx, tx, actor, *r); err != nil {
		return err
	}
	if err := inventory.CheckBareMetalOffline(ctx, tx, r.scope, target.CredentialRevision, r.SystemID, &recovery.RequestedAt); err != nil {
		return err
	}
	event, err := inventory.LatestBareMetalBoot(ctx, tx, r.SystemID)
	if err != nil {
		return err
	}
	*r, err = saveReclaim(ctx, tx, actor, `UPDATE cmp_bare_metal_reclaims SET state='verified',verified_at=statement_timestamp(),
		evidence_sha256=$2,verified_boot_event=$3,credential_revision=$4 WHERE reclaim_id=$1`,
		r.ID, recovery.EvidenceSHA256, event, target.CredentialRevision)
	return err
}

func requireReclaimSanitizerRevoked(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord) error {
	member, generation, err := lockTicketVerifier(ctx, tx, actor, r.Sanitizer)
	if errors.Is(err, identity.ErrForbidden) {
		return nil
	}
	if err != nil {
		return err
	}
	if member == r.sanitizerRevision && generation == r.sanitizerGeneration {
		return ErrConflict
	}
	return nil
}
