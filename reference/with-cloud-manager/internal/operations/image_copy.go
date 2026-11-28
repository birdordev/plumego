package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const copyImage = "copy-image"

type ImageCopy struct {
	Account            string `json:"account"`
	Region             string `json:"region"`
	DestinationAccount string `json:"destination_account"`
	DestinationRegion  string `json:"destination_region"`
	provider.ImageCopySpec
}

type ImageCopyView struct {
	ImageCopy
	SourceRevision      int64 `json:"source_revision"`
	CanRetryObservation bool  `json:"can_retry_observation"`
}

type imageCopyIntent struct {
	manualOwnership                         bool
	account, region                         string
	sourceCredential, observationCredential int64
	binding                                 identity.CloudAccountBinding
}

func (s Store) SubmitImageCopy(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input ImageCopy) (Operation, error) {
	if input.DestinationAccount == "" {
		input.DestinationAccount = input.Account
	}
	if revision < 1 || !operationKey.MatchString(key) || !operationKey.MatchString(input.Account) ||
		!operationKey.MatchString(input.Region) || !operationKey.MatchString(input.DestinationAccount) ||
		!operationKey.MatchString(input.DestinationRegion) || input.Region == input.DestinationRegion {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		ImageCopy
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte("copy-image\x00"), data...))
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		target, err := authorizeCreation(ctx, tx, actor, input.DestinationAccount)
		if err != nil {
			return err
		}
		source, err := authorizeCreation(ctx, tx, actor, input.Account)
		if err != nil {
			return err
		}
		if !input.ValidFor(source.Provider) || !copyAccountsMatch(source, target, input.Account == input.DestinationAccount) {
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
		image, err := inventory.ResolveImageDeletion(ctx, tx, actor, input.Account, input.Region, input.ImageID, true)
		if err != nil {
			return err
		}
		if image.OwnershipRevision != revision || image.MembershipRevision != target.MembershipRevision ||
			image.CredentialRevision != source.CredentialRevision {
			return identity.ErrAccountStale
		}
		if err := checkCopySourceAvailable(ctx, tx, image.ScopeID, input.ImageID); err != nil {
			return err
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,account_id,region,resource_kind,action,status,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES($1,$2,$3,$4,$5,$6,'image','copy-image','awaiting_approval',$7,$8,$9,$10,0)
			RETURNING `+operationFields, id, actor.Tenant, actor.Project, actor.Principal, input.DestinationAccount, input.DestinationRegion,
			key, digest[:], target.CredentialRevision, target.MembershipRevision), &op)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_intents
			(operation_id,launch_token,name,parent_scope_id,parent_id,parent_revision) VALUES ($1,$2,$3,$4,$5,$6)`,
			id, rand.Text(), input.Name, image.ScopeID, input.ImageID, revision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_image_copy_intents
			(operation_id,tenant_id,source_account,source_region,source_credential_revision,source_manual_ownership)
			SELECT $1,$2,$3,$4,$5,EXISTS (SELECT 1 FROM cmp_asset_ownership WHERE (scope_id,external_id)=($6,$7))`,
			id, actor.Tenant, input.Account, input.Region, source.CredentialRevision, image.ScopeID, input.ImageID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_image_copy_reservations(scope_id,image_id,operation_id) VALUES ($1,$2,$3)`,
			image.ScopeID, input.ImageID, id); err != nil {
			return err
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func copyAccountsMatch(source, target identity.AccountAccess, sameAccount bool) bool {
	if source.Provider != target.Provider || !publicCloudProvider(source.Provider) {
		return false
	}
	if source.Provider != provider.HuaweiECS {
		return sameAccount && source.RemoteIdentity == target.RemoteIdentity
	}
	sourceDomain, sourceProject, validSource := strings.Cut(source.RemoteIdentity, ":")
	targetDomain, targetProject, validTarget := strings.Cut(target.RemoteIdentity, ":")
	return !sameAccount && validSource && validTarget && sourceDomain == targetDomain && sourceProject != targetProject
}

func checkCopySourceAvailable(ctx context.Context, tx *sql.Tx, scopeID int64, id string) error {
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_image_copy_reservations
		WHERE (scope_id,image_id)=($1,$2)) OR EXISTS (SELECT 1 FROM cmp_instance_operations
		WHERE (scope_id,external_id)=($1,$2) AND action='delete-image' AND status IN `+activeOperationStatuses+`)`, scopeID, id).Scan(&active); err != nil {
		return err
	}
	if active {
		return ErrConflict
	}
	return nil
}

func checkImageCopy(ctx context.Context, tx *sql.Tx, w *work) error {
	i, n := &w.imageCopy, &w.network
	var reserved bool
	err := tx.QueryRowContext(ctx, `SELECT c.source_account,c.source_region,c.source_credential_revision,c.observation_credential_revision,c.source_manual_ownership,
		i.name,i.parent_scope_id,i.parent_id,i.parent_revision,i.launch_token,i.provider_resource_id,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision,
		EXISTS (SELECT 1 FROM cmp_image_copy_reservations AS r
			WHERE (r.scope_id,r.image_id,r.operation_id)=(i.parent_scope_id,i.parent_id,i.operation_id))
		FROM cmp_network_intents AS i JOIN cmp_image_copy_intents AS c USING(operation_id) WHERE i.operation_id=$1`, w.ID).
		Scan(&i.account, &i.region, &i.sourceCredential, &i.observationCredential, &i.manualOwnership,
			&n.Name, &n.parentScope, &n.ParentID, &n.parentRevision, &n.token, &n.resourceID,
			&n.observer.principal, &n.observer.membership, &n.observer.credential, &reserved)
	if err != nil {
		return err
	}
	if !reserved {
		return identity.ErrAccountStale
	}
	before := w.Status == "awaiting_approval" || w.Status == "queued"
	destination, err := checkCreationAuthority(ctx, tx, w, n.observer, before)
	if err != nil {
		return err
	}
	actor, sourceRevision := w.actor, i.sourceCredential
	if !before && n.observer.principal != "" {
		actor.Principal, sourceRevision = n.observer.principal, i.observationCredential
	}
	if err := lockOwnedCreationAccount(ctx, tx, actor, i.account); err != nil {
		return err
	}
	source, err := inventory.ResolveImageDeletion(ctx, tx, actor, i.account, i.region, n.ParentID, before)
	if err != nil {
		return err
	}
	expectedRevision := n.parentRevision
	if !before && n.observer.principal != "" && sourceRevision != i.sourceCredential {
		var manual bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM cmp_asset_ownership WHERE (scope_id,external_id)=($1,$2))`,
			source.ScopeID, n.ParentID).Scan(&manual); err != nil {
			return err
		}
		if manual != i.manualOwnership || sourceRevision < i.sourceCredential {
			return identity.ErrAccountStale
		}
		if !manual {
			// 继承归属的 ETag 包含凭据代次，标签映射的变化仍会导致不匹配。
			expectedRevision += sourceRevision - i.sourceCredential
		}
	}
	if source.ScopeID != n.parentScope || source.OwnershipRevision != expectedRevision || source.CredentialRevision != sourceRevision ||
		source.MembershipRevision != destination.MembershipRevision || !copyAccountsMatch(source.AccountAccess, destination, i.account == w.Account) {
		return identity.ErrAccountStale
	}
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, w, true); err != nil {
			return err
		}
	}
	i.binding, err = identity.NewStore(tx).CloudAccountBinding(ctx, w.actor.Tenant, i.account)
	return err
}

func (s Store) processImageCopy(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, connect ConnectOperator) (bool, error) {
	target, ok := client.(provider.ImageCopier)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	source := target
	if w.imageCopy.account != w.Account {
		connected, err := connect(ctx, w.imageCopy.binding)
		if err != nil {
			return s.unavailableCopyConnector(ctx, task, w, err)
		}
		source, ok = connected.(provider.ImageCopier)
		if !ok {
			return false, s.endUnavailable(ctx, task, w)
		}
	}
	from := provider.Scope{Provider: w.imageCopy.binding.Provider, RemoteIdentity: w.imageCopy.binding.RemoteIdentity, Region: w.imageCopy.region, Kind: provider.Image}
	to := provider.Scope{Provider: w.binding.Provider, RemoteIdentity: w.binding.RemoteIdentity, Region: w.Region, Kind: provider.Image}
	spec := provider.ImageCopySpec{Name: w.network.Name, ImageID: w.network.ParentID}
	if w.Status == "queued" {
		send, err := source.PrepareImageCopy(ctx, from, to, target, spec, w.network.token)
		if err != nil || send == nil || ctx.Err() != nil {
			return s.rejectCreationPreflight(ctx, task, w, err)
		}
		return s.dispatchPreparedResource(ctx, task, w, func(ctx context.Context, _ work) (provider.LaunchReceipt, error) {
			return send(ctx)
		})
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, w.Status, "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	image, err := source.FindCopiedImage(ctx, from, to, target, spec, w.network.token,
		provider.LaunchReceipt{RequestID: w.ProviderRequestID, ResourceID: w.network.resourceID})
	if err == nil && image.ID != "" {
		if !provider.ValidOwnedImage(to.Provider, image.ID) || image.ID == spec.ImageID || image.Name != spec.Name ||
			len(image.Links) != 0 || (w.network.resourceID != "" && image.ID != w.network.resourceID) {
			return false, s.transition(ctx, task, w.Status, "needs_review", "image_copy_requires_evidence", "")
		}
		if imageReady(to.Provider, image.Status) {
			return false, s.confirmCreation(ctx, task, w, image)
		}
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "image_copy_requires_evidence", "")
	}
	return true, nil
}

func (s Store) unavailableCopyConnector(ctx context.Context, task mq.Task, w work, err error) (bool, error) {
	if task.Attempts >= task.MaxAttempts || errors.Is(err, identity.ErrForbidden) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.endUnavailable(ctx, task, w)
	}
	return true, nil
}

func imageCopyReviewTarget(ctx context.Context, tx *sql.Tx, actor identity.Scope, id string) (identity.AccountAccess, string, error) {
	var account, resourceID string
	err := tx.QueryRowContext(ctx, `SELECT c.source_account,i.provider_resource_id FROM cmp_image_copy_intents AS c
		JOIN cmp_network_intents AS i USING(operation_id) WHERE c.operation_id=$1`, id).Scan(&account, &resourceID)
	if err != nil {
		return identity.AccountAccess{}, "", err
	}
	access, err := authorizeLaunchObservation(ctx, tx, actor, account)
	return access, resourceID, err
}
