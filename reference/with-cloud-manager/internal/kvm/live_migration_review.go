package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

type liveMigrationWork struct {
	migration      LiveMigration
	vm             VM
	source, target Host
}

func loadLiveMigrationWork(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, vm, id string) (liveMigrationWork, error) {
	migration, err := readLiveMigration(ctx, tx, actor, site, vm, id)
	if err != nil {
		return liveMigrationWork{}, hiddenMissing(err)
	}
	source, target, err := lockLiveMigrationHosts(ctx, tx, actor, site, migration.Source, migration.Target)
	if err != nil {
		return liveMigrationWork{}, err
	}
	current, err := readVM(ctx, tx, actor, site, vm, true)
	if err != nil {
		return liveMigrationWork{}, err
	}
	migration, err = readLiveMigration(ctx, tx, actor, site, vm, id)
	return liveMigrationWork{migration: migration, vm: current.VM, source: source, target: target}, err
}

func (work liveMigrationWork) checkCurrent() error {
	if work.vm.Revision != work.migration.VMRevision || work.vm.Placement != work.migration.Placement ||
		work.source.Generation != work.migration.SourceGeneration || work.target.Generation != work.migration.TargetGeneration {
		return ErrStale
	}
	return checkLiveMigrationBinding(work.vm, work.source, work.target)
}

func (s Store) ReviewLiveMigration(ctx context.Context, token string, scope identity.Scope, site, vm, id string,
	revision int64, decision string) (LiveMigration, error) {
	if !hostUUID.MatchString(vm) || !identifier.MatchString(id) || revision < 1 || (decision != "approve" && decision != "reject") {
		return LiveMigration{}, ErrInvalid
	}
	var result LiveMigration
	err := s.inProject(ctx, token, scope, site, identity.Approve, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		work, err := loadLiveMigrationWork(ctx, tx, actor, site, vm, id)
		if err != nil {
			return err
		}
		migration := work.migration
		if migration.Requester == actor.Principal {
			return identity.ErrForbidden
		}
		if migration.VMRevision != revision {
			return ErrStale
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Approve), member.Revision); err != nil {
			return err
		}
		if migration.Reviewer == actor.Principal &&
			((decision == "approve" && migration.Status != "pending" && migration.Status != "rejected") ||
				(decision == "reject" && migration.Status == "rejected")) {
			result = migration
			return nil
		}
		status := "rejected"
		if decision == "approve" {
			if migration.Status != "pending" {
				return ErrConflict
			}
			if err := checkLiveMigrationAdmission(ctx, tx, actor, work); err != nil {
				return err
			}
			status = "approved"
		} else if migration.Status != "pending" && migration.Status != "approved" {
			return ErrConflict
		}
		result, err = scanLiveMigration(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_live_migrations
			SET status=$6,revision=revision+1,reviewer_id=$7,reviewer_revision=$8,reviewed_at=clock_timestamp()
			WHERE `+liveMigrationKey+` RETURNING `+liveMigrationColumns,
			actor.Tenant, actor.Project, site, vm, id, status, actor.Principal, member.Revision))
		return err
	})
	if err != nil {
		return LiveMigration{}, err
	}
	return result, nil
}

func checkLiveMigrationAdmission(ctx context.Context, tx *sql.Tx, actor identity.Scope, work liveMigrationWork) error {
	if err := work.checkCurrent(); err != nil {
		return err
	}
	if work.target.Draining {
		return ErrConflict
	}
	if err := checkVMGrant(ctx, tx, actor, work.migration.Requester, string(identity.Manage), work.migration.requesterVersion); err != nil {
		return err
	}
	for _, host := range []Host{work.source, work.target} {
		if err := checkAgent(ctx, tx, actor, host.Agent); err != nil {
			return err
		}
	}
	needed := Capacity{VCPU: work.vm.Capacity.VCPU, MemoryMiB: work.vm.Capacity.MemoryMiB}
	ok, err := fitsCurrentCapacity(ctx, tx, actor, work.target, needed)
	if err == nil && !ok {
		return ErrNoCapacity
	}
	return err
}

func (s Store) AbortFencedLiveMigration(ctx context.Context, token string, scope identity.Scope, site, vm, id string,
	revision int64, evidence string) (LiveMigration, error) {
	if !hostUUID.MatchString(vm) || !identifier.MatchString(id) || revision < 1 || !fenceEvidence.MatchString(evidence) {
		return LiveMigration{}, ErrInvalid
	}
	var result LiveMigration
	err := s.inProject(ctx, token, scope, site, identity.Approve, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		work, err := loadLiveMigrationWork(ctx, tx, actor, site, vm, id)
		if err != nil {
			return err
		}
		m := work.migration
		if m.Requester == actor.Principal {
			return identity.ErrForbidden
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Approve), member.Revision); err != nil {
			return err
		}
		if m.Status == "aborted" && m.AbortedBy == actor.Principal && m.AbortEvidence == evidence && m.Revision-1 == revision {
			result = m
			return nil
		}
		if m.Revision != revision || work.vm.Revision != m.VMRevision || work.vm.Host != m.Source || work.vm.Placement != m.Placement {
			return ErrStale
		}
		if (m.Status != "dispatched" && m.Status != "source_departed") ||
			work.source.FenceState != "fenced" || work.source.Generation <= m.SourceGeneration ||
			work.target.FenceState != "fenced" || work.target.Generation <= m.TargetGeneration {
			return ErrConflict
		}
		result, err = scanLiveMigration(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_live_migrations
			SET status='aborted',revision=revision+1,aborted_at=clock_timestamp(),aborted_by=$6,abort_evidence=$7
			WHERE `+liveMigrationKey+` RETURNING `+liveMigrationColumns,
			actor.Tenant, actor.Project, site, vm, id, actor.Principal, evidence))
		return err
	})
	if err != nil {
		return LiveMigration{}, err
	}
	return result, nil
}

func ensureVMNotMigrating(ctx context.Context, tx *sql.Tx, vm string) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_kvm_live_migrations
		WHERE vm_id=$1::uuid AND `+activeLiveMigration+`)`, vm).Scan(&active)
	if err == nil && active {
		return ErrConflict
	}
	return err
}

const liveMigrationHostWork = ` FROM cmp_kvm_live_migrations AS live
	WHERE (live.tenant_id,live.project_id,live.site_id)=($1,$2,$3)
	  AND (live.target_host=$4 OR live.source_host=$4) AND ` + activeLiveMigration

func ensureHostNotMigrating(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1`+liveMigrationHostWork+`)`,
		actor.Tenant, actor.Project, host.Site, host.ID).Scan(&active)
	if err == nil && active {
		return ErrConflict
	}
	return err
}
