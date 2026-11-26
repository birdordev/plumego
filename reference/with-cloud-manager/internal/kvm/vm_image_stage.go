package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

func (s Store) AuthorizeImageStage(ctx context.Context, token string, scope identity.Scope, site, hostID, id string, command VMCommand) (VM, error) {
	if !hostUUID.MatchString(id) || command.Revision < 1 {
		return VM{}, ErrInvalid
	}
	var job VM
	err := s.withVMHost(ctx, token, scope, site, hostID, command.AgentIdentity, func(tx *sql.Tx, actor identity.Scope, host Host) error {
		v, err := readVM(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if err := matchVMHost(v.VM, host, command.Revision); err != nil {
			return err
		}
		if v.State != "staging" || v.ImageID == "" || host.Draining {
			return ErrConflict
		}
		job = v.VM
		return checkImageStageApproval(ctx, tx, actor, v)
	})
	return job, err
}

func checkImageStageApproval(ctx context.Context, tx *sql.Tx, actor identity.Scope, job vmRecord) error {
	if err := checkVMGrant(ctx, tx, actor, job.requester, "execute", job.requesterRevision); err != nil {
		return err
	}
	return checkVMGrant(ctx, tx, actor, job.reviewer, "approve", job.reviewerRevision)
}
