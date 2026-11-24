package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

func reservationQuantity(payload []byte) (int, error) {
	var input struct {
		Quantity int `json:"quantity"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Quantity < 1 || input.Quantity > 1000000 {
		return 0, errInvalidTask
	}
	return input.Quantity, nil
}

func (r repository) reserveForWorkflow(ctx context.Context, task mq.Task) error {
	quantity, err := reservationQuantity(task.Payload)
	if err != nil {
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
	var flow, node string
	err = tx.QueryRowContext(ctx, `SELECT workflow_id,node_id FROM scheduler_workflow_nodes
		WHERE tenant_id=$1 AND run_id=$2
		UNION ALL SELECT workflow_id,node_id FROM scheduler_workflow_fanout_children WHERE tenant_id=$1 AND run_id=$2`,
		task.TenantID, task.ID).Scan(&flow, &node)
	if errors.Is(err, sql.ErrNoRows) {
		return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
	}
	if err != nil {
		return err
	}
	if err := lockCompensationWorkflow(ctx, tx, task.TenantID, flow); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `WITH reservation AS (
		INSERT INTO scheduler_reservations (run_id,tenant_id,quantity) VALUES ($1,$2,$3) RETURNING run_id
	) INSERT INTO scheduler_compensations (reservation_id,tenant_id,workflow_id,node_id,effect_key)
	SELECT run_id,$2,$4,$5,$6 FROM reservation`, task.ID, task.TenantID, quantity, flow, node, rand.Text()); err != nil {
		return err
	}
	return tx.Commit()
}

func lockCompensationWorkflow(ctx context.Context, tx *sql.Tx, tenant, flow string) error {
	var found int
	return tx.QueryRowContext(ctx, "SELECT 1 FROM scheduler_workflows WHERE tenant_id=$1 AND id=$2 FOR UPDATE",
		tenant, flow).Scan(&found)
}

func (r repository) dispatchCompensation(ctx context.Context) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var flow, tenant string
	err = tx.QueryRowContext(ctx, `SELECT w.id,w.tenant_id FROM scheduler_compensations c
		JOIN scheduler_workflows w ON (w.tenant_id,w.id)=(c.tenant_id,c.workflow_id)
		WHERE w.state='failed' AND w.dispatch_after<=statement_timestamp() AND c.state='registered'
			AND NOT EXISTS (SELECT 1 FROM scheduler_compensations remaining
				WHERE remaining.tenant_id=c.tenant_id AND remaining.workflow_id=c.workflow_id
					AND remaining.sequence>c.sequence AND remaining.state<>'released')
		ORDER BY w.dispatch_after,w.created_at,w.tenant_id,w.id LIMIT 1 FOR UPDATE OF w SKIP LOCKED`).Scan(&flow, &tenant)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var reservation, state string
	err = tx.QueryRowContext(ctx, `SELECT reservation_id,state FROM scheduler_compensations
		WHERE tenant_id=$1 AND workflow_id=$2 AND state<>'released' ORDER BY sequence DESC LIMIT 1`,
		tenant, flow).Scan(&reservation, &state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && state != "registered") {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT queue_compensation"); err != nil {
		return "", err
	}
	id := rand.Text()
	var at time.Time
	if err := tx.QueryRowContext(ctx, `INSERT INTO scheduled_runs (id,tenant_id,scheduled_at) VALUES ($1,$2,statement_timestamp())
		RETURNING scheduled_at`, id, tenant).Scan(&at); err != nil {
		return "", err
	}
	task, err := queuedTask(id, tenant, submission{Topic: releaseTaskType, Payload: json.RawMessage(`{}`)}, at, at)
	if err != nil {
		return "", err
	}
	task.MaxAttempts = 3
	err = admissionError(r.queue.InsertTx(ctx, tx, task))
	if errors.Is(err, errTenantCapacity) {
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT queue_compensation"); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflows
			SET dispatch_after=statement_timestamp()+interval '1 second' WHERE tenant_id=$1 AND id=$2`, tenant, flow); err != nil {
			return "", err
		}
		return flow, tx.Commit()
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `WITH queued AS (
		UPDATE scheduler_compensations SET state='queued' WHERE reservation_id=$1 RETURNING reservation_id
	)
	INSERT INTO scheduler_compensation_runs (run_id,reservation_id) SELECT $2,reservation_id FROM queued`, reservation, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (r repository) compensateReservation(ctx context.Context, task mq.Task) error {
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
	var reservation, flow string
	err = tx.QueryRowContext(ctx, `SELECT c.reservation_id,c.workflow_id FROM scheduler_compensation_runs j
		JOIN scheduler_compensations c USING (reservation_id)
		WHERE j.run_id=$1 AND c.tenant_id=$2`, task.ID, task.TenantID).Scan(&reservation, &flow)
	if errors.Is(err, sql.ErrNoRows) {
		return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
	}
	if err != nil {
		return err
	}
	if err := lockCompensationWorkflow(ctx, tx, task.TenantID, flow); err != nil {
		return err
	}
	if err := releaseReservation(ctx, tx, task.TenantID, reservation, "queued"); err != nil {
		return err
	}
	return tx.Commit()
}

func releaseReservation(ctx context.Context, tx *sql.Tx, tenant, reservation, expected string) error {
	var id string
	err := tx.QueryRowContext(ctx, `WITH released AS (
		UPDATE scheduler_reservations r SET released_at=COALESCE(r.released_at,clock_timestamp()),release_key=c.effect_key
		FROM scheduler_compensations c WHERE r.run_id=c.reservation_id AND r.tenant_id=c.tenant_id
			AND c.tenant_id=$1 AND c.reservation_id=$2 AND c.state=$3 AND (r.release_key IS NULL OR r.release_key=c.effect_key)
		RETURNING r.run_id
	)
	UPDATE scheduler_compensations c SET state='released' FROM released r
	WHERE c.reservation_id=r.run_id RETURNING c.reservation_id`, tenant, reservation, expected).Scan(&id)
	return err
}

func (r repository) resolveCompensation(ctx context.Context, tenant, flow, reservation, key, action string) error {
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockCompensationWorkflow(ctx, tx, tenant, flow); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM scheduler_compensations
		WHERE tenant_id=$1 AND workflow_id=$2 AND reservation_id=$3`, tenant, flow, reservation).Scan(&state); err != nil {
		return err
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT action FROM scheduler_compensation_commands
		WHERE tenant_id=$1 AND workflow_id=$2 AND reservation_id=$3 AND request_key=$4`,
		tenant, flow, reservation, key).Scan(&previous)
	if err == nil {
		if previous != action {
			return errRequestConflict
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state != "manual" {
		return errRunFinalized
	}
	if action == "retry" {
		if _, err := tx.ExecContext(ctx, "UPDATE scheduler_compensations SET state='registered' WHERE reservation_id=$1",
			reservation); err != nil {
			return err
		}
	} else if err := releaseReservation(ctx, tx, tenant, reservation, "manual"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_compensation_commands
		(tenant_id,workflow_id,reservation_id,request_key,action) VALUES ($1,$2,$3,$4,$5)`,
		tenant, flow, reservation, key, action); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1,'workflow.compensation_manual',$2,
		jsonb_build_object('state','manual'),jsonb_build_object('action',$3::text,'workflow_id',$4::text))`,
		tenant, reservation, action, flow); err != nil {
		return err
	}
	return tx.Commit()
}
