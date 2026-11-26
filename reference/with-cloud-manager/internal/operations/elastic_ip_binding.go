package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"regexp"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0039_elastic_ip_binding.sql
var elasticIPBindingSchema string

const bindElasticIP = "bind-elastic-ip"
const unbindElasticIP = "unbind-elastic-ip"

var elasticIPPortID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

type ElasticIPBinding struct {
	ElasticIPRelease
	InstanceID       string `json:"instance_id,omitempty"`
	InstanceRevision int64  `json:"instance_revision,omitempty"`
	PortID           string `json:"port_id,omitempty"`
}

func isElasticIPBinding(action string) bool {
	return action == bindElasticIP || action == unbindElasticIP
}

func elasticIPStatuses(vendor string) (unbound, bound string) {
	switch vendor {
	case provider.TencentCVM:
		return "UNBIND", "BIND"
	case provider.AlibabaECS:
		return "Available", "InUse"
	case provider.HuaweiECS:
		return "DOWN", "ACTIVE"
	}
	return "", ""
}

func (s Store) SubmitElasticIPBinding(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input ElasticIPBinding) (Operation, error) {
	return s.submitElasticIPChange(ctx, token, scope, key, revision, bindElasticIP, input)
}

func (s Store) SubmitElasticIPUnbinding(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input ElasticIPBinding) (Operation, error) {
	return s.submitElasticIPChange(ctx, token, scope, key, revision, unbindElasticIP, input)
}

func resolveBindingInstance(ctx context.Context, tx *sql.Tx, actor identity.Scope, address inventory.MutationTarget,
	account, region string, binding provider.ElasticIPBinding, revision int64, beforeWrite bool, operationID string) (int64, error) {
	if binding.ID == binding.InstanceID || (address.Provider == provider.HuaweiECS) != (binding.PortID != "") {
		return 0, ErrInvalid
	}
	var target inventory.MutationTarget
	var err error
	if beforeWrite {
		target, err = inventory.ResolveInstanceMutation(ctx, tx, actor, account, region, binding.InstanceID)
	} else {
		target, err = inventory.ResolveNetworkObservation(ctx, tx, actor, account, region, binding.InstanceID, provider.Instance)
	}
	if err != nil {
		return 0, err
	}
	if target.OwnershipRevision != revision || target.AccountAccess != address.AccountAccess {
		return 0, identity.ErrAccountStale
	}
	if !beforeWrite {
		return target.ScopeID, nil
	}
	var observedCredential int64
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT credential_revision,
		EXISTS(SELECT 1 FROM cmp_instance_operations WHERE (scope_id,external_id)=($1,$2)
			AND action='delete' AND status IN `+activeOperationStatuses+`) OR
		EXISTS(SELECT 1 FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
			WHERE (i.parent_scope_id,i.parent_id)=($1,$2) AND i.binding_address IS NOT NULL
			AND o.operation_id<>$3 AND o.status IN `+activeOperationStatuses+`)
		FROM cmp_inventory_scopes WHERE scope_id=$1`, target.ScopeID, binding.InstanceID, operationID).Scan(&observedCredential, &busy)
	if err != nil {
		return 0, err
	}
	if observedCredential != target.CredentialRevision {
		return 0, identity.ErrAccountStale
	}
	if busy {
		return 0, ErrConflict
	}
	return target.ScopeID, nil
}

func (s Store) processElasticIPBinding(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	binder, ok := client.(provider.ElasticIPBinder)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := binder.PrepareElasticIPBinding(ctx, scope, w.elasticIP)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "elastic_ip_binding")
	}
	object, err := binder.ObserveElasticIPBinding(ctx, scope, w.elasticIP)
	if err == nil && !matchesElasticIPBinding(scope.Provider, w.elasticIP, object) {
		err = &provider.Failure{Kind: provider.InvalidResponse}
	}
	settled, settleErr := s.recordBindingEvidence(ctx, task, w, object, err == nil)
	if settled || settleErr != nil {
		return false, settleErr
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "elastic_ip_binding_requires_evidence", "")
	}
	return true, nil
}

func matchesElasticIPBinding(vendor string, binding provider.ElasticIPBinding, object provider.Object) bool {
	if object.ID != binding.ID || len(object.PublicAddresses) != 1 || object.PublicAddresses[0] != binding.Address {
		return false
	}
	unbound, bound := elasticIPStatuses(vendor)
	if !binding.Bind {
		return object.Status == unbound && len(object.Links) == 0
	}
	if object.Status != bound || len(object.Links) < 1 || len(object.Links) > 2 ||
		object.Links[0] != (provider.Link{Kind: provider.Instance, ID: binding.InstanceID, Relation: "bound_to"}) {
		return false
	}
	if len(object.Links) == 2 {
		link := object.Links[1]
		return vendor == provider.HuaweiECS && link.Kind == provider.Network && link.Relation == "in_network" && elasticIPPortID.MatchString(link.ID)
	}
	return vendor != provider.HuaweiECS
}

func (s Store) recordBindingEvidence(ctx context.Context, task mq.Task, w work, object provider.Object, confirmed bool) (bool, error) {
	var observations int16
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if err := inventory.LockElasticIPScope(ctx, tx, w.elasticIPScope); err != nil {
			return err
		}
		if err := checkElasticIPChange(ctx, tx, &w); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `UPDATE cmp_network_intents SET binding_observations=CASE WHEN $2::bool
			THEN LEAST(binding_observations+1,2) ELSE 0 END WHERE operation_id=$1 RETURNING binding_observations`,
			w.ID, confirmed).Scan(&observations); err != nil || observations < 2 {
			return err
		}
		if err := inventory.RecordElasticIPBinding(ctx, tx, w.elasticIPScope, object); err != nil {
			return err
		}
		reason := "elastic_ip_unbinding_confirmed"
		if w.elasticIP.Bind {
			reason = "elastic_ip_binding_confirmed"
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", reason, "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return true, s.endUnavailable(ctx, task, w)
	}
	return observations == 2, err
}
