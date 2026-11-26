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

//go:embed 0065_load_balancer_backend_detach.sql
var loadBalancerBackendDetachSchema string

const detachLoadBalancerBackend = "detach-load-balancer-backend"

type LoadBalancerBackendDetachInput struct {
	Account       string `json:"account"`
	Region        string `json:"region"`
	AssetID       string `json:"asset_id"`
	TargetGroupID string `json:"target_group_id"`
	MemberKey     string `json:"member_key"`
	ConfirmDetach bool   `json:"confirm_detach"`
}

type LoadBalancerBackendDetachView struct {
	Product           string `json:"product"`
	TargetGroupID     string `json:"target_group_id"`
	MemberKey         string `json:"member_key"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

func (s Store) SubmitLoadBalancerBackendDetach(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input LoadBalancerBackendDetachInput) (Operation, error) {
	if !input.ConfirmDetach || !provider.ValidLoadBalancerTargetGroupID(input.TargetGroupID) || !provider.ValidLoadBalancerBackendKey(input.MemberKey) {
		return Operation{}, ErrInvalid
	}
	body, err := json.Marshal(struct {
		LoadBalancerBackendDetachInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(detachLoadBalancerBackend+"\x00"), body...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.LoadBalancer, input.Account, input.Region, input.AssetID, detachLoadBalancerBackend, input.TargetGroupID, digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			change := provider.LoadBalancerBackendTarget{ParentID: input.AssetID, GroupID: input.TargetGroupID, MemberKey: input.MemberKey}
			if err := inventory.LoadBalancerBackendMutationTarget(ctx, tx, target, &change, 0, true); err != nil {
				return nil, err
			}
			if !change.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_load_balancer_backend_detach_intents(operation_id,product,target_group_id,member_key)
      VALUES ($1,$2,$3,$4)`, id, change.Product, change.GroupID, change.MemberKey)
				return err
			}, nil
		})
}

func checkLoadBalancerBackendDetach(ctx context.Context, tx *sql.Tx, w *work) (int64, error) {
	var scopeID int64
	var product string
	var observer creationObserver
	target := &w.backendDetach
	target.ParentID = w.AssetID
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.product,i.target_group_id,i.member_key,i.binding_key,
  i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
  FROM cmp_load_balancer_backend_detach_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
  WHERE operation_id=$1`, w.ID).Scan(&scopeID, &product, &target.GroupID, &target.MemberKey, &w.backendDetachBinding,
		&observer.principal, &observer.membership, &observer.credential); err != nil {
		return 0, err
	}
	resource, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.LoadBalancer)
	if err != nil {
		return 0, err
	}
	if err := inventory.LoadBalancerBackendMutationTarget(ctx, tx, resource, target, 0, beforeWrite); err != nil {
		return 0, err
	}
	if target.Product != product || !target.ValidFor(resource.Provider) || (!beforeWrite && !provider.ValidLoadBalancerBackendKey(w.backendDetachBinding)) {
		return 0, identity.ErrAccountStale
	}
	return resource.CredentialRevision, nil
}

func (s Store) recordBackendBinding(ctx context.Context, task mq.Task, opID, action, binding string) error {
	table := "cmp_load_balancer_backend_detach_intents"
	if action == attachLoadBalancerBackend {
		table = "cmp_load_balancer_backend_attach_intents"
	}
	return s.withLease(ctx, task, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE `+table+` AS i SET binding_key=$2
   FROM cmp_instance_operations AS o WHERE i.operation_id=$1 AND i.operation_id=o.operation_id
     AND o.status='queued' AND i.binding_key IN ('',$2)`, opID, binding)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count != 1 {
			return identity.ErrAccountStale
		}
		return err
	})
}

func (s Store) processLoadBalancerBackendDetach(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.LoadBalancer
	detacher, ok := client.(provider.LoadBalancerBackendDetacher)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		plan, err := detacher.PrepareLoadBalancerBackendDetach(ctx, scope, w.backendDetach)
		if err == nil {
			if plan.Apply == nil || !provider.ValidLoadBalancerBackendKey(plan.BindingKey) {
				return s.dispatchSingleMutation(ctx, task, w, nil, &provider.Failure{Kind: provider.InvalidResponse}, "load_balancer_backend_detach")
			}
			if err := s.recordBackendBinding(ctx, task, w.ID, w.Action, plan.BindingKey); err != nil {
				if errors.Is(err, identity.ErrAccountStale) {
					return false, s.endUnavailable(ctx, task, w)
				}
				return false, err
			}
		}
		return s.dispatchSingleMutation(ctx, task, w, plan.Apply, err, "load_balancer_backend_detach")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	state, observeErr := detacher.ObserveLoadBalancerBackendDetach(ctx, scope, w.backendDetach, w.backendDetachBinding, w.ProviderRequestID)
	var observations int16
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockLoadBalancerScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		credential, err := checkLoadBalancerBackendDetach(ctx, tx, &w)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `UPDATE cmp_load_balancer_backend_detach_intents
    SET absence_observations=CASE WHEN $2::bool THEN LEAST(absence_observations+1,2) ELSE 0 END
    WHERE operation_id=$1 RETURNING absence_observations`, w.ID, observeErr == nil && state == provider.ActionSucceeded).Scan(&observations); err != nil || observations < 2 {
			return err
		}
		if err := inventory.RecordLoadBalancerBackendDetach(ctx, tx, scopeID, credential, w.backendDetach); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "load_balancer_backend_absence_confirmed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return false, s.endUnavailable(ctx, task, w)
	}
	if err != nil || observations == 2 {
		return false, err
	}
	if state == provider.ActionFailed || task.Attempts >= task.MaxAttempts || provider.IsFailure(observeErr, provider.InvalidResponse) ||
		provider.IsFailure(observeErr, provider.Unauthorized) || provider.IsFailure(observeErr, provider.PreconditionFailed) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "load_balancer_backend_detach_requires_evidence", "")
	}
	return true, nil
}
