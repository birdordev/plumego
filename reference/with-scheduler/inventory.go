package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"
)

const inventoryTaskType = "demo.inventory"

type inventoryCommand struct {
	OperationID string `json:"operation_id"`
	Action      string `json:"action"`
	SKU         string `json:"sku"`
	Quantity    int    `json:"quantity"`
}

func (c inventoryCommand) valid() bool {
	return runCursorID.MatchString(c.OperationID) && runCursorID.MatchString(c.SKU) &&
		(c.Action == "reserve" || c.Action == "release") && c.Quantity >= 1 && c.Quantity <= 1000000
}

func parseInventoryCommand(payload []byte) (inventoryCommand, error) {
	var command inventoryCommand
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if !json.Valid(payload) || decoder.Decode(&command) != nil || !command.valid() {
		return inventoryCommand{}, errInvalidTask
	}
	return command, nil
}

type inventoryEffect struct {
	Tenant string `json:"tenant"`
	inventoryCommand
}

func (e inventoryEffect) valid() bool {
	return runCursorID.MatchString(e.Tenant) && e.inventoryCommand.valid()
}

func (r repository) applyInventory(ctx context.Context, task mq.Task) error {
	command, err := parseInventoryCommand(task.Payload)
	effect := inventoryEffect{Tenant: task.TenantID, inventoryCommand: command}
	if err != nil || !effect.valid() {
		return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	written, err := recordResultInTx(ctx, tx, task, sql.NullString{})
	if err != nil {
		return err
	}
	if !written {
		return tx.Commit()
	}
	applied, err := applyStockChange(ctx, tx, effect, task.ID)
	if err != nil {
		return err
	}
	if applied {
		encoded, err := json.Marshal(effect)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE scheduled_outbox SET inventory_effect=$2 WHERE run_id=$1",
			task.ID, string(encoded)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func applyStockChange(ctx context.Context, tx *sql.Tx, effect inventoryEffect, runID string) (bool, error) {
	// 一个 SKU 的实际变更串行化。不同 SKU 上的同一逻辑键仍由主键仲裁。
	var available int64
	err := tx.QueryRowContext(ctx, `SELECT available FROM scheduler_inventory
		WHERE tenant_id=$1 AND sku=$2 FOR NO KEY UPDATE`, effect.Tenant, effect.SKU).Scan(&available)
	if errors.Is(err, sql.ErrNoRows) {
		return false, &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
	}
	if err != nil {
		return false, err
	}

	var sku string
	var quantity int
	err = tx.QueryRowContext(ctx, `INSERT INTO scheduler_inventory_effects
		(tenant_id,operation_id,action,sku,quantity,run_id) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (tenant_id,operation_id,action) DO NOTHING RETURNING sku`,
		effect.Tenant, effect.OperationID, effect.Action, effect.SKU, effect.Quantity, runID).Scan(&sku)
	if errors.Is(err, sql.ErrNoRows) {
		err := tx.QueryRowContext(ctx, `SELECT sku,quantity FROM scheduler_inventory_effects
			WHERE tenant_id=$1 AND operation_id=$2 AND action=$3`, effect.Tenant, effect.OperationID, effect.Action).
			Scan(&sku, &quantity)
		if err != nil {
			return false, err
		}
		if sku != effect.SKU || quantity != effect.Quantity {
			return false, &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}

	delta := effect.Quantity
	if effect.Action == "reserve" {
		if int64(effect.Quantity) > available {
			return false, &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
		}
		delta = -delta
	} else {
		err := tx.QueryRowContext(ctx, `SELECT sku,quantity FROM scheduler_inventory_effects
			WHERE tenant_id=$1 AND operation_id=$2 AND action='reserve'`, effect.Tenant, effect.OperationID).
			Scan(&sku, &quantity)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (sku != effect.SKU || quantity != effect.Quantity)) {
			return false, &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
		}
		if err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE scheduler_inventory SET available=available+$3 WHERE tenant_id=$1 AND sku=$2",
		effect.Tenant, effect.SKU, delta)
	return err == nil, err
}
