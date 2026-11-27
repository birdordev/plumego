package kvm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
)

type VolumeCleanupInput struct {
	ID       string `json:"cleanup_id"`
	Backup   string `json:"backup_image_id"`
	Evidence string `json:"evidence_sha256"`
}

type VolumeCleanup struct {
	VolumeCleanupInput
	Placement        string     `json:"placement_id"`
	VM               string     `json:"vm_id"`
	Host             string     `json:"host"`
	HostGeneration   int64      `json:"host_generation"`
	HostRevision     int64      `json:"host_revision"`
	Revision         int64      `json:"revision"`
	Status           string     `json:"status"`
	Requester        string     `json:"requester"`
	Reviewer         string     `json:"reviewer,omitempty"`
	RequestedAt      time.Time  `json:"requested_at"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	DispatchedAt     *time.Time `json:"dispatched_at,omitempty"`
	PoolSHA256       string     `json:"pool_sha256,omitempty"`
	FirstAbsentAt    *time.Time `json:"first_absent_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	ResolvedBy       string     `json:"resolved_by,omitempty"`
	ResolutionSHA256 string     `json:"resolution_sha256,omitempty"`
	SecondResolution string     `json:"second_resolution_sha256,omitempty"`
	requesterVersion int64
	reviewerVersion  int64
}

const volumeCleanupKey = `(tenant_id,project_id,site_id,placement_id,cleanup_id)=($1,$2,$3,$4,$5)`
const activeVolumeCleanup = `status IN ('pending','approved','dispatched')`
const volumeCleanupColumns = `cleanup_id,backup_image_id,evidence_sha256,placement_id,vm_id::text,host_id,host_generation,host_revision,
	revision,status,requester_id,COALESCE(reviewer_id,''),requested_at,reviewed_at,dispatched_at,pool_sha256,first_absent_at,finished_at,
	COALESCE(resolved_by,''),resolution_sha256,second_resolution_sha256,requester_revision,COALESCE(reviewer_revision,0)`

func scanVolumeCleanup(row interface{ Scan(...any) error }) (VolumeCleanup, error) {
	var job VolumeCleanup
	err := row.Scan(&job.ID, &job.Backup, &job.Evidence, &job.Placement, &job.VM, &job.Host, &job.HostGeneration, &job.HostRevision,
		&job.Revision, &job.Status, &job.Requester, &job.Reviewer, &job.RequestedAt, &job.ReviewedAt, &job.DispatchedAt,
		&job.PoolSHA256, &job.FirstAbsentAt, &job.FinishedAt, &job.ResolvedBy, &job.ResolutionSHA256,
		&job.SecondResolution, &job.requesterVersion, &job.reviewerVersion)
	return job, err
}

func readVolumeCleanup(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, placement, cleanup string) (VolumeCleanup, error) {
	return scanVolumeCleanup(tx.QueryRowContext(ctx, `SELECT `+volumeCleanupColumns+` FROM cmp_kvm_volume_cleanups WHERE `+volumeCleanupKey,
		actor.Tenant, actor.Project, site, placement, cleanup))
}

func (s Store) GetVolumeCleanup(ctx context.Context, token string, scope identity.Scope, site, placement, cleanup string) (VolumeCleanup, error) {
	if !placementKey.MatchString(placement) || !identifier.MatchString(cleanup) {
		return VolumeCleanup{}, ErrInvalid
	}
	var job VolumeCleanup
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		job, err = readVolumeCleanup(ctx, tx, actor, site, placement, cleanup)
		return hiddenMissing(err)
	})
	return job, err
}

func (s Store) RequestVolumeCleanup(ctx context.Context, token string, scope identity.Scope, site, placement string, hostRevision int64, input VolumeCleanupInput) (VolumeCleanup, error) {
	if !placementKey.MatchString(placement) || hostRevision < 1 || !identifier.MatchString(input.ID) ||
		!identifier.MatchString(input.Backup) || !fenceEvidence.MatchString(input.Evidence) {
		return VolumeCleanup{}, ErrInvalid
	}
	var job VolumeCleanup
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		volume, err := lockRetainedVolume(ctx, tx, actor, site, placement)
		if err != nil {
			return err
		}
		job, err = readVolumeCleanup(ctx, tx, actor, site, placement, input.ID)
		if err == nil {
			if job.VolumeCleanupInput != input || job.Requester != actor.Principal || job.HostRevision != hostRevision {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if hostRevision != volume.host.Revision {
			return ErrStale
		}
		if volume.host.FenceState != "" {
			return ErrConflict
		}
		if err := volume.checkRetained(ctx, tx); err != nil {
			return err
		}
		if _, err := volume.readBackup(ctx, tx, actor, input.Backup); err != nil {
			return err
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
			return err
		}
		job, err = scanVolumeCleanup(tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_volume_cleanups
			(tenant_id,project_id,site_id,placement_id,cleanup_id,vm_id,host_id,host_generation,host_revision,
			 backup_image_id,evidence_sha256,requester_id,requester_revision)
			VALUES ($1,$2,$3,$4,$5,$6::uuid,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT DO NOTHING RETURNING `+volumeCleanupColumns, actor.Tenant, actor.Project, site, placement, input.ID,
			volume.disk.ID, volume.host.ID, volume.host.Generation, hostRevision, input.Backup, input.Evidence, actor.Principal, member.Revision))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	})
	if err != nil {
		return VolumeCleanup{}, err
	}
	return job, nil
}
