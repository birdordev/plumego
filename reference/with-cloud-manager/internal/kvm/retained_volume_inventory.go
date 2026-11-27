package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

type RetainedVolumeSummary struct {
	Placement      string `json:"placement_id"`
	Host           string `json:"host"`
	Volume         string `json:"boot_volume"`
	StoragePool    string `json:"storage_pool"`
	SharedPool     string `json:"shared_storage_pool_id,omitempty"`
	HostRevision   int64  `json:"host_revision"`
	MaxBackupBytes int64  `json:"max_backup_bytes"`
	ExactBytes     int64  `json:"expected_backup_bytes,omitempty"`
	Retained       bool   `json:"retained"`
	Requestable    bool   `json:"requestable"`
	LatestStatus   string `json:"latest_status,omitempty"`
}

const retainedVolumeInventoryQuery = `WITH sources AS (
	SELECT placement_id FROM cmp_kvm_vms WHERE vm_id=$4::uuid
	UNION
	SELECT source_placement_id FROM cmp_kvm_cold_migrations
	WHERE (tenant_id,project_id,site_id,vm_id)=($1,$2,$3,$4::uuid) AND status='approved'
)
SELECT p.placement_id,p.host_id,p.boot_volume,p.storage_pool,COALESCE(p.shared_storage_pool_id,''),p.disk_mib,p.generation,
	p.boot_volume_retained,p.released_at IS NOT NULL,h.generation,h.revision,h.fence_state,h.storage_pool,COALESCE(h.shared_storage_pool_id,''),
	COALESCE(i.size_bytes,0),v.placement_id,v.state,v.observation,COALESCE(last.status,''),
	EXISTS (SELECT 1 FROM cmp_kvm_cold_migrations WHERE source_placement_id=p.placement_id ` + retainedBlockingMigration + `),
	EXISTS (SELECT 1 FROM cmp_kvm_volume_cleanups WHERE placement_id=p.placement_id AND ` + activeVolumeCleanup + `)
FROM sources AS s JOIN cmp_kvm_placements AS p USING (placement_id)
JOIN cmp_kvm_vms AS v ON v.vm_id=$4::uuid
JOIN cmp_kvm_hosts AS h ON (h.tenant_id,h.project_id,h.site_id,h.host_id)=(p.tenant_id,p.project_id,p.site_id,p.host_id)
LEFT JOIN cmp_kvm_boot_images AS i ON (i.tenant_id,i.project_id,i.site_id,i.image_id)=(p.tenant_id,p.project_id,p.site_id,p.image_id)
LEFT JOIN LATERAL (SELECT status FROM cmp_kvm_volume_cleanups WHERE placement_id=p.placement_id
	ORDER BY requested_at DESC,cleanup_id DESC LIMIT 1) AS last ON true
WHERE (p.tenant_id,p.project_id,p.site_id)=($1,$2,$3) AND p.placement_id>$5 AND (p.boot_volume_retained OR last.status IS NOT NULL)
ORDER BY p.placement_id LIMIT $6`

func (s Store) ListRetainedVolumes(ctx context.Context, token string, scope identity.Scope, site, vm, after string,
	limit int) (InventoryPage[RetainedVolumeSummary], error) {
	if !hostUUID.MatchString(vm) || (after != "" && !placementKey.MatchString(after)) || limit < 1 || limit > 100 {
		return InventoryPage[RetainedVolumeSummary]{}, ErrInvalid
	}
	page := InventoryPage[RetainedVolumeSummary]{Items: make([]RetainedVolumeSummary, 0, limit+1)}
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if _, err := readVM(ctx, tx, actor, site, vm, false); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, retainedVolumeInventoryQuery, actor.Tenant, actor.Project, site, vm, after, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			entry, err := scanRetainedVolumeSummary(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, entry)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[RetainedVolumeSummary]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.Next = page.Items[limit-1].Placement
	}
	return page, nil
}

func scanRetainedVolumeSummary(row interface{ Scan(...any) error }) (RetainedVolumeSummary, error) {
	var entry RetainedVolumeSummary
	var volume retainedVolume
	var diskMiB int64
	var released, blocked, active bool
	err := row.Scan(&entry.Placement, &entry.Host, &entry.Volume, &entry.StoragePool, &entry.SharedPool, &diskMiB, &volume.disk.Generation,
		&entry.Retained, &released, &volume.host.Generation, &entry.HostRevision, &volume.host.FenceState, &volume.host.StoragePool,
		&volume.host.SharedStoragePool, &entry.ExactBytes, &volume.current.Placement, &volume.current.State, &volume.current.Observation,
		&entry.LatestStatus, &blocked, &active)
	if err != nil {
		return RetainedVolumeSummary{}, err
	}
	volume.placement.ID = entry.Placement
	volume.placement.BootVolumeRetained = entry.Retained
	if released {
		volume.placement.State = "released"
	}
	volume.disk.StoragePool, volume.disk.SharedStoragePool = entry.StoragePool, entry.SharedPool
	entry.MaxBackupBytes = min(MaxBootImageBytes, diskMiB*(1<<20))
	entry.Requestable = volume.matchesRetained() && volume.host.FenceState == "" && !blocked && !active
	return entry, nil
}
