package controller

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
)

func (s Store) RegisterSchedule(ctx context.Context, token string, scope identity.Scope, account string, input RegisterSchedule) (Schedule, error) {
	if !input.valid() {
		return Schedule{}, identity.ErrAccountInvalid
	}
	var item Schedule
	err := s.scheduleAccount(ctx, token, scope, account, true, func(tx *sql.Tx, actor identity.Scope, readable bool) error {
		if *input.Enabled && !readable {
			return identity.ErrAccountConflict
		}
		if err := checkDatabaseScheduleTarget(ctx, tx, actor, account, input.Region, input.TargetID, input.Kind); err != nil {
			return err
		}
		var err error
		item, err = scanSchedule(tx.QueryRowContext(ctx, `INSERT INTO cmp_inventory_schedules
			(tenant_id,account_id,region,kind,interval_seconds,enabled,target_id) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (tenant_id,account_id,region,kind,target_id) DO NOTHING RETURNING `+scheduleFields,
			actor.Tenant, account, input.Region, input.Kind, input.Interval, *input.Enabled, input.TargetID))
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrAccountConflict
		}
		if err != nil {
			return err
		}
		return recordScheduleChange(ctx, tx, actor, account, "inventory-schedule-register", item)
	})
	return item, err
}

func (s Store) ChangeSchedule(ctx context.Context, token string, scope identity.Scope, account string, id, revision int64,
	input ChangeSchedule) (Schedule, error) {
	if !input.valid() || (input.RunNow && !*input.Enabled) {
		return Schedule{}, identity.ErrAccountInvalid
	}
	var item Schedule
	err := s.scheduleAccount(ctx, token, scope, account, true, func(tx *sql.Tx, actor identity.Scope, readable bool) error {
		var err error
		item, err = scanSchedule(tx.QueryRowContext(ctx, "SELECT "+scheduleFields+`
			FROM cmp_inventory_schedules WHERE (tenant_id,account_id,schedule_id)=($1,$2,$3) AND `+manageableSchedule+`
			FOR UPDATE`, actor.Tenant, account, id))
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		if item.Revision != revision {
			return identity.ErrAccountStale
		}
		if *input.Enabled && !readable {
			return identity.ErrAccountConflict
		}
		if *input.Enabled {
			if err := checkDatabaseScheduleTarget(ctx, tx, actor, account, item.Region, item.TargetID, item.Kind); err != nil {
				return err
			}
		}
		if input.Interval == item.Interval && *input.Enabled == item.Enabled && !input.RunNow {
			return nil
		}
		item, err = scanSchedule(tx.QueryRowContext(ctx, `UPDATE cmp_inventory_schedules
			SET interval_seconds=$2,enabled=$3,config_revision=config_revision+1,fence=fence+1,lease_until=NULL,
				next_at=CASE WHEN $3 THEN statement_timestamp() ELSE next_at END,
				scheduled_at=CASE WHEN $3 AND NOT enabled THEN statement_timestamp() ELSE scheduled_at END
			WHERE schedule_id=$1 RETURNING `+scheduleFields, id, input.Interval, *input.Enabled))
		if err != nil {
			return err
		}
		action := "inventory-schedule-update"
		if input.RunNow {
			action = "inventory-schedule-run-now"
		}
		return recordScheduleChange(ctx, tx, actor, account, action, item)
	})
	return item, err
}

func recordScheduleChange(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, action string, item Schedule) error {
	state := "disabled"
	if item.Enabled {
		state = "enabled"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_project_audit
		(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,region,actor_id,outcome,revision,occurred_at)
		VALUES ($1,$2,'account',$3,$4,$5,$6,$7,$8,$9,$10,statement_timestamp())`,
		actor.Tenant, actor.Project, action, item.ID, item.Kind, account, item.Region, actor.Principal, state, item.Revision)
	return err
}
