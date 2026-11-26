package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0104_database_rename.sql
var databaseRenameSchema string

//go:embed 0105_database_rename_events.sql
var databaseRenameEventsSchema string

const renameDatabaseInstance = "rename-database-instance"

type DatabaseRenameInput struct {
	Account     string  `json:"account"`
	Region      string  `json:"region"`
	AssetID     string  `json:"asset_id"`
	CurrentName *string `json:"current_name"`
	Name        string  `json:"name"`
}

type DatabaseRenameView struct {
	Product           string `json:"product"`
	CurrentName       string `json:"current_name"`
	Name              string `json:"name"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

func MigrateDatabaseRenameEvents(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0105_database_rename_events", databaseRenameEventsSchema)
}

func (s Store) SubmitDatabaseRename(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input DatabaseRenameInput) (Operation, error) {
	if !provider.ValidDatabaseRenameName(input.Name) || input.CurrentName == nil ||
		len(*input.CurrentName) > 1024 || strings.ContainsRune(*input.CurrentName, 0) || *input.CurrentName == input.Name {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		DatabaseRenameInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(renameDatabaseInstance+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.DatabaseInstance,
		input.Account, input.Region, input.AssetID, renameDatabaseInstance, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			name, product, err := inventory.DatabaseRenameTarget(ctx, tx, target, input.AssetID)
			if err != nil {
				return nil, err
			}
			change := provider.DatabaseRename{ID: input.AssetID, Product: product, CurrentName: *input.CurrentName, Name: input.Name}
			if !change.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			if name != change.CurrentName {
				return nil, identity.ErrAccountStale
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_database_rename_intents(operation_id,product,current_name,name)
					VALUES ($1,$2,$3,$4)`, id, change.Product, change.CurrentName, change.Name)
				return err
			}, nil
		})
}

func checkDatabaseRename(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID int64
	var observer creationObserver
	change := &w.databaseRename
	change.ID = w.AssetID
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.product,i.current_name,i.name,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_database_rename_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE operation_id=$1`, w.ID).Scan(&scopeID, &change.Product, &change.CurrentName, &change.Name,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.DatabaseInstance)
	if err != nil {
		return err
	}
	name, product, err := inventory.DatabaseRenameTarget(ctx, tx, target, w.AssetID)
	if err != nil {
		return err
	}
	if product != change.Product || !change.ValidFor(target.Provider) ||
		(name != change.CurrentName && (beforeWrite || name != change.Name)) {
		return identity.ErrAccountStale
	}
	return nil
}

func (s Store) processDatabaseRename(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.DatabaseInstance
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	renamer, ok := client.(provider.DatabaseRenamer)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := renamer.PrepareDatabaseRename(ctx, scope, w.databaseRename)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "database_rename")
	}
	status, err := renamer.ObserveDatabaseRename(ctx, scope, w.databaseRename)
	if err == nil && status == provider.ActionSucceeded {
		return false, s.confirmDatabaseRename(ctx, task, w)
	}
	if status == provider.ActionFailed || task.Attempts >= task.MaxAttempts ||
		provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) ||
		provider.IsFailure(err, provider.Unsupported) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "database_rename_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmDatabaseRename(ctx context.Context, task mq.Task, w work) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockDatabaseScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkDatabaseRename(ctx, tx, &w); err != nil {
			return err
		}
		change := w.databaseRename
		if err := inventory.RecordObservedAssetName(ctx, tx, scopeID, change.ID, change.CurrentName, change.Name); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "database_name_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
