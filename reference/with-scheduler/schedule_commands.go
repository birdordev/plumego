package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"time"
)

func (r repository) replaceSchedule(ctx context.Context, expected int64, definition scheduleDefinition) (int64, error) {
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var version int64
	var previousCalendar calendarPin
	var expression, zone string
	var next sql.NullTime
	var delay sql.NullInt64
	var waiting sql.NullString
	var rules []byte
	if err := tx.QueryRowContext(ctx, `SELECT version, expression, time_zone, next_fire_at, fixed_delay_us, waiting_run_id, zone_rules, COALESCE(calendar_name, ''), COALESCE(calendar_version, 0)
		FROM schedules WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, definition.Tenant, definition.ID).
		Scan(&version, &expression, &zone, &next, &delay, &waiting, &rules, &previousCalendar.Name, &previousCalendar.Version); err != nil {
		return 0, err
	}
	if version != expected {
		return 0, errScheduleVersion
	}
	if zone != definition.TimeZone || definition.refreshZone {
		rules, err = loadScheduleZone(definition.TimeZone)
		if err != nil {
			return 0, err
		}
	}
	spec, err := validateSchedule(&definition, rules)
	if err != nil {
		return 0, err
	}
	var nextCalendar calendarPin
	if definition.Calendar != nil {
		nextCalendar = *definition.Calendar
	}
	if expression != definition.Expression || zone != definition.TimeZone || delay != definition.delayMicros ||
		nextCalendar != previousCalendar {
		calendar, err := loadBusinessCalendar(ctx, tx, definition.Tenant, definition.Calendar)
		if err != nil {
			return 0, err
		}
		var now time.Time
		if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return 0, err
		}
		next = sql.NullTime{Time: (calendarRecurrence{spec, calendar}).Next(now).UTC().Truncate(time.Microsecond), Valid: true}
		waiting = sql.NullString{}
		if !next.Time.After(now) {
			return 0, errInvalidTask
		}
	}
	source := sourceFromRequest(ctx)
	calendarName, calendarVersion := calendarBinding(definition.Calendar)
	if err := tx.QueryRowContext(ctx, `UPDATE schedules SET expression = $3, time_zone = $4,
		topic = $5, payload = $6, next_fire_at = $7, misfire_policy = $8,
		catch_up_window_seconds = $9, fixed_delay_us = $10, waiting_run_id = $11, task_options = $12,
		source_request_id = $13, source_trace_id = $14, source_span_id = $15, zone_rules = $16, jitter_window_ms = $17, calendar_name = $18, calendar_version = $19, version = version + 1
		WHERE tenant_id = $1 AND id = $2 RETURNING version`,
		definition.Tenant, definition.ID, definition.Expression, definition.TimeZone, definition.Topic,
		[]byte(definition.Payload), next, definition.MisfirePolicy, definition.CatchUpWindow, definition.delayMicros, waiting, definition.optionsJSON,
		source.requestID, source.traceID, source.spanID, rules, definition.JitterWindow, calendarName, calendarVersion).Scan(&version); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return version, nil
}

func (r repository) triggerSchedule(ctx context.Context, tenant, scheduleID, key string) (string, error) {
	if !validIdempotencyKey(key) {
		return "", errInvalidTask
	}
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	command := runCommand{
		tenant: tenant, key: key,
		hash:            sha256.Sum256([]byte("schedule-trigger\x00" + scheduleID)),
		scheduleID:      sql.NullString{String: scheduleID, Valid: true},
		scheduleVersion: sql.NullInt64{Valid: true},
	}
	var options []byte
	if err := tx.QueryRowContext(ctx, `SELECT topic, payload, version, task_options FROM schedules
		WHERE tenant_id = $1 AND id = $2 FOR SHARE`, tenant, scheduleID).
		Scan(&command.input.Topic, &command.input.Payload, &command.scheduleVersion.Int64, &options); err != nil {
		return "", err
	}
	if err := json.Unmarshal(options, &command.input.taskOptions); err != nil {
		return "", err
	}

	id, err := r.enqueueCommand(ctx, tx, command)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}
