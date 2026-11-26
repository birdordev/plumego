package kvm

import (
	"context"
	"database/sql"
	_ "embed"
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

//go:embed 0111_kvm_placement.sql
var schema string

//go:embed 0112_kvm_vms.sql
var vmSchema string

//go:embed 0113_kvm_vm_shutdown.sql
var shutdownSchema string

//go:embed 0114_kvm_vm_retirement.sql
var retirementSchema string

//go:embed 0115_kvm_ipam.sql
var ipamSchema string

//go:embed 0116_kvm_boot_images.sql
var bootImageSchema string

func kvmMigrations() []struct{ version, schema string } {
	return []struct{ version, schema string }{
		{Migration, schema}, {VMMigration, vmSchema}, {ShutdownMigration, shutdownSchema},
		{RetirementMigration, retirementSchema}, {IPAMMigration, ipamSchema}, {BootImageMigration, bootImageSchema},
	}
}

func RequiredMigrations() []string {
	migrations := kvmMigrations()
	versions := make([]string, 0, len(migrations))
	for _, migration := range migrations {
		versions = append(versions, migration.version)
	}
	return versions
}

func Migrate(ctx context.Context, db *sql.DB) error {
	for _, migration := range kvmMigrations() {
		if err := dbschema.Apply(ctx, db, migration.version, migration.schema); err != nil {
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
