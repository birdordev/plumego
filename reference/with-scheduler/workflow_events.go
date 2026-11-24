package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
)

type eventSourceConfig struct {
	workflowPin
	IngestActor        string `json:"ingest_actor"`
	MaxLatenessSeconds int    `json:"max_lateness_seconds"`
}

func (c eventSourceConfig) valid(source string) bool {
	return runCursorID.MatchString(source) && c.workflowPin.valid(false) &&
		runCursorID.MatchString(c.IngestActor) && c.MaxLatenessSeconds >= 1 && c.MaxLatenessSeconds <= 604800
}

type workflowEventInput struct {
	Offset     int64           `json:"offset"`
	ID         string          `json:"id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

func (e workflowEventInput) valid() bool {
	return e.Offset > 0 && e.Offset < math.MaxInt64 && runCursorID.MatchString(e.ID) &&
		!e.OccurredAt.IsZero() && e.OccurredAt.Year() > 0 && e.OccurredAt.Year() <= 9999 &&
		len(e.Payload) <= 64<<10 && json.Valid(e.Payload)
}

func (e workflowEventInput) fingerprint() ([sha256.Size]byte, error) {
	e.Offset = 0
	e.OccurredAt = e.OccurredAt.UTC().Truncate(time.Microsecond)
	decoder := json.NewDecoder(bytes.NewReader(e.Payload))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return [sha256.Size]byte{}, err
	}
	var err error
	e.Payload, err = json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	raw, err := json.Marshal(e)
	return sha256.Sum256(raw), err
}

var errEventIngestForbidden = errors.New("event source actor does not match")
var errEventReplayRange = errors.New("event replay extends past committed cursor")

func (r repository) configureEventSource(ctx context.Context, tenant, source string, cfg eventSourceConfig) error {
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var graph int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM scheduler_workflow_definitions
		WHERE tenant_id=$1 AND name=$2 AND version=$3`, tenant, cfg.Name, cfg.Version).Scan(&graph); err != nil {
		return err
	}
	var configured string
	err = tx.QueryRowContext(ctx, `INSERT INTO scheduler_event_sources
		(tenant_id,source,ingest_actor,definition_name,definition_version,max_lateness_seconds)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING RETURNING source`,
		tenant, source, cfg.IngestActor, cfg.Name, cfg.Version, cfg.MaxLatenessSeconds).Scan(&configured)
	if errors.Is(err, sql.ErrNoRows) {
		var prior eventSourceConfig
		if err := tx.QueryRowContext(ctx, `SELECT ingest_actor,definition_name,definition_version,max_lateness_seconds
			FROM scheduler_event_sources WHERE tenant_id=$1 AND source=$2`, tenant, source).
			Scan(&prior.IngestActor, &prior.Name, &prior.Version, &prior.MaxLatenessSeconds); err != nil {
			return err
		}
		if prior != cfg {
			return errRequestConflict
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT scheduler_write_audit($1,'workflow.event_source',$2,
		'{}'::jsonb,jsonb_build_object('definition',$3::text,'version',$4::integer))`, tenant, source, cfg.Name, cfg.Version); err != nil {
		return err
	}
	return tx.Commit()
}

func (r repository) ingestWorkflowEvent(ctx context.Context, tenant, source string, event workflowEventInput) error {
	fp, err := event.fingerprint()
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var allowed string
	if err := tx.QueryRowContext(ctx, "SELECT ingest_actor FROM scheduler_event_sources WHERE tenant_id=$1 AND source=$2 FOR UPDATE",
		tenant, source).Scan(&allowed); err != nil {
		return err
	}
	identity, _ := ctx.Value(auditRequestKey{}).(auditIdentity)
	if allowed != identity.actor {
		return errEventIngestForbidden
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_event_identities (tenant_id,source,event_id,fingerprint,workflow_key)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (tenant_id,source,event_id) DO NOTHING`,
		tenant, source, event.ID, fp[:], rand.Text()); err != nil {
		return err
	}
	var prior []byte
	if err := tx.QueryRowContext(ctx, `SELECT fingerprint FROM scheduler_event_identities
		WHERE tenant_id=$1 AND source=$2 AND event_id=$3`, tenant, source, event.ID).Scan(&prior); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(prior, fp[:]) != 1 {
		return errRequestConflict
	}
	var position int64
	err = tx.QueryRowContext(ctx, `INSERT INTO scheduler_source_events (tenant_id,source,source_offset,event_id,occurred_at,payload)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT DO NOTHING RETURNING source_offset`,
		tenant, source, event.Offset, event.ID, event.OccurredAt.UTC().Truncate(time.Microsecond), []byte(event.Payload)).Scan(&position)
	if errors.Is(err, sql.ErrNoRows) {
		var priorID string
		if err := tx.QueryRowContext(ctx, `SELECT event_id FROM scheduler_source_events
			WHERE tenant_id=$1 AND source=$2 AND source_offset=$3`, tenant, source, event.Offset).Scan(&priorID); err != nil {
			return err
		}
		if priorID != event.ID {
			return errRequestConflict
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_event_sources SET highest_offset=GREATEST(highest_offset,$3)
		WHERE tenant_id=$1 AND source=$2`, tenant, source, position); err != nil {
		return err
	}
	return tx.Commit()
}

func (r repository) advanceWorkflowEvents(ctx context.Context) (string, error) {
	consumed, err := r.consumeWorkflowEvent(ctx)
	if err != nil {
		return "", err
	}
	replayed, err := r.consumeEventReplay(ctx)
	if replayed != "" {
		return replayed, err
	}
	return consumed, err
}

func (r repository) consumeWorkflowEvent(ctx context.Context) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var tenant, source string
	var pin workflowPin
	var cursor int64
	err = tx.QueryRowContext(ctx, `SELECT s.tenant_id,s.source,s.cursor_offset,s.definition_name,s.definition_version
		FROM scheduler_event_sources s CROSS JOIN LATERAL (
			SELECT received_at FROM scheduler_source_events e
			WHERE e.tenant_id=s.tenant_id AND e.source=s.source AND e.source_offset=s.cursor_offset+1 AND e.outcome='pending'
			LIMIT 1
		) e WHERE s.cursor_offset<s.highest_offset
		ORDER BY e.received_at,s.tenant_id,s.source LIMIT 1 FOR UPDATE OF s SKIP LOCKED`).
		Scan(&tenant, &source, &cursor, &pin.Name, &pin.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// 取得来源锁后复查当前位置，防止另一副本已提交同一位置。
	var eventID, key string
	var handled sql.NullInt64
	var fp []byte
	var late bool
	err = tx.QueryRowContext(ctx, `SELECT e.event_id,i.workflow_key,i.fingerprint,i.handled_offset,
		e.occurred_at < e.received_at - (s.max_lateness_seconds * interval '1 second')
		FROM scheduler_source_events e JOIN scheduler_event_identities i USING (tenant_id,source,event_id)
		JOIN scheduler_event_sources s USING (tenant_id,source)
		WHERE e.tenant_id=$1 AND e.source=$2 AND e.source_offset=$3 AND e.outcome='pending'
			AND e.source_offset=s.cursor_offset+1`, tenant, source, cursor+1).Scan(&eventID, &key, &fp, &handled, &late)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	outcome := "duplicate"
	workflow := ""
	if !handled.Valid {
		outcome = "late"
		if !late {
			workflow, err = materializeEventWorkflow(ctx, tx, tenant, key, pin, fp)
			if err != nil {
				return "", err
			}
			outcome = "triggered"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduler_event_identities SET handled_offset=$4
			WHERE tenant_id=$1 AND source=$2 AND event_id=$3`, tenant, source, eventID, cursor+1); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_source_events SET outcome=$4,workflow_id=NULLIF($5,'')
		WHERE tenant_id=$1 AND source=$2 AND source_offset=$3`, tenant, source, cursor+1, outcome, workflow); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduler_event_sources SET cursor_offset=$3
		WHERE tenant_id=$1 AND source=$2`, tenant, source, cursor+1); err != nil {
		return "", err
	}
	return source, tx.Commit()
}

func materializeEventWorkflow(ctx context.Context, tx *sql.Tx, tenant, key string, pin workflowPin, fingerprint []byte) (string, error) {
	var raw []byte
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT graph,jsonb_array_length(graph->'nodes') FROM scheduler_workflow_definitions
		WHERE tenant_id=$1 AND name=$2 AND version=$3`, tenant, pin.Name, pin.Version).Scan(&raw, &count); err != nil {
		return "", err
	}
	var hash [sha256.Size]byte
	copy(hash[:], fingerprint)
	return insertWorkflowInTx(ctx, tx, tenant, "event:"+key, raw, count, pin, hash)
}
