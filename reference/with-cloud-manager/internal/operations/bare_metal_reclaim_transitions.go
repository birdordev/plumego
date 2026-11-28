package operations

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

type BareMetalReclaimReview struct {
	Decision string `json:"decision"`
}

type BareMetalReclaimClaim struct {
	SystemID string `json:"system_id"`
}

type BareMetalReclaimReceipt struct {
	SystemID       string `json:"system_id"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

func (s Store) ReviewBareMetalReclaim(ctx context.Context, token string, scope identity.Scope, id string, input BareMetalReclaimReview) (BareMetalReclaim, error) {
	if input.Decision != "approve" && input.Decision != "reject" {
		return BareMetalReclaim{}, ErrInvalid
	}
	return s.changeBareMetalReclaim(ctx, token, scope, id, false, input.Decision == "reject", func(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord) (reclaimRecord, error) {
		if actor.Principal == r.Requester || actor.Principal == r.Sanitizer {
			return r, identity.ErrForbidden
		}
		next := "approved"
		if input.Decision == "reject" {
			next = "rejected"
		}
		revision, err := lockReclaimMember(ctx, tx, actor, actor.Principal, "approver")
		if err != nil {
			return r, err
		}
		if r.State == next && r.Reviewer == actor.Principal {
			return r, nil
		}
		canRejectApproved := r.State == "approved" && next == "rejected"
		if r.State != "requested" && !canRejectApproved {
			return r, ErrConflict
		}
		if next == "approved" {
			if err := checkReclaimAuthorities(ctx, tx, actor, r); err != nil {
				return r, err
			}
		}
		return saveReclaim(ctx, tx, actor, `UPDATE cmp_bare_metal_reclaims SET state=$2,reviewer_id=$3,reviewer_revision=$4,
			reviewed_at=statement_timestamp() WHERE reclaim_id=$1`, r.ID, next, actor.Principal, revision)
	})
}

func (s Store) ClaimBareMetalReclaim(ctx context.Context, token string, scope identity.Scope, id string, input BareMetalReclaimClaim) (BareMetalReclaim, error) {
	if !bareMetalID.MatchString(input.SystemID) {
		return BareMetalReclaim{}, ErrInvalid
	}
	return s.changeBareMetalReclaim(ctx, token, scope, id, true, false, func(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord) (reclaimRecord, error) {
		if input.SystemID != r.SystemID || r.State != "approved" {
			return r, ErrConflict
		}
		if err := checkReclaimAuthorities(ctx, tx, actor, r); err != nil {
			return r, err
		}
		if err := inventory.CheckBareMetalOffline(ctx, tx, r.scope, r.credential, r.SystemID, nil); err != nil {
			return r, err
		}
		return saveReclaim(ctx, tx, actor, `UPDATE cmp_bare_metal_reclaims SET state='claimed',claimed_at=statement_timestamp()
			WHERE reclaim_id=$1 AND reviewed_at>statement_timestamp()-interval '30 minutes'`, r.ID)
	})
}

func (s Store) RecordBareMetalReclaim(ctx context.Context, token string, scope identity.Scope, id string, input BareMetalReclaimReceipt) (BareMetalReclaim, error) {
	if !bareMetalID.MatchString(input.SystemID) || !deploymentDigest.MatchString(input.EvidenceSHA256) {
		return BareMetalReclaim{}, ErrInvalid
	}
	return s.changeBareMetalReclaim(ctx, token, scope, id, true, false, func(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord) (reclaimRecord, error) {
		if input.SystemID != r.SystemID {
			return r, ErrConflict
		}
		if err := checkReclaimAuthorities(ctx, tx, actor, r); err != nil {
			return r, err
		}
		if r.State == "verified" && r.EvidenceSHA256 == input.EvidenceSHA256 {
			return r, nil
		}
		if r.State != "claimed" {
			return r, ErrConflict
		}
		if err := inventory.CheckBareMetalOffline(ctx, tx, r.scope, r.credential, r.SystemID, r.ClaimedAt); err != nil {
			return r, err
		}
		event, err := inventory.LatestBareMetalBoot(ctx, tx, r.SystemID)
		if err != nil {
			return r, err
		}
		return saveReclaim(ctx, tx, actor, `UPDATE cmp_bare_metal_reclaims
			SET state='verified',verified_at=statement_timestamp(),evidence_sha256=$2,verified_boot_event=$3
			WHERE reclaim_id=$1`, r.ID, input.EvidenceSHA256, event)
	})
}

type reclaimTransition func(context.Context, *sql.Tx, identity.Scope, reclaimRecord) (reclaimRecord, error)

func (s Store) changeBareMetalReclaim(ctx context.Context, token string, scope identity.Scope, id string, siteService, allowStaleCredential bool, transition reclaimTransition) (BareMetalReclaim, error) {
	if !operationKey.MatchString(id) {
		return BareMetalReclaim{}, ErrInvalid
	}
	var r reclaimRecord
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := reclaimActor(ctx, tx, token, scope, siteService)
		if err != nil {
			return err
		}
		r, err = readReclaim(ctx, tx, actor, id)
		if err != nil {
			return err
		}
		if siteService && r.Sanitizer != actor.Principal {
			return identity.ErrForbidden
		}
		var target inventory.MutationTarget
		r, target, err = lockReclaimTarget(ctx, tx, actor, r)
		if err != nil {
			return err
		}
		if r.credential != target.CredentialRevision && !allowStaleCredential {
			return identity.ErrAccountStale
		}
		r, err = transition(ctx, tx, actor, r)
		return err
	})
	return r.BareMetalReclaim, err
}

func lockReclaimTarget(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord) (reclaimRecord, inventory.MutationTarget, error) {
	target, err := inventory.ResolveBareMetalObservation(ctx, tx, actor, r.Account, r.Site, r.SystemID)
	if err != nil {
		return r, target, err
	}
	r, err = readReclaim(ctx, tx, actor, r.ID)
	if err == nil && (r.scope != target.ScopeID || r.OwnershipRevision != target.OwnershipRevision) {
		err = identity.ErrAccountStale
	}
	return r, target, err
}

func reclaimActor(ctx context.Context, tx *sql.Tx, token string, scope identity.Scope, siteService bool) (identity.Scope, error) {
	if !siteService {
		return operationActor(ctx, tx, token, scope, identity.Approve)
	}
	actor, err := identity.NewStore(tx).AuthenticateService(ctx, token)
	if err != nil {
		return actor, err
	}
	if actor.Tenant != scope.Tenant {
		return actor, identity.ErrForbidden
	}
	actor.Project = scope.Project
	return actor, nil
}

func saveReclaim(ctx context.Context, tx *sql.Tx, actor identity.Scope, query string, args ...any) (reclaimRecord, error) {
	r, err := scanReclaim(tx.QueryRowContext(ctx, query+" RETURNING "+reclaimColumns, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrConflict
	}
	if err != nil {
		return r, err
	}
	return r, auditReclaim(ctx, tx, actor, r)
}
