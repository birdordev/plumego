package operations

import (
	"context"
	"database/sql"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

const createImage = "create-image"

// 包含首次发送以及最多约 30 分钟的常规观测间隔。
const imageCreationAttempts = 121

type ImageCreation struct {
	Account string `json:"account"`
	Region  string `json:"region"`
	provider.ImageSpec
}

type ImageCreationView struct {
	provider.ImageSpec
	InstanceRevision int64 `json:"instance_revision"`
}

func (s Store) SubmitImageCreation(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input ImageCreation) (Operation, error) {
	return s.submitResourceCreation(ctx, token, scope, key, revision, NetworkInput{
		Account: input.Account, Region: input.Region, Kind: provider.Image,
		NetworkSpec: provider.NetworkSpec{Name: input.Name, ParentID: input.InstanceID},
	}, false)
}

func (s Store) processImageCreation(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	creator, ok := client.(provider.ImageCreator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.Image
	spec := provider.ImageSpec{Name: w.network.Name, InstanceID: w.network.ParentID}
	if w.Status == "queued" {
		send, err := creator.PrepareImageCreation(ctx, scope, spec, w.network.token)
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
	image, err := creator.FindCreatedImage(ctx, scope, spec, w.network.token,
		provider.LaunchReceipt{ResourceID: w.network.resourceID, RequestID: w.ProviderRequestID})
	if err == nil && image.ID != "" {
		if !provider.ValidOwnedImage(scope.Provider, image.ID) || image.Name != spec.Name ||
			(w.network.resourceID != "" && image.ID != w.network.resourceID) || !matchesNetworkLinks(w, image.Links) {
			return false, s.transition(ctx, task, w.Status, "needs_review", "image_creation_requires_evidence", "")
		}
		if imageReady(scope.Provider, image.Status) {
			return false, s.confirmCreation(ctx, task, w, image)
		}
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "image_creation_requires_evidence", "")
	}
	return true, nil
}

func imageReady(name, status string) bool {
	return (name == provider.TencentCVM && status == "NORMAL") ||
		(name == provider.AlibabaECS && status == "Available") ||
		(name == provider.HuaweiECS && status == "active")
}

func releaseImageSource(ctx context.Context, tx *sql.Tx, action, operationID string) error {
	query := "DELETE FROM cmp_instance_image_reservations WHERE operation_id=$1"
	if action == copyImage {
		query = "DELETE FROM cmp_image_copy_reservations WHERE operation_id=$1"
	}
	_, err := tx.ExecContext(ctx, query, operationID)
	return err
}
