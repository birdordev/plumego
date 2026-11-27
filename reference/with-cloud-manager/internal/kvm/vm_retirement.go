package kvm

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
)

func (s Store) RequestVMRetirement(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64) (VM, error) {
	if !hostUUID.MatchString(id) || revision < 1 {
		return VM{}, ErrInvalid
	}
	var result vmRecord
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		v, err := readVM(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if v.Revision != revision {
			return ErrStale
		}
		if v.State != "stopped" {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state='retire_requested',
			retire_requester_id=$2,retire_requester_revision=$3,revision=revision+1,
			updated_at=clock_timestamp() WHERE vm_id=$1::uuid`, id, actor.Principal, member.Revision); err != nil {
			return err
		}
		result, err = readVM(ctx, tx, actor, site, id, false)
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, v.Host, "vm_retire_request", v.Placement)
	})
	return result.VM, err
}

func reviewVMRetirement(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, v vmRecord, member identity.Membership, decision string) error {
	if v.retireRequester == actor.Principal {
		return identity.ErrForbidden
	}
	event := "vm_retire_reject"
	if decision == "approve" {
		if v.State != "retire_requested" {
			return ErrConflict
		}
		if err := checkVMGrant(ctx, tx, actor, v.retireRequester, "manage", v.retireRequesterRevision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state='retire_queued',retire_reviewer_id=$2,
			retire_reviewer_revision=$3,revision=revision+1,updated_at=clock_timestamp() WHERE vm_id=$1::uuid`,
			v.ID, actor.Principal, member.Revision); err != nil {
			return err
		}
		event = "vm_retire_approve"
	} else if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state='stopped',retire_requester_id='',
		retire_requester_revision=0,retire_reviewer_id='',retire_reviewer_revision=0,
		revision=revision+1,updated_at=clock_timestamp() WHERE vm_id=$1::uuid`, v.ID); err != nil {
		return err
	}
	return recordEvent(ctx, tx, actor, site, v.Host, event, v.Placement)
}

func retainReleasedBootVolume(ctx context.Context, tx *sql.Tx, placement string) error {
	var id string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_kvm_placements SET released_at=clock_timestamp(),boot_volume_retained=true
		WHERE placement_id=$1 AND consumed_at IS NOT NULL AND released_at IS NULL RETURNING placement_id`, placement).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
