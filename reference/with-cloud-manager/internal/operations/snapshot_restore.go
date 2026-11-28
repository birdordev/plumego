package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"slices"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const restoreSnapshot = "restore-snapshot"

type SnapshotRestoreSpecView struct {
	SnapshotID     string `json:"snapshot_id"`
	VolumeID       string `json:"source_volume_id"`
	VolumeRevision int64  `json:"source_volume_revision"`
	Class          string `json:"snapshot_class"`
	Name           string `json:"name"`
	Zone           string `json:"zone"`
	DiskType       string `json:"disk_type"`
	SizeGiB        int    `json:"size_gib"`
	Encrypted      bool   `json:"encrypted"`
}

type SnapshotRestore struct {
	SnapshotDeletion
	Name      string `json:"name"`
	Zone      string `json:"zone"`
	DiskType  string `json:"disk_type"`
	SizeGiB   int    `json:"size_gib"`
	Encrypted bool   `json:"encrypted"`
}

func (s Store) SubmitSnapshotRestore(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input SnapshotRestore) (Operation, error) {
	if revision < 1 || !operationKey.MatchString(key) || !input.valid() {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		SnapshotRestore
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(restoreSnapshot+"\x00"), data...))
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		if _, err := authorizeCreation(ctx, tx, actor, input.Account); err != nil {
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
		target, err := inventory.ResolveSnapshotMutation(ctx, tx, actor, input.Account, input.Region, input.AssetID, input.VolumeID, identity.Execute, true)
		if err != nil {
			return err
		}
		if target.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		spec := provider.SnapshotRestoreSpec{
			SnapshotTarget: provider.SnapshotTarget{ID: input.AssetID, VolumeID: input.VolumeID, Class: target.Class},
			Name:           input.Name, Zone: input.Zone, DiskType: input.DiskType, SizeGiB: input.SizeGiB, Encrypted: input.Encrypted,
		}
		if !spec.ValidFor(target.Provider, input.Region) {
			return ErrInvalid
		}
		var busy bool
		if err := tx.QueryRowContext(ctx, "SELECT "+snapshotWriteConflict+" OR "+volumeChangeConflict, target.VolumeScopeID, input.VolumeID, "").Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrConflict
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,account_id,region,action,status,resource_kind,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,'restore-snapshot','awaiting_approval','volume',$7,$8,$9,$10,$11) RETURNING `+operationFields,
			id, actor.Tenant, actor.Project, actor.Principal, input.Account, input.Region, key, digest[:],
			target.CredentialRevision, target.MembershipRevision, revision), &op)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_intents
			(operation_id,launch_token,name,parent_scope_id,parent_id,parent_revision,snapshot_class,zone,
			 restore_snapshot_scope_id,restore_snapshot_id,restore_disk_type,restore_size_gib,restore_encrypted)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			id, rand.Text(), spec.Name, target.VolumeScopeID, spec.VolumeID, revision, spec.Class, spec.Zone,
			target.ScopeID, spec.ID, spec.DiskType, spec.SizeGiB, spec.Encrypted); err != nil {
			return err
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func checkSnapshotRestore(ctx context.Context, tx *sql.Tx, w *work) error {
	var snapshotScope, volumeScope, volumeRevision int64
	var observer creationObserver
	spec := &w.restore
	if err := tx.QueryRowContext(ctx, `SELECT parent_scope_id,parent_id,parent_revision,snapshot_class,
		restore_snapshot_scope_id,restore_snapshot_id,name,zone,restore_disk_type,restore_size_gib,restore_encrypted,
		launch_token,provider_resource_id,observation_reviewer,observation_membership_revision,observation_credential_revision
		FROM cmp_network_intents WHERE operation_id=$1`, w.ID).
		Scan(&volumeScope, &spec.VolumeID, &volumeRevision, &spec.Class, &snapshotScope, &spec.ID, &spec.Name, &spec.Zone, &spec.DiskType,
			&spec.SizeGiB, &spec.Encrypted, &w.network.token, &w.network.resourceID, &observer.principal, &observer.membership, &observer.credential); err != nil {
		return err
	}
	beforeWrite := w.Status == "queued" || w.Status == "awaiting_approval"
	if _, err := checkCreationAuthority(ctx, tx, w, observer, beforeWrite); err != nil {
		return err
	}
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, w, true); err != nil {
			return err
		}
	}
	if !beforeWrite {
		if err := inventory.LockVolumeScopes(ctx, tx, w.actor, w.Account, w.Region); err != nil {
			return err
		}
	}
	actor, permission := w.actor, identity.Execute
	if !beforeWrite {
		permission = identity.View
		if observer.principal != "" {
			actor.Principal = observer.principal
		}
	}
	target, err := inventory.ResolveSnapshotMutation(ctx, tx, actor, w.Account, w.Region, spec.ID, spec.VolumeID, permission, beforeWrite)
	if err != nil {
		return err
	}
	if target.ScopeID != snapshotScope || target.VolumeScopeID != volumeScope || target.OwnershipRevision != volumeRevision ||
		target.Class != spec.Class || volumeRevision != w.ownership {
		return identity.ErrAccountStale
	}
	return nil
}

func (s Store) processSnapshotRestore(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	restorer, ok := client.(provider.SnapshotRestorer)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.Volume
	if w.Status == "queued" {
		send, err := restorer.PrepareSnapshotRestore(ctx, scope, w.restore, w.network.token)
		if err != nil || send == nil || ctx.Err() != nil {
			return s.rejectCreationPreflight(ctx, task, w, err)
		}
		return s.dispatchPreparedResource(ctx, task, w, func(ctx context.Context, _ work) (provider.LaunchReceipt, error) {
			return send(ctx)
		})
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	object, err := restorer.FindRestoredVolume(ctx, scope, w.restore, w.network.token, w.network.resourceID)
	if err == nil && object.ID != "" {
		if w.network.resourceID != "" && object.ID != w.network.resourceID {
			return false, s.transition(ctx, task, w.Status, "needs_review", "creation_receipt_mismatch", "")
		}
		expected := []provider.Link{{Kind: provider.Zone, ID: w.restore.Zone, Relation: "in_zone"},
			{Kind: provider.Snapshot, ID: w.restore.ID, Relation: "restored_from"}}
		if object.ID == w.restore.VolumeID || object.Name != w.restore.Name || !slices.Equal(object.Links, expected) {
			return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
		}
		return false, s.confirmCreation(ctx, task, w, object)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
	}
	return true, nil
}
