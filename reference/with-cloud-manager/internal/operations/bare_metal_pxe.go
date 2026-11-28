package operations

import (
	"context"
	"database/sql"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

const BareMetalPXEMigration = "0119_bare_metal_pxe"

func MigrateBareMetalPXE(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BareMetalPXEMigration)
}

func bareMetalPXEAction(action string) bool {
	return action == provider.StageBareMetalPXE || action == provider.CancelBareMetalPXE
}

func (s Store) SubmitBareMetalPXE(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input BareMetalChangeInput) (Operation, error) {
	if !bareMetalID.MatchString(input.AssetID) || !bareMetalPXEAction(input.Action) {
		return Operation{}, ErrInvalid
	}
	return s.submitBareMetalChange(ctx, token, scope, key, revision, input)
}

func (s Store) observeBareMetalPXE(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	operator, ok := client.(provider.BareMetalBootOperator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	matches, err := operator.ObserveBareMetalPXE(ctx, scope, w.AssetID, w.Action)
	if err == nil && matches {
		err = s.withLease(ctx, task, func(tx *sql.Tx) error {
			if err := checkBareMetalPower(ctx, tx, &w); err != nil {
				return err
			}
			return writeTransition(ctx, tx, task.ID, w.Status, "succeeded", "desired_boot_override_observed", "")
		})
		if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
			return false, s.endUnavailable(ctx, task, w)
		}
		return false, err
	}
	if task.Attempts >= task.MaxAttempts {
		return false, s.transition(ctx, task, w.Status, "needs_review", "observation_budget_exhausted", "")
	}
	return true, nil
}
