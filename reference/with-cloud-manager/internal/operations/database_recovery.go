package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const DatabaseRecoveryMigration = "0169_database_recovery"
const recoverDatabaseInstance = "recover-database-instance"

func MigrateDatabaseRecovery(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, DatabaseRecoveryMigration)
}

type DatabaseRecoveryInput struct {
	Account    string    `json:"account"`
	Region     string    `json:"region"`
	ObservedAt time.Time `json:"observed_at"`
	provider.DatabaseRecovery
}

type DatabaseRecoveryView struct {
	provider.DatabaseRecovery
	ObservedAt          time.Time `json:"observed_at"`
	TargetID            string    `json:"target_id,omitempty"`
	CanRetryObservation bool      `json:"can_retry_observation,omitempty"`
}

func (s Store) SubmitDatabaseRecovery(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input DatabaseRecoveryInput) (Operation, error) {
	input.ObservedAt, input.RestoreTime = input.ObservedAt.UTC(), input.RestoreTime.UTC()
	if input.ObservedAt.IsZero() || (!input.ValidFor(provider.HuaweiECS) && !input.ValidFor(provider.AlibabaECS) && !input.ValidFor(provider.TencentCVM)) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		DatabaseRecoveryInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, ErrInvalid
	}
	digest := sha256.Sum256(append([]byte(recoverDatabaseInstance+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.DatabaseInstance,
		input.Account, input.Region, input.SourceID, recoverDatabaseInstance, "", digest,
		func(ctx context.Context, tx *sql.Tx, actor identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			if !input.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			if err := lockOwnedCreationAccount(ctx, tx, actor, input.Account); err != nil {
				return nil, err
			}
			if err := inventory.CheckDatabaseRecoveryPoint(ctx, tx, target, input.SourceID, input.ObservedAt, input.RestoreTime); err != nil {
				return nil, err
			}
			revisions, err := inventory.CheckDatabaseRecoveryPlacement(ctx, tx, actor, input.Account, input.Region, target, input.DatabaseRecovery)
			if err != nil {
				return nil, err
			}
			spec, err := json.Marshal(input.DatabaseRecovery)
			if err != nil {
				return nil, err
			}
			dependencies, err := json.Marshal(revisions)
			if err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_database_recovery_intents(operation_id,observed_at,spec,dependency_revisions)
					VALUES ($1,$2,$3::jsonb,$4::jsonb)`, id, input.ObservedAt, string(spec), string(dependencies))
				return err
			}, nil
		})
}

func checkDatabaseRecovery(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID int64
	var spec, encodedRevisions []byte
	var observer creationObserver
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.spec,i.dependency_revisions,i.provider_resource_id,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_database_recovery_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE operation_id=$1`, w.ID).Scan(&scopeID, &spec, &encodedRevisions, &w.network.resourceID,
		&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(spec, &w.databaseRecovery); err != nil {
		return err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.DatabaseInstance)
	if err != nil {
		return err
	}
	if !w.databaseRecovery.ValidFor(target.Provider) || w.databaseRecovery.SourceID != w.AssetID ||
		(!beforeWrite && target.CredentialRevision != w.credential) {
		return identity.ErrAccountStale
	}
	if err := lockOwnedCreationAccount(ctx, tx, w.actor, w.Account); err != nil {
		return err
	}
	if !beforeWrite {
		return nil
	}
	var expected [3]int64
	if err := json.Unmarshal(encodedRevisions, &expected); err != nil {
		return err
	}
	revisions, err := inventory.CheckDatabaseRecoveryPlacement(ctx, tx, w.actor, w.Account, w.Region, target, w.databaseRecovery)
	if err == nil && revisions != expected {
		return identity.ErrAccountStale
	}
	return err
}

func (s Store) processDatabaseRecovery(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	recoverer, ok := client.(provider.DatabaseRecoverer)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.DatabaseInstance
	if w.Status == "queued" {
		send, err := recoverer.PrepareDatabaseRecovery(ctx, scope, w.databaseRecovery, w.ID)
		if err != nil || send == nil || ctx.Err() != nil {
			return s.rejectCreationPreflight(ctx, task, w, err)
		}
		return s.dispatchPreparedResource(ctx, task, w, func(ctx context.Context, _ work) (provider.LaunchReceipt, error) {
			return send(ctx)
		})
	}
	canLocateTarget := scope.Provider == provider.TencentCVM
	if !canLocateTarget && (w.Status == "applying" || w.ProviderRequestID == "" || w.network.resourceID == "") {
		return false, s.transition(ctx, task, w.Status, "needs_review", "database_recovery_receipt_unavailable", "")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, w.Status, "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	receipt := provider.LaunchReceipt{RequestID: w.ProviderRequestID, ResourceID: w.network.resourceID}
	target, err := recoverer.ObserveDatabaseRecovery(ctx, scope, w.databaseRecovery, w.ID, receipt)
	if err == nil && target.ID != "" {
		if (receipt.ResourceID != "" && target.ID != receipt.ResourceID) || target.ID == w.AssetID || !target.ValidDatabase() {
			return false, s.transition(ctx, task, w.Status, "needs_review", "database_recovery_requires_evidence", "")
		}
		return false, s.confirmDatabaseRecovery(ctx, task, w, target)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "database_recovery_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmDatabaseRecovery(ctx context.Context, task mq.Task, w work, object provider.Object) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockDatabaseScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkDatabaseRecovery(ctx, tx, &w); err != nil {
			return err
		}
		var duplicate bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_database_recovery_intents
			WHERE (registered_scope_id,provider_resource_id)=($1,$2))`, scopeID, object.ID).Scan(&duplicate)
		if err != nil {
			return err
		}
		if duplicate {
			return ErrConflict
		}
		registered, err := inventory.RegisterCreatedAsset(ctx, tx, w.actor, w.Account, w.Region, provider.DatabaseInstance, w.credential, object)
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRowContext(ctx, `UPDATE cmp_database_recovery_intents SET registered_scope_id=$2,provider_resource_id=$3
			WHERE operation_id=$1 AND (provider_resource_id=$3 OR ($4 AND provider_resource_id=''))
			  AND registered_scope_id IS NULL RETURNING operation_id`,
			w.ID, registered, object.ID, w.binding.Provider == provider.TencentCVM).Scan(&id); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "database_recovery_observed", "")
	})
	if errors.Is(err, ErrConflict) || errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return s.transition(ctx, task, w.Status, "needs_review", "database_recovery_requires_evidence", "")
	}
	return err
}
