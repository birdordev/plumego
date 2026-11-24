package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
)

type eventReplayRange struct {
	First int64 `json:"from"`
	Last  int64 `json:"to"`
}

type eventReplayProgress struct {
	ID string `json:"id"`
	eventReplayRange
	Next     int64 `json:"next_offset"`
	Finished bool  `json:"finished"`
}

func (r repository) startEventReplay(ctx context.Context, tenant, source, key string, span eventReplayRange) (string, error) {
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var cursor int64
	if err := tx.QueryRowContext(ctx, "SELECT cursor_offset FROM scheduler_event_sources WHERE tenant_id=$1 AND source=$2",
		tenant, source).Scan(&cursor); err != nil {
		return "", err
	}
	if span.Last > cursor {
		return "", errEventReplayRange
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO scheduler_event_replays
		(tenant_id,source,id,request_key,first_offset,last_offset,next_offset)
		VALUES ($1,$2,$3,$4,$5,$6,$5) ON CONFLICT DO NOTHING RETURNING id`,
		tenant, source, rand.Text(), key, span.First, span.Last).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var previous eventReplayRange
		if err := tx.QueryRowContext(ctx, `SELECT id,first_offset,last_offset FROM scheduler_event_replays
			WHERE tenant_id=$1 AND source=$2 AND request_key=$3`, tenant, source, key).
			Scan(&id, &previous.First, &previous.Last); err != nil {
			return "", err
		}
		if previous != span {
			return "", errRequestConflict
		}
		return id, tx.Commit()
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1,'workflow.event_replay',$2,'{}'::jsonb,
		jsonb_build_object('source',$3::text,'from',$4::bigint,'to',$5::bigint))`,
		tenant, id, source, span.First, span.Last); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (r repository) consumeEventReplay(ctx context.Context) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var tenant, source, id string
	var offset int64
	err = tx.QueryRowContext(ctx, `SELECT tenant_id,source,id,next_offset FROM scheduler_event_replays
		WHERE next_offset<=last_offset ORDER BY created_at,tenant_id,source,id LIMIT 1 FOR UPDATE SKIP LOCKED`).
		Scan(&tenant, &source, &id, &offset)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var outcome, key string
	var stored sql.NullString
	var pin workflowPin
	var fingerprint []byte
	err = tx.QueryRowContext(ctx, `SELECT e.outcome,e.workflow_id,i.workflow_key,i.fingerprint,s.definition_name,s.definition_version
		FROM scheduler_source_events e JOIN scheduler_event_identities i USING (tenant_id,source,event_id)
		JOIN scheduler_event_sources s USING (tenant_id,source)
		WHERE e.tenant_id=$1 AND e.source=$2 AND e.source_offset=$3`, tenant, source, offset).
		Scan(&outcome, &stored, &key, &fingerprint, &pin.Name, &pin.Version)
	if err != nil {
		return "", err
	}
	switch outcome {
	case "triggered":
		reused, err := materializeEventWorkflow(ctx, tx, tenant, key, pin, fingerprint)
		if err != nil {
			return "", err
		}
		if !stored.Valid || reused != stored.String {
			return "", errors.New("event replay did not match the original workflow")
		}
	case "late", "duplicate":
	default:
		return "", errors.New("event replay found an unprocessed position")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_event_replays SET next_offset=next_offset+1
		WHERE tenant_id=$1 AND source=$2 AND id=$3`, tenant, source, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (r repository) eventReplayStatus(ctx context.Context, tenant, source, id string) (eventReplayProgress, error) {
	var state eventReplayProgress
	err := r.db.QueryRowContext(ctx, `SELECT id,first_offset,last_offset,next_offset,next_offset>last_offset
		FROM scheduler_event_replays WHERE tenant_id=$1 AND source=$2 AND id=$3`, tenant, source, id).
		Scan(&state.ID, &state.First, &state.Last, &state.Next, &state.Finished)
	return state, err
}
