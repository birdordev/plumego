package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

type ColdMigrationReview struct {
	ID       string `json:"migration_id"`
	Decision string `json:"decision"`
}

func (s Store) ReviewColdMigration(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64, review ColdMigrationReview) (ColdMigration, error) {
	if !hostUUID.MatchString(id) || revision < 1 || !identifier.MatchString(review.ID) ||
		(review.Decision != "approve" && review.Decision != "reject") {
		return ColdMigration{}, ErrInvalid
	}
	var result ColdMigration
	err := s.inProject(ctx, token, scope, site, identity.Approve, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		draft, err := readColdMigration(ctx, tx, actor, site, id, review.ID)
		if err != nil {
			return hiddenMissing(err)
		}
		evacuation := draft.SourceFenceGeneration > 0 && review.Decision == "approve"
		if review.Decision == "approve" {
			placement, err := readMigrationPlacement(ctx, tx, actor, site, draft.Placement, draft.Requester)
			if err != nil {
				return err
			}
			if err := lockIPv4Network(ctx, tx, actor, site, placement.Network, evacuation); err != nil {
				return err
			}
		}
		// 先锁网络，再按宿主机 ID 顺序锁主机，最后锁 VM 与预留。
		host, source, err := lockColdMigrationHosts(ctx, tx, actor, site, draft, evacuation)
		if err != nil {
			return err
		}
		v, err := readVM(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		draft, err = readColdMigration(ctx, tx, actor, site, id, review.ID)
		if err != nil {
			return hiddenMissing(err)
		}
		if draft.Requester == actor.Principal {
			return identity.ErrForbidden
		}
		if draft.VMRevision != revision {
			return ErrStale
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Approve), member.Revision); err != nil {
			return err
		}
		status := "rejected"
		if review.Decision == "approve" {
			status = "approved"
		}
		if draft.Status == status && draft.Reviewer == actor.Principal {
			result = draft
			return nil
		}
		if draft.Status != "pending" {
			return ErrConflict
		}
		if status == "approved" {
			if err := applyColdMigration(ctx, tx, actor, host, source, v, draft, member.Revision); err != nil {
				return err
			}
		} else if draft.AutomaticPlacement {
			if err := releaseAutomaticEvacuationHold(ctx, tx, actor, site, draft); err != nil {
				return err
			}
		}
		result, err = scanColdMigration(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_cold_migrations
			SET status=$3,reviewer_id=$4,reviewer_revision=$5,reviewed_at=clock_timestamp()
			WHERE (vm_id,migration_id)=($1::uuid,$2) RETURNING `+coldMigrationColumns,
			id, review.ID, status, actor.Principal, member.Revision))
		return err
	})
	if err != nil {
		return ColdMigration{}, err
	}
	return result, nil
}

func lockColdMigrationHosts(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, draft ColdMigration, evacuation bool) (Host, Host, error) {
	var source Host
	if evacuation && draft.SourceHost == draft.TargetHost {
		return Host{}, Host{}, ErrConflict
	}
	if evacuation && draft.SourceHost < draft.TargetHost {
		var err error
		source, err = readHost(ctx, tx, actor, site, draft.SourceHost, true)
		if err != nil {
			return Host{}, Host{}, err
		}
	}
	target, err := readHost(ctx, tx, actor, site, draft.TargetHost, true)
	if err == nil && evacuation && draft.SourceHost > draft.TargetHost {
		source, err = readHost(ctx, tx, actor, site, draft.SourceHost, true)
	}
	return target, source, err
}

func applyColdMigration(ctx context.Context, tx *sql.Tx, actor identity.Scope, host, source Host, v vmRecord, draft ColdMigration, reviewerVersion int64) error {
	if draft.SourcePlacement != v.Placement || draft.TargetGeneration != host.Generation {
		return ErrStale
	}
	if err := checkColdMigrationSource(ctx, tx, v, source, draft.VMRevision, draft.SourceFenceGeneration); err != nil {
		return err
	}
	if err := checkVMGrant(ctx, tx, actor, draft.Requester, string(identity.Manage), draft.requesterVersion); err != nil {
		return err
	}
	if err := checkAgent(ctx, tx, actor, host.Agent); err != nil {
		return err
	}
	target, err := readMigrationPlacement(ctx, tx, actor, host.Site, draft.Placement, draft.Requester)
	if err != nil {
		return err
	}
	if err := checkColdMigrationTarget(v, target, host, draft.SourceFenceGeneration > 0); err != nil {
		return err
	}
	if draft.RequireVerifiedBackup {
		if err := requireVerifiedHostImage(ctx, tx, actor, host, draft.Image); err != nil {
			return err
		}
	}
	var needed Capacity
	if target.SharedStoragePool != "" {
		needed.DiskMiB = target.Capacity.DiskMiB
	}
	ok, err := fitsCurrentCapacity(ctx, tx, actor, host, needed)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoCapacity
	}
	volume, err := reserveBootVolume(ctx, tx, actor, host.Site, target, VMInput{ImageID: draft.Image})
	if err != nil {
		return err
	}
	if draft.SourceFenceGeneration > 0 {
		if err := transferEvacuationAddress(ctx, tx, v, target); err != nil {
			return err
		}
	}
	if err := consumeVMPlacement(ctx, tx, target.ID, volume, draft.Image); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET placement_id=$2,state='queued',observation='',
		requester_revision=$3,reviewer_id=$4,reviewer_revision=$5,
		shutdown_requester_id='',shutdown_requester_revision=0,shutdown_reviewer_id='',shutdown_reviewer_revision=0,
		retire_requester_id='',retire_requester_revision=0,retire_reviewer_id='',retire_reviewer_revision=0,
		revision=revision+1,updated_at=clock_timestamp() WHERE vm_id=$1::uuid`,
		v.ID, target.ID, draft.requesterVersion, actor.Principal, reviewerVersion)
	return err
}
