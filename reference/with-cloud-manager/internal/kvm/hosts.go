package kvm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"with-cloud-manager/internal/identity"
)

const hostColumns = `host_id,host_uuid::text,agent_id,failure_domain,networks,storage_pool,gpu_model,site_id,
	revision,generation,sequence,draining,reported_at,vcpu,memory_mib,disk_mib,gpu,fence_state,COALESCE(shared_storage_pool_id,'')`

func scanHost(row *sql.Row) (Host, error) {
	var host Host
	var networks []byte
	err := row.Scan(&host.ID, &host.UUID, &host.Agent, &host.FailureDomain, &networks, &host.StoragePool,
		&host.GPUModel, &host.Site, &host.Revision, &host.Generation, &host.Sequence, &host.Draining, &host.ReportedAt,
		&host.Capacity.VCPU, &host.Capacity.MemoryMiB, &host.Capacity.DiskMiB, &host.Capacity.GPU, &host.FenceState, &host.SharedStoragePool)
	if err != nil {
		return Host{}, err
	}
	if err := json.Unmarshal(networks, &host.Networks); err != nil {
		return Host{}, err
	}
	return host, nil
}

func readHost(ctx context.Context, tx *sql.Tx, scope identity.Scope, site, host string, lock bool) (Host, error) {
	query := `SELECT ` + hostColumns + ` FROM cmp_kvm_hosts WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4)`
	if lock {
		query += " FOR NO KEY UPDATE"
	}
	result, err := scanHost(tx.QueryRowContext(ctx, query, scope.Tenant, scope.Project, site, host))
	return result, hiddenMissing(err)
}

func (s Store) GetHost(ctx context.Context, token string, scope identity.Scope, site, host string) (Host, error) {
	if !identifier.MatchString(host) {
		return Host{}, ErrInvalid
	}
	var result Host
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result, err = readHost(ctx, tx, actor, site, host, false)
		return err
	})
	return result, err
}

func (s Store) RegisterHost(ctx context.Context, token string, scope identity.Scope, site string, spec HostSpec) (Host, error) {
	if !spec.valid() {
		return Host{}, ErrInvalid
	}
	networks, err := json.Marshal(spec.Networks)
	if err != nil {
		return Host{}, err
	}
	var result Host
	err = s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if err := checkAgent(ctx, tx, actor, spec.Agent); err != nil {
			return err
		}
		if spec.SharedStoragePool != "" {
			if _, err := readSharedStorage(ctx, tx, actor, site, spec.SharedStoragePool, false); err != nil {
				return err
			}
		}
		var err error
		result, err = scanHost(tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_hosts
			(tenant_id,project_id,site_id,host_id,host_uuid,agent_id,failure_domain,networks,storage_pool,gpu_model,shared_storage_pool_id)
			VALUES ($1,$2,$3,$4,$5::uuid,$6,$7,$8::jsonb,$9,$10,NULLIF($11,''))
			ON CONFLICT DO NOTHING RETURNING `+hostColumns,
			actor.Tenant, actor.Project, site, spec.ID, spec.UUID, spec.Agent, spec.FailureDomain, string(networks),
			spec.StoragePool, spec.GPUModel, spec.SharedStoragePool))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, spec.ID, "register", "")
	})
	return result, err
}

func checkAgent(ctx context.Context, tx *sql.Tx, scope identity.Scope, principal string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT p.principal_id FROM cmp_principals AS p
		JOIN cmp_memberships AS m USING (tenant_id,principal_id)
		WHERE (p.tenant_id,m.project_id,p.principal_id)=($1,$2,$3)
		AND p.kind='service' AND p.active AND m.active AND m.role='viewer'
		FOR SHARE OF p,m`, scope.Tenant, scope.Project, principal).Scan(&id)
	return hiddenMissing(err)
}

func (s Store) ChangeHost(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64, change HostChange) (Host, error) {
	if !identifier.MatchString(id) || revision < 1 || !change.valid() {
		return Host{}, ErrInvalid
	}
	var result Host
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		host, err := readHost(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if host.Revision != revision {
			return ErrStale
		}
		if host.FenceState != "" {
			return ErrConflict
		}
		if change.Action == "rebind" {
			if err := ensureHostNoDispatchedVolumeCleanup(ctx, tx, actor, host); err != nil {
				return err
			}
			if err := ensureHostNotMigrating(ctx, tx, actor, host); err != nil {
				return err
			}
			result, err = rebindHost(ctx, tx, actor, host, change.Agent)
			if err == nil {
				err = invalidateUnactivatedEvacuationPlan(ctx, tx, actor, result)
			}
		} else {
			result, err = setHostDrain(ctx, tx, actor, host, change.Action == "drain")
		}
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, id, change.Action, "")
	})
	return result, err
}

func (c HostChange) valid() bool {
	switch c.Action {
	case "drain", "resume":
		return c.Agent == ""
	case "rebind":
		return identifier.MatchString(c.Agent)
	default:
		return false
	}
}

func setHostDrain(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, draining bool) (Host, error) {
	if host.Draining == draining {
		return Host{}, ErrConflict
	}
	return scanHost(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_hosts
		SET revision=revision+1,draining=$5,reported_at=NULL,vcpu=0,memory_mib=0,disk_mib=0,gpu=0
		WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4) RETURNING `+hostColumns,
		actor.Tenant, actor.Project, host.Site, host.ID, draining))
}

func rebindHost(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, agent string) (Host, error) {
	if host.Agent == agent {
		return Host{}, ErrConflict
	}
	if err := checkAgent(ctx, tx, actor, agent); err != nil {
		return Host{}, err
	}
	result, err := scanHost(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_hosts AS h
		SET agent_id=$5,revision=revision+1,generation=generation+1,sequence=0,
		    draining=true,reported_at=NULL,vcpu=0,memory_mib=0,disk_mib=0,gpu=0
		WHERE (h.tenant_id,h.project_id,h.site_id,h.host_id)=($1,$2,$3,$4)
		  AND NOT EXISTS (SELECT 1 FROM cmp_kvm_hosts AS other WHERE (other.tenant_id,other.agent_id)=($1,$5))
		  AND NOT EXISTS (SELECT 1 FROM cmp_kvm_vms AS v JOIN cmp_kvm_placements AS p USING (placement_id)
			WHERE (p.tenant_id,p.project_id,p.site_id,p.host_id)=($1,$2,$3,$4)
			  AND v.state IN ('staging','staged','defining','starting','stopping','undefining'))
		RETURNING `+hostColumns, actor.Tenant, actor.Project, host.Site, host.ID, agent))
	if errors.Is(err, sql.ErrNoRows) || uniqueViolation(err) {
		return Host{}, ErrConflict
	}
	return result, err
}
