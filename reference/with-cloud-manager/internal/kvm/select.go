package kvm

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
)

func selectHost(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, request PlacementInput) (Host, error) {
	var after string
	for {
		host, err := nextHost(ctx, tx, actor, site, after, request)
		if errors.Is(err, sql.ErrNoRows) {
			return Host{}, ErrNoCapacity
		}
		if err != nil {
			return Host{}, err
		}
		// 取得宿主机锁后用新的读已提交语句读取预留，纳入等待期间已提交的记录。
		after = host.ID
		if err := checkAgent(ctx, tx, actor, host.Agent); err != nil {
			if errors.Is(err, identity.ErrForbidden) {
				continue
			}
			return Host{}, err
		}
		ok, err := fitsCurrentCapacity(ctx, tx, actor, host, request.Capacity)
		if err != nil {
			return Host{}, err
		}
		if ok {
			return host, nil
		}
	}
}

func nextHost(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, after string, request PlacementInput) (Host, error) {
	return scanHost(tx.QueryRowContext(ctx, "SELECT "+hostColumns+` FROM cmp_kvm_hosts AS h
		WHERE (h.tenant_id,h.project_id,h.site_id)=($1,$2,$3) AND h.host_id>$4 AND NOT h.draining
		  AND h.reported_at>clock_timestamp()-$5*interval '1 second'
		  AND h.networks ? $6 AND h.storage_pool=$7
		  AND ($8='' OR h.host_id=$8) AND ($9='' OR h.failure_domain=$9) AND h.failure_domain<>$10
		  AND ($11='' OR h.gpu_model=$11) AND h.vcpu>=$12 AND h.memory_mib>=$13 AND h.disk_mib>=$14 AND h.gpu>=$15
		  AND EXISTS (SELECT 1 FROM cmp_principals AS p JOIN cmp_memberships AS m USING (tenant_id,principal_id)
			WHERE (p.tenant_id,m.project_id,p.principal_id)=(h.tenant_id,h.project_id,h.agent_id)
			AND p.kind='service' AND p.active AND m.active AND m.role='viewer')
		ORDER BY h.host_id LIMIT 1 FOR UPDATE OF h`, actor.Tenant, actor.Project, site, after, reportFreshnessSeconds,
		request.Network, request.StoragePool, request.RequiredHost, request.FailureDomain, request.ExcludedFailureDomain,
		request.GPUModel, request.VCPU, request.MemoryMiB, request.DiskMiB, request.GPU))
}

func fitsCurrentCapacity(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, requested Capacity) (bool, error) {
	// 容量重新分配前持久封存已过期记录，数据库时钟回拨时旧预留也不能恢复。
	if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_placements SET expired_at=statement_timestamp()
		WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4)
		  AND released_at IS NULL AND expired_at IS NULL AND consumed_at IS NULL AND expires_at<=statement_timestamp()`,
		actor.Tenant, actor.Project, host.Site, host.ID); err != nil {
		return false, err
	}
	var used Capacity
	var fresh bool
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(vcpu),0),COALESCE(sum(memory_mib),0),COALESCE(sum(disk_mib),0),
		COALESCE(sum(gpu),0),$5::timestamptz>statement_timestamp()-$6*interval '1 second'
		FROM cmp_kvm_placements
		WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4)
		  AND released_at IS NULL AND expired_at IS NULL`,
		actor.Tenant, actor.Project, host.Site, host.ID, host.ReportedAt, reportFreshnessSeconds).
		Scan(&used.VCPU, &used.MemoryMiB, &used.DiskMiB, &used.GPU, &fresh)
	if err != nil {
		return false, err
	}
	return fresh && requested.fits(host.Capacity, used), nil
}
