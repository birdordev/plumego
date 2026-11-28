package operations

import (
	"context"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

const createSnapshot = "create-snapshot"

type SnapshotInput struct {
	Account string `json:"account"`
	Region  string `json:"region"`
	provider.SnapshotSpec
}

type SnapshotSpecView struct {
	provider.SnapshotSpec
	VolumeRevision      int64  `json:"volume_revision"`
	Class               string `json:"class,omitempty"`
	CanRetryObservation bool   `json:"can_retry_observation,omitempty"`
}

func (s Store) SubmitSnapshotCreation(ctx context.Context, token string, scope identity.Scope, key string, volumeRevision int64, input SnapshotInput) (Operation, error) {
	return s.submitResourceCreation(ctx, token, scope, key, volumeRevision, NetworkInput{
		Account: input.Account, Region: input.Region, Kind: provider.Snapshot,
		NetworkSpec: provider.NetworkSpec{Name: input.Name, ParentID: input.VolumeID},
	}, input.AllowAttached)
}

func (s Store) processSnapshot(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	creator, ok := client.(provider.SnapshotCreator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.Snapshot
	spec := provider.SnapshotSpec{Name: w.network.Name, VolumeID: w.network.ParentID, AllowAttached: w.network.snapshotAllowAttached}
	if w.Status == "queued" {
		send, err := creator.PrepareSnapshotCreation(ctx, scope, spec, w.network.token)
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
	object, err := creator.FindCreatedSnapshot(ctx, scope, spec, w.network.token, w.network.resourceID)
	if err == nil && object.ID != "" {
		if w.network.resourceID != "" && object.ID != w.network.resourceID {
			return false, s.transition(ctx, task, w.Status, "needs_review", "creation_receipt_mismatch", "")
		}
		if object.Name != spec.Name || !object.ValidSnapshot() || len(object.Links) == 0 ||
			object.Links[0] != networkParent(provider.Snapshot, spec.VolumeID) {
			return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
		}
		return false, s.confirmCreation(ctx, task, w, object)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
	}
	return true, nil
}
