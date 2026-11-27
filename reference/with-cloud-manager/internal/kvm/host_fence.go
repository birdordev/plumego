package kvm

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"with-cloud-manager/internal/identity"
)

var fenceEvidence = regexp.MustCompile(`^[0-9a-f]{64}$`)

type HostFenceChange struct {
	Action           string `json:"action"`
	UUID             string `json:"uuid"`
	EvidenceSHA256   string `json:"evidence_sha256,omitempty"`
	ReplacementAgent string `json:"replacement_agent,omitempty"`
}

type HostFenceEvent struct {
	Revision         int64     `json:"revision"`
	Generation       int64     `json:"generation"`
	Actor            string    `json:"actor"`
	Action           string    `json:"action"`
	EvidenceSHA256   string    `json:"evidence_sha256,omitempty"`
	ReplacementAgent string    `json:"replacement_agent,omitempty"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type HostFenceHistory struct {
	Host   Host             `json:"host"`
	Events []HostFenceEvent `json:"events"`
}

type fenceTransition struct {
	permission identity.Permission
	from, to   string
}

func (c HostFenceChange) transition() (fenceTransition, bool) {
	evidence := fenceEvidence.MatchString(c.EvidenceSHA256)
	switch c.Action {
	case "request":
		return fenceTransition{identity.Manage, "", "pending"}, c.ReplacementAgent == "" && c.EvidenceSHA256 == ""
	case "confirm":
		return fenceTransition{identity.Approve, "pending", "fenced"}, c.ReplacementAgent == "" && evidence
	case "request-rejoin":
		return fenceTransition{identity.Manage, "fenced", "rejoin_pending"}, identifier.MatchString(c.ReplacementAgent) && evidence
	case "approve-rejoin":
		return fenceTransition{identity.Approve, "rejoin_pending", ""}, c.ReplacementAgent == "" && c.EvidenceSHA256 == ""
	case "reject-rejoin":
		return fenceTransition{identity.Approve, "rejoin_pending", "fenced"}, c.ReplacementAgent == "" && c.EvidenceSHA256 == ""
	default:
		return fenceTransition{}, false
	}
}

func (s Store) ChangeHostFence(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64, change HostFenceChange) (Host, error) {
	transition, valid := change.transition()
	if !valid || revision < 1 || !identifier.MatchString(id) || !hostUUID.MatchString(change.UUID) {
		return Host{}, ErrInvalid
	}
	var result Host
	err := s.inProject(ctx, token, scope, site, transition.permission, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		host, err := readHost(ctx, tx, actor, site, id, true)
		if err != nil {
			return err
		}
		if host.Revision != revision {
			return ErrStale
		}
		if change.UUID != host.UUID || host.FenceState != transition.from {
			return ErrConflict
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(transition.permission), member.Revision); err != nil {
			return err
		}
		replacement, err := prepareHostFenceChange(ctx, tx, actor, host, change)
		if err != nil {
			return err
		}
		result, err = updateHostFence(ctx, tx, actor, host, transition.to, replacement, change.Action == "request" || replacement != "")
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cmp_kvm_host_fence_events
			(tenant_id,project_id,site_id,host_id,host_revision,host_generation,actor_id,actor_revision,action,evidence_sha256,replacement_agent)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, actor.Tenant, actor.Project, site, id,
			result.Revision, result.Generation, actor.Principal, member.Revision, change.Action, change.EvidenceSHA256, change.ReplacementAgent)
		if err == nil && change.Action == "confirm" {
			return activateEvacuationPlan(ctx, tx, actor, result)
		}
		return err
	})
	if err != nil {
		return Host{}, err
	}
	return result, nil
}

func updateHostFence(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, next, replacement string, advanceGeneration bool) (Host, error) {
	result, err := scanHost(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_hosts
		SET fence_state=$5,revision=revision+1,generation=generation+CASE WHEN $7::boolean THEN 1 ELSE 0 END,
		    agent_id=COALESCE(NULLIF($6,''),agent_id),draining=true,sequence=0,reported_at=NULL,
		    vcpu=0,memory_mib=0,disk_mib=0,gpu=0
		WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4) RETURNING `+hostColumns,
		actor.Tenant, actor.Project, host.Site, host.ID, next, replacement, advanceGeneration))
	if uniqueViolation(err) {
		return Host{}, ErrConflict
	}
	return result, err
}

func prepareHostFenceChange(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, change HostFenceChange) (string, error) {
	if change.Action == "request" {
		return "", nil
	}
	if change.Action == "request-rejoin" {
		return "", checkReplacementFenceAgent(ctx, tx, actor, host, change.ReplacementAgent)
	}
	var requester, replacement string
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT actor_id,actor_revision,replacement_agent FROM cmp_kvm_host_fence_events
		WHERE (tenant_id,project_id,site_id,host_id,host_revision)=($1,$2,$3,$4,$5)
		  AND action=$6`, actor.Tenant, actor.Project, host.Site, host.ID, host.Revision,
		fenceRequestAction(change.Action)).Scan(&requester, &revision, &replacement)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrConflict
	}
	if err != nil {
		return "", err
	}
	if requester == actor.Principal {
		return "", identity.ErrForbidden
	}
	if change.Action != "approve-rejoin" {
		return "", nil
	}
	if err := checkVMGrant(ctx, tx, actor, requester, string(identity.Manage), revision); err != nil {
		return "", err
	}
	if err := checkReplacementFenceAgent(ctx, tx, actor, host, replacement); err != nil {
		return "", err
	}
	if err := checkFenceRejoinWork(ctx, tx, actor, host); err != nil {
		return "", err
	}
	return replacement, nil
}

func fenceRequestAction(review string) string {
	if review == "confirm" {
		return "request"
	}
	return "request-rejoin"
}

func checkReplacementFenceAgent(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, replacement string) error {
	if replacement == host.Agent {
		return ErrConflict
	}
	return checkAgent(ctx, tx, actor, replacement)
}

func checkFenceRejoinWork(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host) error {
	if err := ensureHostNoDispatchedVolumeCleanup(ctx, tx, actor, host); err != nil {
		return err
	}
	if err := ensureHostNotMigrating(ctx, tx, actor, host); err != nil {
		return err
	}
	var unresolved bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1`+fenceBlockingVMs+`)
		OR EXISTS (SELECT 1`+pendingEvacuationCleanup+`)`,
		actor.Tenant, actor.Project, host.Site, host.ID).Scan(&unresolved)
	if err == nil && unresolved {
		return ErrConflict
	}
	return err
}
