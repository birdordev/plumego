package kvm

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

type AgentIdentity struct {
	UUID       string `json:"uuid"`
	Generation int64  `json:"generation"`
}

type VMWork struct {
	Job *VM `json:"job,omitempty"`
}

type VMCommand struct {
	AgentIdentity
	Revision int64 `json:"revision"`
}

type VMObservation struct {
	VMCommand
	Observed string `json:"observed"`
}

func (s Store) withVMHost(ctx context.Context, token string, scope identity.Scope, site, id string, agent AgentIdentity,
	do func(*sql.Tx, identity.Scope, Host) error) error {
	if !identifier.MatchString(site) || !identifier.MatchString(id) || !hostUUID.MatchString(agent.UUID) || agent.Generation < 1 {
		return ErrInvalid
	}
	return dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(tx *sql.Tx) error {
		actor, err := identity.NewStore(tx).AuthenticateService(ctx, token)
		if err != nil {
			return err
		}
		if scope.Tenant != actor.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = scope.Project
		host, err := readHost(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if host.Agent != actor.Principal || host.UUID != agent.UUID {
			return identity.ErrForbidden
		}
		if host.Generation != agent.Generation {
			return ErrStale
		}
		if host.FenceState != "" {
			return ErrConflict
		}
		if err := checkAgent(ctx, tx, actor, host.Agent); err != nil {
			return err
		}
		return do(tx, actor, host)
	})
}

func (s Store) PollVM(ctx context.Context, token string, scope identity.Scope, site, id string, agent AgentIdentity) (VMWork, error) {
	var work VMWork
	err := s.withVMHost(ctx, token, scope, site, id, agent, func(tx *sql.Tx, actor identity.Scope, host Host) error {
		v, err := scanVM(tx.QueryRowContext(ctx, "SELECT "+vmColumns+vmFrom+`
			WHERE (p.tenant_id,p.project_id,p.site_id,p.host_id)=($1,$2,$3,$4)
			  AND ((p.generation=$5 AND (v.state IN ('defining','starting') OR (NOT $6 AND v.state IN ('queued','staging','staged','defined'))))
			    OR (p.generation<=$5 AND v.state IN ('shutdown_queued','stopping','retire_queued','undefining')))
			ORDER BY v.updated_at,v.vm_id LIMIT 1`, actor.Tenant, actor.Project, site, id, host.Generation, host.Draining))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err == nil {
			work.Job = &v.VM
		}
		return err
	})
	return work, err
}

func (s Store) ArmVM(ctx context.Context, token string, scope identity.Scope, site, hostID, id string, action VMCommand) (VM, error) {
	if !hostUUID.MatchString(id) || action.Revision < 1 {
		return VM{}, ErrInvalid
	}
	var result vmRecord
	err := s.withVMHost(ctx, token, scope, site, hostID, action.AgentIdentity, func(tx *sql.Tx, actor identity.Scope, host Host) error {
		v, err := readVM(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if err := matchVMHost(v.VM, host, action.Revision); err != nil {
			return err
		}
		next, event := "defining", "vm_define"
		requester, reviewer := v.requester, v.reviewer
		required := "execute"
		requesterRevision, reviewerRevision := v.requesterRevision, v.reviewerRevision
		switch v.State {
		case "queued":
			ok, err := fitsCurrentCapacity(ctx, tx, actor, host, Capacity{})
			if err != nil {
				return err
			}
			if !ok {
				return ErrNoCapacity
			}
			if v.ImageID != "" {
				next, event = "staging", "vm_stage"
			}
		case "staged":
			if v.ImageID == "" {
				return ErrConflict
			}
		case "defined":
			next, event = "starting", "vm_start"
		case "shutdown_queued":
			next, event = "stopping", "vm_shutdown"
			requester, reviewer = v.shutdownRequester, v.shutdownReviewer
			requesterRevision, reviewerRevision = v.shutdownRequesterRevision, v.shutdownReviewerRevision
		case "retire_queued":
			next, event = "undefining", "vm_undefine"
			required = "manage"
			requester, reviewer = v.retireRequester, v.retireReviewer
			requesterRevision, reviewerRevision = v.retireRequesterRevision, v.retireReviewerRevision
		default:
			return ErrConflict
		}
		if host.Draining && next != "stopping" && next != "undefining" {
			return ErrConflict
		}
		if err := checkVMGrant(ctx, tx, actor, requester, required, requesterRevision); err != nil {
			return err
		}
		if err := checkVMGrant(ctx, tx, actor, reviewer, "approve", reviewerRevision); err != nil {
			return err
		}
		if err := setVMObservation(ctx, tx, id, next, ""); err != nil {
			return err
		}
		result, err = readVM(ctx, tx, actor, site, id, false)
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, hostID, event, v.Placement)
	})
	return result.VM, err
}

func (s Store) ObserveVM(ctx context.Context, token string, scope identity.Scope, site, hostID, id string, sample VMObservation) (VM, error) {
	if !hostUUID.MatchString(id) || sample.Revision < 1 {
		return VM{}, ErrInvalid
	}
	switch sample.Observed {
	case "absent", "defined", "running", "mismatch", "staged":
	default:
		return VM{}, ErrInvalid
	}
	var result vmRecord
	err := s.withVMHost(ctx, token, scope, site, hostID, sample.AgentIdentity, func(tx *sql.Tx, actor identity.Scope, host Host) error {
		v, err := readVM(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if err := matchVMHost(v.VM, host, sample.Revision); err != nil {
			return err
		}
		next := v.State
		if sample.Observed == "staged" && v.State != "staging" {
			return ErrConflict
		}
		switch v.State {
		case "staging":
			if (sample.Observed != "staged" && sample.Observed != "mismatch") || v.ImageID == "" || host.Draining {
				return ErrConflict
			}
			if err := checkImageStageApproval(ctx, tx, actor, v); err != nil {
				return err
			}
			if sample.Observed == "staged" {
				next = "staged"
			}
		case "defining":
			if sample.Observed == "defined" {
				next = "defined"
			}
		case "starting":
			if sample.Observed == "running" {
				next = "running"
			}
		case "shutdown_queued":
			if sample.Observed != "absent" && sample.Observed != "mismatch" {
				return ErrConflict
			}
		case "stopping":
			if sample.Observed == "defined" {
				next = "stopped"
			}
		case "retire_queued":
			if sample.Observed == "defined" {
				return ErrConflict
			}
		case "undefining":
			if sample.Observed == "absent" && v.Observation == "absent" {
				next = "retired"
				if err := retainReleasedBootVolume(ctx, tx, v.Placement); err != nil {
					return err
				}
			}
		default:
			return ErrConflict
		}
		if err := setVMObservation(ctx, tx, id, next, sample.Observed); err != nil {
			return err
		}
		result, err = readVM(ctx, tx, actor, site, id, false)
		if err != nil || next == v.State {
			return err
		}
		return recordEvent(ctx, tx, actor, site, hostID, "vm_"+next, v.Placement)
	})
	return result.VM, err
}

func setVMObservation(ctx context.Context, tx *sql.Tx, id, next, evidence string) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_vms SET state=$2,observation=$3,updated_at=clock_timestamp(),
		revision=revision+CASE WHEN state<>$2 OR (state='undefining' AND observation<>$3) THEN 1 ELSE 0 END WHERE vm_id=$1::uuid`, id, next, evidence)
	return err
}

func matchVMHost(v VM, host Host, revision int64) error {
	if v.Host != host.ID {
		return identity.ErrForbidden
	}
	if v.Revision != revision {
		return ErrStale
	}
	switch v.State {
	case "shutdown_queued", "stopping", "retire_queued", "undefining":
		if v.Generation > host.Generation {
			return ErrStale
		}
	default:
		if v.Generation != host.Generation {
			return ErrStale
		}
	}
	return nil
}
