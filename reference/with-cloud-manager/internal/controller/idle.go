package controller

import (
	"context"
	"errors"
	"regexp"
	"time"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const IdleScheduleKind = "instance-idle"

var idleTargetID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func (s Store) ConfigureIdle(ctx context.Context, key inventory.Key, target string, interval time.Duration) error {
	if !idleTargetID.MatchString(target) || key.Kind != provider.Instance || key.Tenant == "" || key.Account == "" || key.Region == "" {
		return errors.New("invalid idle schedule scope")
	}
	if interval == 0 {
		_, err := s.db.ExecContext(ctx, `UPDATE cmp_inventory_schedules
			SET enabled=false,lease_until=NULL,fence=fence+1
			WHERE (tenant_id,account_id,region,kind,target_id)=($1,$2,$3,'instance-idle',$4) AND enabled`,
			key.Tenant, key.Account, key.Region, target)
		return err
	}
	if interval < time.Hour || interval > 24*time.Hour || interval%time.Second != 0 {
		return errors.New("idle schedule interval must be whole seconds between one and twenty-four hours")
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO cmp_inventory_schedules AS schedule
		(tenant_id,account_id,region,kind,target_id,interval_seconds)
		SELECT s.tenant_id,s.account_id,s.region,'instance-idle',o.external_id,$5
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_objects AS o USING (scope_id)
		WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,'instance',$4)
		  AND a.provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs') AND a.mode IN ('read_only','write_enabled')
		  AND o.removed_at IS NULL
		ON CONFLICT(tenant_id,account_id,region,kind,target_id) DO UPDATE
		SET interval_seconds=EXCLUDED.interval_seconds,enabled=true,
			next_at=CASE WHEN schedule.enabled THEN schedule.next_at ELSE clock_timestamp() END,
			lease_until=CASE WHEN schedule.enabled THEN schedule.lease_until END`,
		key.Tenant, key.Account, key.Region, target, interval/time.Second)
	if err != nil {
		return err
	}
	written, err := res.RowsAffected()
	if err == nil && written != 1 {
		return inventory.ErrUnavailable
	}
	return err
}
