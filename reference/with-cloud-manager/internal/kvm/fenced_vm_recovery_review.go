package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

type FencedVMRecoveryReview struct {
	ID       string `json:"recovery_id"`
	Decision string `json:"decision"`
}

func (s Store) ReviewFencedVMRecovery(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64, review FencedVMRecoveryReview) (FencedVMRecovery, error) {
	if !hostUUID.MatchString(id) || revision < 1 || !identifier.MatchString(review.ID) ||
		(review.Decision != "approve" && review.Decision != "reject") {
		return FencedVMRecovery{}, ErrInvalid
	}
	var result FencedVMRecovery
	err := s.inProject(ctx, token, scope, site, identity.Approve, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		host, v, err := lockVMAndHost(ctx, tx, actor, site, id)
		if err != nil {
			return err
		}
		r, err := scanFencedVMRecovery(tx.QueryRowContext(ctx, fencedVMRecoveryQuery+` AND recovery_id=$5`,
			actor.Tenant, actor.Project, site, id, review.ID))
		if err != nil {
			return hiddenMissing(err)
		}
		if r.Requester == actor.Principal {
			return identity.ErrForbidden
		}
		if r.VMRevision != revision {
			return ErrStale
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Approve), member.Revision); err != nil {
			return err
		}
		status, state := "rejected", ""
		if review.Decision == "approve" {
			status = "approved"
		}
		if r.Status == status && r.Reviewer == actor.Principal {
			result = r
			return nil
		}
		if r.Status != "pending" {
			return ErrConflict
		}
		if status == "approved" {
			if err := checkFencedVMRecoveryTarget(host, v, revision, r.FencedVMRecoveryInput); err != nil {
				return err
			}
			if r.Host != host.ID || r.SourceState != v.State {
				return ErrConflict
			}
			if err := checkVMGrant(ctx, tx, actor, r.Requester, string(identity.Manage), r.requesterRevision); err != nil {
				return err
			}
			state = fencedVMRecoveryResult(v.State, r.Observed)
			if err := applyFencedVMRecovery(ctx, tx, v, r, state, actor.Principal, member.Revision); err != nil {
				return err
			}
		}
		result, err = scanFencedVMRecovery(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_fenced_vm_recoveries
			SET status=$3,result_state=$4,reviewer_id=$5,reviewer_revision=$6,reviewed_at=clock_timestamp()
			WHERE (vm_id,recovery_id)=($1::uuid,$2) RETURNING `+fencedVMRecoveryColumns,
			id, review.ID, status, state, actor.Principal, member.Revision))
		return err
	})
	if err != nil {
		return FencedVMRecovery{}, err
	}
	return result, nil
}

func fencedVMRecoveryResult(state, observed string) string {
	switch observed {
	case "absent":
		switch state {
		case "staging", "staged", "defining", "defined", "starting":
			return "rejected"
		case "undefining":
			return "retired"
		}
	case "defined":
		switch state {
		case "defining", "defined", "starting", "stopping", "undefining":
			return "stopped"
		}
	case "running":
		if state == "starting" || state == "stopping" {
			return "running"
		}
	}
	return ""
}

func applyFencedVMRecovery(ctx context.Context, tx *sql.Tx, v vmRecord, r FencedVMRecovery, next, reviewer string, reviewerRevision int64) error {
	if next == "rejected" || next == "retired" {
		if err := retainReleasedBootVolume(ctx, tx, v.Placement); err != nil {
			return err
		}
	}
	if next == "stopped" && v.shutdownRequester == "" {
		v.shutdownRequester, v.shutdownRequesterRevision = r.Requester, r.requesterRevision
		v.shutdownReviewer, v.shutdownReviewerRevision = reviewer, reviewerRevision
	}
	if next == "running" {
		v.shutdownRequester, v.shutdownRequesterRevision = "", 0
		v.shutdownReviewer, v.shutdownReviewerRevision = "", 0
	}
	if next == "stopped" && v.State == "undefining" {
		v.retireRequester, v.retireRequesterRevision = "", 0
		v.retireReviewer, v.retireReviewerRevision = "", 0
	}
	_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state=$2,observation=$3,revision=revision+1,
		shutdown_requester_id=$4,shutdown_requester_revision=$5,shutdown_reviewer_id=$6,shutdown_reviewer_revision=$7,
		retire_requester_id=$8,retire_requester_revision=$9,retire_reviewer_id=$10,retire_reviewer_revision=$11,
		updated_at=clock_timestamp() WHERE vm_id=$1::uuid`, v.ID, next, r.Observed,
		v.shutdownRequester, v.shutdownRequesterRevision, v.shutdownReviewer, v.shutdownReviewerRevision,
		v.retireRequester, v.retireRequesterRevision, v.retireReviewer, v.retireReviewerRevision)
	return err
}
