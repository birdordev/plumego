package controller

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"
)

var errDispatchContended = errors.New("inventory dispatch candidate changed or is locked")

// DispatchObservation 记录已提交的普通盘点领取。时间来自同一数据库。
type DispatchObservation struct {
	Tenant, Account, Provider, Kind string
	ScheduleID, Fence               int64
	DueAt, ClaimedAt                time.Time
}

// WithDispatchObserver 设置在领取事务提交后同步调用的观察者。
func (s Store) WithDispatchObserver(observe func(DispatchObservation)) Store {
	s.observeDispatch = observe
	return s
}

const dueAccountSchedule = `
	WITH eligible AS MATERIALIZED (
		SELECT a.tenant_id,a.account_id,head.schedule_id,head.next_at,
		       COALESCE(turn.last_turn,0) AS tenant_turn,d.last_claimed_at
		FROM cmp_cloud_accounts AS a
		LEFT JOIN cmp_inventory_account_dispatch AS d USING (tenant_id,account_id)
		LEFT JOIN cmp_inventory_tenant_dispatch AS turn USING (tenant_id)
		CROSS JOIN LATERAL (
			SELECT s.schedule_id,s.next_at FROM cmp_inventory_schedules AS s
			WHERE (s.tenant_id,s.account_id)=(a.tenant_id,a.account_id)
			  AND s.enabled AND s.kind NOT IN ('kubernetes-workload-watch','instance-idle')
			  AND s.next_at<=statement_timestamp() AND (s.lease_until IS NULL OR s.lease_until<=statement_timestamp())
			ORDER BY s.next_at,s.schedule_id LIMIT 1
		) AS head
		WHERE a.mode IN ('read_only','write_enabled')
		  AND (d.lease_until IS NULL OR d.lease_until<=statement_timestamp())
		  AND (d.cooldown_until IS NULL OR d.cooldown_until<=statement_timestamp())
	), picked AS (
		SELECT e.schedule_id,e.tenant_turn
		FROM eligible AS e
		JOIN cmp_tenants AS t USING (tenant_id)
		JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_schedules AS gate USING (schedule_id)
		ORDER BY e.tenant_turn,e.last_claimed_at NULLS FIRST,e.next_at,e.schedule_id
		LIMIT 1 FOR NO KEY UPDATE OF t,a,gate SKIP LOCKED
	)
	SELECT COALESCE((SELECT schedule_id FROM picked),0),COALESCE((SELECT tenant_turn FROM picked),0),
	       EXISTS (SELECT 1 FROM eligible)`

func (s Store) claimAccountSchedule(ctx context.Context, lease time.Duration) (job, error) {
	for _, delay := range []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond} {
		j, err := s.tryAccountSchedule(ctx, lease)
		if !errors.Is(err, errDispatchContended) {
			return j, err
		}
		// 仅在有到期计划但领取冲突时短暂重试，避免同一租户有余量时等待下一轮轮询。
		select {
		case <-ctx.Done():
			return job{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	j, err := s.tryAccountSchedule(ctx, lease)
	if errors.Is(err, errDispatchContended) {
		return job{}, sql.ErrNoRows
	}
	return j, err
}

func (s Store) tryAccountSchedule(ctx context.Context, lease time.Duration) (job, error) {
	var j job
	var dueAt, claimedAt time.Time
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var id, turn int64
		var eligible bool
		if err := tx.QueryRowContext(ctx, dueAccountSchedule).Scan(&id, &turn, &eligible); err != nil {
			return err
		}
		if id == 0 {
			if eligible {
				return errDispatchContended
			}
			return sql.ErrNoRows
		}
		var err error
		j, err = scanLeasedSchedule(tx.QueryRowContext(ctx, `
			UPDATE cmp_inventory_schedules AS s SET fence=s.fence+1,
				lease_until=clock_timestamp()+$2*interval '1 millisecond'
			FROM cmp_cloud_accounts AS a
			WHERE s.schedule_id=$1 AND (s.tenant_id,s.account_id)=(a.tenant_id,a.account_id)
			  AND a.mode IN ('read_only','write_enabled') AND s.enabled AND s.next_at<=statement_timestamp()
			  AND (s.lease_until IS NULL OR s.lease_until<=statement_timestamp())
			RETURNING s.schedule_id,s.fence,s.tenant_id,s.account_id,s.region,s.kind,s.target_id,
				a.provider,a.credential_ref,a.credential_revision,s.interval_seconds,s.failures`, id, lease.Milliseconds()))
		if errors.Is(err, sql.ErrNoRows) {
			return errDispatchContended
		}
		if err != nil {
			return err
		}

		// 取得租户与账号锁后，在新快照复核领取轮次、账号租约和冷却窗口。
		err = tx.QueryRowContext(ctx, `
			WITH leased AS (
				INSERT INTO cmp_inventory_account_dispatch AS d
					(tenant_id,account_id,last_claimed_at,schedule_id,schedule_fence,lease_until)
				SELECT tenant_id,account_id,statement_timestamp(),schedule_id,fence,lease_until
				FROM cmp_inventory_schedules WHERE schedule_id=$1
				ON CONFLICT (tenant_id,account_id) DO UPDATE
				SET last_claimed_at=EXCLUDED.last_claimed_at,schedule_id=EXCLUDED.schedule_id,
					schedule_fence=EXCLUDED.schedule_fence,lease_until=EXCLUDED.lease_until
				WHERE (d.lease_until IS NULL OR d.lease_until<=statement_timestamp())
				  AND (d.cooldown_until IS NULL OR d.cooldown_until<=statement_timestamp())
				RETURNING tenant_id,last_claimed_at
			), turned AS (
				INSERT INTO cmp_inventory_tenant_dispatch AS t (tenant_id,last_turn)
				SELECT tenant_id,nextval('cmp_inventory_dispatch_turn') FROM leased
				ON CONFLICT (tenant_id) DO UPDATE SET last_turn=EXCLUDED.last_turn WHERE t.last_turn=$2
				RETURNING tenant_id
			)
			SELECT s.next_at,l.last_claimed_at
			FROM leased AS l JOIN turned USING (tenant_id)
			JOIN cmp_inventory_schedules AS s ON s.schedule_id=$1`, id, turn).Scan(&dueAt, &claimedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return errDispatchContended
		}
		return err
	})
	if err != nil {
		return job{}, err
	}
	j.accountLimited = true
	if s.observeDispatch != nil {
		s.observeDispatch(DispatchObservation{
			Tenant: j.task.Key.Tenant, Account: j.task.Key.Account, Provider: j.task.Provider, Kind: j.task.ScheduleKind,
			ScheduleID: j.id, Fence: j.fence, DueAt: dueAt.UTC(), ClaimedAt: claimedAt.UTC(),
		})
	}
	return j, nil
}

func accountCooldown(outcome string, delay time.Duration) time.Duration {
	if outcome == "throttled" {
		return delay
	}
	return 0
}

func releaseAccountDispatch(ctx context.Context, tx *sql.Tx, j job, cooldown time.Duration) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE cmp_inventory_account_dispatch
		SET schedule_id=NULL,schedule_fence=NULL,lease_until=NULL,
			cooldown_until=CASE WHEN $5::bigint>0 THEN GREATEST(cooldown_until,clock_timestamp()+$5*interval '1 millisecond')
			                  ELSE cooldown_until END
		WHERE (tenant_id,account_id,schedule_id,schedule_fence)=($1,$2,$3,$4)`,
		j.task.Key.Tenant, j.task.Key.Account, j.id, j.fence, cooldown.Milliseconds())
	return err
}
