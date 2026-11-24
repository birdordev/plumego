package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
)

const (
	readinessTimeout = time.Second
	recoveryDeadline = 30 * time.Second
)

func recoveryReadiness(ctx context.Context, db *sql.DB) string {
	ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	if err := checkSchema(ctx, db); err != nil {
		return "scheduler schema or storage unavailable"
	}
	contracts := registeredTaskContracts()
	topics := make([]string, 0, len(contracts))
	for _, c := range contracts {
		topics = append(topics, c.topic)
	}
	var unregistered, inconsistent, overdue bool
	err := db.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM mq_tasks
			WHERE status IN ('queued', 'leased') AND topic <> ALL($1::text[])
				AND (expires_at IS NULL OR expires_at > statement_timestamp())),
		EXISTS (SELECT 1 FROM mq_task_attempts a
			LEFT JOIN mq_tasks q ON q.id = a.task_id AND q.attempts = a.attempt_no AND q.status = 'leased'
			WHERE a.finalization_state = 'pending' AND
				(q.id IS NULL OR a.finalization_recorded_at IS NULL OR a.finalization_check_at IS NULL
					OR a.finalization_operation IS NULL)),
		EXISTS (SELECT 1 FROM mq_task_attempts
			WHERE finalization_state = 'pending'
				AND finalization_recorded_at <= statement_timestamp() - $2 * interval '1 second')`,
		topics, int(recoveryDeadline.Seconds())).Scan(&unregistered, &inconsistent, &overdue)
	switch {
	case err != nil:
		return "scheduler recovery storage unavailable"
	case inconsistent:
		return "scheduler recovery record requires isolation"
	case overdue:
		return "scheduler recovery deadline exceeded"
	case unregistered:
		return "scheduler task handler is unregistered"
	default:
		return ""
	}
}

func readinessHandler(db *sql.DB, accepting func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !accepting() {
			writeError(w, req, contract.TypeUnavailable, "scheduler role is stopping")
			return
		}
		reason := recoveryReadiness(req.Context(), db)
		if !accepting() {
			reason = "scheduler role is stopping"
		}
		if reason != "" {
			writeError(w, req, contract.TypeUnavailable, reason)
			return
		}
		_ = contract.WriteResponse(w, req, http.StatusOK, map[string]bool{"ready": true}, nil)
	}
}
