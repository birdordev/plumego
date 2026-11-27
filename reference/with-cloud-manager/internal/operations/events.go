package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
)

//go:embed 0019_operation_outbox.sql
var outboxSchema string

//go:embed 0140_operation_effect_id.sql
var effectIDSchema string

const EffectIDMigration = "0140_operation_effect_id"

//go:embed 0141_operation_terminal_event_time.sql
var terminalEventTimeSchema string

const TerminalEventTimeMigration = "0141_operation_terminal_event_time"

func migrateOutbox(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0019_operation_outbox", outboxSchema)
}

type OperationEvent struct {
	Sequence   int64     `json:"sequence,string"`
	Status     string    `json:"status"`
	RecordedAt time.Time `json:"recorded_at"`
}

type EventPage struct {
	Events    []OperationEvent `json:"events"`
	NextAfter int64            `json:"next_after,string"`
	HasMore   bool             `json:"has_more"`
}

func (s Store) Events(ctx context.Context, token string, scope identity.Scope, id string, after int64, limit int) (EventPage, error) {
	if !operationKey.MatchString(id) || after < 0 || limit < 1 || limit > 100 {
		return EventPage{}, ErrInvalid
	}
	page := EventPage{Events: []OperationEvent{}, NextAfter: after}
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) (retErr error) {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		var found string
		err = tx.QueryRowContext(ctx, `SELECT operation_id FROM cmp_instance_operations
			WHERE (tenant_id,project_id,operation_id)=($1,$2,$3)`, actor.Tenant, actor.Project, id).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT event_id,status,recorded_at FROM cmp_instance_operation_events
			WHERE operation_id=$1 AND event_id>$2 ORDER BY event_id LIMIT $3`, id, after, limit+1)
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, rows.Close()) }()
		for rows.Next() {
			if len(page.Events) == limit {
				page.HasMore = true
				break
			}
			var event OperationEvent
			if err := rows.Scan(&event.Sequence, &event.Status, &event.RecordedAt); err != nil {
				return err
			}
			page.Events = append(page.Events, event)
			page.NextAfter = event.Sequence
		}
		return rows.Err()
	})
	if err != nil {
		return EventPage{}, err
	}
	return page, nil
}

func recordOperationEvent(ctx context.Context, tx *sql.Tx, id, status string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_operation_events (operation_id,status) VALUES ($1,$2)`, id, status)
	return err
}
