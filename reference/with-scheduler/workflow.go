package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
)

type workflowNode struct {
	ID        string              `json:"id"`
	DependsOn []string            `json:"depends_on,omitempty"`
	Task      *submission         `json:"task,omitempty"`
	Wait      *workflowWaitSpec   `json:"wait,omitempty"`
	Fanout    *workflowFanoutSpec `json:"fanout,omitempty"`
}

type workflowDefinition struct {
	Nodes []workflowNode `json:"nodes"`
}

func validateWorkflow(tenant string, graph *workflowDefinition) error {
	if len(graph.Nodes) == 0 || len(graph.Nodes) > 64 {
		return errInvalidTask
	}
	degree := make(map[string]int, len(graph.Nodes))
	children := make(map[string][]string, len(graph.Nodes))
	var ready []string
	waitKeys := make(map[string]bool, len(graph.Nodes))
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if _, exists := degree[n.ID]; exists || !runCursorID.MatchString(n.ID) || len(n.DependsOn) >= len(graph.Nodes) {
			return errInvalidTask
		}
		if workflowNodeKinds(n) != 1 {
			return errInvalidTask
		}
		if n.Task != nil {
			cmd, err := prepareScopedSubmission(tenant, n.ID, *n.Task, true)
			if err != nil {
				return err
			}
			n.Task = &cmd.input
		} else if n.Wait != nil {
			w := *n.Wait
			if !w.valid() || waitKeys[w.Key] {
				return errInvalidTask
			}
			w.Deadline = w.Deadline.UTC().Truncate(time.Microsecond)
			n.Wait = &w
			waitKeys[w.Key] = true
		} else {
			f := *n.Fanout
			cmd, err := prepareScopedSubmission(tenant, n.ID, f.Task, true)
			if err != nil || !f.valid() {
				return errInvalidTask
			}
			f.Task = cmd.input
			n.Fanout = &f
		}
		degree[n.ID] = len(n.DependsOn)
		if len(n.DependsOn) == 0 {
			ready = append(ready, n.ID)
		}
	}
	edges := 0
	for _, n := range graph.Nodes {
		seen := make(map[string]bool, len(n.DependsOn))
		for _, predecessor := range n.DependsOn {
			if _, exists := degree[predecessor]; !exists || predecessor == n.ID || seen[predecessor] {
				return errInvalidTask
			}
			seen[predecessor] = true
			children[predecessor] = append(children[predecessor], n.ID)
			edges++
		}
	}
	if edges > 512 {
		return errInvalidTask
	}
	for i := 0; i < len(ready); i++ {
		for _, child := range children[ready[i]] {
			degree[child]--
			if degree[child] == 0 {
				ready = append(ready, child)
			}
		}
	}
	if len(ready) != len(graph.Nodes) {
		return errInvalidTask
	}
	return nil
}

func (r repository) createWorkflow(ctx context.Context, tenant, key string, graph workflowDefinition) (string, error) {
	return r.createPinnedWorkflow(ctx, tenant, key, graph, workflowPin{}, nil)
}

func (r repository) createPinnedWorkflow(ctx context.Context, tenant, key string, graph workflowDefinition, pin workflowPin, intentHash *[sha256.Size]byte) (string, error) {
	if !validIdempotencyKey(key) {
		return "", errInvalidTask
	}
	if err := validateWorkflow(tenant, &graph); err != nil {
		return "", err
	}
	body, err := json.Marshal(graph)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	if intentHash != nil {
		hash = *intentHash
	}
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := insertWorkflowInTx(ctx, tx, tenant, key, body, len(graph.Nodes), pin, hash)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func insertWorkflowInTx(ctx context.Context, tx *sql.Tx, tenant, key string, body []byte, nodeCount int, pin workflowPin, hash [sha256.Size]byte) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO scheduler_workflows (tenant_id,id,request_key,request_hash,definition_name,definition_version)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,0)) ON CONFLICT (tenant_id,request_key) DO NOTHING RETURNING id`,
		tenant, rand.Text(), key, hash[:], pin.Name, pin.Version).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var previous []byte
		if err := tx.QueryRowContext(ctx, `SELECT id,request_hash FROM scheduler_workflows
			WHERE tenant_id=$1 AND request_key=$2`, tenant, key).Scan(&id, &previous); err != nil {
			return "", err
		}
		if subtle.ConstantTimeCompare(previous, hash[:]) != 1 {
			return "", errRequestConflict
		}
		return id, nil
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_workflow_nodes
		(tenant_id, workflow_id, node_id, state, submission)
		SELECT $1, $2, n->>'id',
			CASE WHEN COALESCE(jsonb_array_length(n->'depends_on'), 0)=0 THEN 'ready' ELSE 'blocked' END, COALESCE(n->'task',n->'fanout'->'task','{}'::jsonb)
		FROM jsonb_array_elements($3::jsonb->'nodes') n`, tenant, id, body); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_workflow_edges (tenant_id, workflow_id, predecessor, successor)
		SELECT $1, $2, parent.id, n->>'id' FROM jsonb_array_elements($3::jsonb->'nodes') n
		CROSS JOIN LATERAL jsonb_array_elements_text(n->'depends_on') parent(id)`, tenant, id, body); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_workflow_waits
		(tenant_id, workflow_id, node_id, signal_key, signal_actor, deadline)
		SELECT $1, $2, n->>'id', n->'wait'->>'key', n->'wait'->>'actor', (n->'wait'->>'deadline')::timestamptz
		FROM jsonb_array_elements($3::jsonb->'nodes') n WHERE n ? 'wait'`, tenant, id, body); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_workflow_fanouts
		(tenant_id,workflow_id,node_id,total,max_in_flight)
		SELECT $1,$2,n->>'id',(n->'fanout'->>'count')::integer,(n->'fanout'->>'max_in_flight')::integer
		FROM jsonb_array_elements($3::jsonb->'nodes') n WHERE n ? 'fanout'`, tenant, id, body); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1, 'workflow.create', $2, '{}'::jsonb,
		jsonb_build_object('nodes', $3::integer))`, tenant, id, nodeCount); err != nil {
		return "", err
	}
	return id, nil
}

func (r repository) dispatchWorkflowNode(ctx context.Context) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var tenant, workflow, node string
	var now time.Time
	var raw []byte
	var isWait, isFanout bool
	err = tx.QueryRowContext(ctx, `SELECT w.tenant_id, w.id
		FROM scheduler_workflow_nodes n JOIN scheduler_workflows w
			ON w.tenant_id=n.tenant_id AND w.id=n.workflow_id
		WHERE n.state='ready' AND w.state='running' AND w.dispatch_after <= statement_timestamp()
		ORDER BY w.dispatch_after, w.created_at, w.tenant_id, w.id LIMIT 1 FOR UPDATE OF w SKIP LOCKED`).
		Scan(&tenant, &workflow)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// 取得流程行锁后重新读取，避免竞争副本基于旧快照再次提交同一个节点。
	err = tx.QueryRowContext(ctx, `SELECT n.node_id, n.submission, statement_timestamp(), w.node_id IS NOT NULL, f.node_id IS NOT NULL
		FROM scheduler_workflow_nodes n LEFT JOIN scheduler_workflow_waits w
			ON (w.tenant_id,w.workflow_id,w.node_id)=(n.tenant_id,n.workflow_id,n.node_id)
		LEFT JOIN scheduler_workflow_fanouts f ON (f.tenant_id,f.workflow_id,f.node_id)=(n.tenant_id,n.workflow_id,n.node_id)
		WHERE n.tenant_id=$1 AND n.workflow_id=$2 AND n.state='ready' ORDER BY n.node_id LIMIT 1`, tenant, workflow).
		Scan(&node, &raw, &now, &isWait, &isFanout)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if isWait {
		if err := activateWorkflowWait(ctx, tx, tenant, workflow, node); err != nil {
			return "", err
		}
		return workflow, tx.Commit()
	}
	if isFanout {
		if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflow_fanouts SET activated=true
			WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3`, tenant, workflow, node); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflow_nodes SET state='waiting'
			WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3`, tenant, workflow, node); err != nil {
			return "", err
		}
		return workflow, tx.Commit()
	}
	var task submission
	if err := json.Unmarshal(raw, &task); err != nil {
		return "", err
	}
	if task.RunAt.IsZero() {
		task.RunAt = now
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT queue_workflow_node"); err != nil {
		return "", err
	}
	id := rand.Text()
	if _, err := tx.ExecContext(ctx, "INSERT INTO scheduled_runs (id,tenant_id,scheduled_at) VALUES ($1,$2,$3)", id, tenant, task.RunAt); err != nil {
		return "", err
	}
	err = r.insertTask(ctx, tx, id, tenant, task, task.RunAt, traceSource{})
	if errors.Is(err, errTenantCapacity) {
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT queue_workflow_node"); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflows SET dispatch_after=statement_timestamp()+interval '1 second'
			WHERE tenant_id=$1 AND id=$2`, tenant, workflow); err != nil {
			return "", err
		}
		return workflow, tx.Commit()
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_workflow_nodes SET state='queued',run_id=$4
		WHERE tenant_id=$1 AND workflow_id=$2 AND node_id=$3`, tenant, workflow, node, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

type workflowNodeStatus struct {
	ID        string                `json:"id"`
	State     string                `json:"state"`
	RunID     string                `json:"run_id,omitempty"`
	DependsOn []string              `json:"depends_on,omitempty"`
	SignalKey string                `json:"signal_key,omitempty"`
	Deadline  *time.Time            `json:"deadline,omitempty"`
	Fanout    *workflowFanoutStatus `json:"fanout,omitempty"`
}

type workflowStatus struct {
	DefinitionName    string               `json:"definition_name,omitempty"`
	DefinitionVersion int                  `json:"definition_version,omitempty"`
	EventSource       string               `json:"event_source,omitempty"`
	EventOffset       *int64               `json:"event_offset,omitempty"`
	ID                string               `json:"id"`
	State             string               `json:"state"`
	Nodes             []workflowNodeStatus `json:"nodes"`
}

func (r repository) workflowStatus(ctx context.Context, tenant, id string) (workflowStatus, error) {
	var result workflowStatus
	var nodes []byte
	err := r.db.QueryRowContext(ctx, `SELECT w.id, w.state, COALESCE(w.definition_name,''), COALESCE(w.definition_version,0),
		COALESCE(e.source,''),e.source_offset, (
		SELECT jsonb_agg(jsonb_build_object('id', n.node_id, 'state', n.state, 'run_id', n.run_id, 'signal_key', wait.signal_key, 'deadline', wait.deadline,
			'fanout', CASE WHEN f.node_id IS NOT NULL THEN jsonb_build_object('total',f.total,'created',f.next_ordinal,
				'active',f.active,'succeeded',f.succeeded,'failed',f.failed,'canceled',f.canceled,'cancel_requested',f.cancel_requested) END,
			'depends_on', (
			SELECT jsonb_agg(e.predecessor ORDER BY e.predecessor) FROM scheduler_workflow_edges e
				WHERE e.tenant_id=n.tenant_id AND e.workflow_id=n.workflow_id AND e.successor=n.node_id
		)) ORDER BY n.node_id) FROM scheduler_workflow_nodes n LEFT JOIN scheduler_workflow_waits wait
			ON (wait.tenant_id,wait.workflow_id,wait.node_id)=(n.tenant_id,n.workflow_id,n.node_id)
			LEFT JOIN scheduler_workflow_fanouts f ON (f.tenant_id,f.workflow_id,f.node_id)=(n.tenant_id,n.workflow_id,n.node_id)
			WHERE n.tenant_id=w.tenant_id AND n.workflow_id=w.id
	) FROM scheduler_workflows w LEFT JOIN scheduler_source_events e ON e.tenant_id=w.tenant_id AND e.workflow_id=w.id
	WHERE w.tenant_id=$1 AND w.id=$2`, tenant, id).
		Scan(&result.ID, &result.State, &result.DefinitionName, &result.DefinitionVersion, &result.EventSource, &result.EventOffset, &nodes)
	if err != nil {
		return result, err
	}
	return result, json.Unmarshal(nodes, &result.Nodes)
}

func (h api) createWorkflow(w http.ResponseWriter, req *http.Request) {
	var input struct {
		workflowDefinition
		workflowPin
	}
	if !decodeRequest(w, req, &input) {
		return
	}
	graph, pin := input.workflowDefinition, input.workflowPin
	key := req.Header.Get("Idempotency-Key")
	if strings.HasPrefix(key, "event:") {
		writeError(w, req, contract.TypeBadRequest, "invalid workflow idempotency key")
		return
	}
	var intentHash *[sha256.Size]byte
	if pin.Name != "" {
		if !pin.valid(true) || len(graph.Nodes) != 0 {
			writeError(w, req, contract.TypeBadRequest, "invalid workflow definition selection")
			return
		}
		intent := pin.fingerprint()
		intentHash = &intent
		prior, err := h.repo.workflowCommandReplay(req.Context(), h.tenant, key, intent)
		if err != nil {
			writeWorkflowReleaseError(w, req, err)
			return
		}
		if prior != "" {
			writeWorkflowAccepted(w, req, prior)
			return
		}
		graph, pin, err = h.repo.loadWorkflowDefinition(req.Context(), h.tenant, pin)
		if err != nil {
			writeWorkflowReleaseError(w, req, err)
			return
		}
	} else if pin.Version != 0 {
		writeError(w, req, contract.TypeBadRequest, "workflow definition name is required")
		return
	}
	for _, n := range graph.Nodes {
		if (n.Task != nil && !h.requireTaskRuntime(w, req, n.Task.Topic)) ||
			(n.Fanout != nil && !h.requireTaskRuntime(w, req, n.Fanout.Task.Topic)) {
			return
		}
	}
	id, err := h.repo.createPinnedWorkflow(req.Context(), h.tenant, key, graph, pin, intentHash)
	if writeSubmissionError(w, req, err) {
		return
	}
	writeWorkflowAccepted(w, req, id)
}

func writeWorkflowAccepted(w http.ResponseWriter, req *http.Request, id string) {
	w.Header().Set("Location", "/workflows/"+id)
	_ = contract.WriteResponse(w, req, http.StatusAccepted, map[string]string{"id": id}, nil)
}

func (h api) workflowStatus(w http.ResponseWriter, req *http.Request) {
	result, err := h.repo.workflowStatus(req.Context(), h.tenant, req.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "workflow not found")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "workflow status unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
	}
}
