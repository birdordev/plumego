package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

func (s Store) ReviewVolumeCleanup(ctx context.Context, token string, scope identity.Scope, site, placement, id string,
	revision int64, decision string) (VolumeCleanup, error) {
	if !placementKey.MatchString(placement) || !identifier.MatchString(id) || revision < 1 || (decision != "approve" && decision != "reject") {
		return VolumeCleanup{}, ErrInvalid
	}
	var result VolumeCleanup
	err := s.inProject(ctx, token, scope, site, identity.Approve, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		volume, err := lockRetainedVolume(ctx, tx, actor, site, placement)
		if err != nil {
			return err
		}
		job, err := readVolumeCleanup(ctx, tx, actor, site, placement, id)
		if err != nil {
			return hiddenMissing(err)
		}
		if job.Requester == actor.Principal {
			return identity.ErrForbidden
		}
		if job.Revision != revision {
			return ErrStale
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Approve), member.Revision); err != nil {
			return err
		}
		next := "rejected"
		if decision == "approve" {
			if job.Status != "pending" {
				return ErrConflict
			}
			if err := checkCleanupGeneration(volume, job); err != nil {
				return err
			}
			if err := volume.checkRetained(ctx, tx); err != nil {
				return err
			}
			if err := checkVMGrant(ctx, tx, actor, job.Requester, string(identity.Manage), job.requesterVersion); err != nil {
				return err
			}
			next = "approved"
		} else if job.Status != "pending" && job.Status != "approved" {
			return ErrConflict
		}
		result, err = scanVolumeCleanup(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_volume_cleanups
			SET status=$6,revision=revision+1,reviewer_id=$7,reviewer_revision=$8,reviewed_at=clock_timestamp(),
			    finished_at=CASE WHEN $6='rejected' THEN clock_timestamp() END
			WHERE `+volumeCleanupKey+` RETURNING `+volumeCleanupColumns,
			actor.Tenant, actor.Project, site, placement, id, next, actor.Principal, member.Revision))
		return err
	})
	if err != nil {
		return VolumeCleanup{}, err
	}
	return result, nil
}

type VolumeCleanupResolution struct {
	Observed       string `json:"observed"`
	Evidence       string `json:"evidence_sha256"`
	SecondEvidence string `json:"second_evidence_sha256,omitempty"`
}

func (resolution VolumeCleanupResolution) valid() bool {
	if !fenceEvidence.MatchString(resolution.Evidence) {
		return false
	}
	switch resolution.Observed {
	case "present":
		return resolution.SecondEvidence == ""
	case "absent":
		return fenceEvidence.MatchString(resolution.SecondEvidence) && resolution.SecondEvidence != resolution.Evidence
	default:
		return false
	}
}

func (s Store) ResolveFencedVolumeCleanup(ctx context.Context, token string, scope identity.Scope, site, placement, id string,
	revision int64, resolution VolumeCleanupResolution) (VolumeCleanup, error) {
	if !placementKey.MatchString(placement) || !identifier.MatchString(id) || revision < 1 || !resolution.valid() {
		return VolumeCleanup{}, ErrInvalid
	}
	var result VolumeCleanup
	err := s.inProject(ctx, token, scope, site, identity.Approve, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		volume, err := lockRetainedVolume(ctx, tx, actor, site, placement)
		if err != nil {
			return err
		}
		job, err := readVolumeCleanup(ctx, tx, actor, site, placement, id)
		if err != nil {
			return hiddenMissing(err)
		}
		if job.Requester == actor.Principal {
			return identity.ErrForbidden
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Approve), member.Revision); err != nil {
			return err
		}
		if job.ResolvedBy == actor.Principal && job.Revision-1 == revision && job.ResolutionSHA256 == resolution.Evidence &&
			job.SecondResolution == resolution.SecondEvidence {
			result = job
			return nil
		}
		if job.Revision != revision {
			return ErrStale
		}
		if job.Status != "dispatched" || volume.host.ID != job.Host || volume.host.FenceState != "fenced" ||
			volume.host.Generation <= job.HostGeneration {
			return ErrConflict
		}
		if err := volume.checkRetained(ctx, tx); err != nil {
			return err
		}
		status := "aborted"
		if resolution.Observed == "absent" {
			if err := finishVolumeCleanup(ctx, tx, placement); err != nil {
				return err
			}
			status = "completed"
		}
		result, err = scanVolumeCleanup(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_volume_cleanups
			SET status=$6,revision=revision+1,finished_at=clock_timestamp(),resolved_by=$7,resolution_sha256=$8,second_resolution_sha256=$9
			WHERE `+volumeCleanupKey+` RETURNING `+volumeCleanupColumns,
			actor.Tenant, actor.Project, site, placement, id, status, actor.Principal, resolution.Evidence, resolution.SecondEvidence))
		return err
	})
	if err != nil {
		return VolumeCleanup{}, err
	}
	return result, nil
}

func checkCleanupGeneration(volume retainedVolume, job VolumeCleanup) error {
	if volume.host.ID != job.Host || volume.host.Generation != job.HostGeneration || volume.host.FenceState != "" || volume.disk.ID != job.VM {
		return ErrStale
	}
	return nil
}
