package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

func (s Store) AuthorizeVMBackup(ctx context.Context, token string, scope identity.Scope, site, hostID, id string, command VMCommand) (VM, error) {
	if !hostUUID.MatchString(id) || command.Revision < 1 {
		return VM{}, ErrInvalid
	}
	var result VM
	err := s.withVMHost(ctx, token, scope, site, hostID, command.AgentIdentity, func(tx *sql.Tx, actor identity.Scope, host Host) error {
		v, err := readVM(ctx, tx, actor, site, id, false)
		if err != nil {
			return err
		}
		if v.Host != host.ID {
			return identity.ErrForbidden
		}
		if v.Generation > host.Generation {
			return ErrStale
		}
		if err := checkRetainedVMSource(ctx, tx, v, command.Revision); err != nil {
			return err
		}
		result = v.VM
		return nil
	})
	return result, err
}
