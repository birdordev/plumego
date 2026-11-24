package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
)

const fanoutPageSize = 32

type workflowFanoutSpec struct {
	Count       int        `json:"count"`
	MaxInFlight int        `json:"max_in_flight"`
	Task        submission `json:"task"`
}

func (f workflowFanoutSpec) valid() bool {
	return f.Count >= 1 && f.Count <= 1000000 && f.MaxInFlight >= 1 && f.MaxInFlight <= 256
}

func workflowNodeKinds(n *workflowNode) int {
	count := 0
	for _, present := range []bool{n.Task != nil, n.Wait != nil, n.Fanout != nil} {
		if present {
			count++
		}
	}
	return count
}

type workflowFanoutStatus struct {
	Total           int  `json:"total"`
	Created         int  `json:"created"`
	Active          int  `json:"active"`
	Succeeded       int  `json:"succeeded"`
	Failed          int  `json:"failed"`
	Canceled        int  `json:"canceled"`
	CancelRequested bool `json:"cancel_requested"`
}

func (r repository) expandWorkflowFanout(ctx context.Context) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var tenant, flow string
	err = tx.QueryRowContext(ctx, `SELECT w.tenant_id,w.id FROM scheduler_workflow_fanouts f
		JOIN scheduler_workflows w ON (w.tenant_id,w.id)=(f.tenant_id,f.workflow_id)
		WHERE f.activated AND NOT f.resolved AND NOT f.cancel_requested
			AND f.next_ordinal<f.total AND f.active<f.max_in_flight AND w.dispatch_after<=statement_timestamp()
		ORDER BY w.dispatch_after,w.created_at,w.tenant_id,w.id LIMIT 1 FOR UPDATE OF w SKIP LOCKED`).Scan(&tenant, &flow)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var node string
	var next, slots int
	var raw []byte
	var now time.Time
	err = tx.QueryRowContext(ctx, `SELECT f.node_id,f.next_ordinal,
		least(f.total-f.next_ordinal,f.max_in_flight-f.active,$3),n.submission,statement_timestamp()
		FROM scheduler_workflow_fanouts f JOIN scheduler_workflow_nodes n USING (tenant_id,workflow_id,node_id)
		WHERE f.tenant_id=$1 AND f.workflow_id=$2 AND f.activated AND NOT f.resolved AND NOT f.cancel_requested
			AND f.next_ordinal<f.total AND f.active<f.max_in_flight ORDER BY f.node_id LIMIT 1`, tenant, flow, fanoutPageSize).
		Scan(&node, &next, &slots, &raw, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var input submission
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	if input.RunAt.IsZero() {
		input.RunAt = now
	}

	created := 0
	for created < slots {
		if _, err := tx.ExecContext(ctx, "SAVEPOINT enqueue_fanout_child"); err != nil {
			return "", err
		}
		err := r.insertFanoutChild(ctx, tx, tenant, flow, node, next+created+1, input, now)
		if errors.Is(err, errTenantCapacity) {
			if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT enqueue_fanout_child"); err != nil {
				return "", err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflows
				SET dispatch_after=statement_timestamp()+interval '1 second' WHERE tenant_id=$1 AND id=$2`, tenant, flow); err != nil {
				return "", err
			}
			break
		}
		if err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT enqueue_fanout_child"); err != nil {
			return "", err
		}
		created++
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflow_fanouts SET next_ordinal=next_ordinal+$4,active=active+$4
		WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3`, tenant, flow, node, created); err != nil {
		return "", err
	}
	return flow, tx.Commit()
}

func (r repository) insertFanoutChild(ctx context.Context, tx *sql.Tx, tenant, flow, node string, ordinal int, input submission, now time.Time) error {
	id := rand.Text()
	if _, err := tx.ExecContext(ctx, "INSERT INTO scheduled_runs (id,tenant_id,scheduled_at) VALUES ($1,$2,$3)",
		id, tenant, input.RunAt); err != nil {
		return err
	}
	task, err := queuedTask(id, tenant, input, input.RunAt, now)
	if err != nil {
		return err
	}
	task.Meta["workflow_id"] = flow
	task.Meta["workflow_node"] = node
	task.Meta["workflow_ordinal"] = strconv.Itoa(ordinal)
	if err := r.queue.InsertTx(ctx, tx, task); err != nil {
		return admissionError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduler_workflow_fanout_children
		(tenant_id,workflow_id,node_id,ordinal,run_id) VALUES ($1,$2,$3,$4,$5)`, tenant, flow, node, ordinal, id)
	return err
}

func (r repository) requestFanoutCancel(ctx context.Context, tenant, flow, node string) error {
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM scheduler_workflows WHERE tenant_id=$1 AND id=$2 FOR UPDATE", tenant, flow).
		Scan(&locked); err != nil {
		return err
	}
	var resolved, requested bool
	if err := tx.QueryRowContext(ctx, `SELECT f.resolved,f.cancel_requested FROM scheduler_workflow_fanouts f
		JOIN scheduler_workflow_nodes n USING (tenant_id,workflow_id,node_id)
		WHERE f.tenant_id=$1 AND f.workflow_id=$2 AND f.node_id=$3 AND n.state<>'skipped'`,
		tenant, flow, node).Scan(&resolved, &requested); err != nil {
		return err
	}
	if requested {
		return tx.Commit()
	}
	if resolved {
		return errRunFinalized
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflow_fanouts SET cancel_requested=true
		WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3`, tenant, flow, node); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "SELECT scheduler_settle_fanout($1,$2,$3)", tenant, flow, node); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1,'workflow.fanout_cancel',$2,
		'{}'::jsonb,jsonb_build_object('node_id',$3::text))`, tenant, flow, node); err != nil {
		return err
	}
	return tx.Commit()
}

func (r repository) cancelFanoutChild(ctx context.Context) (string, error) {
	var tenant, id string
	err := r.db.QueryRowContext(ctx, `SELECT c.tenant_id,c.run_id FROM scheduler_workflow_fanouts f
		JOIN scheduler_workflow_fanout_children c USING (tenant_id,workflow_id,node_id)
		JOIN mq_tasks q ON q.id=c.run_id AND q.tenant_id=c.tenant_id
		WHERE f.cancel_requested AND NOT f.resolved AND c.state='queued' AND q.status IN ('queued','leased')
			AND NOT EXISTS (SELECT 1 FROM scheduled_results s WHERE s.run_id=q.id)
		ORDER BY f.tenant_id,f.workflow_id,f.node_id,c.ordinal LIMIT 1`).Scan(&tenant, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := r.cancelRun(ctx, tenant, id); err != nil && !errors.Is(err, errRunFinalized) && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return id, nil
}

type fanoutChild struct {
	Ordinal     int    `json:"ordinal"`
	RunID       string `json:"run_id"`
	State       string `json:"state"`
	ResultReady bool   `json:"result_ready"`
}

type fanoutChildPage struct {
	Items     []fanoutChild `json:"items"`
	NextAfter int           `json:"next_after,omitempty"`
}

func (r repository) fanoutChildren(ctx context.Context, tenant, flow, node string, after int) (fanoutChildPage, error) {
	page := fanoutChildPage{Items: make([]fanoutChild, 0)}
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT 1 FROM scheduler_workflow_fanouts
		WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3`, tenant, flow, node).Scan(&exists); err != nil {
		return page, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT c.ordinal,c.run_id,c.state,s.run_id IS NOT NULL
		FROM scheduler_workflow_fanout_children c LEFT JOIN scheduled_results s ON s.run_id=c.run_id
		WHERE c.tenant_id=$1 AND c.workflow_id=$2 AND c.node_id=$3 AND c.ordinal>$4
		ORDER BY c.ordinal LIMIT 129`, tenant, flow, node, after)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(page.Items) == 128 {
			page.NextAfter = page.Items[127].Ordinal
			break
		}
		var child fanoutChild
		if err := rows.Scan(&child.Ordinal, &child.RunID, &child.State, &child.ResultReady); err != nil {
			return page, err
		}
		page.Items = append(page.Items, child)
	}
	return page, rows.Err()
}

func (h api) cancelWorkflowFanout(w http.ResponseWriter, req *http.Request) {
	err := h.repo.requestFanoutCancel(req.Context(), h.tenant, req.PathValue("id"), req.PathValue("node"))
	switch {
	case errors.Is(err, errRunFinalized):
		writeError(w, req, contract.TypeConflict, "fan-out already resolved")
	case err != nil:
		writeFanoutError(w, req, err)
	default:
		_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
	}
}

func (h api) workflowFanoutChildren(w http.ResponseWriter, req *http.Request) {
	after := 0
	if raw := req.URL.Query().Get("after"); raw != "" {
		var err error
		after, err = strconv.Atoi(raw)
		if err != nil || after < 0 || after > 1000000 {
			writeError(w, req, contract.TypeBadRequest, "invalid fan-out cursor")
			return
		}
	}
	result, err := h.repo.fanoutChildren(req.Context(), h.tenant, req.PathValue("id"), req.PathValue("node"), after)
	if err != nil {
		writeFanoutError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
}

func writeFanoutError(w http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, req, contract.TypeNotFound, "fan-out not found")
		return
	}
	writeError(w, req, contract.TypeUnavailable, "fan-out unavailable")
}
