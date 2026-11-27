package kvm

import (
	"context"
	"database/sql"
	"time"

	"with-cloud-manager/internal/identity"
)

type VolumeCleanupPage struct {
	Host  string          `json:"host"`
	Items []VolumeCleanup `json:"items"`
	Next  string          `json:"next,omitempty"`
}

func (s Store) ListVolumeCleanupHistory(ctx context.Context, token string, scope identity.Scope, site, placement, after string,
	limit int) (InventoryPage[VolumeCleanup], error) {
	if !placementKey.MatchString(placement) || limit < 1 || limit > 100 {
		return InventoryPage[VolumeCleanup]{}, ErrInvalid
	}
	before, id, err := parseTimedInventoryCursor(after)
	if err != nil {
		return InventoryPage[VolumeCleanup]{}, err
	}
	page := InventoryPage[VolumeCleanup]{Items: make([]VolumeCleanup, 0, limit+1)}
	err = s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM cmp_kvm_placements WHERE (tenant_id,project_id,site_id,placement_id)=($1,$2,$3,$4)`,
			actor.Tenant, actor.Project, site, placement).Scan(&exists); err != nil {
			return hiddenMissing(err)
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+volumeCleanupColumns+` FROM cmp_kvm_volume_cleanups
			WHERE (tenant_id,project_id,site_id,placement_id)=($1,$2,$3,$4)
			AND ($5::timestamptz IS NULL OR (requested_at,cleanup_id)<($5,$6))
			ORDER BY requested_at DESC,cleanup_id DESC LIMIT $7`, actor.Tenant, actor.Project, site, placement, before, id, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			job, err := scanVolumeCleanup(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, job)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[VolumeCleanup]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.Next = last.RequestedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return page, nil
}

func (s Store) ListVolumeCleanups(ctx context.Context, token string, scope identity.Scope, site, host, after string,
	limit int) (VolumeCleanupPage, error) {
	if !identifier.MatchString(host) || (after != "" && !placementKey.MatchString(after)) || limit < 1 || limit > 100 {
		return VolumeCleanupPage{}, ErrInvalid
	}
	page := VolumeCleanupPage{Host: host, Items: make([]VolumeCleanup, 0, limit+1)}
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if _, err := readHost(ctx, tx, actor, site, host, false); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+volumeCleanupColumns+` FROM cmp_kvm_volume_cleanups
			WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4) AND placement_id>$5 AND `+activeVolumeCleanup+`
			ORDER BY placement_id LIMIT $6`, actor.Tenant, actor.Project, site, host, after, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			job, err := scanVolumeCleanup(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, job)
		}
		return rows.Err()
	})
	if err != nil {
		return VolumeCleanupPage{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.Next = page.Items[limit-1].Placement
	}
	return page, nil
}
