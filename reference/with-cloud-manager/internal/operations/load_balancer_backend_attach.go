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

//go:embed 0066_load_balancer_backend_attach.sql
var loadBalancerBackendAttachSchema string

//go:embed 0070_load_balancer_change_backfill.sql
var loadBalancerChangeBackfillSchema string

const attachLoadBalancerBackend = "attach-load-balancer-backend"

type LoadBalancerBackendAttachInput struct {
	Account          string `json:"account"`
	Region           string `json:"region"`
	AssetID          string `json:"asset_id"`
	TargetGroupID    string `json:"target_group_id"`
	InstanceID       string `json:"instance_id"`
	InstanceRevision int64  `json:"instance_revision"`
	Port             int    `json:"port"`
}

type LoadBalancerBackendAttachView struct {
	Product           string `json:"product"`
	TargetGroupID     string `json:"target_group_id"`
	InstanceID        string `json:"instance_id"`
	InstanceRevision  int64  `json:"instance_revision"`
	Port              int    `json:"port"`
	MemberKey         string `json:"member_key,omitempty"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

func (s Store) SubmitLoadBalancerBackendAttach(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input LoadBalancerBackendAttachInput) (Operation, error) {
	if input.InstanceRevision < 1 || input.Port < 1 || input.Port > 65535 || !operationKey.MatchString(input.InstanceID) ||
		!provider.ValidLoadBalancerTargetGroupID(input.TargetGroupID) {
		return Operation{}, ErrInvalid
	}
	body, err := json.Marshal(struct {
		LoadBalancerBackendAttachInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(attachLoadBalancerBackend+"\x00"), body...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.LoadBalancer, input.Account, input.Region, input.AssetID, attachLoadBalancerBackend, input.TargetGroupID, digest,
		func(ctx context.Context, tx *sql.Tx, actor identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			change := provider.LoadBalancerBackendAttach{ParentID: input.AssetID, GroupID: input.TargetGroupID, InstanceID: input.InstanceID, Port: input.Port}
			if err := inventory.LoadBalancerBackendAttachTarget(ctx, tx, target, &change, true); err != nil {
				return nil, err
			}
			if !change.ValidFor(target.Provider) {
				return nil, ErrInvalid
			}
			source, err := resolveBackendAttachSource(ctx, tx, actor, input.Account, input.Region, change.InstanceID, input.InstanceRevision, target, true)
			if err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_load_balancer_backend_attach_intents(operation_id,product,target_group_id,instance_scope_id,instance_id,instance_revision,port)
      VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, change.Product, change.GroupID, source.ScopeID, change.InstanceID, input.InstanceRevision, change.Port)
				return err
			}, nil
		})
}

func resolveBackendAttachSource(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string, revision int64,
	target inventory.MutationTarget, beforeWrite bool) (inventory.MutationTarget, error) {
	resolve := inventory.ResolveInstanceMutation
	if !beforeWrite {
		resolve = func(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (inventory.MutationTarget, error) {
			return inventory.ResolveNetworkObservation(ctx, tx, actor, account, region, id, provider.Instance)
		}
	}
	source, err := resolve(ctx, tx, actor, account, region, id)
	if err != nil {
		return source, err
	}
	if source.OwnershipRevision != revision || source.CredentialRevision != target.CredentialRevision || source.MembershipRevision != target.MembershipRevision {
		return source, identity.ErrAccountStale
	}
	if beforeWrite {
		var blocked bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_instance_operations WHERE (scope_id,external_id)=($1,$2)
   AND action='delete' AND status IN `+activeOperationStatuses+`)`, source.ScopeID, id).Scan(&blocked)
		if err == nil && blocked {
			err = ErrConflict
		}
	}
	return source, err
}

func checkLoadBalancerBackendAttach(ctx context.Context, tx *sql.Tx, w *work) (int64, error) {
	var scopeID, sourceScope, sourceRevision int64
	var product string
	var observer creationObserver
	target := &w.backendAttach
	target.ParentID = w.AssetID
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.product,i.target_group_id,i.instance_scope_id,i.instance_id,i.instance_revision,i.port,i.binding_key,
  i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
  FROM cmp_load_balancer_backend_attach_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
  WHERE operation_id=$1`, w.ID).Scan(&scopeID, &product, &target.GroupID, &sourceScope, &target.InstanceID, &sourceRevision, &target.Port, &w.backendAttachBinding,
		&observer.principal, &observer.membership, &observer.credential); err != nil {
		return 0, err
	}
	resource, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.LoadBalancer)
	if err != nil {
		return 0, err
	}
	if err := inventory.LoadBalancerBackendAttachTarget(ctx, tx, resource, target, beforeWrite); err != nil {
		return 0, err
	}
	if target.Product != product || !target.ValidFor(resource.Provider) || (!beforeWrite && !provider.ValidLoadBalancerBackendKey(w.backendAttachBinding)) {
		return 0, identity.ErrAccountStale
	}
	actor := w.actor
	if !beforeWrite && observer.principal != "" {
		actor.Principal = observer.principal
	}
	source, err := resolveBackendAttachSource(ctx, tx, actor, w.Account, w.Region, target.InstanceID, sourceRevision, resource, beforeWrite)
	if err != nil {
		return 0, err
	}
	if source.ScopeID != sourceScope {
		return 0, identity.ErrAccountStale
	}
	return resource.CredentialRevision, nil
}

func (s Store) processLoadBalancerBackendAttach(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.LoadBalancer
	attacher, ok := client.(provider.LoadBalancerBackendAttacher)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		plan, err := attacher.PrepareLoadBalancerBackendAttach(ctx, scope, w.backendAttach)
		if err == nil {
			if plan.Apply == nil || !provider.ValidLoadBalancerBackendKey(plan.BindingKey) {
				return s.dispatchSingleMutation(ctx, task, w, nil, &provider.Failure{Kind: provider.InvalidResponse}, "load_balancer_backend_attach")
			}
			if err := s.recordBackendBinding(ctx, task, w.ID, w.Action, plan.BindingKey); err != nil {
				if errors.Is(err, identity.ErrAccountStale) {
					return false, s.endUnavailable(ctx, task, w)
				}
				return false, err
			}
		}
		return s.dispatchSingleMutation(ctx, task, w, plan.Apply, err, "load_balancer_backend_attach")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	result, err := attacher.ObserveLoadBalancerBackendAttach(ctx, scope, w.backendAttach, w.backendAttachBinding, w.ProviderRequestID)
	if err != nil || result.Status != provider.ActionSucceeded || !provider.ValidLoadBalancerBackendKey(result.MemberKey) {
		if result.Status == provider.ActionFailed || result.Status == provider.ActionSucceeded || task.Attempts >= task.MaxAttempts ||
			provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.PreconditionFailed) {
			return false, s.transition(ctx, task, w.Status, "needs_review", "load_balancer_backend_attach_requires_evidence", "")
		}
		return true, nil
	}
	err = s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockLoadBalancerScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		credential, err := checkLoadBalancerBackendAttach(ctx, tx, &w)
		if err != nil {
			return err
		}
		if err := inventory.RecordLoadBalancerBackendAttach(ctx, tx, scopeID, credential, w.backendAttach, result.MemberKey); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_load_balancer_backend_attach_intents SET member_key=$2 WHERE operation_id=$1`, w.ID, result.MemberKey); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "load_balancer_backend_attachment_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return false, s.endUnavailable(ctx, task, w)
	}
	return false, err
}
