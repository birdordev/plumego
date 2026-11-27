package kvm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

var (
	errEvacuationPlanAuthorization = errors.New("evacuation plan authorization changed")
	errEvacuationPlanSource        = errors.New("evacuation plan source changed")
	errEvacuationPlanRejoin        = errors.New("evacuation plan source rejoin pending")
)

func (s Store) RunEvacuationController(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		worked, err := s.DispatchEvacuationPlan(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
	return nil
}

func (s Store) DispatchEvacuationPlan(parent context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var worked bool
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(tx *sql.Tx) error {
		var actor identity.Scope
		var site, host, id string
		err := tx.QueryRowContext(ctx, `SELECT tenant_id,project_id,site_id,source_host,plan_id,requester_id
			FROM cmp_kvm_evacuation_plans
			WHERE next_attempt_at IS NOT NULL AND next_attempt_at<=statement_timestamp()
			ORDER BY next_attempt_at,tenant_id,project_id,site_id,source_host,plan_id LIMIT 1 FOR UPDATE SKIP LOCKED`).
			Scan(&actor.Tenant, &actor.Project, &site, &host, &id, &actor.Principal)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		worked = true
		plan, err := readEvacuationPlan(ctx, tx, actor, site, host, id)
		if err != nil {
			return err
		}
		return dispatchClaimedEvacuationPlan(ctx, tx, actor, site, plan)
	})
	return worked && err == nil, err
}

func dispatchClaimedEvacuationPlan(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, plan EvacuationPlan) error {
	// 批次失败时回收所有部分写入，同时保留已锁定的预案以记录下一步。
	if _, err := tx.ExecContext(ctx, "SAVEPOINT evacuation_batch"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout='2s'"); err != nil {
		return err
	}
	batch, attemptErr := dispatchEvacuationBatch(ctx, tx, actor, site, plan)
	status, reason := "submitted", ""
	if attemptErr != nil {
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT evacuation_batch"); err != nil {
			return errors.Join(attemptErr, err)
		}
		status, reason = evacuationPlanFailure(attemptErr)
	}
	_, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_evacuation_plans
		SET status=$6,last_result=$7,batch_id=NULLIF($8,''),
		    next_attempt_at=CASE WHEN $6='armed' THEN clock_timestamp()+interval '30 seconds' END,
		    finished_at=CASE WHEN $6<>'armed' THEN clock_timestamp() END
		WHERE `+evacuationPlanKey, actor.Tenant, actor.Project, site, plan.SourceHost, plan.ID, status, reason, batch)
	return err
}

func dispatchEvacuationBatch(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, plan EvacuationPlan) (string, error) {
	if err := lockEvacuationBatchNetworks(ctx, tx, actor, site, plan.Items); err != nil {
		return "", err
	}
	source, err := readHost(ctx, tx, actor, site, plan.SourceHost, true)
	if err != nil {
		return "", err
	}
	if source.Generation != plan.SourceFenceGeneration {
		return "", errEvacuationPlanSource
	}
	switch source.FenceState {
	case "rejoin_pending":
		return "", errEvacuationPlanRejoin
	case "fenced":
	default:
		return "", errEvacuationPlanSource
	}
	member := identity.Membership{Revision: plan.requesterRevision}
	if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			return "", errEvacuationPlanAuthorization
		}
		return "", err
	}
	if err := validateEvacuationPlanItems(ctx, tx, actor, source, source.Generation, plan.Items); err != nil {
		return "", err
	}
	input := EvacuationBatchInput{ID: "plan-" + rand.Text(), SourceFenceGeneration: source.Generation, Items: plan.Items}
	body, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	_, err = insertEvacuationBatch(ctx, tx, actor, member, source, input, sha256.Sum256(body))
	if err != nil {
		return "", err
	}
	return input.ID, nil
}

func evacuationPlanFailure(err error) (status, reason string) {
	switch {
	case errors.Is(err, errEvacuationPlanAuthorization):
		return "failed", "authorization_changed"
	case errors.Is(err, errEvacuationPlanSource):
		return "failed", "source_changed"
	case errors.Is(err, errEvacuationPlanRejoin):
		return "armed", "source_rejoin_pending"
	case errors.Is(err, ErrNoCapacity):
		return "armed", "capacity_unavailable"
	case errors.Is(err, ErrNoVerifiedTarget):
		return "armed", "verified_target_unavailable"
	case errors.Is(err, ErrStale), errors.Is(err, ErrConflict), errors.Is(err, identity.ErrForbidden):
		return "failed", "vm_or_backup_changed"
	default:
		return "armed", "storage_retry"
	}
}
