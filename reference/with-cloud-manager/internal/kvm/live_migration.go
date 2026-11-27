package kvm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
)

type LiveMigrationInput struct {
	ID     string `json:"migration_id"`
	Target string `json:"target_host"`
}

type LiveMigration struct {
	LiveMigrationInput
	VM               string     `json:"vm_id"`
	Placement        string     `json:"placement_id"`
	Source           string     `json:"source_host"`
	SourceGeneration int64      `json:"source_generation"`
	TargetGeneration int64      `json:"target_generation"`
	VMRevision       int64      `json:"vm_revision"`
	Revision         int64      `json:"revision"`
	Status           string     `json:"status"`
	Requester        string     `json:"requester"`
	Reviewer         string     `json:"reviewer,omitempty"`
	RequestedAt      time.Time  `json:"requested_at"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	PreparedAt       *time.Time `json:"prepared_at,omitempty"`
	DispatchedAt     *time.Time `json:"dispatched_at,omitempty"`
	DepartedAt       *time.Time `json:"source_departed_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	AbortedAt        *time.Time `json:"aborted_at,omitempty"`
	AbortedBy        string     `json:"aborted_by,omitempty"`
	AbortEvidence    string     `json:"abort_evidence_sha256,omitempty"`
	NetworkSHA256    string     `json:"network_sha256,omitempty"`
	MAC              string     `json:"source_mac,omitempty"`
	requesterVersion int64
	reviewerVersion  int64
}

const liveMigrationKey = `(tenant_id,project_id,site_id,vm_id,migration_id)=($1,$2,$3,$4::uuid,$5)`
const liveMigrationColumns = `migration_id,target_host,vm_id::text,placement_id,source_host,source_generation,target_generation,
	vm_revision,revision,status,requester_id,COALESCE(reviewer_id,''),requested_at,reviewed_at,prepared_at,
	dispatched_at,source_departed_at,completed_at,network_sha256,source_mac,requester_revision,COALESCE(reviewer_revision,0),
	aborted_at,COALESCE(aborted_by,''),abort_evidence`
const activeLiveMigration = `status IN ('pending','approved','dispatched','source_departed')`

func scanLiveMigration(row interface{ Scan(...any) error }) (LiveMigration, error) {
	var m LiveMigration
	err := row.Scan(&m.ID, &m.Target, &m.VM, &m.Placement, &m.Source, &m.SourceGeneration, &m.TargetGeneration,
		&m.VMRevision, &m.Revision, &m.Status, &m.Requester, &m.Reviewer, &m.RequestedAt, &m.ReviewedAt, &m.PreparedAt,
		&m.DispatchedAt, &m.DepartedAt, &m.CompletedAt, &m.NetworkSHA256, &m.MAC, &m.requesterVersion, &m.reviewerVersion,
		&m.AbortedAt, &m.AbortedBy, &m.AbortEvidence)
	return m, err
}

func readLiveMigration(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, vm, id string) (LiveMigration, error) {
	return scanLiveMigration(tx.QueryRowContext(ctx, `SELECT `+liveMigrationColumns+` FROM cmp_kvm_live_migrations WHERE `+liveMigrationKey,
		actor.Tenant, actor.Project, site, vm, id))
}

func (s Store) GetLiveMigration(ctx context.Context, token string, scope identity.Scope, site, vm, id string) (LiveMigration, error) {
	if !hostUUID.MatchString(vm) || !identifier.MatchString(id) {
		return LiveMigration{}, ErrInvalid
	}
	var result LiveMigration
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result, err = readLiveMigration(ctx, tx, actor, site, vm, id)
		return hiddenMissing(err)
	})
	return result, err
}

func (s Store) RequestLiveMigration(ctx context.Context, token string, scope identity.Scope, site, vm string, revision int64, input LiveMigrationInput) (LiveMigration, error) {
	if !hostUUID.MatchString(vm) || revision < 1 || !identifier.MatchString(input.ID) || !identifier.MatchString(input.Target) {
		return LiveMigration{}, ErrInvalid
	}
	var result LiveMigration
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		prior, found, err := replayLiveMigration(ctx, tx, actor, site, vm, revision, input)
		if found || err != nil {
			result = prior
			return err
		}
		current, err := readVM(ctx, tx, actor, site, vm, false)
		if err != nil {
			return err
		}
		source, target, err := lockLiveMigrationHosts(ctx, tx, actor, site, current.Host, input.Target)
		if err != nil {
			return err
		}
		current, err = readVM(ctx, tx, actor, site, vm, true)
		if err != nil {
			return err
		}
		prior, found, err = replayLiveMigration(ctx, tx, actor, site, vm, revision, input)
		if found || err != nil {
			result = prior
			return err
		}
		if current.Revision != revision {
			return ErrStale
		}
		if err := checkLiveMigrationBinding(current.VM, source, target); err != nil {
			return err
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
			return err
		}
		result, err = scanLiveMigration(tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_live_migrations
			(tenant_id,project_id,site_id,vm_id,migration_id,placement_id,source_host,target_host,source_generation,target_generation,
			vm_revision,requester_id,requester_revision)
			VALUES ($1,$2,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT DO NOTHING RETURNING `+liveMigrationColumns, actor.Tenant, actor.Project, site, vm, input.ID, current.Placement,
			source.ID, target.ID, source.Generation, target.Generation, revision, actor.Principal, member.Revision))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	})
	if err != nil {
		return LiveMigration{}, err
	}
	return result, nil
}

func replayLiveMigration(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, vm string, revision int64, input LiveMigrationInput) (LiveMigration, bool, error) {
	prior, err := readLiveMigration(ctx, tx, actor, site, vm, input.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return LiveMigration{}, false, nil
	}
	if err != nil {
		return LiveMigration{}, false, err
	}
	if prior.LiveMigrationInput != input || prior.VMRevision != revision || prior.Requester != actor.Principal {
		return LiveMigration{}, true, ErrConflict
	}
	return prior, true, nil
}

func lockLiveMigrationHosts(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, source, target string) (Host, Host, error) {
	if source == target {
		return Host{}, Host{}, ErrConflict
	}
	first, second := min(source, target), max(source, target)
	left, err := readHost(ctx, tx, actor, site, first, true)
	if err != nil {
		return Host{}, Host{}, err
	}
	right, err := readHost(ctx, tx, actor, site, second, true)
	if first != source {
		return right, left, err
	}
	return left, right, err
}

func checkLiveMigrationBinding(vm VM, source, target Host) error {
	if !liveMigrationSourceMatches(vm, source) || vm.SharedStoragePool != target.SharedStoragePool ||
		vm.StoragePool != target.StoragePool || target.FenceState != "" {
		return ErrConflict
	}
	for _, network := range target.Networks {
		if network == vm.Network {
			return nil
		}
	}
	return ErrConflict
}

func liveMigrationSourceMatches(vm VM, source Host) bool {
	return vm.Host == source.ID && vm.Generation == source.Generation && vm.State == "running" && vm.Observation == "running" &&
		vm.SharedStoragePool != "" && vm.SharedStoragePool == source.SharedStoragePool && vm.StoragePool == source.StoragePool &&
		vm.Address == "" && vm.ImageID != "" && vm.Capacity.GPU == 0 && source.FenceState == ""
}
