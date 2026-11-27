package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

type VolumeCleanupCommand struct {
	AgentIdentity
	Action     string `json:"action"`
	Revision   int64  `json:"revision,omitempty"`
	PoolSHA256 string `json:"pool_sha256,omitempty"`
}

type VolumeCleanupWork struct {
	Cleanup VolumeCleanup `json:"cleanup"`
	Disk    VM            `json:"disk"`
	Backup  BootImageSpec `json:"backup"`
}

func (command VolumeCleanupCommand) valid() bool {
	switch command.Action {
	case "inspect":
		return command.Revision == 0 && command.PoolSHA256 == ""
	case "arm", "verify", "present", "absent":
		return command.Revision > 0 && fenceEvidence.MatchString(command.PoolSHA256)
	default:
		return false
	}
}

func (s Store) ApplyVolumeCleanupCommand(ctx context.Context, token string, scope identity.Scope, site, host, placement, id string,
	command VolumeCleanupCommand) (VolumeCleanupWork, error) {
	if !placementKey.MatchString(placement) || !identifier.MatchString(id) || !command.valid() {
		return VolumeCleanupWork{}, ErrInvalid
	}
	var work VolumeCleanupWork
	err := s.withVMHost(ctx, token, scope, site, host, command.AgentIdentity, func(tx *sql.Tx, actor identity.Scope, source Host) error {
		job, err := readVolumeCleanup(ctx, tx, actor, site, placement, id)
		if err != nil {
			return hiddenMissing(err)
		}
		if job.Host != source.ID {
			return identity.ErrForbidden
		}
		if job.HostGeneration != source.Generation {
			return ErrStale
		}
		volume, err := lockRetainedVolume(ctx, tx, actor, site, placement)
		if err != nil {
			return err
		}
		if err := checkCleanupGeneration(volume, job); err != nil {
			return err
		}
		backup, err := volume.readBackup(ctx, tx, actor, job.Backup)
		if err != nil {
			return err
		}
		volume.disk.ImageSizeBytes = backup.SizeBytes
		work = VolumeCleanupWork{Cleanup: job, Disk: volume.disk, Backup: backup}
		if command.Action == "inspect" {
			return nil
		}
		if job.Revision != command.Revision {
			return ErrStale
		}
		if err := volume.checkRetained(ctx, tx); err != nil {
			return err
		}
		if err := applyVolumeCleanupCommand(ctx, tx, actor, site, job, command); err != nil {
			return err
		}
		work.Cleanup, err = readVolumeCleanup(ctx, tx, actor, site, placement, id)
		return err
	})
	if err != nil {
		return VolumeCleanupWork{}, err
	}
	return work, nil
}

func applyVolumeCleanupCommand(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, job VolumeCleanup,
	command VolumeCleanupCommand) error {
	args := []any{actor.Tenant, actor.Project, site, job.Placement, job.ID}
	switch command.Action {
	case "arm", "verify":
		if command.Action == "arm" && job.Status != "approved" {
			return ErrConflict
		}
		if command.Action == "verify" && (job.Status != "dispatched" || job.PoolSHA256 != command.PoolSHA256 || job.FirstAbsentAt != nil) {
			return ErrConflict
		}
		if err := checkVMGrant(ctx, tx, actor, job.Requester, string(identity.Manage), job.requesterVersion); err != nil {
			return err
		}
		if err := checkVMGrant(ctx, tx, actor, job.Reviewer, string(identity.Approve), job.reviewerVersion); err != nil {
			return err
		}
		if command.Action == "verify" {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_volume_cleanups
			SET status='dispatched',revision=revision+1,dispatched_at=clock_timestamp(),pool_sha256=$6
			WHERE `+volumeCleanupKey, append(args, command.PoolSHA256)...)
		return err
	case "present", "absent":
		if job.Status != "dispatched" || job.PoolSHA256 != command.PoolSHA256 {
			return ErrConflict
		}
		if command.Action == "present" {
			_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_volume_cleanups SET first_absent_at=NULL,revision=revision+1
				WHERE `+volumeCleanupKey+` AND first_absent_at IS NOT NULL`, args...)
			return err
		}
		if job.FirstAbsentAt == nil {
			_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_volume_cleanups SET first_absent_at=clock_timestamp(),revision=revision+1
				WHERE `+volumeCleanupKey, args...)
			return err
		}
		if err := finishVolumeCleanup(ctx, tx, job.Placement); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_volume_cleanups SET status='completed',finished_at=clock_timestamp(),revision=revision+1
			WHERE `+volumeCleanupKey, args...)
		return err
	default:
		return ErrInvalid
	}
}
