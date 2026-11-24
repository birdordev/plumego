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
	"time"

	"github.com/birdordev/plumego/contract"
)

const (
	deadLetterPageSize = 50
	replayWindowBudget = 25
	replayRunBudget    = 2
)

type deadLetter struct {
	ID              int64      `json:"id"`
	RunID           string     `json:"run_id"`
	ScheduleID      *string    `json:"schedule_id,omitempty"`
	ScheduleVersion *int64     `json:"schedule_version,omitempty"`
	Attempt         *int       `json:"attempt,omitempty"`
	Topic           string     `json:"topic"`
	TaskVersion     string     `json:"task_version"`
	FailureCode     string     `json:"failure_code"`
	FailedAt        time.Time  `json:"failed_at"`
	ReplayedAt      *time.Time `json:"replayed_at,omitempty"`
}

type deadLetterPage struct {
	Items      []deadLetter `json:"items"`
	NextBefore int64        `json:"next_before,omitempty"`
}

func (r repository) deadLetters(ctx context.Context, tenant, runID string, before int64) (deadLetterPage, error) {
	if _, err := r.runStatus(ctx, tenant, runID); err != nil {
		return deadLetterPage{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT entry_id, run_id, schedule_id, schedule_version, attempt_no,
		topic, task_version, failure_code, failed_at, replayed_at FROM scheduler_dead_letters
		WHERE tenant_id = $1 AND run_id = $2 AND ($3 = 0 OR entry_id < $3)
		ORDER BY entry_id DESC LIMIT $4`, tenant, runID, before, deadLetterPageSize+1)
	if err != nil {
		return deadLetterPage{}, err
	}
	defer rows.Close()
	page := deadLetterPage{Items: make([]deadLetter, 0)}
	for rows.Next() {
		if len(page.Items) == deadLetterPageSize {
			page.NextBefore = page.Items[len(page.Items)-1].ID
			break
		}
		var item deadLetter
		if err := rows.Scan(&item.ID, &item.RunID, &item.ScheduleID, &item.ScheduleVersion,
			&item.Attempt, &item.Topic, &item.TaskVersion, &item.FailureCode, &item.FailedAt, &item.ReplayedAt); err != nil {
			return deadLetterPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

type replaySpec struct {
	Topic      string    `json:"topic"`
	Failure    string    `json:"failure_code"`
	FailedFrom time.Time `json:"failed_from"`
	FailedTo   time.Time `json:"failed_to"`
	Limit      int       `json:"limit"`
}

type replayItem struct {
	EntryID int64  `json:"entry_id"`
	RunID   string `json:"run_id"`
}

func (s replaySpec) valid() bool {
	if s.Topic != "" {
		if _, ok := lookupTaskContract(s.Topic, 1); !ok {
			return false
		}
	}
	switch s.Failure {
	case "", "permanent", "outcome_unknown", "queue_timeout", "age_limit", "attempts_exhausted":
	default:
		return false
	}
	return !s.FailedFrom.IsZero() && !s.FailedTo.IsZero() &&
		s.FailedTo.After(s.FailedFrom) && s.FailedTo.Sub(s.FailedFrom) <= 7*24*time.Hour &&
		s.Limit >= 1 && s.Limit <= replayWindowBudget
}

func selectReplay(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, tenant string, input replaySpec, lock bool) ([]replayItem, error) {
	query := `SELECT d.entry_id, d.run_id FROM scheduler_dead_letters d
		JOIN scheduled_runs r ON r.id = d.run_id AND r.tenant_id = d.tenant_id
		JOIN mq_tasks q ON q.id = d.run_id AND q.tenant_id = d.tenant_id
		WHERE d.tenant_id = $1 AND d.failed_at >= $2 AND d.failed_at < $3
		AND ($4 = '' OR d.topic = $4) AND ($5 = '' OR d.failure_code = $5)
		AND d.replayed_at IS NULL AND d.failure_code <> 'outcome_unknown'
		AND q.status = 'dead' AND q.attempts = d.attempt_no AND q.last_error_at = d.failed_at
		AND NOT EXISTS (SELECT 1 FROM scheduler_workflow_nodes n WHERE n.run_id = q.id)
		AND NOT EXISTS (SELECT 1 FROM scheduler_workflow_fanout_children n WHERE n.run_id = q.id)
		AND NOT EXISTS (SELECT 1 FROM scheduler_compensation_runs j WHERE j.run_id = q.id)
		AND (q.expires_at IS NULL OR q.expires_at > clock_timestamp())
		AND r.dead_letter_replays < $6
		ORDER BY d.failed_at, d.entry_id LIMIT $7`
	if lock {
		query += " FOR UPDATE OF q SKIP LOCKED"
	}
	rows, err := q.QueryContext(ctx, query, tenant, input.FailedFrom, input.FailedTo, input.Topic, input.Failure, replayRunBudget, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]replayItem, 0)
	for rows.Next() {
		var item replayItem
		if err := rows.Scan(&item.EntryID, &item.RunID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r repository) replayDeadLetters(ctx context.Context, tenant, key string, spec replaySpec) ([]replayItem, error) {
	if !spec.valid() || !validIdempotencyKey(key) {
		return nil, errInvalidTask
	}
	spec.FailedFrom, spec.FailedTo = spec.FailedFrom.UTC(), spec.FailedTo.UTC()
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_replay_budget (tenant_id, window_start, used)
		VALUES ($1, date_trunc('minute', clock_timestamp()), 0) ON CONFLICT DO NOTHING`, tenant); err != nil {
		return nil, err
	}
	var window time.Time
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT window_start, used FROM scheduler_replay_budget
		WHERE tenant_id = $1 FOR UPDATE`, tenant).Scan(&window, &used); err != nil {
		return nil, err
	}
	var priorHash, response []byte
	err = tx.QueryRowContext(ctx, `SELECT fingerprint, response FROM scheduler_replay_commands
		WHERE tenant_id = $1 AND command_key = $2`, tenant, key).Scan(&priorHash, &response)
	if err == nil {
		if subtle.ConstantTimeCompare(priorHash, hash[:]) != 1 {
			return nil, errRequestConflict
		}
		var prior []replayItem
		if err := json.Unmarshal(response, &prior); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return prior, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var current time.Time
	if err := tx.QueryRowContext(ctx, "SELECT date_trunc('minute', clock_timestamp())").Scan(&current); err != nil {
		return nil, err
	}
	if !window.Equal(current) {
		used = 0
	}
	if used >= replayWindowBudget {
		return nil, errTenantCapacity
	}
	spec.Limit = min(spec.Limit, replayWindowBudget-used)
	items, err := selectReplay(ctx, tx, tenant, spec, true)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if err := replayDeadLetter(ctx, tx, tenant, item); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_replay_budget SET window_start = $2, used = $3
		WHERE tenant_id = $1`, tenant, current, used+len(items)); err != nil {
		return nil, err
	}
	response, err = json.Marshal(items)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_replay_commands
		(tenant_id, command_key, fingerprint, response) VALUES ($1,$2,$3,$4)`, tenant, key, hash[:], response); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func replayDeadLetter(ctx context.Context, tx *sql.Tx, tenant string, item replayItem) error {
	source := sourceFromRequest(ctx)
	if _, err := tx.ExecContext(ctx, `UPDATE mq_tasks SET status = 'queued', available_at = clock_timestamp(),
		updated_at = clock_timestamp(), max_attempts = execution_attempts + 1,
		meta = (COALESCE(meta, '{}'::jsonb) - ARRAY['replay_request_id', 'replay_trace_id', 'replay_span_id'])
			|| jsonb_strip_nulls(jsonb_build_object('replay_request_id', NULLIF($3, ''),
				'replay_trace_id', NULLIF($4, ''), 'replay_span_id', NULLIF($5, '')))
		WHERE tenant_id = $1 AND id = $2`, tenant, item.RunID, source.requestID, source.traceID, source.spanID); err != nil {
		return admissionError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_runs SET dead_letter_replays = dead_letter_replays + 1
		WHERE tenant_id = $1 AND id = $2`, tenant, item.RunID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE scheduler_dead_letters SET replayed_at = clock_timestamp() WHERE tenant_id = $1 AND entry_id = $2", tenant, item.EntryID)
	return err
}

func (h api) deadLetters(w http.ResponseWriter, req *http.Request) {
	var before int64
	if raw := req.URL.Query().Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeError(w, req, contract.TypeBadRequest, "invalid dead-letter cursor")
			return
		}
		before = value
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	page, err := h.repo.deadLetters(ctx, h.tenant, req.PathValue("id"), before)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, req, contract.TypeNotFound, "run not found")
		return
	}
	if err != nil {
		writeError(w, req, contract.TypeUnavailable, "dead-letter history unavailable")
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusOK, page, nil)
}

func (h api) replayDeadLetters(w http.ResponseWriter, req *http.Request) {
	var input struct {
		replaySpec
		Preview *bool `json:"preview"`
	}
	if !decodeRequest(w, req, &input) {
		return
	}
	if input.Preview == nil || !input.valid() {
		writeError(w, req, contract.TypeBadRequest, "preview, time range and replay limit are required")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	var items []replayItem
	var err error
	if *input.Preview {
		items, err = selectReplay(ctx, h.repo.db, h.tenant, input.replaySpec, false)
	} else {
		items, err = h.repo.replayDeadLetters(ctx, h.tenant, req.Header.Get("Idempotency-Key"), input.replaySpec)
	}
	switch {
	case errors.Is(err, errInvalidTask):
		writeError(w, req, contract.TypeBadRequest, "a valid idempotency key is required")
	case errors.Is(err, errRequestConflict):
		writeError(w, req, contract.TypeConflict, "replay idempotency key already used")
	case errors.Is(err, errTenantCapacity):
		w.Header().Set("Retry-After", "60")
		writeError(w, req, contract.TypeRateLimited, "replay capacity exceeded")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "dead-letter replay unavailable")
	case *input.Preview:
		_ = contract.WriteResponse(w, req, http.StatusOK, items, nil)
	default:
		_ = contract.WriteResponse(w, req, http.StatusAccepted, items, nil)
	}
}
