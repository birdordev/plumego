package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

const SQLBatchLimit = 128

// InsertBatchTx 使用一条 SQL 插入已准备的任务。调用方负责提交或回滚事务。
func (s *SQLStore) InsertBatchTx(ctx context.Context, tx *sql.Tx, tasks []mq.Task) error {
	if s == nil || tx == nil {
		return mq.ErrNotInitialized
	}
	if len(tasks) > SQLBatchLimit {
		return mq.ErrInvalidConfig
	}
	if len(tasks) == 0 {
		return nil
	}
	args := make([]any, 0, len(tasks)*18)
	values := make([]string, 0, len(tasks))
	for _, task := range tasks {
		meta, err := marshalMeta(task.Meta)
		if err != nil {
			return err
		}
		row := taskInsertArgs(task, mq.TaskStatusQueued, meta)
		values = append(values, s.insertPlaceholders(len(args), len(row)))
		args = append(args, row...)
	}
	query := "INSERT INTO " + s.cfg.Table + " (" + taskInsertColumns + ") VALUES " + strings.Join(values, ", ")
	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil && isDuplicateError(err) {
		return mq.ErrDuplicateTask
	}
	return err
}

// TaskAcknowledgment 标识待确认任务的一次领取。
type TaskAcknowledgment struct {
	TaskID     string
	LeaseOwner string
}

// AckBatch 在 PostgreSQL 中合并确认。逐条结果按输入顺序返回。
// 整体错误表示没有可用的逐条结果，提交错误可能意味着结果未知。
func (s *SQLStore) AckBatch(ctx context.Context, batch []TaskAcknowledgment, now time.Time) ([]error, error) {
	if s == nil || s.db == nil {
		return nil, mq.ErrNotInitialized
	}
	if s.cfg.Dialect != DialectPostgres {
		return nil, errors.New("batch acknowledgment requires PostgreSQL")
	}
	if len(batch) > SQLBatchLimit {
		return nil, mq.ErrInvalidConfig
	}
	if len(batch) == 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = s.nowFunc()
	}
	args := make([]any, 1, 1+len(batch)*2)
	args[0] = now
	values := make([]string, 0, len(batch))
	outcome := make(map[TaskAcknowledgment]bool, len(batch))
	for _, item := range batch {
		if _, exists := outcome[item]; exists {
			continue
		}
		outcome[item] = false
		i := len(args) + 1
		values = append(values, fmt.Sprintf("($%d::text, $%d::text)", i, i+1))
		args = append(args, item.TaskID, item.LeaseOwner)
	}
	query := fmt.Sprintf(`WITH wanted(id, owner) AS (VALUES %s), locked AS (
		SELECT q.id, w.owner FROM %[2]s q JOIN wanted w ON q.id = w.id AND q.lease_owner = w.owner
		WHERE q.status = 'leased' AND q.lease_until >= $1 ORDER BY q.id FOR UPDATE OF q
	)
	UPDATE %[2]s q SET status = 'done', lease_owner = NULL, lease_until = NULL, updated_at = $1
	FROM locked WHERE q.id = locked.id RETURNING q.id, locked.owner`, strings.Join(values, ", "), s.cfg.Table)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item TaskAcknowledgment
		if err := rows.Scan(&item.TaskID, &item.LeaseOwner); err != nil {
			return nil, err
		}
		outcome[item] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.attemptLogEnabled() {
		for item, updated := range outcome {
			if updated {
				s.finishAttemptBestEffort(ctx, tx, item.TaskID, true, "", now)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	results := make([]error, len(batch))
	for i, item := range batch {
		if !outcome[item] {
			results[i] = mq.ErrLeaseLost
		}
	}
	return results, nil
}
