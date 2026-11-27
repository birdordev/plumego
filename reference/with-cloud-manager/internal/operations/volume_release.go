package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0159_volume_release.sql
var volumeReleaseSchema string

const VolumeReleaseMigration = "0159_volume_release"
const releaseVolume = "release-volume"

func MigrateVolumeRelease(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, VolumeReleaseMigration, volumeReleaseSchema)
}

const volumeChangeConflict = `EXISTS (
	SELECT 1 FROM cmp_instance_operations
	WHERE (scope_id,external_id)=($1,$2) AND action IN ('resize-volume','release-volume')
	  AND operation_id<>$3 AND status IN ` + activeOperationStatuses + `)`

type VolumeReleaseInput struct {
	Account        string `json:"account"`
	Region         string `json:"region"`
	AssetID        string `json:"asset_id"`
	SizeGiB        int    `json:"size_gib"`
	ConfirmAssetID string `json:"confirm_asset_id"`
}

type VolumeReleaseView struct {
	Name              string `json:"name"`
	SizeGiB           int    `json:"size_gib"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

type VolumeReleasePreview struct {
	VolumeReleaseView
	AssetID      string `json:"asset_id"`
	DataDisk     bool   `json:"data_disk"`
	Attached     bool   `json:"attached_or_unavailable"`
	Snapshots    bool   `json:"known_snapshots"`
	ActiveChange bool   `json:"active_change"`
}

func (s Store) PreviewVolumeRelease(ctx context.Context, token string, scope identity.Scope, account, region, id string) (VolumeReleasePreview, error) {
	if !operationKey.MatchString(account) || !operationKey.MatchString(region) || !operationKey.MatchString(id) {
		return VolumeReleasePreview{}, ErrInvalid
	}
	var preview VolumeReleasePreview
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		target, err := inventory.ResolveMutationPreview(ctx, tx, actor, account, region, id, provider.Volume)
		if err != nil {
			return err
		}
		preview, err = volumeReleasePreview(ctx, tx, target, id, "")
		return err
	})
	return preview, err
}

func volumeReleasePreview(ctx context.Context, tx *sql.Tx, target inventory.MutationTarget, id, exclude string) (VolumeReleasePreview, error) {
	state, err := inventory.ReadVolumeReleaseState(ctx, tx, target, id)
	if err != nil {
		return VolumeReleasePreview{}, err
	}
	if !(provider.VolumeRelease{ID: id, SizeGiB: state.SizeGiB}).ValidFor(target.Provider) {
		return VolumeReleasePreview{}, ErrInvalid
	}
	preview := VolumeReleasePreview{AssetID: id,
		VolumeReleaseView: VolumeReleaseView{Name: state.Name, SizeGiB: state.SizeGiB, OwnershipRevision: target.OwnershipRevision},
		DataDisk:          state.DataDisk, Attached: state.Attached, Snapshots: state.Snapshots}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM cmp_instance_operations WHERE (volume_scope_id,volume_id)=($1,$2) AND status IN `+
		activeOperationStatuses+`) OR `+snapshotWriteConflict+" OR "+retirementDiskReserved+" OR "+volumeChangeConflict,
		target.ScopeID, id, exclude).Scan(&preview.ActiveChange)
	return preview, err
}

func validateVolumeRelease(ctx context.Context, tx *sql.Tx, target inventory.MutationTarget, change provider.VolumeRelease, exclude string) (VolumeReleasePreview, error) {
	preview, err := volumeReleasePreview(ctx, tx, target, change.ID, exclude)
	if err != nil {
		return preview, err
	}
	if preview.SizeGiB != change.SizeGiB {
		return preview, identity.ErrAccountStale
	}
	if !preview.DataDisk || preview.ActiveChange || preview.Attached || preview.Snapshots {
		return preview, ErrConflict
	}
	return preview, nil
}

func (s Store) SubmitVolumeRelease(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input VolumeReleaseInput) (Operation, error) {
	if input.SizeGiB <= 0 || input.AssetID != input.ConfirmAssetID {
		return Operation{}, ErrInvalid
	}
	body, err := json.Marshal(struct {
		VolumeReleaseInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(releaseVolume+"\x00"), body...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.Volume,
		input.Account, input.Region, input.AssetID, releaseVolume, "", digest,
		func(ctx context.Context, tx *sql.Tx, _ identity.Scope, target inventory.MutationTarget) (func(string) error, error) {
			change := provider.VolumeRelease{ID: input.AssetID, SizeGiB: input.SizeGiB}
			preview, err := validateVolumeRelease(ctx, tx, target, change, "")
			if err != nil {
				return nil, err
			}
			return func(id string) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_intents(operation_id,launch_token,name,volume_release_size_gib)
					VALUES ($1,$2,$3,$4)`, id, rand.Text(), preview.Name, change.SizeGiB)
				return err
			}, nil
		})
}

func checkVolumeRelease(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID int64
	var observer creationObserver
	w.volumeRelease.ID = w.AssetID
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.volume_release_size_gib,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`,
		w.ID).Scan(&scopeID, &w.volumeRelease.SizeGiB, &observer.principal, &observer.membership, &observer.credential); err != nil {
		return err
	}
	target, beforeWrite, err := resolveApprovedAssetChange(ctx, tx, w, scopeID, observer, provider.Volume)
	if err != nil {
		return err
	}
	if !w.volumeRelease.ValidFor(target.Provider) {
		return identity.ErrAccountStale
	}
	if !beforeWrite {
		return nil
	}
	_, err = validateVolumeRelease(ctx, tx, target, w.volumeRelease, w.ID)
	return err
}

func (s Store) processVolumeRelease(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.Volume
	releaser, ok := client.(provider.VolumeReleaser)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := releaser.PrepareVolumeRelease(ctx, scope, w.volumeRelease)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "volume_release")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	status, observationErr := releaser.ObserveVolumeRelease(ctx, scope, w.volumeRelease, w.ProviderRequestID)
	var observations int16
	settleErr := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockVolumeScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkVolumeRelease(ctx, tx, &w); err != nil {
			return err
		}
		observations, err = recordAbsenceObservation(ctx, tx, w.ID, observationErr == nil && status == provider.ActionSucceeded)
		if err != nil || observations < 2 {
			return err
		}
		if err := inventory.MarkVolumeReleased(ctx, tx, scopeID, w.AssetID); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "volume_inventory_absence_confirmed", "")
	})
	if errors.Is(settleErr, identity.ErrForbidden) || errors.Is(settleErr, identity.ErrAccountStale) {
		return false, s.endUnavailable(ctx, task, w)
	}
	if observations == 2 || settleErr != nil {
		return false, settleErr
	}
	if status == provider.ActionFailed || task.Attempts >= task.MaxAttempts || provider.IsFailure(observationErr, provider.InvalidResponse) || provider.IsFailure(observationErr, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "volume_release_requires_evidence", "")
	}
	return true, nil
}
