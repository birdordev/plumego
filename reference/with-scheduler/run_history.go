package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
)

type plannedOccurrence struct {
	RunID           string       `json:"run_id"`
	ScheduleID      string       `json:"schedule_id"`
	ScheduleVersion int64        `json:"schedule_version"`
	ScheduledAt     time.Time    `json:"scheduled_at"`
	Calendar        *calendarPin `json:"calendar,omitempty"`
}

func (r repository) plannedOccurrence(ctx context.Context, tenant, scheduleID string, at time.Time) (plannedOccurrence, error) {
	var result plannedOccurrence
	var pin calendarPin
	err := r.db.QueryRowContext(ctx, `SELECT id, schedule_id, schedule_version, scheduled_at, COALESCE(calendar_name, ''), COALESCE(calendar_version, 0)
		FROM scheduled_runs WHERE tenant_id = $1 AND schedule_id = $2 AND scheduled_at = $3 AND request_key IS NULL`,
		tenant, scheduleID, at).Scan(&result.RunID, &result.ScheduleID, &result.ScheduleVersion, &result.ScheduledAt, &pin.Name, &pin.Version)
	result.Calendar = pin.binding()
	return result, err
}

func (h api) occurrence(w http.ResponseWriter, req *http.Request) {
	params, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil || len(params) != 1 || len(params["scheduled_at"]) != 1 {
		writeError(w, req, contract.TypeBadRequest, "one scheduled_at is required")
		return
	}
	at, err := time.Parse(time.RFC3339Nano, params.Get("scheduled_at"))
	at = at.UTC()
	if err != nil || at.Nanosecond()%1000 != 0 || at.Year() < 1 || at.Year() > 9999 {
		writeError(w, req, contract.TypeBadRequest, "scheduled_at must be RFC 3339 with at most microsecond precision")
		return
	}
	result, err := h.repo.plannedOccurrence(req.Context(), h.tenant, req.PathValue("id"), at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "planned occurrence not found")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "planned occurrence unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
	}
}

const attemptPageSize = 50

type runAttempt struct {
	Number             int        `json:"attempt"`
	ClaimedAt          time.Time  `json:"claimed_at"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	Success            *bool      `json:"success,omitempty"`
	FailureCode        string     `json:"failure_code,omitempty"`
	ExecutionNo        *int       `json:"execution_no,omitempty"`
	ExecutionStartedAt *time.Time `json:"execution_started_at,omitempty"`
	ExecutionOutcome   string     `json:"execution_outcome,omitempty"`
	Finalization       string     `json:"finalization_operation,omitempty"`
	FinalizationState  string     `json:"finalization_state,omitempty"`
	ConfirmationError  string     `json:"finalization_failure,omitempty"`
}

type attemptPage struct {
	Items      []runAttempt `json:"items"`
	NextBefore int          `json:"next_before,omitempty"`
}

func (r repository) runAttempts(ctx context.Context, tenant, id string, before int) (attemptPage, error) {
	var exists int
	if err := r.db.QueryRowContext(ctx, "SELECT 1 FROM scheduled_runs WHERE tenant_id = $1 AND id = $2", tenant, id).Scan(&exists); err != nil {
		return attemptPage{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.attempt_no, a.started_at, a.finished_at, a.success, a.reason,
		a.execution_no, a.execution_started_at, COALESCE(a.execution_outcome, ''),
		COALESCE(a.finalization_operation, ''), COALESCE(a.finalization_state, ''), COALESCE(a.finalization_failure, '')
		FROM scheduled_runs r JOIN scheduler_attempt_history a ON r.id = a.task_id
		WHERE r.tenant_id = $1 AND r.id = $2 AND ($3 = 0 OR a.attempt_no < $3)
		ORDER BY a.attempt_no DESC LIMIT $4`, tenant, id, before, attemptPageSize+1)
	if err != nil {
		return attemptPage{}, err
	}
	defer rows.Close()
	page := attemptPage{Items: make([]runAttempt, 0)}
	for rows.Next() {
		if len(page.Items) == attemptPageSize {
			page.NextBefore = page.Items[len(page.Items)-1].Number
			break
		}
		var item runAttempt
		var success bool
		var reason sql.NullString
		if err := rows.Scan(&item.Number, &item.ClaimedAt, &item.FinishedAt, &success, &reason,
			&item.ExecutionNo, &item.ExecutionStartedAt, &item.ExecutionOutcome, &item.Finalization,
			&item.FinalizationState, &item.ConfirmationError); err != nil {
			return attemptPage{}, err
		}
		if item.FinishedAt != nil {
			item.Success = &success
			if !success {
				item.FailureCode = attemptFailureCode(reason.String)
			}
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

func attemptFailureCode(reason string) string {
	switch reason {
	case "permanent task failure":
		return "permanent"
	case "task outcome unknown":
		return "outcome_unknown"
	case "task rate limited":
		return "rate_limited"
	case "task execution timed out":
		return "execution_timeout"
	case "task queue wait limit exceeded":
		return "queue_timeout"
	case "task age limit exceeded", "task retry would exceed age limit":
		return "age_limit"
	case "max attempts exceeded":
		return "attempts_exhausted"
	case "worker shutdown":
		return "worker_shutdown"
	case "schedule claim paused":
		return "schedule_claim_paused"
	case "execution overlap skipped":
		return "overlap_skipped"
	case "execution overlap deferred":
		return "overlap_deferred"
	case "execution budget deferred":
		return "budget_deferred"
	case "worker lease expired":
		return "lease_expired"
	case "task canceled":
		return "canceled"
	default:
		return "task_failed"
	}
}

func (h api) attempts(w http.ResponseWriter, req *http.Request) {
	params, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil || (len(params) != 0 && (len(params) != 1 || len(params["before"]) != 1)) {
		writeError(w, req, contract.TypeBadRequest, "invalid attempt cursor")
		return
	}
	before := 0
	if len(params) != 0 {
		n, parseErr := strconv.ParseInt(params.Get("before"), 10, 32)
		if parseErr != nil || n < 1 {
			writeError(w, req, contract.TypeBadRequest, "invalid attempt cursor")
			return
		}
		before = int(n)
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	result, err := h.repo.runAttempts(ctx, h.tenant, req.PathValue("id"), before)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "run not found")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "run attempts unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
	}
}
