package kvm

import (
	"context"
	"database/sql"
	"time"

	"with-cloud-manager/internal/identity"
)

func (s Store) ListLiveMigrations(ctx context.Context, token string, scope identity.Scope, site, vm, after string, limit int) (InventoryPage[LiveMigration], error) {
	if !hostUUID.MatchString(vm) || limit < 1 || limit > 100 {
		return InventoryPage[LiveMigration]{}, ErrInvalid
	}
	before, id, err := parseTimedInventoryCursor(after)
	if err != nil {
		return InventoryPage[LiveMigration]{}, err
	}
	page := InventoryPage[LiveMigration]{Items: make([]LiveMigration, 0, limit+1)}
	err = s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if _, err := readVM(ctx, tx, actor, site, vm, false); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+liveMigrationColumns+` FROM cmp_kvm_live_migrations
			WHERE (tenant_id,project_id,site_id,vm_id)=($1,$2,$3,$4::uuid)
			AND ($5::timestamptz IS NULL OR (requested_at,migration_id)<($5,$6))
			ORDER BY requested_at DESC,migration_id DESC LIMIT $7`, actor.Tenant, actor.Project, site, vm, before, id, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			item, err := scanLiveMigration(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[LiveMigration]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.Next = last.RequestedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return page, nil
}

type LiveMigrationTarget struct {
	ID            string `json:"id"`
	FailureDomain string `json:"failure_domain"`
}

func (s Store) ListLiveMigrationTargets(ctx context.Context, token string, scope identity.Scope, site, vm, after string, limit int) (InventoryPage[LiveMigrationTarget], error) {
	if !hostUUID.MatchString(vm) || (after != "" && !identifier.MatchString(after)) || limit < 1 || limit > 100 {
		return InventoryPage[LiveMigrationTarget]{}, ErrInvalid
	}
	page := InventoryPage[LiveMigrationTarget]{Items: make([]LiveMigrationTarget, 0, limit+1)}
	err := s.inProject(ctx, token, scope, site, identity.Manage, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		current, err := readVM(ctx, tx, actor, site, vm, false)
		if err != nil {
			return err
		}
		source, err := readHost(ctx, tx, actor, site, current.Host, false)
		if err != nil {
			return err
		}
		if !liveMigrationSourceMatches(current.VM, source) {
			return ErrConflict
		}
		rows, err := tx.QueryContext(ctx, `SELECT host_id,failure_domain FROM cmp_kvm_hosts
			WHERE (tenant_id,project_id,site_id)=($1,$2,$3) AND host_id>$4 AND host_id<>$5
			AND shared_storage_pool_id=$6 AND storage_pool=$7 AND networks ? $8::text
			AND NOT draining AND fence_state='' AND reported_at>=statement_timestamp()-($9::int * interval '1 second')
			AND vcpu>=$10 AND memory_mib>=$11
			ORDER BY host_id LIMIT $12`, actor.Tenant, actor.Project, site, after, source.ID,
			current.SharedStoragePool, current.StoragePool, current.Network, reportFreshnessSeconds,
			current.Capacity.VCPU, current.Capacity.MemoryMiB, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var host LiveMigrationTarget
			if err := rows.Scan(&host.ID, &host.FailureDomain); err != nil {
				return err
			}
			page.Items = append(page.Items, host)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[LiveMigrationTarget]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.Next = page.Items[limit-1].ID
	}
	return page, nil
}
