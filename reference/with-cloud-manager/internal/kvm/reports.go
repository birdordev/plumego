package kvm

import (
	"context"
	"database/sql"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

func (s Store) Report(ctx context.Context, token string, scope identity.Scope, site, id string, report Report) (Host, error) {
	if !identifier.MatchString(site) || !identifier.MatchString(id) || !hostUUID.MatchString(report.UUID) ||
		report.Generation < 1 || report.Sequence < 1 || !report.Capacity.valid() {
		return Host{}, ErrInvalid
	}

	if report.Inventory != nil && (report.Inventory.HostRevision < 1 || !validObservedDomains(report.Inventory.Domains)) {
		return Host{}, ErrInvalid
	}
	var result Host
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := identity.NewStore(tx).AuthenticateService(ctx, token)
		if err != nil {
			return err
		}
		if actor.Tenant != scope.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = scope.Project
		// 先锁宿主机行，使换绑和上报按同一顺序生效。
		host, err := readHost(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if actor.Principal != host.Agent || report.UUID != host.UUID {
			return identity.ErrForbidden
		}
		if err := checkAgent(ctx, tx, actor, host.Agent); err != nil {
			return err
		}
		if report.Generation != host.Generation || report.Sequence < host.Sequence ||
			(report.Sequence == host.Sequence && (host.ReportedAt == nil || report.Capacity != host.Capacity)) {
			return ErrStale
		}
		if host.FenceState != "" {
			return ErrConflict
		}
		if report.Inventory != nil {
			if err := verifyCapacityInventory(ctx, tx, actor, host, *report.Inventory); err != nil {
				return err
			}
		}
		if report.Capacity.GPU > 0 && host.GPUModel == "" {
			return ErrInvalid
		}
		if report.Sequence == host.Sequence {
			result = host
			return nil
		}
		result, err = scanHost(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_hosts
			SET sequence=$5,reported_at=clock_timestamp(),vcpu=$6,memory_mib=$7,disk_mib=$8,gpu=$9
			WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4) RETURNING `+hostColumns,
			actor.Tenant, actor.Project, site, id, report.Sequence, report.Capacity.VCPU, report.Capacity.MemoryMiB,
			report.Capacity.DiskMiB, report.Capacity.GPU))
		return err
	})
	return result, err
}
