package main

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

type compensationStatus struct {
	ReservationID string `json:"reservation_id"`
	Node          string `json:"node"`
	State         string `json:"state"`
}

func (h api) listCompensations(w http.ResponseWriter, req *http.Request) {
	ctx, flow := req.Context(), req.PathValue("id")
	var found int
	if err := h.repo.db.QueryRowContext(ctx, "SELECT 1 FROM scheduler_workflows WHERE tenant_id=$1 AND id=$2",
		h.tenant, flow).Scan(&found); err != nil {
		writeCompensationError(w, req, err)
		return
	}
	after := req.URL.Query().Get("after")
	if after != "" && !runCursorID.MatchString(after) {
		writeError(w, req, contract.TypeBadRequest, "invalid compensation cursor")
		return
	}
	rows, err := h.repo.db.QueryContext(ctx, `SELECT reservation_id,node_id,state FROM scheduler_compensations
		WHERE tenant_id=$1 AND workflow_id=$2 AND reservation_id>$3 ORDER BY reservation_id LIMIT 129`, h.tenant, flow, after)
	if err != nil {
		writeCompensationError(w, req, err)
		return
	}
	defer rows.Close()
	items := make([]compensationStatus, 0)
	next := ""
	for rows.Next() {
		if len(items) == 128 {
			next = items[127].ReservationID
			break
		}
		var item compensationStatus
		if err := rows.Scan(&item.ReservationID, &item.Node, &item.State); err != nil {
			writeCompensationError(w, req, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeCompensationError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusOK, struct {
		Items []compensationStatus `json:"items"`
		Next  string               `json:"next_after,omitempty"`
	}{items, next}, nil)
}

func (h api) resolveCompensation(w http.ResponseWriter, req *http.Request) {
	identity, _ := req.Context().Value(auditRequestKey{}).(auditIdentity)
	if h.compensationActor == "" || identity.actor != h.compensationActor {
		writeError(w, req, contract.TypeForbidden, "manual compensation forbidden")
		return
	}
	var input struct {
		Action string `json:"action"`
	}
	if !decodeRequest(w, req, &input) {
		return
	}
	key := req.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) || (input.Action != "retry" && input.Action != "release") {
		writeError(w, req, contract.TypeBadRequest, "invalid compensation action")
		return
	}
	err := h.repo.resolveCompensation(req.Context(), h.tenant, req.PathValue("id"),
		req.PathValue("run"), key, input.Action)
	if err != nil {
		writeCompensationError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
}

func writeCompensationError(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "compensation not found")
	case errors.Is(err, errRunFinalized), errors.Is(err, errRequestConflict):
		writeError(w, req, contract.TypeConflict, "compensation action conflicts with current state")
	default:
		writeError(w, req, contract.TypeUnavailable, "compensation unavailable")
	}
}
