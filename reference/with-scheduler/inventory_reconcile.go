package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

type effectKey struct {
	OperationID string `json:"operation_id"`
	Action      string `json:"action"`
}

type inventorySourceEffect struct {
	key                        effectKey
	sku, runID, status         string
	quantity                   int
	committedAt                time.Time
	receipt, outbox, delivered bool
}

type inventoryReceiverEffect struct {
	key         effectKey
	sku         string
	quantity    int
	committedAt time.Time
}

type inventoryReconciliationItem struct {
	effectKey
	RunID string `json:"run_id,omitempty"`
	State string `json:"state"`
}

type inventoryReconciliation struct {
	Tenant    string                        `json:"tenant"`
	CheckedAt time.Time                     `json:"checked_at"`
	Items     []inventoryReconciliationItem `json:"items"`
	Counts    map[string]int                `json:"counts"`
	Next      *effectKey                    `json:"next,omitempty"`
}

func reconcileInventory(ctx context.Context, source, receiver *sql.DB, tenant string, after effectKey,
	limit int, grace time.Duration) (inventoryReconciliation, error) {
	sourceTx, err := source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return inventoryReconciliation{}, err
	}
	defer func() { _ = sourceTx.Rollback() }()
	receiverTx, err := receiver.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return inventoryReconciliation{}, err
	}
	defer func() { _ = receiverTx.Rollback() }()
	var sourceAt, receiverAt time.Time
	if err := sourceTx.QueryRowContext(ctx, "SELECT statement_timestamp()").Scan(&sourceAt); err != nil {
		return inventoryReconciliation{}, err
	}
	if err := receiverTx.QueryRowContext(ctx, "SELECT statement_timestamp()").Scan(&receiverAt); err != nil {
		return inventoryReconciliation{}, err
	}
	checkedAt := sourceAt
	if receiverAt.Before(checkedAt) {
		checkedAt = receiverAt
	}
	cutoff := checkedAt.Add(-grace)
	common := ` WHERE e.tenant_id=$1 AND (e.operation_id COLLATE "C",e.action COLLATE "C") > ($2,$3)
		ORDER BY e.operation_id COLLATE "C",e.action COLLATE "C" LIMIT $4`
	rows, err := sourceTx.QueryContext(ctx, `SELECT e.operation_id,e.action,e.sku,e.quantity,e.run_id,e.committed_at,
		COALESCE(q.status,''),s.run_id IS NOT NULL,o.inventory_effect IS NOT NULL,o.delivered_at IS NOT NULL
		FROM scheduler_inventory_effects e LEFT JOIN scheduler_queue_history q ON q.id=e.run_id AND q.tenant_id=e.tenant_id
		LEFT JOIN scheduled_results s ON s.run_id=e.run_id LEFT JOIN scheduled_outbox o ON o.run_id=e.run_id`+common,
		tenant, after.OperationID, after.Action, limit+1)
	if err != nil {
		return inventoryReconciliation{}, err
	}
	defer rows.Close()
	sent := make(map[effectKey]inventorySourceEffect)
	for rows.Next() {
		var e inventorySourceEffect
		if err := rows.Scan(&e.key.OperationID, &e.key.Action, &e.sku, &e.quantity, &e.runID, &e.committedAt,
			&e.status, &e.receipt, &e.outbox, &e.delivered); err != nil {
			return inventoryReconciliation{}, err
		}
		sent[e.key] = e
	}
	if err := rows.Err(); err != nil {
		return inventoryReconciliation{}, err
	}
	atReceiver, err := receiverTx.QueryContext(ctx, `SELECT e.operation_id,e.action,e.sku,e.quantity,e.committed_at
		FROM scheduler_projected_inventory_effects e`+common, tenant, after.OperationID, after.Action, limit+1)
	if err != nil {
		return inventoryReconciliation{}, err
	}
	defer atReceiver.Close()
	observed := make(map[effectKey]inventoryReceiverEffect)
	keys := make([]effectKey, 0, 2*(limit+1))
	for key := range sent {
		keys = append(keys, key)
	}
	for atReceiver.Next() {
		var e inventoryReceiverEffect
		if err := atReceiver.Scan(&e.key.OperationID, &e.key.Action, &e.sku, &e.quantity, &e.committedAt); err != nil {
			return inventoryReconciliation{}, err
		}
		observed[e.key] = e
		if _, exists := sent[e.key]; !exists {
			keys = append(keys, e.key)
		}
	}
	if err := atReceiver.Err(); err != nil {
		return inventoryReconciliation{}, err
	}
	slices.SortFunc(keys, func(a, b effectKey) int {
		if cmp := strings.Compare(a.OperationID, b.OperationID); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Action, b.Action)
	})
	result := inventoryReconciliation{Tenant: tenant, CheckedAt: checkedAt, Counts: make(map[string]int),
		Items: make([]inventoryReconciliationItem, 0, min(limit, len(keys)))}
	if len(keys) > limit {
		keys = keys[:limit]
		next := keys[limit-1]
		result.Next = &next
	}
	for _, key := range keys {
		a, hasSource := sent[key]
		b, hasReceiver := observed[key]
		state := classifyInventoryEffect(cutoff, a, hasSource, b, hasReceiver)
		result.Items = append(result.Items, inventoryReconciliationItem{key, a.runID, state})
		result.Counts[state]++
	}
	return result, nil
}

func classifyInventoryEffect(cutoff time.Time, a inventorySourceEffect, hasSource bool,
	b inventoryReceiverEffect, hasReceiver bool) string {
	switch {
	case !hasSource && b.committedAt.After(cutoff):
		return "pending_source"
	case !hasSource:
		return "source_missing"
	case !a.receipt || !a.outbox || (a.status != "leased" && a.status != "done"):
		return "source_inconsistent"
	case hasReceiver && (a.sku != b.sku || a.quantity != b.quantity):
		return "receiver_conflict"
	case hasReceiver && a.delivered && a.status == "done":
		return "matched"
	case !hasReceiver && a.delivered:
		return "receiver_missing"
	case a.committedAt.After(cutoff):
		return "pending_confirmation"
	case hasReceiver:
		return "confirmation_overdue"
	default:
		return "receiver_missing"
	}
}

func runInventoryReconcile(ctx context.Context, db *sql.DB) error {
	tenant := os.Getenv("SCHEDULER_INVENTORY_TENANT")
	after := effectKey{os.Getenv("SCHEDULER_INVENTORY_AFTER_OPERATION"), os.Getenv("SCHEDULER_INVENTORY_AFTER_ACTION")}
	rawLimit := os.Getenv("SCHEDULER_INVENTORY_PAGE_SIZE")
	if rawLimit == "" {
		rawLimit = "100"
	}
	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit < 1 || limit > 500 || !runCursorID.MatchString(tenant) ||
		(after.OperationID != "" && (!runCursorID.MatchString(after.OperationID) ||
			(after.Action != "reserve" && after.Action != "release"))) || (after.OperationID == "" && after.Action != "") {
		return errors.New("invalid inventory reconciliation scope or cursor")
	}
	grace, err := workerDurationEnv("SCHEDULER_INVENTORY_GRACE", time.Minute)
	if err != nil {
		return err
	}
	receiverDSN := os.Getenv("SCHEDULER_RECEIVER_DATABASE_URL")
	if receiverDSN == "" {
		return errors.New("SCHEDULER_RECEIVER_DATABASE_URL is required")
	}
	receiver, err := sql.Open("pgx", receiverDSN)
	if err != nil {
		return errors.New("invalid receiver database configuration")
	}
	defer receiver.Close()
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := reconcileInventory(bounded, db, receiver, tenant, after, limit, grace)
	if err != nil {
		return errors.New("inventory reconciliation query failed")
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	if len(result.Items) > result.Counts["matched"]+result.Counts["pending_source"]+result.Counts["pending_confirmation"] {
		return errors.New("inventory reconciliation found differences")
	}
	return nil
}
