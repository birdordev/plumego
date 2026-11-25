package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type inventoryRedrive struct {
	tenant, run, commandKey, actor string
}

var (
	errInventoryRedriveConflict = errors.New("inventory redrive command conflicts with an earlier command")
	errInventoryRedriveBlocked  = errors.New("inventory redrive requires an old terminal run, no active lease, and an absent independent receipt")
	errInventoryRedrivePaced    = errors.New("inventory redrive rate limit reached")
)

func (cmd inventoryRedrive) valid() bool {
	return runCursorID.MatchString(cmd.tenant) && runCursorID.MatchString(cmd.run) &&
		runCursorID.MatchString(cmd.commandKey) && runCursorID.MatchString(cmd.actor)
}

func (cmd inventoryRedrive) existing(ctx context.Context, tx *sql.Tx) (bool, error) {
	var run, actor string
	err := tx.QueryRowContext(ctx, `SELECT run_id,actor FROM scheduler_inventory_redrives
		WHERE tenant_id=$1 AND command_key=$2`, cmd.tenant, cmd.commandKey).Scan(&run, &actor)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if run != cmd.run || actor != cmd.actor {
		return false, errInventoryRedriveConflict
	}
	return true, nil
}

func redriveInventory(ctx context.Context, source, receiver *sql.DB, cmd inventoryRedrive, grace time.Duration) error {
	tx, err := source.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// 串行处理同一租户的人工命令和限速。幂等重试在限速检查前结束。
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_inventory_redrive_limits VALUES ($1,clock_timestamp())
		ON CONFLICT DO NOTHING`, cmd.tenant); err != nil {
		return err
	}
	var ready bool
	if err := tx.QueryRowContext(ctx, `SELECT next_at<=clock_timestamp() FROM scheduler_inventory_redrive_limits
		WHERE tenant_id=$1 FOR UPDATE`, cmd.tenant).Scan(&ready); err != nil {
		return err
	}
	if replay, err := cmd.existing(ctx, tx); err != nil || replay {
		return err
	}
	if !ready {
		return errInventoryRedrivePaced
	}
	var effect inventoryEffect
	var event []byte
	err = tx.QueryRowContext(ctx, `SELECT o.inventory_effect FROM scheduler_inventory_effects e
		JOIN scheduler_queue_history q ON q.id=e.run_id AND q.tenant_id=e.tenant_id
		JOIN scheduled_results s ON s.run_id=e.run_id JOIN scheduled_outbox o ON o.run_id=e.run_id
		WHERE e.tenant_id=$1 AND e.run_id=$2 AND q.status='done'
			AND e.committed_at<=statement_timestamp() - $3 * interval '1 microsecond'
			AND (o.lease_until IS NULL OR o.lease_until<=statement_timestamp()) FOR UPDATE OF o`,
		cmd.tenant, cmd.run, grace.Microseconds()).Scan(&event)
	if errors.Is(err, sql.ErrNoRows) {
		return errInventoryRedriveBlocked
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(event, &effect) != nil || !effect.valid() || effect.Tenant != cmd.tenant {
		return errInventoryRedriveBlocked
	}
	var receiverHasEffect bool
	err = receiver.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scheduler_received_events WHERE event_id=$1)
		OR EXISTS (SELECT 1 FROM scheduler_projected_inventory_effects
			WHERE tenant_id=$2 AND operation_id=$3 AND action=$4)`,
		cmd.run, effect.Tenant, effect.OperationID, effect.Action).Scan(&receiverHasEffect)
	if err != nil {
		return err
	}
	if receiverHasEffect {
		return errInventoryRedriveBlocked
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_outbox
		SET delivered_at=NULL,available_at=clock_timestamp(),lease_until=NULL,lease_token=NULL WHERE run_id=$1`, cmd.run); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_inventory_redrives (tenant_id,command_key,run_id,actor)
		VALUES ($1,$2,$3,$4)`, cmd.tenant, cmd.commandKey, cmd.run, cmd.actor); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_inventory_redrive_limits
		SET next_at=clock_timestamp()+interval '1 second' WHERE tenant_id=$1`, cmd.tenant); err != nil {
		return err
	}
	return tx.Commit()
}

func runInventoryRedrive(ctx context.Context, db *sql.DB) error {
	cmd := inventoryRedrive{os.Getenv("SCHEDULER_INVENTORY_TENANT"), os.Getenv("SCHEDULER_INVENTORY_RUN_ID"),
		os.Getenv("SCHEDULER_INVENTORY_COMMAND_KEY"), os.Getenv("SCHEDULER_INVENTORY_ACTOR")}
	if !cmd.valid() {
		return errors.New("invalid inventory redrive command")
	}
	grace, err := workerDurationEnv("SCHEDULER_INVENTORY_GRACE", time.Minute)
	if err != nil {
		return err
	}
	if dsn := os.Getenv("SCHEDULER_RECEIVER_DATABASE_URL"); dsn != "" {
		receiver, err := sql.Open("pgx", dsn)
		if err != nil {
			return errors.New("invalid receiver database configuration")
		}
		defer receiver.Close()
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := redriveInventory(bounded, db, receiver, cmd, grace); err != nil {
			switch {
			case errors.Is(err, errInventoryRedriveConflict), errors.Is(err, errInventoryRedriveBlocked),
				errors.Is(err, errInventoryRedrivePaced):
				return err
			default:
				return errors.New("inventory redrive could not confirm eligibility or commit")
			}
		}
		return nil
	}
	return errors.New("SCHEDULER_RECEIVER_DATABASE_URL is required")
}
