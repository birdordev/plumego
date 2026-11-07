package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spcent/plumego/x/messaging/mq"
)

var (
	attemptDriverOnce sync.Once
	attemptDriverName = "mq_attempt_driver"
	errAttempt        = errors.New("attempt log failed")
)

func TestSQLStoreAttemptLogHookOnAck(t *testing.T) {
	db := openAttemptDB(t)

	cfg := DefaultSQLConfig()
	cfg.EnableAttemptLog = true

	var got AttemptLogError
	cfg.AttemptLogHook = func(ctx context.Context, info AttemptLogError) {
		got = info
	}

	store := NewSQL(db, cfg)

	err := store.Ack(t.Context(), "task-1", "consumer-1", time.Now())
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if got.Op == "" {
		t.Fatalf("expected hook call")
	}
	if got.Op != "attempt.finish" {
		t.Fatalf("unexpected op: %s", got.Op)
	}
	if got.TaskID != "task-1" {
		t.Fatalf("unexpected task id: %s", got.TaskID)
	}
	if got.Attempt != 1 {
		t.Fatalf("unexpected attempt: %d", got.Attempt)
	}
	if !errors.Is(got.Err, errAttempt) {
		t.Fatalf("unexpected error: %v", got.Err)
	}
}

func openAttemptDB(t *testing.T) *sql.DB {
	attemptDriverOnce.Do(func() {
		sql.Register(attemptDriverName, attemptDriver{})
	})

	db, err := sql.Open(attemptDriverName, "")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

type attemptDriver struct{}

type attemptConn struct{}

type attemptTx struct{}

type attemptResult struct{ rows int64 }

type attemptRows struct {
	cols   []string
	values [][]driver.Value
	idx    int
}

func (d attemptDriver) Open(name string) (driver.Conn, error) {
	return &attemptConn{}, nil
}

func (c *attemptConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("prepare not supported")
}

func (c *attemptConn) Close() error { return nil }

func (c *attemptConn) Begin() (driver.Tx, error) { return &attemptTx{}, nil }

func (c *attemptConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return &attemptTx{}, nil
}

func (c *attemptConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	query = strings.TrimSpace(query)

	switch {
	case strings.HasPrefix(query, "UPDATE mq_tasks SET status"):
		return attemptResult{rows: 1}, nil
	case strings.HasPrefix(query, "SAVEPOINT"):
		return attemptResult{rows: 0}, nil
	case strings.HasPrefix(query, "ROLLBACK TO SAVEPOINT"):
		return attemptResult{rows: 0}, nil
	case strings.HasPrefix(query, "RELEASE SAVEPOINT"):
		return attemptResult{rows: 0}, nil
	case strings.Contains(query, "mq_task_attempts"):
		return nil, errAttempt
	default:
		return attemptResult{rows: 0}, nil
	}
}

func (c *attemptConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	query = strings.TrimSpace(query)

	if strings.HasPrefix(query, "SELECT attempts FROM mq_tasks") {
		return &attemptRows{cols: []string{"attempts"}, values: [][]driver.Value{{int64(1)}}}, nil
	}
	return &attemptRows{cols: []string{"attempts"}, values: [][]driver.Value{}}, nil
}

func (t *attemptTx) Commit() error   { return nil }
func (t *attemptTx) Rollback() error { return nil }

func (r attemptResult) LastInsertId() (int64, error) { return 0, nil }
func (r attemptResult) RowsAffected() (int64, error) { return r.rows, nil }

func (r *attemptRows) Columns() []string { return r.cols }

func (r *attemptRows) Close() error { return nil }

func (r *attemptRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.values) {
		return io.EOF
	}
	row := r.values[r.idx]
	for i := range dest {
		dest[i] = row[i]
	}
	r.idx++
	return nil
}

var _ mq.TaskStore = (*SQLStore)(nil)
