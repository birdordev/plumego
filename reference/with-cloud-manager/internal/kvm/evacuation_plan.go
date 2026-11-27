package kvm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"with-cloud-manager/internal/identity"
)

type EvacuationPlanInput struct {
	ID    string                     `json:"plan_id"`
	Items []EvacuationBatchItemInput `json:"items"`
}

type EvacuationPlan struct {
	EvacuationPlanInput
	SourceHost            string     `json:"source_host"`
	SourceFenceGeneration int64      `json:"source_fence_generation"`
	HostRevision          int64      `json:"host_revision"`
	Requester             string     `json:"requester"`
	Status                string     `json:"status"`
	LastResult            string     `json:"last_result,omitempty"`
	ArmedAt               time.Time  `json:"armed_at"`
	ActivatedAt           *time.Time `json:"activated_at,omitempty"`
	NextAttemptAt         *time.Time `json:"next_attempt_at,omitempty"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
	CancelledBy           string     `json:"cancelled_by,omitempty"`
	BatchID               string     `json:"batch_id,omitempty"`
	requesterRevision     int64
}

const evacuationPlanColumns = `plan_id,items,source_host,source_fence_generation,host_revision,requester_id,
	status,last_result,armed_at,activated_at,next_attempt_at,finished_at,COALESCE(cancelled_by,''),COALESCE(batch_id,''),requester_revision`

const evacuationPlanKey = `(tenant_id,project_id,site_id,source_host,plan_id)=($1,$2,$3,$4,$5)`

func scanEvacuationPlan(row *sql.Row) (EvacuationPlan, error) {
	var plan EvacuationPlan
	var items []byte
	err := row.Scan(&plan.ID, &items, &plan.SourceHost, &plan.SourceFenceGeneration, &plan.HostRevision, &plan.Requester,
		&plan.Status, &plan.LastResult, &plan.ArmedAt, &plan.ActivatedAt, &plan.NextAttemptAt, &plan.FinishedAt,
		&plan.CancelledBy, &plan.BatchID, &plan.requesterRevision)
	if err != nil {
		return EvacuationPlan{}, err
	}
	err = json.Unmarshal(items, &plan.Items)
	return plan, err
}

func readEvacuationPlan(ctx context.Context, tx *sql.Tx, scope identity.Scope, site, host, id string) (EvacuationPlan, error) {
	return scanEvacuationPlan(tx.QueryRowContext(ctx, `SELECT `+evacuationPlanColumns+`
		FROM cmp_kvm_evacuation_plans WHERE `+evacuationPlanKey, scope.Tenant, scope.Project, site, host, id))
}

func (s Store) ArmEvacuationPlan(ctx context.Context, token string, scope identity.Scope, site, host string, revision int64, input EvacuationPlanInput) (EvacuationPlan, error) {
	if !identifier.MatchString(host) || revision < 1 || !identifier.MatchString(input.ID) || !validEvacuationItems(input.Items) {
		return EvacuationPlan{}, ErrInvalid
	}
	items, err := json.Marshal(input.Items)
	if err != nil {
		return EvacuationPlan{}, err
	}
	var result EvacuationPlan
	err = s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		source, err := readHost(ctx, tx, actor, site, host, true)
		if err != nil {
			return err
		}
		result, err = readEvacuationPlan(ctx, tx, actor, site, host, input.ID)
		if err == nil {
			if result.HostRevision != revision || result.Requester != actor.Principal || !slices.Equal(result.Items, input.Items) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if source.Revision != revision {
			return ErrStale
		}
		generation := source.Generation
		switch source.FenceState {
		case "":
			generation++
		case "pending":
		default:
			return ErrConflict
		}
		if err := validateEvacuationPlanItems(ctx, tx, actor, source, generation, input.Items); err != nil {
			return err
		}
		if err := checkEvacuationPlanOverlap(ctx, tx, actor, source, generation, items); err != nil {
			return err
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
			return err
		}
		result, err = scanEvacuationPlan(tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_evacuation_plans
			(tenant_id,project_id,site_id,source_host,plan_id,source_fence_generation,host_revision,requester_id,requester_revision,items)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING RETURNING `+evacuationPlanColumns,
			actor.Tenant, actor.Project, site, host, input.ID, generation, revision, actor.Principal, member.Revision, items))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	})
	if err != nil {
		return EvacuationPlan{}, err
	}
	return result, nil
}

func checkEvacuationPlanOverlap(ctx context.Context, tx *sql.Tx, actor identity.Scope, source Host, generation int64, items []byte) error {
	// 同一宿主机的预案登记持有源主机行锁，避免同时将一台 VM 编入两个预案。
	var overlap bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM cmp_kvm_evacuation_plans AS p
		CROSS JOIN LATERAL jsonb_array_elements(p.items) AS existing(item)
		JOIN jsonb_array_elements($6::jsonb) AS proposed(item) ON existing.item->>'vm_id'=proposed.item->>'vm_id'
		WHERE (p.tenant_id,p.project_id,p.site_id,p.source_host,p.source_fence_generation)=($1,$2,$3,$4,$5)
		  AND p.status='armed')`, actor.Tenant, actor.Project, source.Site, source.ID, generation, items).Scan(&overlap)
	if err == nil && overlap {
		return ErrConflict
	}
	return err
}

func validateEvacuationPlanItems(ctx context.Context, tx *sql.Tx, actor identity.Scope, source Host, generation int64, items []EvacuationBatchItemInput) error {
	for _, item := range items {
		vm, err := readVM(ctx, tx, actor, source.Site, item.VM, false)
		if err != nil {
			return err
		}
		if vm.Revision != item.Revision {
			return ErrStale
		}
		if vm.Capacity.GPU != 0 {
			return ErrConflict
		}
		if err := checkEvacuationVM(ctx, tx, vm, source.ID, generation); err != nil {
			return err
		}
		image, err := readBootImage(ctx, tx, actor, source.Site, item.Image)
		if err != nil {
			return err
		}
		if image.SizeBytes > vm.Capacity.DiskMiB*(1<<20) {
			return ErrConflict
		}
	}
	return nil
}

func (s Store) GetEvacuationPlan(ctx context.Context, token string, scope identity.Scope, site, host, id string) (EvacuationPlan, error) {
	if !identifier.MatchString(host) || !identifier.MatchString(id) {
		return EvacuationPlan{}, ErrInvalid
	}
	var plan EvacuationPlan
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		plan, err = readEvacuationPlan(ctx, tx, actor, site, host, id)
		return hiddenMissing(err)
	})
	return plan, err
}

func (s Store) CancelEvacuationPlan(ctx context.Context, token string, scope identity.Scope, site, host, id string) (EvacuationPlan, error) {
	if !identifier.MatchString(host) || !identifier.MatchString(id) {
		return EvacuationPlan{}, ErrInvalid
	}
	var plan EvacuationPlan
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		var err error
		plan, err = scanEvacuationPlan(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_evacuation_plans
			SET status='cancelled',cancelled_by=$6,finished_at=clock_timestamp(),next_attempt_at=NULL,last_result=''
			WHERE `+evacuationPlanKey+` AND status='armed' RETURNING `+evacuationPlanColumns,
			actor.Tenant, actor.Project, site, host, id, actor.Principal))
		if errors.Is(err, sql.ErrNoRows) {
			plan, err = readEvacuationPlan(ctx, tx, actor, site, host, id)
			if err == nil && plan.Status != "cancelled" {
				return ErrConflict
			}
		}
		if err != nil {
			return hiddenMissing(err)
		}
		return checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision)
	})
	if err != nil {
		return EvacuationPlan{}, err
	}
	return plan, nil
}

func activateEvacuationPlan(ctx context.Context, tx *sql.Tx, actor identity.Scope, source Host) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_evacuation_plans
		SET activated_at=clock_timestamp(),next_attempt_at=clock_timestamp()
		WHERE (tenant_id,project_id,site_id,source_host,source_fence_generation)=($1,$2,$3,$4,$5)
		  AND status='armed' AND activated_at IS NULL`, actor.Tenant, actor.Project, source.Site, source.ID, source.Generation)
	return err
}

func invalidateUnactivatedEvacuationPlan(ctx context.Context, tx *sql.Tx, actor identity.Scope, source Host) error {
	_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_evacuation_plans
		SET status='failed',last_result='source_changed',finished_at=clock_timestamp()
		WHERE (tenant_id,project_id,site_id,source_host,source_fence_generation)=($1,$2,$3,$4,$5)
		  AND status='armed' AND activated_at IS NULL`, actor.Tenant, actor.Project, source.Site, source.ID, source.Generation)
	return err
}
