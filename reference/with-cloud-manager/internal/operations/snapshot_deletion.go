package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0043_snapshot_deletion.sql
var snapshotDeletionSchema string

const deleteSnapshot = "delete-snapshot"

const snapshotWriteConflict = `EXISTS (
	SELECT 1 FROM cmp_network_intents AS intent JOIN cmp_instance_operations AS op USING(operation_id)
	WHERE (intent.parent_scope_id,intent.parent_id)=($1,$2) AND op.action IN ('create-snapshot','delete-snapshot','restore-snapshot','rollback-snapshot')
	  AND op.operation_id<>$3 AND op.status IN ` + activeOperationStatuses + `)`

type SnapshotDeletion struct {
	Account  string `json:"account"`
	Region   string `json:"region"`
	AssetID  string `json:"asset_id"`
	VolumeID string `json:"volume_id"`
}

func (input SnapshotDeletion) valid() bool {
	return operationKey.MatchString(input.Account) && operationKey.MatchString(input.Region) &&
		operationKey.MatchString(input.AssetID) && operationKey.MatchString(input.VolumeID)
}

func (s Store) SubmitSnapshotDeletion(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input SnapshotDeletion) (Operation, error) {
	return s.submitSnapshotMutation(ctx, token, scope, key, revision, input, deleteSnapshot)
}

func (s Store) submitSnapshotMutation(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input SnapshotDeletion, action string) (Operation, error) {
	if revision < 1 || !operationKey.MatchString(key) || !input.valid() {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		SnapshotDeletion
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(action+"\x00"), data...))
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		if _, err := identity.NewStore(tx).AuthorizeAccount(ctx, actor, input.Account, identity.Execute); err != nil {
			return err
		}
		if err := lockBudget(ctx, tx, actor); err != nil {
			return err
		}
		if found, err := findReplay(ctx, tx, actor, key, digest, &op); found || err != nil {
			return err
		}
		if err := checkBudget(ctx, tx, actor); err != nil {
			return err
		}
		target, err := resolveSnapshotWrite(ctx, tx, actor, input.Account, input.Region, input.AssetID, input.VolumeID, action)
		if err != nil {
			return err
		}
		if target.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		spec := provider.SnapshotTarget{ID: input.AssetID, VolumeID: input.VolumeID, Class: target.Class}
		if !spec.ValidFor(target.Provider) || (action == rollbackSnapshot && !spec.ValidForRollback(target.Provider)) {
			return ErrInvalid
		}
		var busy bool
		if err := tx.QueryRowContext(ctx, "SELECT "+snapshotWriteConflict, target.VolumeScopeID, input.VolumeID, "").Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrConflict
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,account_id,region,action,status,resource_kind,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'awaiting_approval','snapshot',$10,$11,$12,$13,$14)
			ON CONFLICT DO NOTHING RETURNING `+operationFields, id, actor.Tenant, actor.Project, actor.Principal, target.ScopeID,
			input.AssetID, input.Account, input.Region, action, key, digest[:], target.CredentialRevision, target.MembershipRevision, revision), &op)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_intents
			(operation_id,launch_token,name,parent_scope_id,parent_id,parent_revision,snapshot_class)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, rand.Text(), target.Name, target.VolumeScopeID, input.VolumeID, revision, target.Class); err != nil {
			return err
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func checkSnapshotMutation(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID, volumeScope, volumeRevision int64
	var observer creationObserver
	w.snapshot.ID = w.AssetID
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.parent_scope_id,i.parent_id,i.parent_revision,i.snapshot_class,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`, w.ID).
		Scan(&scopeID, &volumeScope, &w.snapshot.VolumeID, &volumeRevision, &w.snapshot.Class, &observer.principal, &observer.membership, &observer.credential); err != nil {
		return err
	}
	beforeWrite := w.Status == "queued" || w.Status == "awaiting_approval"
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, w, true); err != nil {
			return err
		}
	}
	actor, memberRevision, credentialRevision := w.actor, w.membership, w.credential
	if !beforeWrite {
		if observer.principal != "" {
			actor.Principal, memberRevision, credentialRevision = observer.principal, observer.membership, observer.credential
			if _, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Approve); err != nil {
				return err
			}
		}
	}
	var target inventory.SnapshotMutationTarget
	var err error
	if beforeWrite {
		target, err = resolveSnapshotWrite(ctx, tx, actor, w.Account, w.Region, w.snapshot.ID, w.snapshot.VolumeID, w.Action)
	} else {
		target, err = inventory.ResolveSnapshotMutation(ctx, tx, actor, w.Account, w.Region, w.snapshot.ID, w.snapshot.VolumeID, identity.View, false)
	}
	if err != nil {
		return err
	}
	if target.ScopeID != scopeID || target.VolumeScopeID != volumeScope || target.OwnershipRevision != volumeRevision || volumeRevision != w.ownership ||
		target.CredentialRevision != credentialRevision || target.MembershipRevision != memberRevision || target.Class != w.snapshot.Class {
		return identity.ErrAccountStale
	}
	w.snapshotScope = scopeID
	return nil
}

func (s Store) processSnapshotDeletion(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	deleter, ok := client.(provider.SnapshotDeleter)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.Snapshot
	if w.Status == "queued" {
		send, err := deleter.PrepareSnapshotDeletion(ctx, scope, w.snapshot)
		return s.dispatchSingleMutation(ctx, task, w, send, err, "snapshot_deletion")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	result, err := deleter.ObserveSnapshotDeletion(ctx, scope, w.snapshot)
	settled, settleErr := s.recordSnapshotAbsence(ctx, task, w, err == nil && result == provider.ActionSucceeded)
	if settled || settleErr != nil {
		return false, settleErr
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "snapshot_deletion_requires_evidence", "")
	}
	return true, nil
}

func (s Store) recordSnapshotAbsence(ctx context.Context, task mq.Task, w work, absent bool) (bool, error) {
	var observations int16
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if err := checkSnapshotMutation(ctx, tx, &w); err != nil {
			return err
		}
		var err error
		observations, err = recordAbsenceObservation(ctx, tx, w.ID, absent)
		if err != nil || observations < 2 {
			return err
		}
		if err := inventory.MarkResourceDeleted(ctx, tx, w.snapshotScope, w.AssetID); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "snapshot_absence_confirmed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return true, s.endUnavailable(ctx, task, w)
	}
	return observations == 2, err
}
