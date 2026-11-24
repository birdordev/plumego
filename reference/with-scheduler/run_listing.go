package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
)

const (
	defaultRunPageSize = 50
	maxRunPageSize     = 100
)

var (
	errRunListQuery = errors.New("invalid run listing parameters")
	runCursorID     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

type runCursor struct {
	createdAt time.Time
	id        string
}

func (c runCursor) encode(status string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.createdAt.UnixMicro(), 10) + ":" + c.id + ":" + status))
}

func decodeRunCursor(encoded, status string) (runCursor, error) {
	if len(encoded) > 256 {
		return runCursor{}, errRunListQuery
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return runCursor{}, errRunListQuery
	}
	stamp, suffix, ok := strings.Cut(string(raw), ":")
	if !ok {
		return runCursor{}, errRunListQuery
	}
	id, filter, ok := strings.Cut(suffix, ":")
	micros, parseErr := strconv.ParseInt(stamp, 10, 64)
	if !ok || parseErr != nil || micros < 0 || micros > 253402300799999999 ||
		!runCursorID.MatchString(id) || filter != status {
		return runCursor{}, errRunListQuery
	}
	return runCursor{time.UnixMicro(micros).UTC(), id}, nil
}

type runListQuery struct {
	limit        int
	status       string
	after        *runCursor
	includeTotal bool
}

func parseRunListQuery(raw string) (runListQuery, error) {
	params, err := url.ParseQuery(raw)
	if err != nil {
		return runListQuery{}, errRunListQuery
	}
	query := runListQuery{limit: defaultRunPageSize}
	for key, values := range params {
		if len(values) != 1 || values[0] == "" {
			return runListQuery{}, errRunListQuery
		}
		switch key {
		case "limit":
			limit, err := strconv.Atoi(values[0])
			if err != nil || limit < 1 || limit > maxRunPageSize {
				return runListQuery{}, errRunListQuery
			}
			query.limit = limit
		case "status":
			switch values[0] {
			case "queued", "leased", "done", "dead", "expired", "canceled", "skipped":
				query.status = values[0]
			default:
				return runListQuery{}, errRunListQuery
			}
		case "cursor":
		case "include_total":
			switch values[0] {
			case "true":
				query.includeTotal = true
			case "false":
			default:
				return runListQuery{}, errRunListQuery
			}
		default:
			return runListQuery{}, errRunListQuery
		}
	}
	if encoded := params.Get("cursor"); encoded != "" {
		after, err := decodeRunCursor(encoded, query.status)
		if err != nil {
			return runListQuery{}, err
		}
		query.after = &after
	}
	return query, nil
}

type listedRun struct {
	runStatus
	CreatedAt time.Time `json:"created_at"`
}

type runPage struct {
	Items      []listedRun `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Total      *int64      `json:"total,omitempty"`
}

func (r repository) listRuns(ctx context.Context, tenant string, query runListQuery) (runPage, error) {
	statement := `SELECT r.id, q.status, r.scheduled_at, s.run_id IS NOT NULL, r.schedule_id, r.schedule_version,
		(r.schedule_id IS NOT NULL AND r.request_key IS NOT NULL), r.created_at,
		q.attempts, q.execution_attempts, CASE WHEN q.status = 'queued' THEN q.available_at END
		FROM scheduled_runs r JOIN scheduler_queue_history q ON q.id = r.id AND q.tenant_id = r.tenant_id
		LEFT JOIN scheduled_results s ON s.run_id = r.id
		WHERE r.tenant_id = $1 AND ($3 = '' OR q.status = $3)`
	args := []any{tenant, query.limit + 1, query.status}
	if query.after != nil {
		statement += " AND (r.created_at, r.id) < ($4, $5)"
		args = append(args, query.after.createdAt, query.after.id)
	}
	rows, err := r.db.QueryContext(ctx, statement+" ORDER BY r.created_at DESC, r.id DESC LIMIT $2", args...)
	if err != nil {
		return runPage{}, err
	}
	defer rows.Close()
	page := runPage{Items: make([]listedRun, 0, query.limit)}
	for rows.Next() {
		var item listedRun
		if err := rows.Scan(&item.ID, &item.QueueStatus, &item.ScheduledAt, &item.ResultReady,
			&item.ScheduleID, &item.ScheduleVersion, &item.ManualTrigger, &item.CreatedAt,
			&item.ClaimAttempts, &item.ExecutionAttempts, &item.AvailableAt); err != nil {
			return runPage{}, err
		}
		if len(page.Items) == query.limit {
			last := page.Items[len(page.Items)-1]
			page.NextCursor = (runCursor{last.CreatedAt, last.ID}).encode(query.status)
			break
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return runPage{}, err
	}
	if err := rows.Close(); err != nil {
		return runPage{}, err
	}
	if query.includeTotal {
		var total int64
		err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_runs r
			JOIN scheduler_queue_history q ON q.id = r.id AND q.tenant_id = r.tenant_id
			WHERE r.tenant_id = $1 AND ($2 = '' OR q.status = $2)`, tenant, query.status).Scan(&total)
		if err != nil {
			return runPage{}, err
		}
		page.Total = &total
	}
	return page, nil
}

func (h api) listRuns(w http.ResponseWriter, req *http.Request) {
	query, err := parseRunListQuery(req.URL.RawQuery)
	if err != nil {
		writeError(w, req, contract.TypeBadRequest, "invalid run listing parameters")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	result, err := h.repo.listRuns(ctx, h.tenant, query)
	if err != nil {
		writeError(w, req, contract.TypeUnavailable, "run list unavailable")
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusOK, result, nil)
}
