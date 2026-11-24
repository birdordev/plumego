package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/birdordev/plumego/contract"
)

type workflowPin struct {
	Name    string `json:"definition_name"`
	Version int    `json:"definition_version,omitempty"`
}

func (p workflowPin) valid(activeAllowed bool) bool {
	return runCursorID.MatchString(p.Name) && p.Version <= 2147483647 &&
		(p.Version > 0 || (activeAllowed && p.Version == 0))
}

func (p workflowPin) fingerprint() [sha256.Size]byte {
	return sha256.Sum256([]byte("workflow-definition\x00" + p.Name + "\x00" + strconv.Itoa(p.Version)))
}

func (r repository) workflowCommandReplay(ctx context.Context, tenant, key string, hash [sha256.Size]byte) (string, error) {
	if !validIdempotencyKey(key) {
		return "", errInvalidTask
	}
	var id string
	var previous []byte
	err := r.db.QueryRowContext(ctx, "SELECT id,request_hash FROM scheduler_workflows WHERE tenant_id=$1 AND request_key=$2",
		tenant, key).Scan(&id, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare(previous, hash[:]) != 1 {
		return "", errRequestConflict
	}
	return id, nil
}

func (r repository) loadWorkflowDefinition(ctx context.Context, tenant string, pin workflowPin) (workflowDefinition, workflowPin, error) {
	var graph workflowDefinition
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT d.version,d.graph FROM scheduler_workflow_definitions d
		WHERE d.tenant_id=$1 AND d.name=$2 AND d.version=CASE WHEN $3=0 THEN (
			SELECT active_version FROM scheduler_workflow_releases a WHERE a.tenant_id=$1 AND a.name=$2
		) ELSE $3 END`, tenant, pin.Name, pin.Version).Scan(&pin.Version, &raw)
	if err != nil {
		return graph, pin, err
	}
	return graph, pin, json.Unmarshal(raw, &graph)
}

func (r repository) publishWorkflowDefinition(ctx context.Context, tenant string, pin workflowPin, graph workflowDefinition) error {
	if err := validateWorkflow(tenant, &graph); err != nil {
		return err
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return err
	}
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO scheduler_workflow_definitions (tenant_id,name,version,graph)
		VALUES ($1,$2,$3,$4::jsonb) ON CONFLICT DO NOTHING`, tenant, pin.Name, pin.Version, raw)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		var matches bool
		if err := tx.QueryRowContext(ctx, `SELECT graph=$4::jsonb FROM scheduler_workflow_definitions
			WHERE tenant_id=$1 AND name=$2 AND version=$3`, tenant, pin.Name, pin.Version, raw).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return errRequestConflict
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1,'workflow.definition_publish',$2,'{}'::jsonb,
		jsonb_build_object('version',$3::integer))`, tenant, pin.Name, pin.Version); err != nil {
		return err
	}
	return tx.Commit()
}

func (r repository) activateWorkflowDefinition(ctx context.Context, tenant, key string, pin workflowPin, expected int) error {
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var target int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM scheduler_workflow_definitions
		WHERE tenant_id=$1 AND name=$2 AND version=$3`, tenant, pin.Name, pin.Version).Scan(&target); err != nil {
		return err
	}
	recorded, err := tx.ExecContext(ctx, `INSERT INTO scheduler_workflow_release_commands
		(tenant_id,name,request_key,version,expected_version) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT DO NOTHING`, tenant, pin.Name, key, pin.Version, expected)
	if err != nil {
		return err
	}
	recordedCount, err := recorded.RowsAffected()
	if err != nil {
		return err
	}
	if recordedCount == 0 {
		var version, originalExpected int
		if err := tx.QueryRowContext(ctx, `SELECT version,expected_version FROM scheduler_workflow_release_commands
			WHERE tenant_id=$1 AND name=$2 AND request_key=$3`, tenant, pin.Name, key).Scan(&version, &originalExpected); err != nil {
			return err
		}
		if version != pin.Version || originalExpected != expected {
			return errRequestConflict
		}
		return tx.Commit()
	}
	var change sql.Result
	if expected == 0 {
		change, err = tx.ExecContext(ctx, `INSERT INTO scheduler_workflow_releases (tenant_id,name,active_version)
			VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, pin.Name, pin.Version)
	} else {
		change, err = tx.ExecContext(ctx, `UPDATE scheduler_workflow_releases SET active_version=$3
			WHERE tenant_id=$1 AND name=$2 AND active_version=$4 AND active_version<>$3`, tenant, pin.Name, pin.Version, expected)
	}
	if err != nil {
		return err
	}
	count, err := change.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		var current int
		err := tx.QueryRowContext(ctx, `SELECT active_version FROM scheduler_workflow_releases
			WHERE tenant_id=$1 AND name=$2`, tenant, pin.Name).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && current != pin.Version) {
			return errRequestConflict
		}
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1,'workflow.definition_activate',$2,
		jsonb_build_object('version',$3::integer),jsonb_build_object('version',$4::integer))`,
		tenant, pin.Name, expected, pin.Version); err != nil {
		return err
	}
	return tx.Commit()
}

func (h api) authorizeWorkflowRelease(w http.ResponseWriter, req *http.Request) bool {
	identity, _ := req.Context().Value(auditRequestKey{}).(auditIdentity)
	if h.workflowReleaseActor == "" || identity.actor != h.workflowReleaseActor {
		writeError(w, req, contract.TypeForbidden, "workflow release forbidden")
		return false
	}
	return true
}

func (h api) publishWorkflowDefinition(w http.ResponseWriter, req *http.Request) {
	if !h.authorizeWorkflowRelease(w, req) {
		return
	}
	var graph workflowDefinition
	version, err := strconv.Atoi(req.PathValue("version"))
	pin := workflowPin{Name: req.PathValue("name"), Version: version}
	if err != nil || !pin.valid(false) {
		writeError(w, req, contract.TypeBadRequest, "invalid workflow version")
		return
	}
	if !decodeRequest(w, req, &graph) {
		return
	}
	for _, node := range graph.Nodes {
		if (node.Task != nil && node.Task.Topic == sandboxTaskType) ||
			(node.Fanout != nil && node.Fanout.Task.Topic == sandboxTaskType) {
			if !h.requireTaskRuntime(w, req, sandboxTaskType) {
				return
			}
		}
	}
	if err := h.repo.publishWorkflowDefinition(req.Context(), h.tenant, pin, graph); err != nil {
		writeWorkflowReleaseError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
}

func (h api) activateWorkflowDefinition(w http.ResponseWriter, req *http.Request) {
	if !h.authorizeWorkflowRelease(w, req) {
		return
	}
	var input struct {
		Version  int  `json:"version"`
		Expected *int `json:"expected_version"`
	}
	if !decodeRequest(w, req, &input) {
		return
	}
	pin := workflowPin{Name: req.PathValue("name"), Version: input.Version}
	key := req.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) || !pin.valid(false) || input.Expected == nil || *input.Expected < 0 || *input.Expected > 2147483647 {
		writeError(w, req, contract.TypeBadRequest, "invalid workflow version selection")
		return
	}
	if err := h.repo.activateWorkflowDefinition(req.Context(), h.tenant, key, pin, *input.Expected); err != nil {
		writeWorkflowReleaseError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
}

func writeWorkflowReleaseError(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "workflow definition not found")
	case errors.Is(err, errInvalidTask):
		writeError(w, req, contract.TypeBadRequest, "invalid workflow definition")
	case errors.Is(err, errRequestConflict):
		writeError(w, req, contract.TypeConflict, "workflow definition conflicts with stored version")
	default:
		writeError(w, req, contract.TypeUnavailable, "workflow definition unavailable")
	}
}
