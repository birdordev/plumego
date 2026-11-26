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

//go:embed 0109_cache_rename.sql
var cacheRenameSchema string

//go:embed 0110_cache_rename_events.sql
var cacheRenameEventsSchema string

const renameCacheInstance = "rename-cache-instance"

type CacheRenameInput struct {
	Account     string  `json:"account"`
	Region      string  `json:"region"`
	AssetID     string  `json:"asset_id"`
	CurrentName *string `json:"current_name"`
	Name        string  `json:"name"`
}

type CacheRenameView struct {
	Product           string `json:"product"`
	CurrentName       string `json:"current_name"`
	Name              string `json:"name"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

func MigrateCacheRenameEvents(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0110_cache_rename_events", cacheRenameEventsSchema)
}

func (s Store) SubmitCacheRename(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input CacheRenameInput) (Operation, error) {
	if !provider.ValidCacheRenameName(input.Name) || input.CurrentName == nil ||
		len(*input.CurrentName) > 1024 || strings.ContainsRune(*input.CurrentName, 0) || *input.CurrentName == input.Name {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		CacheRenameInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(renameCacheInstance+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.CacheInstance,
		input.Account, input.Region, input.AssetID, renameCacheInstance, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			name, product, err := inventory.CacheRenameTarget(ctx, tx, target, input.AssetID)
			if err != nil {
				return nil, err
			}
			change := provider.CacheRename{ID: input.AssetID, Product: product, CurrentName: *input.CurrentName, Name: input.Name}
			if !change.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			if name != change.CurrentName {
				return nil, identity.ErrAccountStale
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_cache_rename_intents(operation_id,product,current_name,name)
					VALUES ($1,$2,$3,$4)`, id, change.Product, change.CurrentName, change.Name)
				return err
			}, nil
		})
}

func checkCacheRename(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID int64
	var observer creationObserver
	change := &w.cacheRename
	change.ID = w.AssetID
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.product,i.current_name,i.name,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_cache_rename_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE operation_id=$1`, w.ID).Scan(&scopeID, &change.Product, &change.CurrentName, &change.Name,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.CacheInstance)
	if err != nil {
		return err
	}
	name, product, err := inventory.CacheRenameTarget(ctx, tx, target, w.AssetID)
	if err != nil {
		return err
	}
	if product != change.Product || !change.ValidFor(target.Provider) ||
		(name != change.CurrentName && (beforeWrite || name != change.Name)) {
		return identity.ErrAccountStale
	}
	return nil
}

func (s Store) processCacheRename(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.CacheInstance
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	renamer, ok := client.(provider.CacheRenamer)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := renamer.PrepareCacheRename(ctx, scope, w.cacheRename)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "cache_rename")
	}
	status, err := renamer.ObserveCacheRename(ctx, scope, w.cacheRename)
	if err == nil && status == provider.ActionSucceeded {
		return false, s.confirmCacheRename(ctx, task, w)
	}
	if status == provider.ActionFailed || task.Attempts >= task.MaxAttempts ||
		provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) ||
		provider.IsFailure(err, provider.Unsupported) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "cache_rename_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmCacheRename(ctx context.Context, task mq.Task, w work) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockCacheScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkCacheRename(ctx, tx, &w); err != nil {
			return err
		}
		change := w.cacheRename
		if err := inventory.RecordObservedAssetName(ctx, tx, scopeID, change.ID, change.CurrentName, change.Name); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "cache_name_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
