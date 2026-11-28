package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const deleteImage = "delete-image"

type ImageDeletion struct {
	Account string `json:"account"`
	Region  string `json:"region"`
	AssetID string `json:"asset_id"`
}

type ImageDeletionView struct {
	Name              string `json:"name"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

func (s Store) SubmitImageDeletion(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input ImageDeletion) (Operation, error) {
	if revision < 1 || !operationKey.MatchString(key) || !operationKey.MatchString(input.Account) ||
		!operationKey.MatchString(input.Region) || !operationKey.MatchString(input.AssetID) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		ImageDeletion
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(deleteImage+"\x00"), data...))
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
		target, err := inventory.ResolveImageDeletion(ctx, tx, actor, input.Account, input.Region, input.AssetID, true)
		if err != nil {
			return err
		}
		if target.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		if !provider.ValidOwnedImage(target.Provider, input.AssetID) {
			return ErrInvalid
		}
		if err := checkImageDependencies(ctx, tx, actor.Tenant, input, target, ""); err != nil {
			return err
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,account_id,region,action,status,resource_kind,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'delete-image','awaiting_approval','image',$9,$10,$11,$12,$13)
			ON CONFLICT DO NOTHING RETURNING `+operationFields, id, actor.Tenant, actor.Project, actor.Principal, target.ScopeID,
			input.AssetID, input.Account, input.Region, key, digest[:], target.CredentialRevision, target.MembershipRevision, revision), &op)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cmp_network_intents (operation_id,launch_token,name)
			SELECT $1,$2,name FROM cmp_inventory_objects WHERE (scope_id,external_id)=($3,$4)`,
			id, rand.Text(), target.ScopeID, input.AssetID)
		if err != nil {
			return err
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func checkImageDependencies(ctx context.Context, tx *sql.Tx, tenant string, input ImageDeletion, target inventory.MutationTarget, operationID string) error {
	var current, blocked bool
	err := tx.QueryRowContext(ctx, `SELECT s.credential_revision=$6, EXISTS (
		SELECT 1 FROM cmp_image_copy_reservations WHERE (scope_id,image_id)=($1,$5)
		UNION ALL
		SELECT 1 FROM cmp_instance_operations WHERE (scope_id,external_id)=($1,$5)
		  AND operation_id<>$7 AND status IN `+activeOperationStatuses+`
		UNION ALL
		SELECT 1 FROM cmp_instance_operations AS op JOIN cmp_instance_launches AS launch USING (operation_id)
		WHERE (op.tenant_id,op.account_id,op.region,launch.image_id)=($2,$3,$4,$5)
		  AND op.action='create' AND op.status IN `+activeOperationStatuses+`
		UNION ALL
		SELECT 1 FROM cmp_inventory_scopes AS scope JOIN cmp_inventory_objects AS o USING (scope_id)
		WHERE (scope.tenant_id,scope.account_id,scope.region,scope.kind)=($2,$3,$4,'instance') AND o.removed_at IS NULL
		  AND o.links @> jsonb_build_array(jsonb_build_object('kind','image','id',$5::text,'relation','uses_image'))
		UNION ALL
		SELECT 1 FROM cmp_instance_catalog_items AS item JOIN cmp_instance_catalog_versions AS version
		  USING(tenant_id,project_id,template_id,version)
		WHERE (version.tenant_id,version.account_id,version.region,version.image_id)=($2,$3,$4,$5) AND item.enabled
	) FROM cmp_inventory_scopes AS s WHERE s.scope_id=$1`,
		target.ScopeID, tenant, input.Account, input.Region, input.AssetID, target.CredentialRevision, operationID).Scan(&current, &blocked)
	if err != nil {
		return err
	}
	if !current {
		return identity.ErrAccountStale
	}
	if blocked {
		return ErrConflict
	}
	return nil
}

func checkImageDeletion(ctx context.Context, tx *sql.Tx, w work) (int64, error) {
	var scopeID int64
	var observer creationObserver
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.observation_reviewer,i.observation_membership_revision,
		i.observation_credential_revision FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE operation_id=$1`, w.ID).Scan(&scopeID, &observer.principal, &observer.membership, &observer.credential); err != nil {
		return 0, err
	}
	beforeWrite := w.Status == "queued" || w.Status == "awaiting_approval"
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, &w, true); err != nil {
			return 0, err
		}
	}
	actor, memberRevision, credentialRevision := w.actor, w.membership, w.credential
	if !beforeWrite && observer.principal != "" {
		actor.Principal, memberRevision, credentialRevision = observer.principal, observer.membership, observer.credential
		if _, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Approve); err != nil {
			return 0, err
		}
	}
	target, err := inventory.ResolveImageDeletion(ctx, tx, actor, w.Account, w.Region, w.AssetID, beforeWrite)
	if err != nil {
		return 0, err
	}
	if target.ScopeID != scopeID || target.OwnershipRevision != w.ownership || target.CredentialRevision != credentialRevision ||
		target.MembershipRevision != memberRevision || !provider.ValidOwnedImage(target.Provider, w.AssetID) {
		return 0, identity.ErrAccountStale
	}
	if beforeWrite {
		input := ImageDeletion{Account: w.Account, Region: w.Region, AssetID: w.AssetID}
		return scopeID, checkImageDependencies(ctx, tx, actor.Tenant, input, target, w.ID)
	}
	return scopeID, nil
}

func (s Store) processImageDeletion(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	deleter, ok := client.(provider.ImageDeleter)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.Image
	if w.Status == "queued" {
		send, err := deleter.PrepareImageDeletion(ctx, scope, w.AssetID)
		return s.dispatchSingleMutation(ctx, task, w, send, err, "image_deletion")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	result, err := deleter.ObserveImageDeletion(ctx, scope, w.AssetID)
	var observations int16
	settleErr := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, targetErr := checkImageDeletion(ctx, tx, w)
		if targetErr != nil {
			return targetErr
		}
		observations, targetErr = recordAbsenceObservation(ctx, tx, w.ID, err == nil && result == provider.ActionSucceeded)
		if targetErr != nil || observations < 2 {
			return targetErr
		}
		if targetErr := inventory.MarkResourceDeleted(ctx, tx, scopeID, w.AssetID); targetErr != nil {
			return targetErr
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "image_absence_confirmed", "")
	})
	if errors.Is(settleErr, identity.ErrForbidden) || errors.Is(settleErr, identity.ErrAccountStale) {
		return false, s.endUnavailable(ctx, task, w)
	}
	if observations == 2 || settleErr != nil {
		return false, settleErr
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "image_deletion_requires_evidence", "")
	}
	return true, nil
}
