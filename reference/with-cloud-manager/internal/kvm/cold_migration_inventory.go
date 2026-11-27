package kvm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
)

type ColdMigrationEligibility struct {
	Mode                  string `json:"mode"`
	VM                    VM     `json:"vm"`
	SourceFenceGeneration int64  `json:"source_fence_generation,omitempty"`
}

func (s Store) GetColdMigrationEligibility(ctx context.Context, token string, scope identity.Scope, site, vm string) (ColdMigrationEligibility, error) {
	if !hostUUID.MatchString(vm) {
		return ColdMigrationEligibility{}, ErrInvalid
	}
	result := ColdMigrationEligibility{Mode: "unavailable"}
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		current, err := readVM(ctx, tx, actor, site, vm, false)
		if err != nil {
			return err
		}
		result.VM = current.VM
		if current.Capacity.GPU != 0 {
			return nil
		}
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_kvm_cold_migrations
			WHERE vm_id=$1::uuid AND status='pending')`, vm).Scan(&pending); err != nil || pending {
			return err
		}
		if err := checkRetainedVMSource(ctx, tx, current, current.Revision); err == nil {
			result.Mode = "cold"
			return nil
		} else if !errors.Is(err, ErrConflict) {
			return err
		}
		host, err := readHost(ctx, tx, actor, site, current.Host, false)
		if err != nil {
			return err
		}
		if err := checkFencedEvacuationSource(ctx, tx, current, host, host.Generation); err != nil {
			if errors.Is(err, ErrConflict) {
				return nil
			}
			return err
		}
		result.Mode, result.SourceFenceGeneration = "evacuation", host.Generation
		return nil
	})
	return result, err
}

func (s Store) ListColdMigrations(ctx context.Context, token string, scope identity.Scope, site, vm, after string, limit int) (InventoryPage[ColdMigration], error) {
	if !hostUUID.MatchString(vm) || limit < 1 || limit > 100 {
		return InventoryPage[ColdMigration]{}, ErrInvalid
	}
	before, id, err := parseTimedInventoryCursor(after)
	if err != nil {
		return InventoryPage[ColdMigration]{}, err
	}
	page := InventoryPage[ColdMigration]{Items: make([]ColdMigration, 0, limit+1)}
	err = s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if _, err := readVM(ctx, tx, actor, site, vm, false); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, coldMigrationSelect+`
			AND ($5::timestamptz IS NULL OR (requested_at,migration_id)<($5,$6))
			ORDER BY requested_at DESC,migration_id DESC LIMIT $7`, actor.Tenant, actor.Project, site, vm, before, id, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			item, err := scanColdMigration(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[ColdMigration]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.Next = last.RequestedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return page, nil
}

func (s Store) GetColdMigrationReceipt(ctx context.Context, token string, scope identity.Scope, site, vm, id string) (ColdMigration, error) {
	if !hostUUID.MatchString(vm) || !identifier.MatchString(id) {
		return ColdMigration{}, ErrInvalid
	}
	var result ColdMigration
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result, err = readColdMigration(ctx, tx, actor, site, vm, id)
		return hiddenMissing(err)
	})
	return result, err
}
