package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

type bulkSelection struct {
	Topic string `json:"topic"`
	Limit int    `json:"limit"`
}

type bulkItem struct {
	RunID   string `json:"run_id"`
	Outcome string `json:"outcome"`
}

type bulkOperation struct {
	ID        string        `json:"id"`
	Selection bulkSelection `json:"selection"`
	State     string        `json:"state"`
	Total     int           `json:"total"`
	Processed int           `json:"processed"`
	Items     []bulkItem    `json:"items"`
}

func (r repository) planBulkCancel(ctx context.Context, tenant, key string, filter bulkSelection) (string, error) {
	if _, ok := taskContractForTopic(filter.Topic); !ok || filter.Limit < 1 || filter.Limit > 500 || !validIdempotencyKey(key) {
		return "", errInvalidTask
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	actor, _ := ctx.Value(auditRequestKey{}).(auditIdentity)
	id := rand.Text()
	insert, err := tx.ExecContext(ctx, `INSERT INTO scheduler_bulk_operations
		(tenant_id, id, request_key, topic, selection_limit, actor, request_id, reason_code)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (tenant_id, request_key) DO NOTHING`,
		tenant, id, key, filter.Topic, filter.Limit, actor.actor, actor.requestID, actor.reason)
	if err != nil {
		return "", err
	}
	rows, err := insert.RowsAffected()
	if err != nil {
		return "", err
	}
	if rows == 0 {
		var previous bulkSelection
		if err := tx.QueryRowContext(ctx, `SELECT id, topic, selection_limit FROM scheduler_bulk_operations
			WHERE tenant_id = $1 AND request_key = $2`, tenant, key).Scan(&id, &previous.Topic, &previous.Limit); err != nil {
			return "", err
		}
		if previous != filter {
			return "", errRequestConflict
		}
		return id, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_bulk_items (tenant_id, operation_id, run_id, task_updated_at)
		SELECT q.tenant_id, $2, q.id, q.updated_at FROM mq_tasks q
		JOIN scheduled_runs r ON r.id = q.id AND r.tenant_id = q.tenant_id
		WHERE q.tenant_id = $1 AND q.topic = $3 AND q.status = 'queued'
		ORDER BY q.available_at, q.id LIMIT $4`, tenant, id, filter.Topic, filter.Limit); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (r repository) readBulkOperation(ctx context.Context, tenant, id string) (bulkOperation, error) {
	var op bulkOperation
	var items []byte
	err := r.db.QueryRowContext(ctx, `SELECT o.id, o.topic, o.selection_limit, o.state,
		count(i.run_id), count(i.run_id) FILTER (WHERE i.outcome <> 'pending'),
		COALESCE(jsonb_agg(jsonb_build_object('run_id', i.run_id, 'outcome', i.outcome)
			ORDER BY i.run_id) FILTER (WHERE i.run_id IS NOT NULL), '[]'::jsonb)
		FROM scheduler_bulk_operations o LEFT JOIN scheduler_bulk_items i
			ON i.tenant_id = o.tenant_id AND i.operation_id = o.id
		WHERE o.tenant_id = $1 AND o.id = $2 GROUP BY o.tenant_id, o.id`, tenant, id).
		Scan(&op.ID, &op.Selection.Topic, &op.Selection.Limit, &op.State, &op.Total, &op.Processed, &items)
	if err != nil {
		return op, err
	}
	return op, json.Unmarshal(items, &op.Items)
}

func (r repository) setBulkPaused(ctx context.Context, tenant, id string, paused bool) error {
	var state string
	return r.db.QueryRowContext(ctx, `UPDATE scheduler_bulk_operations
		SET state = CASE WHEN state = 'completed' THEN state WHEN $3 THEN 'paused' ELSE 'running' END
		WHERE tenant_id = $1 AND id = $2 RETURNING state`, tenant, id, paused).Scan(&state)
}

func (r repository) processBulkCancel(ctx context.Context) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var id, tenant string
	var actor auditIdentity
	err = tx.QueryRowContext(ctx, `SELECT id, tenant_id, actor, request_id, reason_code
		FROM scheduler_bulk_operations WHERE state = 'running'
		ORDER BY created_at, tenant_id, id LIMIT 1 FOR UPDATE SKIP LOCKED`).
		Scan(&id, &tenant, &actor.actor, &actor.requestID, &actor.reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('scheduler.actor', $1, true),
		set_config('scheduler.request_id', $2, true), set_config('scheduler.reason_code', $3, true)`,
		actor.actor, actor.requestID, actor.reason); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `WITH batch AS MATERIALIZED (
		SELECT run_id, task_updated_at FROM scheduler_bulk_items
		WHERE tenant_id = $1 AND operation_id = $2 AND outcome = 'pending'
		ORDER BY run_id LIMIT 16
	), changed AS (
		UPDATE mq_tasks q SET status = 'canceled', lease_owner = NULL, lease_until = NULL,
			updated_at = clock_timestamp() FROM batch b
		WHERE q.tenant_id = $1 AND q.id = b.run_id AND q.status = 'queued'
			AND q.updated_at = b.task_updated_at
			AND NOT EXISTS (SELECT 1 FROM scheduled_results r WHERE r.run_id = q.id)
		RETURNING q.id
	)
	UPDATE scheduler_bulk_items i SET outcome = CASE
		WHEN c.id IS NOT NULL THEN 'canceled'
		WHEN EXISTS (SELECT 1 FROM mq_tasks q WHERE q.tenant_id = $1 AND q.id = b.run_id) THEN 'changed'
		ELSE 'missing' END
	FROM batch b LEFT JOIN changed c ON c.id = b.run_id
	WHERE i.tenant_id = $1 AND i.operation_id = $2 AND i.run_id = b.run_id`, tenant, id); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_bulk_operations o SET state = 'completed'
		WHERE o.tenant_id = $1 AND o.id = $2 AND NOT EXISTS (SELECT 1 FROM scheduler_bulk_items i
			WHERE i.tenant_id = $1 AND i.operation_id = $2 AND i.outcome = 'pending')`, tenant, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (h api) planBulkCancel(w http.ResponseWriter, req *http.Request) {
	var filter bulkSelection
	if !decodeRequest(w, req, &filter) {
		return
	}
	id, err := h.repo.planBulkCancel(req.Context(), h.tenant, req.Header.Get("Idempotency-Key"), filter)
	if writeSubmissionError(w, req, err) {
		return
	}
	w.Header().Set("Location", "/bulk-cancellations/"+id)
	h.writeBulkOperation(w, req, id, true)
}

func (h api) getBulkOperation(w http.ResponseWriter, req *http.Request) {
	h.writeBulkOperation(w, req, req.PathValue("id"), false)
}

func (h api) pauseBulkOperation(w http.ResponseWriter, req *http.Request) {
	var input struct {
		Paused *bool `json:"paused"`
	}
	if !decodeRequest(w, req, &input) {
		return
	}
	if input.Paused == nil {
		writeError(w, req, contract.TypeBadRequest, "paused is required")
		return
	}
	id := req.PathValue("id")
	if err := h.repo.setBulkPaused(req.Context(), h.tenant, id, *input.Paused); err != nil {
		writeBulkError(w, req, err)
		return
	}
	h.writeBulkOperation(w, req, id, false)
}

func (h api) writeBulkOperation(w http.ResponseWriter, req *http.Request, id string, created bool) {
	op, err := h.repo.readBulkOperation(req.Context(), h.tenant, id)
	if err != nil {
		writeBulkError(w, req, err)
		return
	}
	if created {
		_ = contract.WriteResponse(w, req, http.StatusCreated, op, nil)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusOK, op, nil)
}

func writeBulkError(w http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, req, contract.TypeNotFound, "bulk cancellation not found")
		return
	}
	writeError(w, req, contract.TypeUnavailable, "bulk cancellation unavailable")
}
