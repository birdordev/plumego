package kvm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
)

type ColdMigrationInput struct {
	ID                    string `json:"migration_id"`
	Placement             string `json:"target_placement_id,omitempty"`
	Image                 string `json:"image_id"`
	Evidence              string `json:"evidence_sha256"`
	SourceFenceGeneration int64  `json:"source_fence_generation,omitempty"`
}

type ColdMigration struct {
	ColdMigrationInput
	VM                 string     `json:"vm_id"`
	SourceHost         string     `json:"source_host"`
	SourcePlacement    string     `json:"source_placement_id"`
	TargetHost         string     `json:"target_host"`
	TargetGeneration   int64      `json:"target_generation"`
	VMRevision         int64      `json:"vm_revision"`
	Status             string     `json:"status"`
	Requester          string     `json:"requester"`
	Reviewer           string     `json:"reviewer,omitempty"`
	RequestedAt        time.Time  `json:"requested_at"`
	ReviewedAt         *time.Time `json:"reviewed_at,omitempty"`
	CleanupSHA256      string     `json:"cleanup_sha256,omitempty"`
	CleanupReviewer    string     `json:"cleanup_reviewer,omitempty"`
	CleanupAt          *time.Time `json:"cleanup_at,omitempty"`
	AutomaticPlacement bool       `json:"automatic_placement,omitempty"`
	requesterVersion   int64
}

type ColdMigrationView struct {
	Migration *ColdMigration `json:"migration"`
}

const coldMigrationColumns = `migration_id,target_placement_id,image_id,evidence_sha256,vm_id::text,
	source_host,source_placement_id,target_host,target_generation,vm_revision,status,
	requester_id,COALESCE(reviewer_id,''),requested_at,reviewed_at,requester_revision,
	source_fence_generation,cleanup_sha256,COALESCE(cleanup_reviewer_id,''),cleanup_at,automatic_placement`

const coldMigrationSelect = `SELECT ` + coldMigrationColumns + ` FROM cmp_kvm_cold_migrations
	WHERE (tenant_id,project_id,site_id,vm_id)=($1,$2,$3,$4::uuid)`

func scanColdMigration(row interface{ Scan(...any) error }) (ColdMigration, error) {
	var m ColdMigration
	err := row.Scan(&m.ID, &m.Placement, &m.Image, &m.Evidence, &m.VM, &m.SourceHost, &m.SourcePlacement,
		&m.TargetHost, &m.TargetGeneration, &m.VMRevision, &m.Status, &m.Requester, &m.Reviewer,
		&m.RequestedAt, &m.ReviewedAt, &m.requesterVersion,
		&m.SourceFenceGeneration, &m.CleanupSHA256, &m.CleanupReviewer, &m.CleanupAt, &m.AutomaticPlacement)
	return m, err
}

func (s Store) GetColdMigration(ctx context.Context, token string, scope identity.Scope, site, id string) (ColdMigrationView, error) {
	if !hostUUID.MatchString(id) {
		return ColdMigrationView{}, ErrInvalid
	}
	var result ColdMigrationView
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if _, err := readVM(ctx, tx, actor, site, id, false); err != nil {
			return err
		}
		m, err := scanColdMigration(tx.QueryRowContext(ctx, coldMigrationSelect+` ORDER BY requested_at DESC,migration_id DESC LIMIT 1`,
			actor.Tenant, actor.Project, site, id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err == nil {
			result.Migration = &m
		}
		return err
	})
	return result, err
}

func (s Store) RequestColdMigration(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64, input ColdMigrationInput) (ColdMigration, error) {
	automatic := input.Placement == "" && input.SourceFenceGeneration > 0
	if !hostUUID.MatchString(id) || revision < 1 || !identifier.MatchString(input.ID) ||
		(!automatic && !placementKey.MatchString(input.Placement)) || !identifier.MatchString(input.Image) ||
		!fenceEvidence.MatchString(input.Evidence) || input.SourceFenceGeneration < 0 {
		return ColdMigration{}, ErrInvalid
	}
	var result ColdMigration
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		v, err := readVM(ctx, tx, actor, site, id, false)
		if err != nil {
			return err
		}
		prior, found, err := replayColdMigration(ctx, tx, actor, site, id, revision, input)
		if found || err != nil {
			result = prior
			return err
		}
		if automatic {
			result, err = requestAutomaticEvacuation(ctx, tx, actor, member, site, v, revision, input)
			return err
		}
		var source Host
		if input.SourceFenceGeneration > 0 {
			source, err = readHost(ctx, tx, actor, site, v.Host, false)
			if err != nil {
				return err
			}
		}
		if err := checkColdMigrationSource(ctx, tx, v, source, revision, input.SourceFenceGeneration); err != nil {
			return err
		}
		target, err := readMigrationPlacement(ctx, tx, actor, site, input.Placement, actor.Principal)
		if err != nil {
			return err
		}
		hosts := ColdMigration{SourceHost: v.Host, TargetHost: target.Host}
		host, source, err := lockColdMigrationHosts(ctx, tx, actor, site, hosts, input.SourceFenceGeneration > 0)
		if err != nil {
			return err
		}
		v, err = readVM(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		prior, found, err = replayColdMigration(ctx, tx, actor, site, id, revision, input)
		if found || err != nil {
			result = prior
			return err
		}
		if err := checkColdMigrationSource(ctx, tx, v, source, revision, input.SourceFenceGeneration); err != nil {
			return err
		}
		target, err = readMigrationPlacement(ctx, tx, actor, site, input.Placement, actor.Principal)
		if err != nil {
			return err
		}
		if err := checkColdMigrationTarget(v, target, host, input.SourceFenceGeneration > 0); err != nil {
			return err
		}
		if _, err := reserveBootVolume(ctx, tx, actor, site, target, VMInput{ImageID: input.Image}); err != nil {
			return err
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
			return err
		}
		result, err = insertColdMigration(ctx, tx, actor, member, site, v, target, input)
		return err
	})
	if err != nil {
		return ColdMigration{}, err
	}
	return result, nil
}

func insertColdMigration(ctx context.Context, tx *sql.Tx, actor identity.Scope, member identity.Membership, site string,
	vm vmRecord, target Placement, input ColdMigrationInput) (ColdMigration, error) {
	result, err := scanColdMigration(tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_cold_migrations
		(vm_id,migration_id,tenant_id,project_id,site_id,source_host,target_host,source_placement_id,target_placement_id,
		 target_generation,vm_revision,image_id,evidence_sha256,requester_id,requester_revision,source_fence_generation,automatic_placement)
		VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT DO NOTHING RETURNING `+coldMigrationColumns,
		vm.ID, input.ID, actor.Tenant, actor.Project, site, vm.Host, target.Host, vm.Placement, target.ID,
		target.Generation, vm.Revision, input.Image, input.Evidence, actor.Principal, member.Revision, input.SourceFenceGeneration, input.Placement == ""))
	if errors.Is(err, sql.ErrNoRows) {
		return ColdMigration{}, ErrConflict
	}
	return result, err
}

func replayColdMigration(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, vm string, revision int64, input ColdMigrationInput) (ColdMigration, bool, error) {
	prior, err := readColdMigration(ctx, tx, actor, site, vm, input.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return ColdMigration{}, false, nil
	}
	if err != nil {
		return ColdMigration{}, false, err
	}
	saved := prior.ColdMigrationInput
	if prior.AutomaticPlacement {
		saved.Placement = ""
	}
	if saved != input || prior.VMRevision != revision || prior.Requester != actor.Principal {
		return ColdMigration{}, true, ErrConflict
	}
	return prior, true, nil
}

func readColdMigration(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, vm, id string) (ColdMigration, error) {
	return scanColdMigration(tx.QueryRowContext(ctx, coldMigrationSelect+` AND migration_id=$5`,
		actor.Tenant, actor.Project, site, vm, id))
}

func readMigrationPlacement(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, id, requester string) (Placement, error) {
	result, err := scanPlacement(tx.QueryRowContext(ctx, "SELECT "+placementColumns+` FROM cmp_kvm_placements
		WHERE (tenant_id,project_id,site_id,placement_id,requester_id)=($1,$2,$3,$4,$5)`,
		actor.Tenant, actor.Project, site, id, requester))
	return result, hiddenMissing(err)
}

func checkRetainedVMSource(ctx context.Context, tx *sql.Tx, v vmRecord, revision int64) error {
	if v.Revision != revision {
		return ErrStale
	}
	if (v.State != "retired" && v.State != "rejected") || v.Observation != "absent" {
		return ErrConflict
	}
	var retained bool
	err := tx.QueryRowContext(ctx, `SELECT boot_volume_retained FROM cmp_kvm_placements WHERE placement_id=$1`, v.Placement).Scan(&retained)
	if err == nil && !retained {
		return ErrConflict
	}
	return err
}

func checkColdMigrationSource(ctx context.Context, tx *sql.Tx, v vmRecord, host Host, revision, generation int64) error {
	if generation > 0 {
		if v.Revision != revision {
			return ErrStale
		}
		return checkFencedEvacuationSource(ctx, tx, v, host, generation)
	}
	return checkRetainedVMSource(ctx, tx, v, revision)
}

func checkColdMigrationTarget(v vmRecord, target Placement, host Host, evacuation bool) error {
	if target.Generation != host.Generation {
		return ErrStale
	}
	if target.State != "held" || target.Host == v.Host || target.Capacity != v.Capacity ||
		target.Network != v.Network || target.Capacity.GPU != 0 ||
		host.FenceState != "" || host.Draining {
		return ErrConflict
	}
	if evacuation {
		if target.EvacuationVM != v.ID || target.Address != "" {
			return ErrConflict
		}
	} else if target.EvacuationVM != "" || target.Address != v.Address {
		return ErrConflict
	}
	if host.ReportedAt == nil {
		return ErrNoCapacity
	}
	return nil
}
