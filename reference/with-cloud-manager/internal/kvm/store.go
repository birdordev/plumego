package kvm

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
)

const Migration = "0111_kvm_placement"
const VMMigration = "0112_kvm_vms"
const ShutdownMigration = "0113_kvm_vm_shutdown"
const RetirementMigration = "0114_kvm_vm_retirement"
const IPAMMigration = "0115_kvm_ipam"
const BootImageMigration = "0116_kvm_boot_images"
const HostFenceMigration = "0123_kvm_host_fence"
const FencedVMRecoveryMigration = "0124_kvm_fenced_vm_recovery"
const ColdMigrationVersion = "0125_kvm_cold_migrations"
const EvacuationMigration = "0126_kvm_evacuation"
const AutomaticEvacuationMigration = "0127_kvm_evacuation_placement"
const EvacuationBatchMigration = "0128_kvm_evacuation_batches"
const EvacuationPlanMigration = "0129_kvm_evacuation_plans"
const HostImageMigration = "0130_kvm_host_images"
const SharedStorageMigration = "0131_kvm_shared_storage"
const LiveMigrationVersion = "0132_kvm_live_migrations"
const VolumeCleanupMigration = "0133_kvm_volume_cleanups"

func RequiredMigrations() []string {
	return []string{
		Migration,
		VMMigration,
		ShutdownMigration,
		RetirementMigration,
		IPAMMigration,
		BootImageMigration,
		HostFenceMigration,
		FencedVMRecoveryMigration,
		ColdMigrationVersion,
		EvacuationMigration,
		AutomaticEvacuationMigration,
		EvacuationBatchMigration,
		EvacuationPlanMigration,
		HostImageMigration,
		SharedStorageMigration,
		LiveMigrationVersion,
		VolumeCleanupMigration,
	}
}

func Migrate(ctx context.Context, db *sql.DB) error {
	for _, version := range RequiredMigrations() {
		if err := dbschema.Apply(ctx, db, version); err != nil {
			return err
		}
	}
	return nil
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) Store { return Store{db: db} }

func (s Store) inProject(ctx context.Context, token string, scope identity.Scope, site string,
	permission identity.Permission, readOnly bool, work func(*sql.Tx, identity.Scope, identity.Membership) error) error {
	if !identifier.MatchString(site) {
		return ErrInvalid
	}
	return s.authorizedProject(ctx, token, scope, permission, readOnly, work)
}

func (s Store) authorizedProject(ctx context.Context, token string, scope identity.Scope,
	permission identity.Permission, readOnly bool, work func(*sql.Tx, identity.Scope, identity.Membership) error) error {
	return dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: readOnly}, func(tx *sql.Tx) error {
		store := identity.NewStore(tx)
		actor, err := store.AuthenticateRequest(ctx, token)
		if err != nil {
			return err
		}
		if actor.Tenant != scope.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = scope.Project
		member, err := store.AuthorizeProject(ctx, actor, permission)
		if err != nil {
			return err
		}
		return work(tx, actor, member)
	})
}

func recordEvent(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, host, action, placement string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_kvm_events
		(tenant_id,project_id,site_id,host_id,actor_id,action,placement_id)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''))`, actor.Tenant, actor.Project, site, host, actor.Principal, action, placement)
	return err
}

func hiddenMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrForbidden
	}
	return err
}

func uniqueViolation(err error) bool {
	var state interface{ SQLState() string }
	return errors.As(err, &state) && state.SQLState() == "23505"
}
