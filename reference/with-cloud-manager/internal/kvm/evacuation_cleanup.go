package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

type EvacuationCleanup struct {
	VM       string `json:"vm_id"`
	ID       string `json:"migration_id"`
	Evidence string `json:"evidence_sha256"`
}

const pendingEvacuationCleanup = ` FROM cmp_kvm_cold_migrations AS m
	WHERE (m.tenant_id,m.project_id,m.site_id,m.source_host)=($1,$2,$3,$4)
	  AND m.source_fence_generation>0 AND m.status='approved' AND m.cleanup_at IS NULL`

func (s Store) ConfirmEvacuationCleanup(ctx context.Context, token string, scope identity.Scope, site, hostID string, revision int64, proof EvacuationCleanup) (ColdMigration, error) {
	if !identifier.MatchString(hostID) || !hostUUID.MatchString(proof.VM) || !identifier.MatchString(proof.ID) ||
		!fenceEvidence.MatchString(proof.Evidence) || revision < 1 {
		return ColdMigration{}, ErrInvalid
	}
	var result ColdMigration
	err := s.inProject(ctx, token, scope, site, identity.Approve, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		host, err := readHost(ctx, tx, actor, site, hostID, true)
		if err != nil {
			return err
		}
		m, err := readColdMigration(ctx, tx, actor, site, proof.VM, proof.ID)
		if err != nil {
			return hiddenMissing(err)
		}
		if m.SourceHost != hostID || m.Status != "approved" || m.SourceFenceGeneration <= 0 {
			return ErrConflict
		}
		if m.Requester == actor.Principal {
			return identity.ErrForbidden
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Approve), member.Revision); err != nil {
			return err
		}
		if revision != host.Revision || m.SourceFenceGeneration != host.Generation {
			return ErrStale
		}
		if m.CleanupAt != nil {
			if m.CleanupReviewer != actor.Principal || m.CleanupSHA256 != proof.Evidence {
				return ErrConflict
			}
			result = m
			return nil
		}
		if host.FenceState != "fenced" && host.FenceState != "rejoin_pending" {
			return ErrConflict
		}
		result, err = scanColdMigration(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_cold_migrations
			SET cleanup_at=clock_timestamp(),cleanup_reviewer_id=$3,cleanup_sha256=$4
			WHERE (vm_id,migration_id)=($1::uuid,$2) RETURNING `+coldMigrationColumns, proof.VM, proof.ID, actor.Principal, proof.Evidence))
		return err
	})
	if err != nil {
		return ColdMigration{}, err
	}
	return result, nil
}
