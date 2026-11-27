package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

func (s Store) RequestVMShutdown(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64) (VM, error) {
	if !hostUUID.MatchString(id) || revision < 1 {
		return VM{}, ErrInvalid
	}
	var result vmRecord
	err := s.inProject(ctx, token, scope, site, identity.Execute, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		v, err := readVM(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if v.Revision != revision {
			return ErrStale
		}
		if v.State != "running" {
			return ErrConflict
		}
		if err := ensureVMNotMigrating(ctx, tx, v.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state='shutdown_requested',
			shutdown_requester_id=$2,shutdown_requester_revision=$3,revision=revision+1,
			updated_at=clock_timestamp() WHERE vm_id=$1::uuid`, id, actor.Principal, member.Revision); err != nil {
			return err
		}
		result, err = readVM(ctx, tx, actor, site, id, false)
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, v.Host, "vm_shutdown_request", v.Placement)
	})
	return result.VM, err
}

func reviewVMShutdown(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, v vmRecord, member identity.Membership, decision string) error {
	if v.shutdownRequester == actor.Principal {
		return identity.ErrForbidden
	}
	event := "vm_shutdown_reject"
	if decision == "approve" {
		if v.State != "shutdown_requested" {
			return ErrConflict
		}
		if err := checkVMGrant(ctx, tx, actor, v.shutdownRequester, "execute", v.shutdownRequesterRevision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state='shutdown_queued',shutdown_reviewer_id=$2,
			shutdown_reviewer_revision=$3,revision=revision+1,updated_at=clock_timestamp() WHERE vm_id=$1::uuid`,
			v.ID, actor.Principal, member.Revision); err != nil {
			return err
		}
		event = "vm_shutdown_approve"
	} else if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state='running',shutdown_requester_id='',
		shutdown_requester_revision=0,shutdown_reviewer_id='',shutdown_reviewer_revision=0,
		revision=revision+1,updated_at=clock_timestamp() WHERE vm_id=$1::uuid`, v.ID); err != nil {
		return err
	}
	return recordEvent(ctx, tx, actor, site, v.Host, event, v.Placement)
}
