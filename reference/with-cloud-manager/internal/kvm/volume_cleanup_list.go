package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

type VolumeCleanupPage struct {
	Host  string          `json:"host"`
	Items []VolumeCleanup `json:"items"`
	Next  string          `json:"next,omitempty"`
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
