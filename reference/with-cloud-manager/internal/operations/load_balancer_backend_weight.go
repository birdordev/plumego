package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0064_load_balancer_backend_weight.sql
var loadBalancerBackendWeightSchema string

const setLoadBalancerBackendWeight = "set-load-balancer-backend-weight"

func isLoadBalancerMutation(action string) bool {
	return isLoadBalancerRename(action) || action == setLoadBalancerBackendWeight || action == detachLoadBalancerBackend || action == attachLoadBalancerBackend
}

type LoadBalancerBackendWeightInput struct {
	Account       string `json:"account"`
	Region        string `json:"region"`
	AssetID       string `json:"asset_id"`
	TargetGroupID string `json:"target_group_id"`
	MemberKey     string `json:"member_key"`
	CurrentWeight *int   `json:"current_weight"`
	Weight        *int   `json:"weight"`
}

type LoadBalancerBackendWeightView struct {
	Product           string `json:"product"`
	TargetGroupID     string `json:"target_group_id"`
	MemberKey         string `json:"member_key"`
	CurrentWeight     int    `json:"current_weight"`
	Weight            int    `json:"weight"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

func (s Store) SubmitLoadBalancerBackendWeight(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input LoadBalancerBackendWeightInput) (Operation, error) {
	if input.CurrentWeight == nil || input.Weight == nil || *input.CurrentWeight < 0 || *input.CurrentWeight > 100 ||
		*input.Weight < 0 || *input.Weight > 100 || *input.CurrentWeight == *input.Weight ||
		!provider.ValidLoadBalancerTargetGroupID(input.TargetGroupID) || !provider.ValidLoadBalancerBackendKey(input.MemberKey) {
		return Operation{}, ErrInvalid
	}
	request, err := json.Marshal(struct {
		LoadBalancerBackendWeightInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(setLoadBalancerBackendWeight+"\x00"), request...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.LoadBalancer, input.Account, input.Region, input.AssetID, setLoadBalancerBackendWeight, input.TargetGroupID, digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			change := provider.LoadBalancerBackendWeight{LoadBalancerBackendTarget: provider.LoadBalancerBackendTarget{ParentID: input.AssetID, GroupID: input.TargetGroupID, MemberKey: input.MemberKey}, Current: *input.CurrentWeight, Desired: *input.Weight}
			if err := inventory.LoadBalancerBackendMutationTarget(ctx, tx, target, &change.LoadBalancerBackendTarget, change.Current, true); err != nil {
				return nil, err
			}
			if !change.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_load_balancer_backend_weight_intents(operation_id,product,target_group_id,member_key,current_weight,weight)
     VALUES ($1,$2,$3,$4,$5,$6)`, id, change.Product, change.GroupID, change.MemberKey, change.Current, change.Desired)
				return err
			}, nil
		})
}

func checkLoadBalancerBackendWeight(ctx context.Context, tx *sql.Tx, w *work) (int64, error) {
	var scopeID int64
	var product string
	var observer creationObserver
	change := &w.backendWeight
	change.ParentID = w.AssetID
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.product,i.target_group_id,i.member_key,i.current_weight,i.weight,
  i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
  FROM cmp_load_balancer_backend_weight_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
  WHERE operation_id=$1`, w.ID).Scan(&scopeID, &product, &change.GroupID, &change.MemberKey, &change.Current, &change.Desired,
		&observer.principal, &observer.membership, &observer.credential); err != nil {
		return 0, err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.LoadBalancer)
	if err != nil {
		return 0, err
	}
	if err := inventory.LoadBalancerBackendMutationTarget(ctx, tx, target, &change.LoadBalancerBackendTarget, change.Current, beforeWrite); err != nil {
		return 0, err
	}
	if change.Product != product || !change.ValidFor(target.Provider) {
		return 0, identity.ErrAccountStale
	}
	return target.CredentialRevision, nil
}

func (s Store) processLoadBalancerBackendWeight(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.LoadBalancer
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	operator, ok := client.(provider.LoadBalancerBackendWeightOperator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := operator.PrepareLoadBalancerBackendWeight(ctx, scope, w.backendWeight)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "load_balancer_backend_weight")
	}
	status, err := operator.ObserveLoadBalancerBackendWeight(ctx, scope, w.backendWeight, w.ProviderRequestID)
	if err != nil || status != provider.ActionSucceeded {
		if status == provider.ActionFailed || task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) ||
			provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.PreconditionFailed) {
			return false, s.transition(ctx, task, w.Status, "needs_review", "load_balancer_backend_weight_requires_evidence", "")
		}
		return true, nil
	}
	err = s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockLoadBalancerScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		credential, err := checkLoadBalancerBackendWeight(ctx, tx, &w)
		if err != nil {
			return err
		}
		if err := inventory.RecordLoadBalancerBackendWeight(ctx, tx, scopeID, credential, w.backendWeight); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "load_balancer_backend_weight_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return false, s.endUnavailable(ctx, task, w)
	}
	return false, err
}
