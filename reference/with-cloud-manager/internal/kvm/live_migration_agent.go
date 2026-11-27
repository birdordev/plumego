package kvm

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

const liveMigrationPreparationMaxAge = 90 * time.Second

type LiveMigrationCommand struct {
	AgentIdentity
	Action        string `json:"action"`
	Revision      int64  `json:"revision,omitempty"`
	NetworkSHA256 string `json:"network_sha256,omitempty"`
	MAC           string `json:"source_mac,omitempty"`
}

type LiveMigrationWork struct {
	Migration  LiveMigration `json:"migration"`
	VM         VM            `json:"vm"`
	TargetUUID string        `json:"target_uuid"`
}

func (cmd LiveMigrationCommand) valid() bool {
	if !hostUUID.MatchString(cmd.UUID) || cmd.Generation < 1 {
		return false
	}
	switch cmd.Action {
	case "inspect":
		return cmd.Revision == 0 && cmd.NetworkSHA256 == "" && cmd.MAC == ""
	case "prepare":
		return cmd.Revision > 0 && fenceEvidence.MatchString(cmd.NetworkSHA256) && cmd.MAC == ""
	case "arm", "complete":
		mac, err := net.ParseMAC(cmd.MAC)
		return cmd.Revision > 0 && fenceEvidence.MatchString(cmd.NetworkSHA256) &&
			err == nil && len(mac) == 6 && mac.String() == cmd.MAC && mac[0]&1 == 0
	case "depart":
		return cmd.Revision > 0 && cmd.NetworkSHA256 == "" && cmd.MAC == ""
	default:
		return false
	}
}

func (s Store) ApplyLiveMigrationCommand(ctx context.Context, token string, scope identity.Scope, site, host, vm, id string,
	cmd LiveMigrationCommand) (LiveMigrationWork, error) {
	if !identifier.MatchString(site) || !identifier.MatchString(host) || !hostUUID.MatchString(vm) || !identifier.MatchString(id) || !cmd.valid() {
		return LiveMigrationWork{}, ErrInvalid
	}
	var result LiveMigrationWork
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(tx *sql.Tx) error {
		actor, err := identity.NewStore(tx).AuthenticateService(ctx, token)
		if err != nil {
			return err
		}
		if actor.Tenant != scope.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = scope.Project
		work, err := loadLiveMigrationWork(ctx, tx, actor, site, vm, id)
		if err != nil {
			return err
		}
		sourceSide := host == work.source.ID
		local := work.target
		if sourceSide {
			local = work.source
		}
		if host != local.ID || actor.Principal != local.Agent || cmd.UUID != local.UUID {
			return identity.ErrForbidden
		}
		if err := checkAgent(ctx, tx, actor, local.Agent); err != nil {
			return err
		}
		if cmd.Generation != local.Generation {
			return ErrStale
		}
		if local.FenceState != "" {
			return ErrConflict
		}
		result = LiveMigrationWork{Migration: work.migration, VM: work.vm, TargetUUID: work.target.UUID}
		if cmd.Action == "inspect" {
			if work.migration.Status != "completed" && work.migration.Status != "rejected" && work.migration.Status != "aborted" {
				return work.checkCurrent()
			}
			return nil
		}
		if cmd.Revision != work.migration.Revision {
			return ErrStale
		}
		if err := work.checkCurrent(); err != nil {
			return err
		}
		if err := applyLiveMigrationCommand(ctx, tx, actor, site, sourceSide, work, cmd); err != nil {
			return err
		}
		result.Migration, err = readLiveMigration(ctx, tx, actor, site, vm, id)
		if err != nil {
			return err
		}
		if cmd.Action == "complete" {
			current, err := readVM(ctx, tx, actor, site, vm, false)
			result.VM = current.VM
			return err
		}
		return nil
	})
	if err != nil {
		return LiveMigrationWork{}, err
	}
	return result, nil
}

func applyLiveMigrationCommand(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, source bool,
	work liveMigrationWork, cmd LiveMigrationCommand) error {
	m := work.migration
	args := []any{actor.Tenant, actor.Project, site, m.VM, m.ID}
	switch cmd.Action {
	case "prepare":
		if source || m.Status != "approved" || work.target.Draining {
			return ErrConflict
		}
		_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_live_migrations
			SET prepared_at=clock_timestamp(),network_sha256=$6,revision=revision+1 WHERE `+liveMigrationKey+`
			  AND (prepared_at IS NULL OR prepared_at<=statement_timestamp()-interval '30 seconds' OR network_sha256<>$6)`,
			append(args, cmd.NetworkSHA256)...)
		return err
	case "arm":
		if !source || m.Status != "approved" || m.NetworkSHA256 != cmd.NetworkSHA256 {
			return ErrConflict
		}
		return armLiveMigration(ctx, tx, actor, site, work, cmd)
	case "depart":
		if !source || m.Status != "dispatched" {
			return ErrConflict
		}
		_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_live_migrations
			SET status='source_departed',source_departed_at=clock_timestamp(),revision=revision+1 WHERE `+liveMigrationKey, args...)
		return err
	case "complete":
		if source || m.Status != "source_departed" || m.NetworkSHA256 != cmd.NetworkSHA256 || m.MAC != cmd.MAC {
			return ErrConflict
		}
		return completeLiveMigration(ctx, tx, actor, site, work)
	default:
		return ErrInvalid
	}
}

func armLiveMigration(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, work liveMigrationWork, cmd LiveMigrationCommand) error {
	m := work.migration
	if m.PreparedAt == nil || work.target.Draining {
		return ErrConflict
	}
	if err := checkVMGrant(ctx, tx, actor, m.Requester, string(identity.Manage), m.requesterVersion); err != nil {
		return err
	}
	if err := checkVMGrant(ctx, tx, actor, m.Reviewer, string(identity.Approve), m.reviewerVersion); err != nil {
		return err
	}
	if err := checkAgent(ctx, tx, actor, work.target.Agent); err != nil {
		return err
	}
	ok, err := fitsCurrentCapacity(ctx, tx, actor, work.target, Capacity{})
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoCapacity
	}
	var dispatch string
	err = tx.QueryRowContext(ctx, `UPDATE cmp_kvm_live_migrations
		SET status='dispatched',dispatched_at=clock_timestamp(),source_mac=$6,revision=revision+1
		WHERE `+liveMigrationKey+` AND prepared_at<=statement_timestamp()
		  AND prepared_at>statement_timestamp()-$7*interval '1 second' RETURNING migration_id`,
		actor.Tenant, actor.Project, site, m.VM, m.ID, cmd.MAC, int64(liveMigrationPreparationMaxAge/time.Second)).Scan(&dispatch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	return err
}

func completeLiveMigration(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, work liveMigrationWork) error {
	m := work.migration
	var placement string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_kvm_placements SET host_id=$3,generation=$4
		WHERE (placement_id,host_id)=($1,$2) AND consumed_at IS NOT NULL AND released_at IS NULL
		RETURNING placement_id`, m.Placement, m.Source, m.Target, work.target.Generation).Scan(&placement)
	if errors.Is(err, sql.ErrNoRows) || uniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET revision=revision+1,updated_at=clock_timestamp()
		WHERE vm_id=$1::uuid`, m.VM); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cmp_kvm_live_migrations
		SET status='completed',completed_at=clock_timestamp(),revision=revision+1 WHERE `+liveMigrationKey,
		actor.Tenant, actor.Project, site, m.VM, m.ID)
	return err
}
