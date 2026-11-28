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

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const VolumeCreationMigration = "0158_volume_creation"
const createVolume = "create-volume"

func MigrateVolumeCreation(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, VolumeCreationMigration)
}

type VolumeCreationInput struct {
	Account string `json:"account"`
	Region  string `json:"region"`
	provider.VolumeSpec
}

func (s Store) SubmitVolumeCreation(ctx context.Context, token string, scope identity.Scope, key string, input VolumeCreationInput) (Operation, error) {
	if !operationKey.MatchString(key) || !operationKey.MatchString(input.Account) || !operationKey.MatchString(input.Region) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(input)
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(createVolume+"\x00"), data...))
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		access, err := authorizeCreation(ctx, tx, actor, input.Account)
		if err != nil {
			return err
		}
		if !input.ValidFor(access.Provider, input.Region) {
			return ErrInvalid
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
		if err := inventory.CheckVolumePlacement(ctx, tx, actor.Tenant, input.Account, input.Region, input.Zone, access.CredentialRevision); err != nil {
			return err
		}

		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,account_id,region,action,status,resource_kind,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,'create-volume','awaiting_approval','volume',$7,$8,$9,$10,0) RETURNING `+operationFields,
			id, actor.Tenant, actor.Project, actor.Principal, input.Account, input.Region, key, digest[:],
			access.CredentialRevision, access.MembershipRevision), &op)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_intents
			(operation_id,launch_token,name,zone,volume_create_type,volume_create_size_gib,volume_create_encrypted)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, rand.Text(), input.Name, input.Zone, input.DiskType, input.SizeGiB, input.Encrypted); err != nil {
			return err
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func checkVolumeCreation(ctx context.Context, tx *sql.Tx, w *work) error {
	spec := &w.volumeSpec
	var observer creationObserver
	if err := tx.QueryRowContext(ctx, `SELECT name,zone,volume_create_type,volume_create_size_gib,volume_create_encrypted,
		launch_token,provider_resource_id,observation_reviewer,observation_membership_revision,observation_credential_revision
		FROM cmp_network_intents WHERE operation_id=$1`, w.ID).
		Scan(&spec.Name, &spec.Zone, &spec.DiskType, &spec.SizeGiB, &spec.Encrypted, &w.network.token, &w.network.resourceID,
			&observer.principal, &observer.membership, &observer.credential); err != nil {
		return err
	}
	beforeWrite := w.Status == "queued" || w.Status == "awaiting_approval"
	access, err := checkCreationAuthority(ctx, tx, w, observer, beforeWrite)
	if err != nil {
		return err
	}
	if !spec.ValidFor(access.Provider, w.Region) {
		return ErrConflict
	}
	if !beforeWrite {
		return nil
	}
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, w, true); err != nil {
			return err
		}
	}
	return inventory.CheckVolumePlacement(ctx, tx, w.actor.Tenant, w.Account, w.Region, spec.Zone, access.CredentialRevision)
}

func (s Store) processVolumeCreation(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	creator, ok := client.(provider.VolumeCreator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.Volume
	if w.Status == "queued" {
		send, err := creator.PrepareVolumeCreation(ctx, scope, w.volumeSpec, w.network.token)
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
	object, err := creator.FindCreatedVolume(ctx, scope, w.volumeSpec, w.network.token, w.network.resourceID)
	if err == nil && object.ID != "" {
		volume := object.Volume
		expected := []provider.Link{{Kind: provider.Zone, ID: w.volumeSpec.Zone, Relation: "in_zone"}}
		if (w.network.resourceID != "" && object.ID != w.network.resourceID) || object.Name != w.volumeSpec.Name ||
			!slices.Equal(object.Links, expected) || volume == nil || volume.SizeGiB == nil ||
			*volume.SizeGiB != w.volumeSpec.SizeGiB || volume.StorageClass != w.volumeSpec.DiskType {
			return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
		}
		return false, s.confirmCreation(ctx, task, w, object)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
	}
	return true, nil
}
