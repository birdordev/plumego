package kvm

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
)

type retainedVolume struct {
	host      Host
	placement Placement
	disk      VM
	current   VM
}

func lockRetainedVolume(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, placement string) (retainedVolume, error) {
	var result retainedVolume
	var hostID, vmID string
	err := tx.QueryRowContext(ctx, `SELECT host_id FROM cmp_kvm_placements
		WHERE (tenant_id,project_id,site_id,placement_id)=($1,$2,$3,$4)`, actor.Tenant, actor.Project, site, placement).Scan(&hostID)
	if err != nil {
		return result, hiddenMissing(err)
	}
	result.host, err = readHost(ctx, tx, actor, site, hostID, true)
	if err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT vm_id::text FROM cmp_kvm_vms WHERE placement_id=$1),
		(SELECT vm_id::text FROM cmp_kvm_cold_migrations WHERE source_placement_id=$1 AND status='approved'),'')`, placement).
		Scan(&vmID)
	if err != nil {
		return result, err
	}
	if vmID == "" {
		return result, ErrConflict
	}
	current, err := readVM(ctx, tx, actor, site, vmID, true)
	if err != nil {
		return result, err
	}
	var imageID, volume string
	p, err := scanPlacement(tx.QueryRowContext(ctx, `SELECT `+placementColumns+`,COALESCE(image_id,''),boot_volume
		FROM cmp_kvm_placements WHERE (placement_id,host_id)=($1,$2) FOR UPDATE`, placement, hostID), &imageID, &volume)
	if err != nil {
		return result, hiddenMissing(err)
	}
	result.current, result.placement = current.VM, p
	result.disk = VM{ID: vmID, Placement: p.ID, Host: p.Host, Generation: p.Generation,
		StoragePool: p.StoragePool, SharedStoragePool: p.SharedStoragePool, Volume: volume, Capacity: p.Capacity, ImageID: imageID}
	return result, nil
}

func (volume retainedVolume) matchesRetained() bool {
	if !volume.placement.BootVolumeRetained || volume.placement.State != "released" ||
		volume.disk.Generation > volume.host.Generation || volume.disk.StoragePool != volume.host.StoragePool ||
		volume.disk.SharedStoragePool != volume.host.SharedStoragePool {
		return false
	}
	return volume.current.Placement != volume.placement.ID ||
		((volume.current.State == "retired" || volume.current.State == "rejected") && volume.current.Observation == "absent")
}

const retainedBlockingMigration = `AND (status='pending' OR (status='approved' AND source_fence_generation>0 AND cleanup_at IS NULL))`

func (volume retainedVolume) checkRetained(ctx context.Context, tx *sql.Tx) error {
	if !volume.matchesRetained() {
		return ErrConflict
	}
	var blocked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_kvm_cold_migrations WHERE source_placement_id=$1 `+retainedBlockingMigration+`)`,
		volume.placement.ID).Scan(&blocked)
	if err == nil && blocked {
		return ErrConflict
	}
	return err
}

func (volume retainedVolume) readBackup(ctx context.Context, tx *sql.Tx, actor identity.Scope, id string) (BootImageSpec, error) {
	backup, err := readBootImage(ctx, tx, actor, volume.host.Site, id)
	if err != nil {
		return BootImageSpec{}, err
	}
	if backup.SizeBytes > volume.disk.Capacity.DiskMiB*(1<<20) {
		return BootImageSpec{}, ErrConflict
	}
	if volume.disk.ImageID != "" {
		original, err := readBootImage(ctx, tx, actor, volume.host.Site, volume.disk.ImageID)
		if err != nil {
			return BootImageSpec{}, err
		}
		if original.SizeBytes != backup.SizeBytes {
			return BootImageSpec{}, ErrConflict
		}
	}
	return backup.BootImageSpec, nil
}

func ensureHostNoDispatchedVolumeCleanup(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_kvm_volume_cleanups
		WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4) AND status='dispatched')`,
		actor.Tenant, actor.Project, host.Site, host.ID).Scan(&active)
	if err == nil && active {
		return ErrConflict
	}
	return err
}

func finishVolumeCleanup(ctx context.Context, tx *sql.Tx, placement string) error {
	var applied string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_kvm_placements SET boot_volume_retained=false
		WHERE placement_id=$1 AND released_at IS NOT NULL AND boot_volume_retained RETURNING placement_id`, placement).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
