package kvm

import (
	"context"
	"database/sql"
	"encoding/json"

	"with-cloud-manager/internal/identity"
)

type ProvisioningHost struct {
	ID          string   `json:"id"`
	Site        string   `json:"site"`
	Networks    []string `json:"networks"`
	StoragePool string   `json:"storage_pool"`
	SharedPool  string   `json:"shared_storage_pool_id,omitempty"`
}

func (s Store) ListProvisioningHosts(ctx context.Context, token string, scope identity.Scope, q InventoryQuery) (InventoryPage[ProvisioningHost], error) {
	site, host, err := q.cursor(identifier)
	if err != nil {
		return InventoryPage[ProvisioningHost]{}, err
	}
	page := InventoryPage[ProvisioningHost]{Items: make([]ProvisioningHost, 0, q.Limit+1)}
	err = s.authorizedProject(ctx, token, scope, identity.Execute, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		rows, err := tx.QueryContext(ctx, `SELECT h.site_id,h.host_id,h.networks,h.storage_pool,COALESCE(h.shared_storage_pool_id,'')
			FROM cmp_kvm_hosts h WHERE (h.tenant_id,h.project_id)=($1,$2) AND ($3='' OR h.site_id=$3)
			AND (h.site_id,h.host_id)>($4,$5) AND NOT h.draining AND h.fence_state=''
			AND h.reported_at>statement_timestamp()-$6*interval '1 second' AND h.vcpu>0 AND h.memory_mib>0 AND h.disk_mib>0
			AND EXISTS (SELECT 1 FROM cmp_principals p JOIN cmp_memberships m USING (tenant_id,principal_id)
				WHERE (p.tenant_id,m.project_id,p.principal_id)=(h.tenant_id,h.project_id,h.agent_id)
				AND p.kind='service' AND p.active AND m.active AND m.role='viewer')
			ORDER BY h.site_id,h.host_id LIMIT $7`, actor.Tenant, actor.Project, q.Site, site, host, reportFreshnessSeconds, q.Limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var item ProvisioningHost
			var networks []byte
			if err := rows.Scan(&item.Site, &item.ID, &networks, &item.StoragePool, &item.SharedPool); err != nil {
				return err
			}
			if err := json.Unmarshal(networks, &item.Networks); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[ProvisioningHost]{}, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[q.Limit-1]
		page.Next = last.Site + "/" + last.ID
	}
	return page, nil
}

func (s Store) ListBootImages(ctx context.Context, token string, scope identity.Scope, site, after string, limit int) (InventoryPage[BootImage], error) {
	if (after != "" && !identifier.MatchString(after)) || limit < 1 || limit > 100 {
		return InventoryPage[BootImage]{}, ErrInvalid
	}
	page := InventoryPage[BootImage]{Items: make([]BootImage, 0, limit+1)}
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		rows, err := tx.QueryContext(ctx, `SELECT image_id,sha256,size_bytes,created_at FROM cmp_kvm_boot_images
			WHERE (tenant_id,project_id,site_id)=($1,$2,$3) AND image_id>$4 ORDER BY image_id LIMIT $5`,
			actor.Tenant, actor.Project, site, after, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			image := BootImage{Site: site}
			if err := rows.Scan(&image.ID, &image.SHA256, &image.SizeBytes, &image.CreatedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, image)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[BootImage]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.Next = page.Items[limit-1].ID
	}
	return page, nil
}

type ProvisioningStatus struct {
	Placement Placement `json:"placement"`
	VM        string    `json:"vm_id,omitempty"`
}

const vmByOriginalPlacement = `COALESCE((SELECT vm_id FROM cmp_kvm_vms WHERE placement_id=$1),
	(SELECT vm_id FROM cmp_kvm_cold_migrations WHERE source_placement_id=$1 AND status='approved'))`

func (s Store) GetProvisioningStatus(ctx context.Context, token string, scope identity.Scope, site, id string) (ProvisioningStatus, error) {
	if !placementKey.MatchString(id) {
		return ProvisioningStatus{}, ErrInvalid
	}
	var result ProvisioningStatus
	err := s.inProject(ctx, token, scope, site, identity.Execute, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result.Placement, err = scanPlacement(tx.QueryRowContext(ctx, `SELECT `+placementColumns+`,COALESCE((`+vmByOriginalPlacement+`)::text,'')
			FROM cmp_kvm_placements WHERE (placement_id,tenant_id,project_id,site_id,requester_id)=($1,$2,$3,$4,$5)`,
			id, actor.Tenant, actor.Project, site, actor.Principal), &result.VM)
		return hiddenMissing(err)
	})
	return result, err
}
