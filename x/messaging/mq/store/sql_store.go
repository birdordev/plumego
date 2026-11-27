package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

func NewSQL(db *sql.DB, cfg SQLConfig) *SQLStore {
	if cfg.Table == "" {
		cfg.Table = "mq_tasks"
	}
	if cfg.DLQTable == "" {
		cfg.DLQTable = "mq_task_dlq"
	}
	if cfg.AttemptsTable == "" {
		cfg.AttemptsTable = "mq_task_attempts"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &SQLStore{db: db, cfg: cfg, nowFunc: cfg.Now}
}

var _ mq.TaskStore = (*SQLStore)(nil)

func (s *SQLStore) Insert(ctx context.Context, task mq.Task) error {
	if s == nil || s.db == nil {
		return mq.ErrNotInitialized
	}
	return s.insert(ctx, s.db, task)
}

// InsertTx 在调用方事务中写入已准备好的任务。调用方负责提交事务。
func (s *SQLStore) InsertTx(ctx context.Context, tx *sql.Tx, task mq.Task) error {
	if s == nil || tx == nil {
		return mq.ErrNotInitialized
	}
	return s.insert(ctx, tx, task)
}

func (s *SQLStore) insert(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, task mq.Task) error {
	meta, err := marshalMeta(task.Meta)
	if err != nil {
		return err
	}

	status := mq.TaskStatusQueued
	query, args := s.buildInsert(task, status, meta)
	_, err = exec.ExecContext(ctx, query, args...)
	if err != nil {
		if isDuplicateError(err) {
			return mq.ErrDuplicateTask
		}
		return err
	}
	return nil
}

// Reserve 领取合格任务。默认在同一事务中迁移过期任务，清理失败会中止领取。
func (s *SQLStore) Reserve(ctx context.Context, opts mq.ReserveOptions) ([]mq.Task, error) {
	return s.reserve(ctx, opts, "")
}

// ReserveTenant 在原子领取事务中只领取和清理指定租户的任务。
func (s *SQLStore) ReserveTenant(ctx context.Context, opts mq.ReserveOptions, tenant string) ([]mq.Task, error) {
	if tenant == "" {
		return nil, errors.New("task reservation requires a tenant")
	}
	return s.reserve(ctx, opts, tenant)
}

func (s *SQLStore) reserve(ctx context.Context, opts mq.ReserveOptions, tenant string) ([]mq.Task, error) {
	if s == nil || s.db == nil {
		return nil, mq.ErrNotInitialized
	}
	if opts.Limit <= 0 {
		return nil, nil
	}

	now := opts.Now
	if now.IsZero() {
		now = s.nowFunc()
	}
	if opts.Lease <= 0 {
		opts.Lease = mq.DefaultLeaseDuration
	}
	leaseUntil := now.Add(opts.Lease)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if !s.cfg.DeferredExpiration || s.cfg.Dialect != DialectPostgres {
		if err := s.expireUnavailable(ctx, tx, now, tenant); err != nil {
			return nil, fmt.Errorf("expire unavailable tasks: %w", err)
		}
	}

	selectQuery, selectArgs := s.buildReserveSelect(opts, now, tenant)
	var rows *sql.Rows
	var capacity int
	if s.cfg.Dialect == DialectPostgres {
		const columns = "id, topic, tenant_id, payload, meta, priority, dedupe_key, available_at, expires_at, attempts, max_attempts, lease_owner, lease_until, created_at, updated_at"
		i := len(selectArgs) + 1
		output := "SELECT * FROM reserved ORDER BY " + reserveOrder
		if s.cfg.PriorityAging > 0 {
			output = "SELECT reserved.* FROM reserved JOIN eligible USING(id) ORDER BY " + reserveAgedOrder("eligible.ready_at")
		}
		query := fmt.Sprintf("WITH eligible AS (%s), reserved AS (UPDATE %s SET status = %s, lease_owner = %s, lease_until = %s, attempts = attempts + 1, updated_at = %s WHERE id IN (SELECT id FROM eligible) RETURNING %s) %s",
			selectQuery, s.cfg.Table, s.placeholder(i), s.placeholder(i+1), s.placeholder(i+2), s.placeholder(i+3), columns, output)
		selectArgs = append(selectArgs, mq.TaskStatusLeased, opts.ConsumerID, leaseUntil, now)
		rows, err = tx.QueryContext(ctx, query, selectArgs...)
		capacity = min(opts.Limit, 64)
	} else {
		rows, capacity, err = s.reserveAndRead(ctx, tx, selectQuery, selectArgs, opts, now, leaseUntil)
	}
	if err != nil {
		return nil, err
	}

	if rows == nil {
		return nil, tx.Commit()
	}
	defer func() { _ = rows.Close() }()

	tasks := make([]mq.Task, 0, capacity)
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, task)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	if len(tasks) == 0 {
		return nil, tx.Commit()
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if s.attemptLogEnabled() {
		s.insertAttemptsBestEffort(ctx, tx, tasks, now)
	}

	err = tx.Commit()
	return tasks, err
}

// reserveAndRead uses separate statements for dialects without UPDATE RETURNING.
func (s *SQLStore) reserveAndRead(ctx context.Context, tx *sql.Tx, query string, args []any, opts mq.ReserveOptions, now, leaseUntil time.Time) (*sql.Rows, int, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}

	ids := make([]string, 0, min(opts.Limit, 64))
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			_ = rows.Close()
			return nil, 0, scanErr
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, err
	}
	_ = rows.Close()

	if len(ids) == 0 {
		return nil, 0, nil
	}

	updateQuery, updateArgs := s.buildReserveUpdate(ids, opts.ConsumerID, leaseUntil, now)
	if _, err = tx.ExecContext(ctx, updateQuery, updateArgs...); err != nil {
		return nil, 0, err
	}

	selectTasksQuery, selectTasksArgs := s.buildSelectTasks(ids)
	rows, err = tx.QueryContext(ctx, selectTasksQuery, selectTasksArgs...)
	if err != nil {
		return nil, 0, err
	}
	return rows, len(ids), nil
}

// Ack rolls back any open transaction before returning a lease error.
func (s *SQLStore) Ack(ctx context.Context, taskID, consumerID string, now time.Time) error {
	if s == nil || s.db == nil {
		return mq.ErrNotInitialized
	}
	if now.IsZero() {
		now = s.nowFunc()
	}

	if !s.attemptLogEnabled() {
		query, args := s.buildAck(taskID, consumerID, now)
		res, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return mq.ErrLeaseLost
		}
		return nil
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	query, args := s.buildAck(taskID, consumerID, now)
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return mq.ErrLeaseLost
	}

	s.finishAttemptBestEffort(ctx, tx, taskID, true, "", now)

	return tx.Commit()
}

// Release rolls back any open transaction before returning a lease error.
func (s *SQLStore) Release(ctx context.Context, taskID, consumerID string, opts mq.ReleaseOptions) error {
	if s == nil || s.db == nil {
		return mq.ErrNotInitialized
	}
	if opts.Now.IsZero() {
		opts.Now = s.nowFunc()
	}
	if opts.RetryAt.IsZero() {
		opts.RetryAt = opts.Now
	}

	if !s.attemptLogEnabled() {
		query, args := s.buildRelease(taskID, consumerID, opts)
		res, err := s.db.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return mq.ErrLeaseLost
		}
		return nil
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	query, args := s.buildRelease(taskID, consumerID, opts)
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return mq.ErrLeaseLost
	}

	s.finishAttemptBestEffort(ctx, tx, taskID, false, opts.Reason, opts.Now)

	return tx.Commit()
}

// MoveToDLQ rolls back any open transaction before returning a lease error.
func (s *SQLStore) MoveToDLQ(ctx context.Context, taskID, consumerID, reason string, now time.Time) error {
	if s == nil || s.db == nil {
		return mq.ErrNotInitialized
	}
	if now.IsZero() {
		now = s.nowFunc()
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	task, err := s.selectForDLQ(ctx, tx, taskID, consumerID, now)
	if err != nil {
		return err
	}

	if err = s.insertDLQ(ctx, tx, task, reason, now); err != nil {
		return err
	}

	query, args := s.buildMoveToDLQ(taskID, consumerID, reason, now)
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return mq.ErrLeaseLost
	}

	if s.attemptLogEnabled() {
		s.finishAttemptBestEffort(ctx, tx, taskID, false, reason, now)
	}

	return tx.Commit()
}

func (s *SQLStore) ExtendLease(ctx context.Context, taskID, consumerID string, lease time.Duration, now time.Time) error {
	if s == nil || s.db == nil {
		return mq.ErrNotInitialized
	}
	if now.IsZero() {
		now = s.nowFunc()
	}
	if lease <= 0 {
		lease = mq.DefaultLeaseDuration
	}

	query, args := s.buildExtendLease(taskID, consumerID, now.Add(lease), now)
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return mq.ErrLeaseLost
	}
	return nil
}

func (s *SQLStore) Stats(ctx context.Context) (mq.Stats, error) {
	if s == nil || s.db == nil {
		return mq.Stats{}, mq.ErrNotInitialized
	}

	query := fmt.Sprintf(`SELECT
	SUM(CASE WHEN status = '%s' THEN 1 ELSE 0 END) AS queued,
	SUM(CASE WHEN status = '%s' THEN 1 ELSE 0 END) AS leased,
	SUM(CASE WHEN status = '%s' THEN 1 ELSE 0 END) AS dead,
	SUM(CASE WHEN status = '%s' THEN 1 ELSE 0 END) AS expired
FROM %s`, mq.TaskStatusQueued, mq.TaskStatusLeased, mq.TaskStatusDead, mq.TaskStatusExpired, s.cfg.Table)

	var queued, leased, dead, expired sql.NullInt64
	if err := s.db.QueryRowContext(ctx, query).Scan(&queued, &leased, &dead, &expired); err != nil {
		return mq.Stats{}, err
	}

	return mq.Stats{
		Queued:  nullInt64(queued),
		Leased:  nullInt64(leased),
		Dead:    nullInt64(dead),
		Expired: nullInt64(expired),
	}, nil
}

// ReplayDLQ moves tasks from the dead-letter state back to queued.
func (s *SQLStore) ReplayDLQ(ctx context.Context, opts ReplayOptions) (ReplayResult, error) {
	if s == nil || s.db == nil {
		return ReplayResult{}, mq.ErrNotInitialized
	}

	now := opts.Now
	if now.IsZero() {
		now = s.nowFunc()
	}
	availableAt := opts.AvailableAt
	if availableAt.IsZero() {
		availableAt = now
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ReplayResult{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	total, err := s.countDead(ctx, tx)
	if err != nil {
		return ReplayResult{}, err
	}
	if total == 0 {
		err = tx.Commit()
		return ReplayResult{Replayed: 0, Remaining: 0}, err
	}

	var replayed int
	if opts.Max <= 0 || opts.Max >= total {
		query, args := s.buildReplayUpdateAll(availableAt, now, opts.ResetAttempts)
		res, execErr := tx.ExecContext(ctx, query, args...)
		if execErr != nil {
			err = execErr
			return ReplayResult{}, err
		}
		affected, execErr := res.RowsAffected()
		if execErr != nil {
			err = execErr
			return ReplayResult{}, err
		}
		replayed = int(affected)
	} else {
		ids, selectErr := s.selectDeadIDs(ctx, tx, opts.Max)
		if selectErr != nil {
			err = selectErr
			return ReplayResult{}, err
		}
		if len(ids) > 0 {
			query, args := s.buildReplayUpdate(ids, availableAt, now, opts.ResetAttempts)
			res, execErr := tx.ExecContext(ctx, query, args...)
			if execErr != nil {
				err = execErr
				return ReplayResult{}, err
			}
			affected, execErr := res.RowsAffected()
			if execErr != nil {
				err = execErr
				return ReplayResult{}, err
			}
			replayed = int(affected)
		}
	}

	err = tx.Commit()
	if err != nil {
		return ReplayResult{}, err
	}

	remaining := total - replayed
	if remaining < 0 {
		remaining = 0
	}

	return ReplayResult{
		Replayed:  replayed,
		Remaining: remaining,
	}, nil
}

func (s *SQLStore) expireUnavailable(ctx context.Context, tx *sql.Tx, now time.Time, tenant string) error {
	query := fmt.Sprintf("UPDATE %s SET status = %s, lease_owner = NULL, lease_until = NULL, updated_at = %s"+
		" WHERE expires_at IS NOT NULL AND expires_at <= %s AND (status = %s OR (status = %s AND lease_until < %s))",
		s.cfg.Table, s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4), s.placeholder(5), s.placeholder(6))
	args := []any{mq.TaskStatusExpired, now, now, mq.TaskStatusQueued, mq.TaskStatusLeased, now}
	if tenant != "" {
		query += " AND tenant_id = " + s.placeholder(len(args)+1)
		args = append(args, tenant)
	}
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

// ExpireBatch 迁移一个 PostgreSQL 状态分区中最多 Limit 条过期记录。Leased 分区只处理已失去有效租约的记录。
func (s *SQLStore) ExpireBatch(ctx context.Context, opts ExpirationOptions) (int64, error) {
	if s == nil || s.db == nil {
		return 0, mq.ErrNotInitialized
	}
	if s.cfg.Dialect != DialectPostgres {
		return 0, errors.New("bounded expiration requires PostgreSQL")
	}
	if opts.Limit <= 0 {
		return 0, nil
	}
	if opts.Now.IsZero() {
		opts.Now = s.nowFunc()
	}
	condition := "status = 'queued'"
	if opts.Leased {
		condition = "status = 'leased' AND lease_until < $1"
	}
	query := fmt.Sprintf(`WITH candidates AS (
		SELECT id FROM %s WHERE expires_at <= $1 AND %s
		ORDER BY expires_at, id LIMIT $2 FOR UPDATE SKIP LOCKED
	) UPDATE %s SET status = 'expired', lease_owner = NULL, lease_until = NULL, updated_at = $1
	WHERE id IN (SELECT id FROM candidates)`, s.cfg.Table, condition, s.cfg.Table)
	result, err := s.db.ExecContext(ctx, query, opts.Now, opts.Limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// NextAvailable 返回 PostgreSQL 队列中下一次未来可领取的时间。提示可能随并发操作失效。
func (s *SQLStore) NextAvailable(ctx context.Context, topics []string, now time.Time) (time.Time, error) {
	if s == nil || s.db == nil {
		return time.Time{}, mq.ErrNotInitialized
	}
	if s.cfg.Dialect != DialectPostgres {
		return time.Time{}, nil
	}
	if now.IsZero() {
		now = s.nowFunc()
	}
	args := make([]any, 1, 1+len(topics))
	args[0] = now
	filter := ""
	if len(topics) > 0 {
		placeholders := make([]string, 0, len(topics))
		for _, topic := range topics {
			args = append(args, topic)
			placeholders = append(placeholders, s.placeholder(len(args)))
		}
		filter = " AND topic IN (" + strings.Join(placeholders, ", ") + ")"
	}
	filter += s.claimExclusion()
	query := fmt.Sprintf(`SELECT min(at) FROM (
		(SELECT available_at AS at FROM %[1]s WHERE status = 'queued' AND available_at > $1
			AND (expires_at IS NULL OR expires_at > available_at)%[2]s ORDER BY available_at LIMIT 1)
		UNION ALL
		(SELECT lease_until + interval '1 microsecond' FROM %[1]s WHERE status = 'leased' AND lease_until >= $1
			AND available_at <= lease_until AND (expires_at IS NULL OR expires_at > lease_until + interval '1 microsecond')%[2]s
			ORDER BY lease_until LIMIT 1)
	) upcoming`, s.cfg.Table, filter)
	var next sql.NullTime
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&next)
	return next.Time, err
}

func (s *SQLStore) buildInsert(task mq.Task, status string, meta []byte) (string, []any) {
	args := taskInsertArgs(task, status, meta)
	return "INSERT INTO " + s.cfg.Table + " (" + taskInsertColumns + ") VALUES " + s.insertPlaceholders(0, len(args)), args
}

const taskInsertColumns = "id, topic, tenant_id, payload, meta, priority, dedupe_key, status, attempts, max_attempts, available_at, lease_owner, lease_until, expires_at, last_error, last_error_at, created_at, updated_at"

func (s *SQLStore) insertPlaceholders(offset, length int) string {
	placeholders := make([]string, length)
	for i := range placeholders {
		placeholders[i] = s.placeholder(offset + i + 1)
	}
	return "(" + strings.Join(placeholders, ", ") + ")"
}

func taskInsertArgs(task mq.Task, status string, meta []byte) []any {
	return []any{
		task.ID,
		task.Topic,
		task.TenantID,
		task.Payload,
		nullBytes(meta),
		int(task.Priority),
		nullString(task.DedupeKey),
		status,
		task.Attempts,
		task.MaxAttempts,
		task.AvailableAt,
		nullString(task.LeaseOwner),
		nullTime(task.LeaseUntil),
		nullTime(task.ExpiresAt),
		nil,
		nil,
		task.CreatedAt,
		task.UpdatedAt,
	}
}

const reserveOrder = "priority DESC, created_at ASC, id ASC"

const reserveReadyTime = "GREATEST(created_at, available_at, lease_until)"

func reserveAgedOrder(readyTime string) string {
	// 已老化的任务按可领取时间排序。未老化任务沿用严格优先级。
	return "CASE WHEN " + readyTime + " <= $2 THEN 0 ELSE 1 END, CASE WHEN " +
		readyTime + " <= $2 THEN " + readyTime + " END ASC, " + reserveOrder
}

func (s *SQLStore) claimExclusion() string {
	if s.cfg.Dialect != DialectPostgres || s.cfg.BlockedClaimsTable == "" {
		return ""
	}
	return " AND NOT EXISTS (SELECT 1 FROM " + s.cfg.BlockedClaimsTable + " b WHERE b.task_id = " + s.cfg.Table + ".id)"
}

func (s *SQLStore) buildReserveSelect(opts mq.ReserveOptions, now time.Time, tenant string) (string, []any) {
	args := make([]any, 0, 6+len(opts.Topics))
	idx := 1

	var query string
	order := reserveOrder
	if s.cfg.Dialect == DialectPostgres {
		columns := "id"
		args = append(args, now)
		idx++
		if s.cfg.PriorityAging > 0 {
			columns += ", " + reserveReadyTime + " AS ready_at"
			order = reserveAgedOrder(reserveReadyTime)
			args = append(args, now.Add(-s.cfg.PriorityAging))
			idx++
		}
		query = fmt.Sprintf("SELECT %s FROM %s WHERE (status = 'queued' OR (status = 'leased' AND lease_until < $1)) AND available_at <= $1 AND (expires_at IS NULL OR expires_at > $1)", columns, s.cfg.Table)
	} else {
		query = fmt.Sprintf("SELECT id FROM %s WHERE (status = %s OR (status = %s AND lease_until < %s)) AND available_at <= %s AND (expires_at IS NULL OR expires_at > %s)",
			s.cfg.Table, s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4), s.placeholder(5))
		args = append(args, mq.TaskStatusQueued, mq.TaskStatusLeased, now, now, now)
		idx += len(args)
	}

	if len(opts.Topics) > 0 {
		placeholders := make([]string, 0, len(opts.Topics))
		for _, topic := range opts.Topics {
			placeholders = append(placeholders, s.placeholder(idx))
			args = append(args, topic)
			idx++
		}
		query += fmt.Sprintf(" AND topic IN (%s)", strings.Join(placeholders, ", "))
	}
	if tenant != "" {
		query += " AND tenant_id = " + s.placeholder(idx)
		args = append(args, tenant)
		idx++
	}

	query += s.claimExclusion()
	query += fmt.Sprintf(" ORDER BY %s LIMIT %s FOR UPDATE SKIP LOCKED", order, s.placeholder(idx))
	args = append(args, opts.Limit)

	return query, args
}

func (s *SQLStore) buildReserveUpdate(ids []string, consumerID string, leaseUntil, now time.Time) (string, []any) {
	args := make([]any, 0, 4+len(ids))
	idx := 1

	query := fmt.Sprintf("UPDATE %s SET status = %s, lease_owner = %s, lease_until = %s, attempts = attempts + 1, updated_at = %s WHERE id IN (", s.cfg.Table, s.placeholder(idx), s.placeholder(idx+1), s.placeholder(idx+2), s.placeholder(idx+3))
	args = append(args, mq.TaskStatusLeased, consumerID, leaseUntil, now)
	idx += 4

	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, s.placeholder(idx))
		args = append(args, id)
		idx++
	}
	query += strings.Join(placeholders, ", ") + ")"

	return query, args
}

func (s *SQLStore) buildSelectTasks(ids []string) (string, []any) {
	cols := []string{
		"id", "topic", "tenant_id", "payload", "meta", "priority", "dedupe_key",
		"available_at", "expires_at", "attempts", "max_attempts", "lease_owner", "lease_until",
		"created_at", "updated_at",
	}
	args := make([]any, 0, len(ids))
	idx := 1
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, s.placeholder(idx))
		args = append(args, id)
		idx++
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE id IN (%s) ORDER BY %s", strings.Join(cols, ", "), s.cfg.Table, strings.Join(placeholders, ", "), reserveOrder)
	return query, args
}

func (s *SQLStore) buildAck(taskID, consumerID string, now time.Time) (string, []any) {
	query := fmt.Sprintf("UPDATE %s SET status = %s, lease_owner = NULL, lease_until = NULL, updated_at = %s WHERE id = %s AND lease_owner = %s AND status = %s AND lease_until >= %s", s.cfg.Table, s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4), s.placeholder(5), s.placeholder(6))
	args := []any{mq.TaskStatusDone, now, taskID, consumerID, mq.TaskStatusLeased, now}
	return query, args
}

func (s *SQLStore) buildRelease(taskID, consumerID string, opts mq.ReleaseOptions) (string, []any) {
	query := fmt.Sprintf("UPDATE %s SET status = %s, available_at = %s, last_error = %s, last_error_at = %s, lease_owner = NULL, lease_until = NULL, updated_at = %s WHERE id = %s AND lease_owner = %s AND status = %s AND lease_until >= %s", s.cfg.Table, s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4), s.placeholder(5), s.placeholder(6), s.placeholder(7), s.placeholder(8), s.placeholder(9))
	args := []any{mq.TaskStatusQueued, opts.RetryAt, opts.Reason, opts.Now, opts.Now, taskID, consumerID, mq.TaskStatusLeased, opts.Now}
	return query, args
}

func (s *SQLStore) buildMoveToDLQ(taskID, consumerID, reason string, now time.Time) (string, []any) {
	query := fmt.Sprintf("UPDATE %s SET status = %s, last_error = %s, last_error_at = %s, lease_owner = NULL, lease_until = NULL, updated_at = %s WHERE id = %s AND lease_owner = %s AND status = %s AND lease_until >= %s", s.cfg.Table, s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4), s.placeholder(5), s.placeholder(6), s.placeholder(7), s.placeholder(8))
	args := []any{mq.TaskStatusDead, reason, now, now, taskID, consumerID, mq.TaskStatusLeased, now}
	return query, args
}

func (s *SQLStore) buildExtendLease(taskID, consumerID string, leaseUntil, now time.Time) (string, []any) {
	query := fmt.Sprintf("UPDATE %s SET lease_until = %s, updated_at = %s WHERE id = %s AND lease_owner = %s AND status = %s AND lease_until >= %s", s.cfg.Table, s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4), s.placeholder(5), s.placeholder(6))
	args := []any{leaseUntil, now, taskID, consumerID, mq.TaskStatusLeased, now}
	return query, args
}

func (s *SQLStore) countDead(ctx context.Context, tx *sql.Tx) (int, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE status = %s", s.cfg.Table, s.placeholder(1))
	row := tx.QueryRowContext(ctx, query, mq.TaskStatusDead)
	var total int
	if err := row.Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *SQLStore) selectDeadIDs(ctx context.Context, tx *sql.Tx, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	query := fmt.Sprintf("SELECT id FROM %s WHERE status = %s ORDER BY updated_at ASC LIMIT %s FOR UPDATE", s.cfg.Table, s.placeholder(1), s.placeholder(2))
	rows, err := tx.QueryContext(ctx, query, mq.TaskStatusDead, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *SQLStore) buildReplayUpdateAll(availableAt, now time.Time, resetAttempts bool) (string, []any) {
	args := make([]any, 0, 4)
	idx := 1

	query := fmt.Sprintf("UPDATE %s SET status = %s, available_at = %s, lease_owner = NULL, lease_until = NULL, updated_at = %s", s.cfg.Table, s.placeholder(idx), s.placeholder(idx+1), s.placeholder(idx+2))
	args = append(args, mq.TaskStatusQueued, availableAt, now)
	idx += 3

	if resetAttempts {
		query += fmt.Sprintf(", attempts = %s", s.placeholder(idx))
		args = append(args, 0)
		idx++
	}

	query += fmt.Sprintf(" WHERE status = %s", s.placeholder(idx))
	args = append(args, mq.TaskStatusDead)

	return query, args
}

func (s *SQLStore) buildReplayUpdate(ids []string, availableAt, now time.Time, resetAttempts bool) (string, []any) {
	args := make([]any, 0, 4+len(ids))
	idx := 1

	query := fmt.Sprintf("UPDATE %s SET status = %s, available_at = %s, lease_owner = NULL, lease_until = NULL, updated_at = %s", s.cfg.Table, s.placeholder(idx), s.placeholder(idx+1), s.placeholder(idx+2))
	args = append(args, mq.TaskStatusQueued, availableAt, now)
	idx += 3

	if resetAttempts {
		query += fmt.Sprintf(", attempts = %s", s.placeholder(idx))
		args = append(args, 0)
		idx++
	}

	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, s.placeholder(idx))
		args = append(args, id)
		idx++
	}
	query += fmt.Sprintf(" WHERE id IN (%s)", strings.Join(placeholders, ", "))

	return query, args
}

func (s *SQLStore) attemptLogEnabled() bool {
	return s.cfg.EnableAttemptLog && s.cfg.AttemptsTable != ""
}

func (s *SQLStore) insertAttemptsBestEffort(ctx context.Context, tx *sql.Tx, tasks []mq.Task, now time.Time) {
	if !s.attemptLogEnabled() || tx == nil {
		return
	}
	if len(tasks) >= 2 && len(tasks) <= 1024 && s.insertAttemptBatch(ctx, tx, tasks, now) {
		return
	}
	for i, task := range tasks {
		attemptNo := task.Attempts
		name := fmt.Sprintf("attempt_log_%d", i)
		s.runAttemptSavepoint(ctx, tx, name, "attempt.insert", task.ID, &attemptNo, func() error {
			query, args := s.buildInsertAttempt(task.ID, attemptNo, now)
			_, err := tx.ExecContext(ctx, query, args...)
			return err
		})
	}
}

// insertAttemptBatch rolls back to a savepoint before allowing a per-row retry.
func (s *SQLStore) insertAttemptBatch(ctx context.Context, tx *sql.Tx, tasks []mq.Task, now time.Time) bool {
	const name = "attempt_batch"
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return false
	}
	var query strings.Builder
	fmt.Fprintf(&query, "INSERT INTO %s (task_id, attempt_no, reason, started_at, finished_at, success) VALUES ", s.cfg.AttemptsTable)
	args := make([]any, 0, 3*len(tasks))
	for i, task := range tasks {
		if i != 0 {
			query.WriteString(", ")
		}
		n := len(args) + 1
		fmt.Fprintf(&query, "(%s, %s, NULL, %s, NULL, FALSE)", s.placeholder(n), s.placeholder(n+1), s.placeholder(n+2))
		args = append(args, task.ID, task.Attempts, now)
	}
	_, err := tx.ExecContext(ctx, query.String(), args...)
	if err != nil {
		_, _ = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+name)
	}
	_, releaseErr := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name)
	if releaseErr != nil {
		s.logAttemptError(ctx, "attempt.batch.release", "", 0, releaseErr)
	}
	return err == nil
}

func (s *SQLStore) finishAttemptBestEffort(ctx context.Context, tx *sql.Tx, taskID string, success bool, reason string, now time.Time) {
	if !s.attemptLogEnabled() || tx == nil {
		return
	}
	attemptNo := 0
	name := "attempt_finish"
	s.runAttemptSavepoint(ctx, tx, name, "attempt.finish", taskID, &attemptNo, func() error {
		var err error
		attemptNo, err = s.selectAttemptNumber(ctx, tx, taskID)
		if err != nil {
			return err
		}
		query, args := s.buildFinishAttempt(taskID, attemptNo, success, reason, now)
		_, err = tx.ExecContext(ctx, query, args...)
		return err
	})
}

func (s *SQLStore) selectAttemptNumber(ctx context.Context, tx *sql.Tx, taskID string) (int, error) {
	query := fmt.Sprintf("SELECT attempts FROM %s WHERE id = %s", s.cfg.Table, s.placeholder(1))
	var attempts int
	if err := tx.QueryRowContext(ctx, query, taskID).Scan(&attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, mq.ErrTaskNotFound
		}
		return 0, err
	}
	return attempts, nil
}

func (s *SQLStore) runAttemptSavepoint(ctx context.Context, tx *sql.Tx, name, op, taskID string, attempt *int, fn func() error) {
	if tx == nil {
		return
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SAVEPOINT %s", name)); err != nil {
		s.logAttemptError(ctx, op+".savepoint", taskID, attemptValue(attempt), err)
		return
	}
	if err := fn(); err != nil {
		_, _ = tx.ExecContext(ctx, fmt.Sprintf("ROLLBACK TO SAVEPOINT %s", name))
		_, _ = tx.ExecContext(ctx, fmt.Sprintf("RELEASE SAVEPOINT %s", name))
		s.logAttemptError(ctx, op, taskID, attemptValue(attempt), err)
		return
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("RELEASE SAVEPOINT %s", name)); err != nil {
		s.logAttemptError(ctx, op+".release", taskID, attemptValue(attempt), err)
	}
}

func (s *SQLStore) logAttemptError(ctx context.Context, op, taskID string, attempt int, err error) {
	if err == nil || s.cfg.AttemptLogHook == nil {
		return
	}
	s.cfg.AttemptLogHook(ctx, AttemptLogError{
		Op:      op,
		TaskID:  taskID,
		Attempt: attempt,
		Err:     err,
	})
}

func attemptValue(attempt *int) int {
	if attempt == nil {
		return 0
	}
	return *attempt
}

func (s *SQLStore) buildInsertAttempt(taskID string, attemptNo int, now time.Time) (string, []any) {
	cols := []string{"task_id", "attempt_no", "reason", "started_at", "finished_at", "success"}
	placeholders := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for i := range cols {
		placeholders = append(placeholders, s.placeholder(i+1))
	}
	args = append(args, taskID, attemptNo, nil, now, nil, false)
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", s.cfg.AttemptsTable, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	return query, args
}

func (s *SQLStore) buildFinishAttempt(taskID string, attemptNo int, success bool, reason string, now time.Time) (string, []any) {
	query := fmt.Sprintf("UPDATE %s SET finished_at = %s, success = %s, reason = %s WHERE task_id = %s AND attempt_no = %s", s.cfg.AttemptsTable, s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4), s.placeholder(5))
	args := []any{now, success, nullString(reason), taskID, attemptNo}
	return query, args
}

func (s *SQLStore) selectForDLQ(ctx context.Context, tx *sql.Tx, taskID, consumerID string, now time.Time) (mq.Task, error) {
	query := fmt.Sprintf("SELECT id, topic, tenant_id, payload, meta, priority, dedupe_key, available_at, expires_at, attempts, max_attempts, lease_owner, lease_until, created_at, updated_at FROM %s WHERE id = %s AND lease_owner = %s AND status = %s AND lease_until >= %s FOR UPDATE", s.cfg.Table, s.placeholder(1), s.placeholder(2), s.placeholder(3), s.placeholder(4))
	row := tx.QueryRowContext(ctx, query, taskID, consumerID, mq.TaskStatusLeased, now)
	task, err := scanTaskRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mq.Task{}, mq.ErrLeaseLost
		}
		return mq.Task{}, err
	}
	return task, nil
}

func (s *SQLStore) insertDLQ(ctx context.Context, tx *sql.Tx, task mq.Task, reason string, now time.Time) error {
	meta, err := marshalMeta(task.Meta)
	if err != nil {
		return err
	}

	cols := []string{"task_id", "topic", "tenant_id", "payload", "meta", "reason", "failed_at"}
	placeholders := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for i := range cols {
		placeholders = append(placeholders, s.placeholder(i+1))
	}
	args = append(args, task.ID, task.Topic, nullString(task.TenantID), task.Payload, nullBytes(meta), reason, now)

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", s.cfg.DLQTable, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLStore) placeholder(idx int) string {
	if s.cfg.Dialect == DialectPostgres {
		return fmt.Sprintf("$%d", idx)
	}
	return "?"
}

func marshalMeta(meta map[string]string) ([]byte, error) {
	if len(meta) == 0 {
		return nil, nil
	}
	return json.Marshal(meta)
}

func scanTask(rows *sql.Rows) (mq.Task, error) {
	return scanTaskFrom(rows.Scan)
}

func scanTaskRow(row *sql.Row) (mq.Task, error) {
	return scanTaskFrom(row.Scan)
}

func scanTaskFrom(scan func(dest ...any) error) (mq.Task, error) {
	var task mq.Task
	var meta sql.NullString
	var dedupe sql.NullString
	var tenant sql.NullString
	var leaseOwner sql.NullString
	var leaseUntil sql.NullTime
	var expiresAt sql.NullTime

	err := scan(
		&task.ID,
		&task.Topic,
		&tenant,
		&task.Payload,
		&meta,
		&task.Priority,
		&dedupe,
		&task.AvailableAt,
		&expiresAt,
		&task.Attempts,
		&task.MaxAttempts,
		&leaseOwner,
		&leaseUntil,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if err != nil {
		return task, err
	}

	if tenant.Valid {
		task.TenantID = tenant.String
	}
	if dedupe.Valid {
		task.DedupeKey = dedupe.String
	}
	if leaseOwner.Valid {
		task.LeaseOwner = leaseOwner.String
	}
	if leaseUntil.Valid {
		task.LeaseUntil = leaseUntil.Time
	}
	if expiresAt.Valid {
		task.ExpiresAt = expiresAt.Time
	}
	if meta.Valid {
		if err := json.Unmarshal([]byte(meta.String), &task.Meta); err != nil {
			return task, err
		}
	}

	return task, nil
}

func nullString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullInt64(v sql.NullInt64) int64 {
	if v.Valid {
		return v.Int64
	}
	return 0
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return !errors.Is(err, sql.ErrNoRows) && (strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique") || strings.Contains(msg, "constraint"))
}
