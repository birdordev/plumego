package kvm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
)

type FencedVMRecoveryInput struct {
	ID                   string `json:"recovery_id"`
	HostUUID             string `json:"host_uuid"`
	FenceGeneration      int64  `json:"fence_generation"`
	Observed             string `json:"observed"`
	EvidenceSHA256       string `json:"evidence_sha256"`
	SecondEvidenceSHA256 string `json:"second_evidence_sha256,omitempty"`
}

func (input FencedVMRecoveryInput) valid() bool {
	if !identifier.MatchString(input.ID) || !hostUUID.MatchString(input.HostUUID) ||
		input.FenceGeneration < 1 || !fenceEvidence.MatchString(input.EvidenceSHA256) {
		return false
	}
	switch input.Observed {
	case "defined", "running":
		return input.SecondEvidenceSHA256 == ""
	case "absent":
		return fenceEvidence.MatchString(input.SecondEvidenceSHA256) && input.EvidenceSHA256 != input.SecondEvidenceSHA256
	default:
		return false
	}
}

type FencedVMRecovery struct {
	FencedVMRecoveryInput
	VM                string     `json:"vm_id"`
	Host              string     `json:"host"`
	VMRevision        int64      `json:"vm_revision"`
	SourceState       string     `json:"source_state"`
	Status            string     `json:"status"`
	ResultState       string     `json:"result_state,omitempty"`
	Requester         string     `json:"requester"`
	Reviewer          string     `json:"reviewer,omitempty"`
	RequestedAt       time.Time  `json:"requested_at"`
	ReviewedAt        *time.Time `json:"reviewed_at,omitempty"`
	requesterRevision int64
}

type FencedVMRecoveryView struct {
	Recovery *FencedVMRecovery `json:"recovery"`
}

const fencedVMRecoveryColumns = `recovery_id,host_uuid::text,fence_generation,observed,evidence_sha256,second_evidence_sha256,
	vm_id::text,host_id,vm_revision,source_state,status,result_state,
	requester_id,COALESCE(reviewer_id,''),requested_at,reviewed_at,requester_revision`

const fencedVMRecoveryQuery = `SELECT ` + fencedVMRecoveryColumns +
	` FROM cmp_kvm_fenced_vm_recoveries WHERE (tenant_id,project_id,site_id,vm_id)=($1,$2,$3,$4::uuid)`

func scanFencedVMRecovery(row *sql.Row) (FencedVMRecovery, error) {
	var r FencedVMRecovery
	err := row.Scan(&r.ID, &r.HostUUID, &r.FenceGeneration, &r.Observed, &r.EvidenceSHA256, &r.SecondEvidenceSHA256,
		&r.VM, &r.Host, &r.VMRevision, &r.SourceState, &r.Status, &r.ResultState,
		&r.Requester, &r.Reviewer, &r.RequestedAt, &r.ReviewedAt, &r.requesterRevision)
	return r, err
}

func (s Store) GetFencedVMRecovery(ctx context.Context, token string, scope identity.Scope, site, id string) (FencedVMRecoveryView, error) {
	if !hostUUID.MatchString(id) {
		return FencedVMRecoveryView{}, ErrInvalid
	}
	var result FencedVMRecoveryView
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if _, err := readVM(ctx, tx, actor, site, id, false); err != nil {
			return err
		}
		r, err := scanFencedVMRecovery(tx.QueryRowContext(ctx, fencedVMRecoveryQuery+` ORDER BY requested_at DESC,recovery_id DESC LIMIT 1`,
			actor.Tenant, actor.Project, site, id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err == nil {
			result.Recovery = &r
		}
		return err
	})
	return result, err
}

func (s Store) RequestFencedVMRecovery(ctx context.Context, token string, scope identity.Scope, site, id string, revision int64, input FencedVMRecoveryInput) (FencedVMRecovery, error) {
	if !hostUUID.MatchString(id) || revision < 1 || !input.valid() {
		return FencedVMRecovery{}, ErrInvalid
	}
	var result FencedVMRecovery
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		host, v, err := lockVMAndHost(ctx, tx, actor, site, id)
		if err != nil {
			return err
		}
		prior, err := scanFencedVMRecovery(tx.QueryRowContext(ctx, fencedVMRecoveryQuery+` AND recovery_id=$5`,
			actor.Tenant, actor.Project, site, id, input.ID))
		if err == nil {
			if prior.FencedVMRecoveryInput != input || prior.VMRevision != revision || prior.Requester != actor.Principal {
				return ErrConflict
			}
			result = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := checkFencedVMRecoveryTarget(host, v, revision, input); err != nil {
			return err
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
			return err
		}
		result, err = scanFencedVMRecovery(tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_fenced_vm_recoveries
			(vm_id,recovery_id,tenant_id,project_id,site_id,host_id,host_uuid,fence_generation,vm_revision,source_state,
			observed,evidence_sha256,second_evidence_sha256,requester_id,requester_revision)
			VALUES ($1::uuid,$2,$3,$4,$5,$6,$7::uuid,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT DO NOTHING RETURNING `+fencedVMRecoveryColumns, id, input.ID, actor.Tenant, actor.Project,
			site, host.ID, host.UUID, host.Generation, revision, v.State, input.Observed, input.EvidenceSHA256,
			input.SecondEvidenceSHA256, actor.Principal, member.Revision))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	})
	if err != nil {
		return FencedVMRecovery{}, err
	}
	return result, nil
}

func lockVMAndHost(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, id string) (Host, vmRecord, error) {
	v, err := readVM(ctx, tx, actor, site, id, false)
	if err != nil {
		return Host{}, v, err
	}
	host, err := readHost(ctx, tx, actor, site, v.Host, true)
	if err != nil {
		return host, v, err
	}
	v, err = readVM(ctx, tx, actor, site, id, true)
	if err == nil && v.Host != host.ID {
		err = ErrStale
	}
	return host, v, err
}

func checkFencedVMRecoveryTarget(host Host, v vmRecord, revision int64, input FencedVMRecoveryInput) error {
	if revision != v.Revision || input.FenceGeneration != host.Generation {
		return ErrStale
	}
	if host.UUID != input.HostUUID || host.FenceState != "fenced" || v.Generation >= host.Generation ||
		fencedVMRecoveryResult(v.State, input.Observed) == "" {
		return ErrConflict
	}
	return nil
}
