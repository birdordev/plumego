package operations

import (
	"context"
	"database/sql"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

func (s Store) confirmInstancePower(ctx context.Context, task mq.Task, w work, status string) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockInstanceScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkOperationTarget(ctx, tx, &w); err != nil {
			return err
		}
		if err := inventory.RecordInstanceStatus(ctx, tx, scopeID, w.AssetID, status); err != nil {
			return err
		}
		return writeTransition(ctx, tx, task.ID, w.Status, "succeeded", "desired_state_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
