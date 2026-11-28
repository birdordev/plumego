package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const (
	renameLoadBalancer            = "rename-load-balancer"
	renameLoadBalancerListener    = "rename-load-balancer-listener"
	renameLoadBalancerTargetGroup = "rename-load-balancer-target-group"
)

func isLoadBalancerRename(action string) bool {
	return action == renameLoadBalancer || action == renameLoadBalancerListener || action == renameLoadBalancerTargetGroup
}

type LoadBalancerRenameInput struct {
	Account     string  `json:"account"`
	Region      string  `json:"region"`
	AssetID     string  `json:"asset_id"`
	CurrentName *string `json:"current_name"`
	Name        string  `json:"name"`
}

type LoadBalancerListenerRenameInput struct {
	LoadBalancerRenameInput
	ListenerID string `json:"listener_id"`
}

type LoadBalancerTargetGroupRenameInput struct {
	LoadBalancerRenameInput
	TargetGroupID string `json:"target_group_id"`
}

type LoadBalancerRenameView struct {
	TargetGroupID     string `json:"target_group_id,omitempty"`
	ListenerID        string `json:"listener_id,omitempty"`
	Product           string `json:"product"`
	CurrentName       string `json:"current_name"`
	Name              string `json:"name"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

func (s Store) SubmitLoadBalancerRename(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input LoadBalancerRenameInput) (Operation, error) {
	return s.submitLoadBalancerRename(ctx, token, scope, key, revision, input, provider.LoadBalancerRename{}, renameLoadBalancer)
}

func (s Store) SubmitLoadBalancerListenerRename(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input LoadBalancerListenerRenameInput) (Operation, error) {
	if !provider.ValidLoadBalancerListenerID(input.ListenerID) {
		return Operation{}, ErrInvalid
	}
	return s.submitLoadBalancerRename(ctx, token, scope, key, revision, input.LoadBalancerRenameInput, provider.LoadBalancerRename{ListenerID: input.ListenerID}, renameLoadBalancerListener)
}

func (s Store) SubmitLoadBalancerTargetGroupRename(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input LoadBalancerTargetGroupRenameInput) (Operation, error) {
	if !provider.ValidLoadBalancerTargetGroupID(input.TargetGroupID) {
		return Operation{}, ErrInvalid
	}
	return s.submitLoadBalancerRename(ctx, token, scope, key, revision, input.LoadBalancerRenameInput, provider.LoadBalancerRename{TargetGroupID: input.TargetGroupID}, renameLoadBalancerTargetGroup)
}

func (s Store) submitLoadBalancerRename(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input LoadBalancerRenameInput, rename provider.LoadBalancerRename, action string) (Operation, error) {
	if !provider.ValidLoadBalancerRenameName(input.Name) || input.CurrentName == nil || len(*input.CurrentName) > 1024 || *input.CurrentName == input.Name {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		LoadBalancerRenameInput
		Revision      int64
		ListenerID    string `json:"listener_id,omitempty"`
		TargetGroupID string `json:"target_group_id,omitempty"`
	}{input, revision, rename.ListenerID, rename.TargetGroupID})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(action+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.LoadBalancer, input.Account, input.Region, input.AssetID, action, rename.TargetGroupID, digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			rename.ID, rename.CurrentName, rename.Name = input.AssetID, *input.CurrentName, input.Name
			name, product, err := loadBalancerRenameInventory(ctx, tx, target, rename)
			if err != nil {
				return nil, err
			}
			rename.Product = product
			if !rename.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			if name != rename.CurrentName {
				return nil, identity.ErrAccountStale
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_load_balancer_rename_intents (operation_id,product,current_name,name,listener_id,target_group_id)
     VALUES ($1,$2,$3,$4,$5,$6)`, id, product, rename.CurrentName, rename.Name, rename.ListenerID, rename.TargetGroupID)
				return err
			}, nil
		})
}

func checkActiveTargetGroupMutation(ctx context.Context, tx *sql.Tx, scopeID int64, id string) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM (
    SELECT operation_id FROM cmp_load_balancer_rename_intents WHERE target_group_id<>'' AND target_group_id=$1
    UNION ALL SELECT operation_id FROM cmp_load_balancer_backend_weight_intents WHERE target_group_id=$1
    UNION ALL SELECT operation_id FROM cmp_load_balancer_backend_detach_intents WHERE target_group_id=$1
    UNION ALL SELECT operation_id FROM cmp_load_balancer_backend_attach_intents WHERE target_group_id=$1
  ) AS i JOIN cmp_instance_operations AS o USING(operation_id)
  WHERE o.scope_id=$2
    AND o.status IN ('awaiting_approval','queued','applying','observing','needs_review'))`, id, scopeID).Scan(&active)
	if err == nil && active {
		return ErrConflict
	}
	return err
}

func loadBalancerRenameInventory(ctx context.Context, tx *sql.Tx, target inventory.MutationTarget, rename provider.LoadBalancerRename) (string, string, error) {
	if rename.ListenerID != "" || rename.TargetGroupID != "" {
		return inventory.LoadBalancerComponentName(ctx, tx, target, rename)
	}
	var name, product string
	err := tx.QueryRowContext(ctx, `SELECT o.name,o.load_balancer->>'product'
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (o.scope_id,o.external_id)=($1,$2) AND s.credential_revision=$3 AND o.load_balancer->>'product' IS NOT NULL`,
		target.ScopeID, rename.ID, target.CredentialRevision).Scan(&name, &product)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", identity.ErrAccountStale
	}
	return name, product, err
}

func checkLoadBalancerRename(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID int64
	var observer creationObserver
	w.rename.ID = w.AssetID
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.product,i.current_name,i.name,i.listener_id,i.target_group_id,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_load_balancer_rename_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE operation_id=$1`, w.ID).Scan(&scopeID, &w.rename.Product, &w.rename.CurrentName, &w.rename.Name, &w.rename.ListenerID, &w.rename.TargetGroupID,
		&observer.principal, &observer.membership, &observer.credential); err != nil {
		return err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.LoadBalancer)
	if err != nil {
		return err
	}
	name, product, err := loadBalancerRenameInventory(ctx, tx, target, w.rename)
	if err != nil {
		return err
	}
	if product != w.rename.Product || !w.rename.ValidFor(target.Provider) ||
		(name != w.rename.CurrentName && (beforeWrite || name != w.rename.Name)) {
		return identity.ErrAccountStale
	}
	return nil
}

func (s Store) processLoadBalancerRename(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.LoadBalancer
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	renamer, ok := client.(provider.LoadBalancerRenamer)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := renamer.PrepareLoadBalancerRename(ctx, scope, w.rename)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "load_balancer_rename")
	}
	status, err := renamer.ObserveLoadBalancerRename(ctx, scope, w.rename, w.ProviderRequestID)
	if err == nil && status == provider.ActionSucceeded {
		return false, s.confirmLoadBalancerRename(ctx, task, w)
	}
	if status == provider.ActionFailed || task.Attempts >= task.MaxAttempts ||
		provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "load_balancer_rename_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmLoadBalancerRename(ctx context.Context, task mq.Task, w work) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockLoadBalancerScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkLoadBalancerRename(ctx, tx, &w); err != nil {
			return err
		}
		if w.rename.ListenerID != "" || w.rename.TargetGroupID != "" {
			if err := inventory.RecordLoadBalancerComponentName(ctx, tx, scopeID, w.rename); err != nil {
				return err
			}
			reason := "load_balancer_listener_name_observed"
			if w.rename.TargetGroupID != "" {
				reason = "load_balancer_target_group_name_observed"
			}
			return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", reason, "")
		}
		if err := inventory.RecordObservedAssetName(ctx, tx, scopeID, w.AssetID, w.rename.CurrentName, w.rename.Name); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "load_balancer_name_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
