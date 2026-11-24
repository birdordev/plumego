package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/birdordev/plumego/x/messaging/scheduler"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed scheduler.sql
var schedulerSchema string

//go:embed scheduler_v2.sql
var schedulerSchemaV2 string

//go:embed scheduler_v3.sql
var schedulerSchemaV3 string

//go:embed scheduler_v4.sql
var schedulerSchemaV4 string

//go:embed scheduler_v5.sql
var schedulerSchemaV5 string

//go:embed scheduler_v6.sql
var schedulerSchemaV6 string

//go:embed scheduler_v7.sql
var schedulerSchemaV7 string

//go:embed scheduler_v8.sql
var schedulerSchemaV8 string

//go:embed scheduler_v9.sql
var schedulerSchemaV9 string

//go:embed scheduler_v10.sql
var schedulerSchemaV10 string

//go:embed scheduler_v11.sql
var schedulerSchemaV11 string

//go:embed scheduler_v12.sql
var schedulerSchemaV12 string

//go:embed scheduler_v13.sql
var schedulerSchemaV13 string

//go:embed scheduler_v14.sql
var schedulerSchemaV14 string

//go:embed scheduler_v15.sql
var schedulerSchemaV15 string

//go:embed scheduler_v16.sql
var schedulerSchemaV16 string

//go:embed scheduler_v17.sql
var schedulerSchemaV17 string

//go:embed scheduler_v18.sql
var schedulerSchemaV18 string

//go:embed scheduler_v19.sql
var schedulerSchemaV19 string

//go:embed scheduler_v20.sql
var schedulerSchemaV20 string

//go:embed scheduler_v21.sql
var schedulerSchemaV21 string

//go:embed scheduler_v22.sql
var schedulerSchemaV22 string

//go:embed scheduler_v23.sql
var schedulerSchemaV23 string

//go:embed scheduler_v24.sql
var schedulerSchemaV24 string

//go:embed scheduler_v25.sql
var schedulerSchemaV25 string

//go:embed scheduler_v26.sql
var schedulerSchemaV26 string

//go:embed scheduler_v27.sql
var schedulerSchemaV27 string

//go:embed scheduler_v28.sql
var schedulerSchemaV28 string

//go:embed scheduler_v29.sql
var schedulerSchemaV29 string

//go:embed scheduler_v30.sql
var schedulerSchemaV30 string

//go:embed scheduler_v31.sql
var schedulerSchemaV31 string

//go:embed scheduler_v32.sql
var schedulerSchemaV32 string

//go:embed scheduler_v33.sql
var schedulerSchemaV33 string

//go:embed scheduler_v34.sql
var schedulerSchemaV34 string

//go:embed scheduler_v35.sql
var schedulerSchemaV35 string

//go:embed scheduler_v36.sql
var schedulerSchemaV36 string

//go:embed scheduler_v37.sql
var schedulerSchemaV37 string

//go:embed scheduler_v38.sql
var schedulerSchemaV38 string

var (
	errRequestConflict = errors.New("idempotency key already used for a different request")
	errScheduleExists  = errors.New("schedule already exists")
	errInvalidTask     = errors.New("invalid task submission")
	errTenantCapacity  = errors.New("tenant capacity exceeded")
)

type repository struct {
	db    *sql.DB
	queue *mqstore.SQLStore
}

type submission struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
	RunAt   time.Time       `json:"run_at"`
	taskOptions
}

type scheduleDefinition struct {
	Tenant        string          `json:"-"`
	ID            string          `json:"id"`
	Expression    string          `json:"expression"`
	TimeZone      string          `json:"time_zone"`
	Topic         string          `json:"topic"`
	Payload       json.RawMessage `json:"payload"`
	MisfirePolicy string          `json:"misfire_policy"`
	CatchUpWindow int             `json:"catch_up_window_seconds"`
	Mode          string          `json:"schedule_mode"`
	JitterWindow  int             `json:"jitter_window_ms"`
	Calendar      *calendarPin    `json:"calendar,omitempty"`
	taskOptions
	optionsJSON []byte
	delayMicros sql.NullInt64
	refreshZone bool
}

func admissionError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "P5101" {
		return errTenantCapacity
	}
	return err
}

func validIdempotencyKey(key string) bool {
	return strings.TrimSpace(key) != "" && len(key) <= 256
}

type runCommand struct {
	tenant, key     string
	input           submission
	hash            [32]byte
	scheduleID      sql.NullString
	scheduleVersion sql.NullInt64
}

func prepareSubmission(tenant, key string, input submission) (runCommand, error) {
	return prepareScopedSubmission(tenant, key, input, false)
}

func prepareScopedSubmission(tenant, key string, input submission, inWorkflow bool) (runCommand, error) {
	if strings.TrimSpace(tenant) == "" || !validIdempotencyKey(key) ||
		input.Topic == releaseTaskType || (input.Topic == reserveTaskType && !inWorkflow) {
		return runCommand{}, errInvalidTask
	}
	if input.Overlap != nil && input.Overlap.Scope == "schedule" {
		return runCommand{}, errInvalidTask
	}
	if input.TaskVersion == 1 {
		// 显式 v1 与旧请求共用指纹。
		input.TaskVersion = 0
	}
	if input.Priority == "normal" {
		input.Priority = ""
	}
	if _, _, err := validateTaskContract(input.Topic, input.Payload, input.taskOptions); err != nil {
		return runCommand{}, err
	}
	input.RunAt = input.RunAt.UTC().Truncate(time.Microsecond)
	raw, err := json.Marshal(input)
	if err != nil {
		return runCommand{}, err
	}
	return runCommand{tenant: tenant, key: key, input: input, hash: sha256.Sum256(raw)}, nil
}

func (r repository) submit(ctx context.Context, tenant, key string, input submission) (string, error) {
	command, err := prepareSubmission(tenant, key, input)
	if err != nil {
		return "", err
	}
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := r.enqueueCommand(ctx, tx, command)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (r repository) enqueueCommand(ctx context.Context, tx *sql.Tx, command runCommand) (string, error) {
	at := command.input.RunAt
	if at.IsZero() {
		if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
			return "", err
		}
	}
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO scheduled_runs
		(id, tenant_id, scheduled_at, request_key, request_hash, schedule_id, schedule_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, request_key) DO NOTHING RETURNING id`,
		rand.Text(), command.tenant, at, command.key, command.hash[:], command.scheduleID, command.scheduleVersion).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var priorHash []byte
		if err := tx.QueryRowContext(ctx, `SELECT id, request_hash FROM scheduled_runs
			WHERE tenant_id = $1 AND request_key = $2`, command.tenant, command.key).Scan(&id, &priorHash); err != nil {
			return "", err
		}
		if subtle.ConstantTimeCompare(priorHash, command.hash[:]) != 1 {
			return "", errRequestConflict
		}
		return id, nil
	}
	if err != nil {
		return "", err
	}
	if err := r.insertTask(ctx, tx, id, command.tenant, command.input, at, sourceFromRequest(ctx)); err != nil {
		return "", err
	}
	return id, nil
}

func (r repository) addSchedule(ctx context.Context, definition scheduleDefinition) error {
	rules, err := loadScheduleZone(definition.TimeZone)
	if err != nil {
		return err
	}
	spec, err := validateSchedule(&definition, rules)
	if err != nil {
		return err
	}
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var now time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return err
	}
	now = now.UTC().Truncate(time.Microsecond)
	calendar, err := loadBusinessCalendar(ctx, tx, definition.Tenant, definition.Calendar)
	if err != nil {
		return err
	}
	next := (calendarRecurrence{spec, calendar}).Next(now).UTC().Truncate(time.Microsecond)
	if !next.After(now) {
		return errInvalidTask
	}
	source := sourceFromRequest(ctx)
	calendarName, calendarVersion := calendarBinding(definition.Calendar)
	_, err = tx.ExecContext(ctx, `INSERT INTO schedules (tenant_id, id, expression, time_zone, topic, payload, next_fire_at, misfire_policy, catch_up_window_seconds, fixed_delay_us, task_options,
		source_request_id, source_trace_id, source_span_id, zone_rules, jitter_window_ms, calendar_name, calendar_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		definition.Tenant, definition.ID, definition.Expression, definition.TimeZone, definition.Topic, []byte(definition.Payload), next,
		definition.MisfirePolicy, definition.CatchUpWindow, definition.delayMicros, definition.optionsJSON, source.requestID, source.traceID, source.spanID, rules, definition.JitterWindow, calendarName, calendarVersion)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errScheduleExists
	}
	if err != nil {
		return admissionError(err)
	}
	return tx.Commit()
}

func validateSchedule(definition *scheduleDefinition, rules []byte) (scheduler.CronSpec, error) {
	if definition.Topic == reserveTaskType || definition.Topic == releaseTaskType {
		return scheduler.CronSpec{}, errInvalidTask
	}
	if strings.TrimSpace(definition.Tenant) == "" || strings.TrimSpace(definition.ID) == "" || len(definition.ID) > 128 ||
		definition.JitterWindow < 0 || definition.JitterWindow > 60000 {
		return scheduler.CronSpec{}, errInvalidTask
	}
	_, options, err := validateTaskContract(definition.Topic, definition.Payload, definition.taskOptions)
	if err != nil {
		return scheduler.CronSpec{}, err
	}
	definition.optionsJSON = options
	if definition.MisfirePolicy == "" {
		definition.MisfirePolicy = "catch_up"
	}
	if definition.CatchUpWindow == 0 {
		definition.CatchUpWindow = 3600
	}
	if !validMisfirePolicy(definition.MisfirePolicy, definition.CatchUpWindow) {
		return scheduler.CronSpec{}, errInvalidTask
	}
	spec, err := parseSchedule(definition.Expression, definition.TimeZone, rules)
	if err != nil {
		return scheduler.CronSpec{}, errors.Join(errInvalidTask, err)
	}
	if definition.Calendar != nil && (!definition.Calendar.valid() ||
		strings.HasPrefix(strings.TrimSpace(definition.Expression), "@every ") || definition.Mode == "fixed_delay") {
		return scheduler.CronSpec{}, errInvalidTask
	}
	switch definition.Mode {
	case "", "fixed_rate":
		definition.delayMicros = sql.NullInt64{}
	case "fixed_delay":
		every, ok := strings.CutPrefix(definition.Expression, "@every ")
		if !ok {
			return scheduler.CronSpec{}, errInvalidTask
		}
		interval, err := time.ParseDuration(every)
		if err != nil || interval <= 0 || interval > 365*24*time.Hour || interval%time.Microsecond != 0 {
			return scheduler.CronSpec{}, errInvalidTask
		}
		definition.delayMicros = sql.NullInt64{Int64: interval.Microseconds(), Valid: true}
	default:
		return scheduler.CronSpec{}, errInvalidTask
	}
	return spec, nil
}

func parseSchedule(expression, zone string, rules []byte) (scheduler.CronSpec, error) {
	if zone == "" || zone == "Local" || len(expression) > 256 {
		return scheduler.CronSpec{}, errInvalidTask
	}
	location, err := scheduleLocation(zone, rules)
	if err != nil {
		return scheduler.CronSpec{}, fmt.Errorf("load schedule timezone: %w", err)
	}
	if every, ok := strings.CutPrefix(expression, "@every "); ok {
		interval, err := time.ParseDuration(every)
		if err != nil || interval < time.Microsecond || interval%time.Microsecond != 0 {
			return scheduler.CronSpec{}, errInvalidTask
		}
	}
	return scheduler.ParseCronSpecWithLocation(expression, location)
}

// materializeNext 原子生成一个到期实例，并推进对应计划的游标。
func (r repository) materializeNext(ctx context.Context) (string, error) {
	return r.materializeOwned(ctx, nil, shardClaim{})
}

const dueScheduleFilter = `NOT paused AND next_dispatch_at <= statement_timestamp()
	AND NOT EXISTS (SELECT 1 FROM scheduler_tenant_usage u WHERE u.tenant_id = schedules.tenant_id
		AND (u.pending_runs >= u.max_pending_runs OR u.pending_bytes + octet_length(schedules.payload) > u.max_pending_bytes))`

func (r repository) materializeOwned(ctx context.Context, metrics *schedulerMetrics, claim shardClaim) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var params []any
	var shardFilter string
	if claim.owner != "" {
		owned, err := claim.lock(ctx, tx)
		if err != nil || !owned {
			return "", err
		}
		shardFilter = " AND schedule_shard = $1"
		params = []any{claim.shard}
	}
	delayFilter := ""
	if claim.owner != "" {
		delayFilter = " AND r.schedule_shard = $1"
	}
	if err := advanceCompletedDelays(ctx, tx, delayFilter, params); err != nil {
		return "", err
	}
	var d scheduleDefinition
	var pin calendarPin
	var source traceSource
	var rules []byte
	var at, dispatchAt, now time.Time
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT tenant_id, id, expression, time_zone, topic, payload, next_fire_at, next_dispatch_at, version, misfire_policy, catch_up_window_seconds, fixed_delay_us, clock_timestamp(), task_options,
		source_request_id, source_trace_id, source_span_id, zone_rules, COALESCE(calendar_name, ''), COALESCE(calendar_version, 0)
		FROM schedules WHERE `+dueScheduleFilter+shardFilter+
		` ORDER BY next_dispatch_at, tenant_id, id LIMIT 1 FOR UPDATE OF schedules SKIP LOCKED`, params...).
		Scan(&d.Tenant, &d.ID, &d.Expression, &d.TimeZone, &d.Topic, &d.Payload, &at, &dispatchAt, &version, &d.MisfirePolicy, &d.CatchUpWindow, &d.delayMicros, &now, &d.optionsJSON,
			&source.requestID, &source.traceID, &source.spanID, &rules, &pin.Name, &pin.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", tx.Commit()
	}
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(d.optionsJSON, &d.taskOptions); err != nil {
		return "", err
	}
	spec, err := parseSchedule(d.Expression, d.TimeZone, rules)
	if err != nil {
		return "", err
	}
	var fire, next time.Time
	var action string
	offset := dispatchAt.Sub(at)
	if d.delayMicros.Valid {
		fire, next, action = fixedDelayOccurrence(d, at, dispatchAt, now)
	} else {
		calendar, err := loadBusinessCalendar(ctx, tx, d.Tenant, pin.binding())
		if err != nil {
			return "", err
		}
		fire, next, action = scheduleOccurrence(d, calendarRecurrence{spec, calendar}, at, now.Add(-offset))
	}
	if !next.After(at) && (pin.Name == "" || !next.IsZero()) {
		return "", fmt.Errorf("schedule %q cannot advance", d.ID)
	}
	var id string
	var waiting sql.NullString
	nextAt := sql.NullTime{Time: next.UTC(), Valid: !next.IsZero()}
	if !fire.IsZero() {
		id = rand.Text()
		calendarName, calendarVersion := calendarBinding(pin.binding())
		if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_runs (id, tenant_id, schedule_id, schedule_version, scheduled_at, advances_fixed_delay, calendar_name, calendar_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, d.Tenant, d.ID, version, fire.UTC(), d.delayMicros.Valid, calendarName, calendarVersion); err != nil {
			return "", err
		}
		input := submission{Topic: d.Topic, Payload: d.Payload, taskOptions: d.taskOptions}
		if err := r.insertTask(ctx, tx, id, d.Tenant, input, fire.UTC().Add(offset), source); err != nil {
			return "", err
		}
		if d.delayMicros.Valid {
			waiting = sql.NullString{String: id, Valid: true}
			nextAt = sql.NullTime{}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schedules SET next_fire_at = $1, waiting_run_id = $6,
		last_misfire_action = COALESCE(NULLIF($4, ''), last_misfire_action),
		last_misfire_at = CASE WHEN $4 = '' THEN last_misfire_at ELSE $5 END
		WHERE tenant_id = $2 AND id = $3`, nextAt, d.Tenant, d.ID, action, now, waiting); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if metrics != nil && id != "" {
		task, _ := lookupTaskContract(d.Topic, d.TaskVersion)
		metrics.observe(triggerLatency, task.topic, "created", time.Since(fire))
		metrics.observe(dispatchLatency, task.topic, "created", time.Since(fire.Add(offset)))
	}
	return id, nil
}

func (r repository) insertTask(ctx context.Context, tx *sql.Tx, id, tenant string, input submission, at time.Time, source traceSource) error {
	var now time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return err
	}
	task, err := queuedTask(id, tenant, input, at, now)
	if err != nil {
		return err
	}
	source.attach(task.Meta, "origin")
	return admissionError(r.queue.InsertTx(ctx, tx, task))
}

func queuedTask(id, tenant string, input submission, at, now time.Time) (mq.Task, error) {
	priority, ok := taskPriority(input.Priority)
	if !ok {
		return mq.Task{}, errInvalidTask
	}
	// 优先级属于队列记录，旧版 Worker 仍可校验原执行契约。
	input.Priority = ""
	c, options, err := validateTaskContract(input.Topic, input.Payload, input.taskOptions)
	if err != nil {
		return mq.Task{}, err
	}
	return mq.Task{
		ID: id, Topic: c.topic, TenantID: tenant, Payload: input.Payload, Priority: priority,
		Meta:        map[string]string{"task_version": strconv.Itoa(c.version), "task_type": c.taskType, "task_options": string(options)},
		AvailableAt: at, MaxAttempts: mq.DefaultTaskMaxAttempts, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// recordResult 校验当前领取代次，并以实例 ID 为业务幂等键提交结果。
func (r repository) recordResult(ctx context.Context, task mq.Task) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := recordResultInTx(ctx, tx, task, sql.NullString{}); err != nil {
		return err
	}
	return tx.Commit()
}

func recordResultInTx(ctx context.Context, tx *sql.Tx, task mq.Task, object sql.NullString) (bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT q.id FROM mq_tasks q JOIN scheduled_runs r ON r.id = q.id AND r.tenant_id = q.tenant_id
		WHERE q.id = $1 AND q.tenant_id = $2 AND q.status = 'leased' AND q.lease_owner = $3
		AND q.lease_until > clock_timestamp() FOR UPDATE OF q`, task.ID, task.TenantID, task.LeaseOwner).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, mq.ErrLeaseLost
	}
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `WITH inserted AS (
		INSERT INTO scheduled_results (run_id, object_id) VALUES ($1, $2) ON CONFLICT (run_id) DO NOTHING RETURNING run_id
	)
	INSERT INTO scheduled_outbox (run_id) SELECT run_id FROM inserted`, id, object)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

type runStatus struct {
	ID                string     `json:"id"`
	QueueStatus       string     `json:"queue_status"`
	ScheduledAt       time.Time  `json:"scheduled_at"`
	ResultReady       bool       `json:"result_ready"`
	ScheduleID        *string    `json:"schedule_id,omitempty"`
	ScheduleVersion   *int64     `json:"schedule_version,omitempty"`
	ManualTrigger     bool       `json:"manual_trigger,omitempty"`
	ClaimAttempts     int        `json:"claim_attempts"`
	ExecutionAttempts int        `json:"execution_attempts"`
	AvailableAt       *time.Time `json:"available_at,omitempty"`
	ProgressPercent   *int       `json:"progress_percent,omitempty"`
}

type runResult struct {
	RunID       string    `json:"run_id"`
	CompletedAt time.Time `json:"completed_at"`
	objectID    sql.NullString
	size        sql.NullInt64
	expired     bool
}

func (r repository) runResult(ctx context.Context, tenant, id string) (runResult, error) {
	var result runResult
	err := r.db.QueryRowContext(ctx, `SELECT s.run_id, s.completed_at, s.object_id, o.size_bytes,
		COALESCE(o.state <> 'ready' OR o.expires_at <= statement_timestamp(), s.object_id IS NOT NULL)
		FROM scheduled_results s JOIN scheduled_runs r ON r.id = s.run_id
		LEFT JOIN scheduler_result_objects o ON o.id = s.object_id AND o.tenant_id = r.tenant_id AND o.run_id = r.id
		WHERE r.tenant_id = $1 AND r.id = $2`, tenant, id).
		Scan(&result.RunID, &result.CompletedAt, &result.objectID, &result.size, &result.expired)
	return result, err
}

func (r repository) runStatus(ctx context.Context, tenant, id string) (runStatus, error) {
	var result runStatus
	err := r.db.QueryRowContext(ctx, `SELECT r.id, q.status, r.scheduled_at, s.run_id IS NOT NULL, r.schedule_id, r.schedule_version,
		(r.schedule_id IS NOT NULL AND r.request_key IS NOT NULL), q.attempts, q.execution_attempts,
		CASE WHEN q.status = 'queued' THEN q.available_at END,
		CASE WHEN s.object_id IS NOT NULL THEN 100 WHEN q.status = 'leased' THEN p.percent END
		FROM scheduled_runs r JOIN scheduler_queue_history q ON q.id = r.id AND q.tenant_id = r.tenant_id
		LEFT JOIN scheduled_results s ON s.run_id = r.id
		LEFT JOIN scheduler_run_progress p ON p.run_id = r.id AND p.claim_no = q.attempts
		WHERE r.tenant_id = $1 AND r.id = $2`, tenant, id).
		Scan(&result.ID, &result.QueueStatus, &result.ScheduledAt, &result.ResultReady, &result.ScheduleID, &result.ScheduleVersion, &result.ManualTrigger,
			&result.ClaimAttempts, &result.ExecutionAttempts, &result.AvailableAt, &result.ProgressPercent)
	return result, err
}

type scheduleStatus struct {
	ID                string          `json:"id"`
	TimeZone          string          `json:"time_zone"`
	ZoneRulesDigest   string          `json:"time_zone_rules_sha256"`
	Version           int64           `json:"version"`
	Paused            bool            `json:"paused"`
	ClaimPaused       bool            `json:"claim_paused"`
	NextFireAt        *time.Time      `json:"next_fire_at"`
	NextDispatchAt    *time.Time      `json:"next_dispatch_at"`
	JitterWindow      int             `json:"jitter_window_ms"`
	Mode              string          `json:"schedule_mode"`
	WaitingRunID      *string         `json:"waiting_run_id,omitempty"`
	LastFiredAt       *time.Time      `json:"last_fired_at,omitempty"`
	LastMisfireAction *string         `json:"last_misfire_action,omitempty"`
	LastMisfireAt     *time.Time      `json:"last_misfire_at,omitempty"`
	MisfirePolicy     string          `json:"misfire_policy"`
	CatchUpWindow     int             `json:"catch_up_window_seconds"`
	Overlap           *overlapOptions `json:"overlap,omitempty"`
	Calendar          *calendarPin    `json:"calendar,omitempty"`
}

func (r repository) scheduleStatus(ctx context.Context, tenant, id string) (scheduleStatus, error) {
	var result scheduleStatus
	var pin calendarPin
	var overlap []byte
	err := r.db.QueryRowContext(ctx, `SELECT id, time_zone, encode(sha256(zone_rules), 'hex'), version, paused, claim_paused, next_fire_at, next_dispatch_at, jitter_window_ms,
		CASE WHEN fixed_delay_us IS NULL THEN 'fixed_rate' ELSE 'fixed_delay' END, waiting_run_id,
		(SELECT scheduled_at FROM scheduled_runs WHERE tenant_id = s.tenant_id AND schedule_id = s.id AND request_key IS NULL
		 ORDER BY scheduled_at DESC LIMIT 1), last_misfire_action, last_misfire_at,
		 misfire_policy, catch_up_window_seconds, task_options->'overlap', COALESCE(calendar_name, ''), COALESCE(calendar_version, 0)
		FROM schedules s WHERE tenant_id = $1 AND id = $2`, tenant, id).
		Scan(&result.ID, &result.TimeZone, &result.ZoneRulesDigest, &result.Version, &result.Paused, &result.ClaimPaused, &result.NextFireAt, &result.NextDispatchAt, &result.JitterWindow, &result.Mode, &result.WaitingRunID, &result.LastFiredAt,
			&result.LastMisfireAction, &result.LastMisfireAt, &result.MisfirePolicy, &result.CatchUpWindow, &overlap, &pin.Name, &pin.Version)
	result.Calendar = pin.binding()
	if err == nil && len(overlap) > 0 {
		err = json.Unmarshal(overlap, &result.Overlap)
	}
	return result, err
}
