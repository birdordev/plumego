package main

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/birdordev/plumego/contract"
)

func (h api) configureEventSource(w http.ResponseWriter, req *http.Request) {
	if !h.authorizeWorkflowRelease(w, req) {
		return
	}
	var input eventSourceConfig
	if !decodeRequest(w, req, &input) {
		return
	}
	source := req.PathValue("source")
	if !input.valid(source) {
		writeError(w, req, contract.TypeBadRequest, "invalid workflow event source")
		return
	}
	if err := h.repo.configureEventSource(req.Context(), h.tenant, source, input); err != nil {
		writeWorkflowEventError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
}

func (h api) ingestWorkflowEvent(w http.ResponseWriter, req *http.Request) {
	var input workflowEventInput
	if !decodeRequest(w, req, &input) {
		return
	}
	if !input.valid() {
		writeError(w, req, contract.TypeBadRequest, "invalid workflow event")
		return
	}
	source := req.PathValue("source")
	if err := h.repo.ingestWorkflowEvent(req.Context(), h.tenant, source, input); err != nil {
		writeWorkflowEventError(w, req, err)
		return
	}
	w.Header().Set("Location", "/event-sources/"+source+"/events/"+strconv.FormatInt(input.Offset, 10))
	_ = contract.WriteResponse(w, req, http.StatusAccepted, map[string]int64{"offset": input.Offset}, nil)
}

func (h api) eventSourceStatus(w http.ResponseWriter, req *http.Request) {
	var state struct {
		Cursor  int64 `json:"cursor"`
		Highest int64 `json:"highest_offset"`
	}
	err := h.repo.db.QueryRowContext(req.Context(), `SELECT cursor_offset,highest_offset FROM scheduler_event_sources
		WHERE tenant_id=$1 AND source=$2`, h.tenant, req.PathValue("source")).Scan(&state.Cursor, &state.Highest)
	if err != nil {
		writeWorkflowEventError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusOK, state, nil)
}

func (h api) workflowEventStatus(w http.ResponseWriter, req *http.Request) {
	offset, err := strconv.ParseInt(req.PathValue("offset"), 10, 64)
	if err != nil || offset < 1 || offset == math.MaxInt64 {
		writeError(w, req, contract.TypeBadRequest, "invalid event position")
		return
	}
	var state struct {
		workflowEventInput
		Outcome    string `json:"outcome"`
		WorkflowID string `json:"workflow_id,omitempty"`
	}
	state.Offset = offset
	err = h.repo.db.QueryRowContext(req.Context(), `SELECT event_id,occurred_at,payload,outcome,COALESCE(workflow_id,'')
		FROM scheduler_source_events WHERE tenant_id=$1 AND source=$2 AND source_offset=$3`,
		h.tenant, req.PathValue("source"), offset).Scan(&state.ID, &state.OccurredAt, &state.Payload, &state.Outcome, &state.WorkflowID)
	if err != nil {
		writeWorkflowEventError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusOK, state, nil)
}

func (h api) startEventReplay(w http.ResponseWriter, req *http.Request) {
	if !h.authorizeWorkflowRelease(w, req) {
		return
	}
	var span eventReplayRange
	if !decodeRequest(w, req, &span) {
		return
	}
	key := req.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) || span.First < 1 || span.Last < span.First || span.Last == math.MaxInt64 ||
		span.Last-span.First >= 1000 {
		writeError(w, req, contract.TypeBadRequest, "invalid event replay range")
		return
	}
	source := req.PathValue("source")
	id, err := h.repo.startEventReplay(req.Context(), h.tenant, source, key, span)
	if err != nil {
		writeWorkflowEventError(w, req, err)
		return
	}
	w.Header().Set("Location", "/event-sources/"+source+"/replays/"+id)
	_ = contract.WriteResponse(w, req, http.StatusAccepted, map[string]string{"id": id}, nil)
}

func (h api) eventReplayStatus(w http.ResponseWriter, req *http.Request) {
	state, err := h.repo.eventReplayStatus(req.Context(), h.tenant, req.PathValue("source"), req.PathValue("id"))
	if err != nil {
		writeWorkflowEventError(w, req, err)
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusOK, state, nil)
}

func writeWorkflowEventError(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "workflow event record not found")
	case errors.Is(err, errEventIngestForbidden):
		writeError(w, req, contract.TypeForbidden, "event source access forbidden")
	case errors.Is(err, errRequestConflict), errors.Is(err, errEventReplayRange):
		writeError(w, req, contract.TypeConflict, "event request conflicts with persisted stream")
	default:
		writeError(w, req, contract.TypeUnavailable, "workflow event store unavailable")
	}
}
