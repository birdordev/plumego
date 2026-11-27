package kvm

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"path"
	"regexp"
	"time"

	"with-cloud-manager/internal/identity"
)

var nfsExport = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,255}$`)

type SharedStorageSource struct {
	Server string `json:"server"`
	Export string `json:"export"`
}

func (source SharedStorageSource) Valid() bool {
	address, err := netip.ParseAddr(source.Server)
	return err == nil && address.Is4() && address.IsGlobalUnicast() && address.String() == source.Server &&
		nfsExport.MatchString(source.Export) && path.Clean(source.Export) == source.Export
}

type SharedStorageSpec struct {
	ID string `json:"id"`
	SharedStorageSource
	CapacityMiB int64 `json:"capacity_mib"`
}

func (pool SharedStorageSpec) Valid() bool {
	return identifier.MatchString(pool.ID) && pool.SharedStorageSource.Valid() && pool.CapacityMiB > 0 && pool.CapacityMiB <= 1<<40
}

type SharedStorage struct {
	SharedStorageSpec
	ReservedMiB  int64     `json:"reserved_mib"`
	RetainedMiB  int64     `json:"retained_mib"`
	RegisteredAt time.Time `json:"registered_at"`
}

const sharedStorageKey = `(tenant_id,project_id,site_id,pool_id)=($1,$2,$3,$4)`
const sharedStorageHoldKey = `(tenant_id,project_id,site_id,shared_storage_pool_id)=($1,$2,$3,$4)`

func (s Store) RegisterSharedStorage(ctx context.Context, token string, scope identity.Scope, site string, spec SharedStorageSpec) (SharedStorage, error) {
	if !spec.Valid() {
		return SharedStorage{}, ErrInvalid
	}
	result := SharedStorage{SharedStorageSpec: spec}
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,4010131))`, spec.Server); err != nil {
			return err
		}
		var overlap bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_kvm_shared_storage
			WHERE server_address=$1 AND (export_path=$2 OR starts_with(export_path,$2||'/') OR starts_with($2,export_path||'/')))`,
			spec.Server, spec.Export).Scan(&overlap)
		if err != nil {
			return err
		}
		if overlap {
			return ErrConflict
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_shared_storage
			(tenant_id,project_id,site_id,pool_id,server_address,export_path,capacity_mib,registered_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING RETURNING registered_at`,
			actor.Tenant, actor.Project, site, spec.ID, spec.Server, spec.Export, spec.CapacityMiB, actor.Principal).
			Scan(&result.RegisteredAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	})
	if err != nil {
		return SharedStorage{}, err
	}
	return result, nil
}

func (s Store) GetSharedStorage(ctx context.Context, token string, scope identity.Scope, site, id string, withUsage bool) (SharedStorage, error) {
	if !identifier.MatchString(id) {
		return SharedStorage{}, ErrInvalid
	}
	var pool SharedStorage
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		pool, err = readSharedStorage(ctx, tx, actor, site, id, false)
		if err != nil || !withUsage {
			return err
		}
		return readSharedStorageUsage(ctx, tx, actor, site, &pool)
	})
	return pool, err
}

func readSharedStorage(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, id string, lock bool) (SharedStorage, error) {
	query := `SELECT server_address,export_path,capacity_mib,registered_at FROM cmp_kvm_shared_storage WHERE ` + sharedStorageKey
	if lock {
		query += " FOR NO KEY UPDATE"
	}
	pool := SharedStorage{SharedStorageSpec: SharedStorageSpec{ID: id}}
	err := tx.QueryRowContext(ctx, query, actor.Tenant, actor.Project, site, id).
		Scan(&pool.Server, &pool.Export, &pool.CapacityMiB, &pool.RegisteredAt)
	return pool, hiddenMissing(err)
}

func readSharedStorageUsage(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, pool *SharedStorage) error {
	return tx.QueryRowContext(ctx, `SELECT
		COALESCE(sum(disk_mib) FILTER (WHERE released_at IS NULL),0),
		COALESCE(sum(disk_mib) FILTER (WHERE boot_volume_retained),0)
		FROM cmp_kvm_placements WHERE `+sharedStorageHoldKey+`
		  AND ((released_at IS NULL AND expired_at IS NULL) OR boot_volume_retained)`,
		actor.Tenant, actor.Project, site, pool.ID).Scan(&pool.ReservedMiB, &pool.RetainedMiB)
}

func checkSharedStorageCapacity(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, pool SharedStorage, disk int64) error {
	if _, err := tx.ExecContext(ctx, `WITH expired AS (SELECT placement_id FROM cmp_kvm_placements
		WHERE `+sharedStorageHoldKey+` AND released_at IS NULL AND expired_at IS NULL
		  AND consumed_at IS NULL AND expires_at<=statement_timestamp()
		FOR UPDATE SKIP LOCKED)
		UPDATE cmp_kvm_placements AS p SET expired_at=statement_timestamp() FROM expired
		WHERE p.placement_id=expired.placement_id`, actor.Tenant, actor.Project, site, pool.ID); err != nil {
		return err
	}
	if err := readSharedStorageUsage(ctx, tx, actor, site, &pool); err != nil {
		return err
	}
	if disk > pool.CapacityMiB-pool.ReservedMiB-pool.RetainedMiB {
		return ErrNoCapacity
	}
	return nil
}

func checkSharedStorageReport(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, report Report) error {
	if report.SharedStorage == nil {
		if host.SharedStoragePool != "" && report.Capacity != (Capacity{}) {
			return ErrConflict
		}
		return nil
	}
	if host.SharedStoragePool == "" || report.SharedStorage.ID != host.SharedStoragePool {
		return ErrConflict
	}
	pool, err := readSharedStorage(ctx, tx, actor, host.Site, host.SharedStoragePool, false)
	if err != nil {
		return err
	}
	if report.SharedStorage.SharedStorageSource != pool.SharedStorageSource {
		return ErrConflict
	}
	return nil
}
