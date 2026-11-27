package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

func (s Store) GetEvacuationBatch(ctx context.Context, token string, scope identity.Scope, site, host, id string) (EvacuationBatch, error) {
	if !identifier.MatchString(host) || !identifier.MatchString(id) {
		return EvacuationBatch{}, ErrInvalid
	}
	var result EvacuationBatch
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result, _, err = readEvacuationBatch(ctx, tx, actor, site, host, id)
		return hiddenMissing(err)
	})
	return result, err
}

func readEvacuationBatch(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, host, id string) (EvacuationBatch, []byte, error) {
	var result EvacuationBatch
	var hash []byte
	err := tx.QueryRowContext(ctx, `SELECT batch_id,source_host,source_fence_generation,host_revision,requester_id,requested_at,request_sha256
		FROM cmp_kvm_evacuation_batches WHERE (tenant_id,project_id,site_id,source_host,batch_id)=($1,$2,$3,$4,$5)`,
		actor.Tenant, actor.Project, site, host, id).Scan(&result.ID, &result.SourceHost, &result.SourceFenceGeneration,
		&result.HostRevision, &result.Requester, &result.RequestedAt, &hash)
	if err != nil {
		return EvacuationBatch{}, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+coldMigrationColumns+` FROM cmp_kvm_cold_migrations AS m JOIN (
		SELECT vm_id AS vm_key,migration_id AS migration_key,position
		FROM cmp_kvm_evacuation_batch_items WHERE (tenant_id,project_id,site_id,source_host,batch_id)=($1,$2,$3,$4,$5)
	) AS link ON (m.vm_id,m.migration_id)=(link.vm_key,link.migration_key)
	WHERE (m.tenant_id,m.project_id,m.site_id,m.source_host)=($1,$2,$3,$4)
	ORDER BY link.position`, actor.Tenant, actor.Project, site, host, id)
	if err != nil {
		return EvacuationBatch{}, nil, err
	}
	defer func() { _ = rows.Close() }()
	result.Items = make([]ColdMigration, 0)
	for rows.Next() {
		item, err := scanColdMigration(rows)
		if err != nil {
			return EvacuationBatch{}, nil, err
		}
		result.Items = append(result.Items, item)
	}
	return result, hash, rows.Err()
}
