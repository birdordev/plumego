package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
)

type workflowWaitSpec struct {
	Key      string    `json:"key"`
	Actor    string    `json:"actor"`
	Deadline time.Time `json:"deadline"`
}

func (w workflowWaitSpec) valid() bool {
	return runCursorID.MatchString(w.Key) && runCursorID.MatchString(w.Actor) &&
		!w.Deadline.IsZero() && w.Deadline.Year() > 0 && w.Deadline.Year() <= 9999
}

var (
	errSignalForbidden = errors.New("workflow signal actor does not match")
	errSignalClosed    = errors.New("workflow signal is closed")
)

func activateWorkflowWait(ctx context.Context, tx *sql.Tx, tenant, flow, node string) error {
	var received, expired bool
	if err := tx.QueryRowContext(ctx, `UPDATE scheduler_workflow_waits SET activated_at=clock_timestamp()
		WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3
		RETURNING signal_at IS NOT NULL, deadline <= activated_at`, tenant, flow, node).Scan(&received, &expired); err != nil {
		return err
	}
	if received || expired {
		return completeWorkflowWait(ctx, tx, tenant, flow, node, "ready", received)
	}
	_, err := tx.ExecContext(ctx, `UPDATE scheduler_workflow_nodes SET state='waiting'
		WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3 AND state='ready'`, tenant, flow, node)
	return err
}

func completeWorkflowWait(ctx context.Context, tx *sql.Tx, tenant, flow, node, expected string, success bool) error {
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflow_waits SET resolved_at=clock_timestamp()
		WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3`, tenant, flow, node); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "SELECT scheduler_complete_workflow_node($1,$2,$3,$4,$5)",
		tenant, flow, node, expected, success)
	return err
}

func (r repository) advanceWorkflow(ctx context.Context) (string, error) {
	for _, operation := range []func(context.Context) (string, error){r.expireWorkflowWait, r.cancelFanoutChild, r.dispatchCompensation, r.expandWorkflowFanout} {
		flow, err := operation(ctx)
		if err != nil || flow != "" {
			return flow, err
		}
	}
	return r.dispatchWorkflowNode(ctx)
}

func (r repository) expireWorkflowWait(ctx context.Context) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var tenant, flow, node string
	err = tx.QueryRowContext(ctx, `SELECT w.tenant_id,w.id FROM scheduler_workflow_waits t
		JOIN scheduler_workflows w ON (w.tenant_id,w.id)=(t.tenant_id,t.workflow_id)
		WHERE t.activated_at IS NOT NULL AND t.resolved_at IS NULL AND t.deadline <= statement_timestamp()
		ORDER BY t.deadline,t.tenant_id,t.workflow_id,t.node_id LIMIT 1 FOR UPDATE OF w SKIP LOCKED`).Scan(&tenant, &flow)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	err = tx.QueryRowContext(ctx, `SELECT node_id FROM scheduler_workflow_waits
		WHERE tenant_id=$1 AND workflow_id=$2 AND activated_at IS NOT NULL AND resolved_at IS NULL
			AND deadline <= clock_timestamp() ORDER BY deadline,node_id LIMIT 1`, tenant, flow).Scan(&node)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := completeWorkflowWait(ctx, tx, tenant, flow, node, "waiting", false); err != nil {
		return "", err
	}
	return flow, tx.Commit()
}

func (r repository) receiveWorkflowSignal(ctx context.Context, tenant, flow, key string) (time.Time, error) {
	var received time.Time
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return received, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM scheduler_workflows WHERE tenant_id=$1 AND id=$2 FOR UPDATE",
		tenant, flow).Scan(&locked); err != nil {
		return received, err
	}
	var node, allowed, state string
	var prior sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT t.node_id,t.signal_actor,n.state,t.signal_at
		FROM scheduler_workflow_waits t JOIN scheduler_workflow_nodes n USING (tenant_id,workflow_id,node_id)
		WHERE t.tenant_id=$1 AND t.workflow_id=$2 AND t.signal_key=$3`, tenant, flow, key).
		Scan(&node, &allowed, &state, &prior)
	if err != nil {
		return received, err
	}
	identity, _ := ctx.Value(auditRequestKey{}).(auditIdentity)
	if identity.actor != allowed {
		return received, errSignalForbidden
	}
	if prior.Valid {
		return prior.Time, tx.Commit()
	}
	switch state {
	case "blocked", "ready", "waiting":
	default:
		return received, errSignalClosed
	}
	err = tx.QueryRowContext(ctx, `WITH tick AS MATERIALIZED (SELECT clock_timestamp() AS now)
		UPDATE scheduler_workflow_waits t SET signal_at=tick.now FROM tick
		WHERE t.tenant_id=$1 AND t.workflow_id=$2 AND t.node_id=$3 AND t.deadline > tick.now
		RETURNING t.signal_at`, tenant, flow, node).Scan(&received)
	if errors.Is(err, sql.ErrNoRows) {
		return received, errSignalClosed
	}
	if err != nil {
		return received, err
	}
	if state == "waiting" {
		if err := completeWorkflowWait(ctx, tx, tenant, flow, node, "waiting", true); err != nil {
			return received, err
		}
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1, 'workflow.signal', $2,
		'{}'::jsonb, jsonb_build_object('node_id',$3::text))`, tenant, flow, node); err != nil {
		return received, err
	}
	return received, tx.Commit()
}

func (h api) signalWorkflow(w http.ResponseWriter, req *http.Request) {
	var input struct {
		Key string `json:"key"`
	}
	if !decodeRequest(w, req, &input) {
		return
	}
	if !runCursorID.MatchString(input.Key) {
		writeError(w, req, contract.TypeBadRequest, "invalid workflow signal key")
		return
	}
	received, err := h.repo.receiveWorkflowSignal(req.Context(), h.tenant, req.PathValue("id"), input.Key)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "workflow signal not found")
	case errors.Is(err, errSignalForbidden):
		writeError(w, req, contract.TypeForbidden, "workflow signal forbidden")
	case errors.Is(err, errSignalClosed):
		writeError(w, req, contract.TypeGone, "workflow signal is closed")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "workflow signal unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, map[string]time.Time{"received_at": received}, nil)
	}
}
