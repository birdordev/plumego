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

//go:embed 0091_workload_scale.sql
var workloadScaleSchema string

const scaleWorkload = "scale-workload"

type WorkloadScaleInput struct {
	Account  string `json:"account"`
	Region   string `json:"region"`
	AssetID  string `json:"asset_id"`
	Snapshot string `json:"snapshot"`
	provider.WorkloadScaleTarget
	Replicas int `json:"replicas"`
}

func (s Store) SubmitWorkloadScale(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input WorkloadScaleInput) (Operation, error) {
	if !input.WorkloadScaleTarget.Valid() || input.Snapshot == "" || input.Replicas < 1 || input.Replicas > provider.MaxWorkloadScaleReplicas {
		return Operation{}, ErrInvalid
	}
	body, err := json.Marshal(struct {
		WorkloadScaleInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(scaleWorkload+"\x00"), body...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.KubernetesCluster, input.Account, input.Region,
		input.AssetID, scaleWorkload, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			if !publicCloudProvider(target.Provider) {
				return nil, ErrInvalid
			}
			item, err := inventory.WorkloadScaleTarget(ctx, tx, target, input.AssetID, input.Snapshot, input.WorkloadScaleTarget)
			if err != nil {
				return nil, err
			}
			if item.Desired == nil || *item.Desired == input.Replicas {
				return nil, ErrInvalid
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_workload_scale_intents
					(operation_id,uid,namespace,kind,name,current_replicas,replicas) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
					id, item.UID, item.Namespace, item.Kind, item.Name, *item.Desired, input.Replicas)
				return err
			}, nil
		})
}

func checkWorkloadScale(ctx context.Context, tx *sql.Tx, w *work) (inventory.MutationTarget, error) {
	var scopeID int64
	var observer creationObserver
	change := &w.workloadScale
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.uid,i.namespace,i.kind,i.name,i.current_replicas,i.replicas,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_workload_scale_intents AS i JOIN cmp_instance_operations AS o USING(operation_id) WHERE i.operation_id=$1`, w.ID).
		Scan(&scopeID, &change.UID, &change.Namespace, &change.Kind, &change.Name, &change.Current, &change.Desired,
			&observer.principal, &observer.membership, &observer.credential)
	if err != nil {
		return inventory.MutationTarget{}, err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.KubernetesCluster)
	if err != nil {
		return inventory.MutationTarget{}, err
	}
	if !change.Valid() || !publicCloudProvider(target.Provider) {
		return inventory.MutationTarget{}, identity.ErrAccountStale
	}
	if beforeWrite {
		item, err := inventory.WorkloadScaleTarget(ctx, tx, target, w.AssetID, "", change.WorkloadScaleTarget)
		if err != nil {
			return inventory.MutationTarget{}, err
		}
		if item.Desired == nil || *item.Desired != change.Current {
			return inventory.MutationTarget{}, identity.ErrAccountStale
		}
	}
	return target, nil
}

type ConnectWorkloadScaler func(context.Context, identity.CloudAccountBinding, string, string) (provider.WorkloadScaler, error)

func (s Store) RunWorkerWithWorkloadScale(ctx context.Context, connect ConnectOperator, scale ConnectWorkloadScaler) error {
	s.connectWorkloadScaler = scale
	return s.RunWorker(ctx, connect)
}

func (s Store) processWorkloadScale(ctx context.Context, task mq.Task, w work) (bool, error) {
	if s.connectWorkloadScaler == nil {
		return false, s.endUnavailable(ctx, task, w)
	}
	client, err := s.connectWorkloadScaler(ctx, w.binding, w.Region, w.AssetID)
	if err != nil {
		if task.Attempts >= task.MaxAttempts || errors.Is(err, identity.ErrForbidden) || provider.IsFailure(err, provider.Unsupported) {
			return false, s.endUnavailable(ctx, task, w)
		}
		return true, nil
	}
	defer client.CloseIdleConnections()
	if w.Status == "queued" {
		apply, err := client.PrepareWorkloadScale(ctx, w.workloadScale)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "workload_scale")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	result, err := client.ObserveWorkloadScale(ctx, w.workloadScale)
	if err == nil && result == provider.ActionSucceeded {
		return false, s.confirmWorkloadScale(ctx, task, w)
	}
	if task.Attempts >= task.MaxAttempts || (err != nil && !remotePreflightRetryable(err)) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "workload_scale_requires_evidence", "")
	}
	return true, nil
}

func (s Store) confirmWorkloadScale(ctx context.Context, task mq.Task, w work) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		target, err := checkWorkloadScale(ctx, tx, &w)
		if err != nil {
			return err
		}
		if err := inventory.RecordWorkloadScale(ctx, tx, target, w.AssetID, w.workloadScale); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "workload_scale_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
