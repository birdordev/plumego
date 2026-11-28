package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

type VMReview struct {
	Decision string `json:"decision"`
}

func (s Store) ReviewVM(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64, review VMReview) (VM, error) {
	if !hostUUID.MatchString(id) || revision < 1 || (review.Decision != "approve" && review.Decision != "reject") {
		return VM{}, ErrInvalid
	}
	var result vmRecord
	err := s.inProject(ctx, token, scope, site, identity.Approve, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		v, err := readVM(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		switch v.State {
		case "shutdown_requested", "shutdown_queued":
			if v.Revision != revision {
				return ErrStale
			}
			if err := reviewVMShutdown(ctx, tx, actor, site, v, member, review.Decision); err != nil {
				return err
			}
			result, err = readVM(ctx, tx, actor, site, id, false)
			return err
		case "retire_requested", "retire_queued":
			if v.Revision != revision {
				return ErrStale
			}
			if err := reviewVMRetirement(ctx, tx, actor, site, v, member, review.Decision); err != nil {
				return err
			}
			result, err = readVM(ctx, tx, actor, site, id, false)
			return err
		}
		if v.requester == actor.Principal {
			return identity.ErrForbidden
		}
		if v.Revision != revision {
			return ErrStale
		}
		next, event := "queued", "vm_approve"
		if review.Decision == "reject" {
			next, event = "rejected", "vm_reject"
		}
		canRejectQueued := v.State == "queued" && next == "rejected"
		if v.State != "awaiting_approval" && !canRejectQueued {
			return ErrConflict
		}
		if next == "queued" {
			if err := checkVMGrant(ctx, tx, actor, v.requester, "execute", v.requesterRevision); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state=$2,reviewer_id=$3,reviewer_revision=$4,
			revision=revision+1,updated_at=clock_timestamp() WHERE vm_id=$1::uuid`, id, next, actor.Principal, member.Revision); err != nil {
			return err
		}
		if next == "rejected" {
			if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_placements SET released_at=clock_timestamp()
				WHERE placement_id=$1`, v.Placement); err != nil {
				return err
			}
		}
		result, err = readVM(ctx, tx, actor, site, id, false)
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, v.Host, event, v.Placement)
	})
	return result.VM, err
}

func checkVMGrant(ctx context.Context, tx *sql.Tx, scope identity.Scope, principal, needed string, revision int64) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT p.principal_id FROM cmp_principals AS p
		JOIN cmp_memberships AS m USING (tenant_id,principal_id)
		WHERE (p.tenant_id,m.project_id,p.principal_id,m.revision)=($1,$2,$3,$4)
		AND p.active AND m.active AND (($5='approve' AND m.role='approver') OR
			($5='execute' AND m.role IN ('admin','operator')) OR ($5='manage' AND m.role='admin')) FOR SHARE OF p,m`,
		scope.Tenant, scope.Project, principal, revision, needed).Scan(&id)
	return hiddenMissing(err)
}
