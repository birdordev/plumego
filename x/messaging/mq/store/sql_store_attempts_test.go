package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
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

func TestSQLReserveIncludesExpiredLeases(t *testing.T) {
	store := NewSQL(nil, DefaultSQLConfig())
	now := time.Unix(12345, 0)
	query, args := store.buildReserveSelect(mq.ReserveOptions{Topics: []string{"send"}, Limit: 2}, now)
	if !strings.Contains(query, "WHERE (status = $1 OR (status = $2 AND lease_until < $3))") ||
		!strings.Contains(query, "AND available_at <= $4 AND (expires_at IS NULL OR expires_at > $5)") ||
		!strings.Contains(query, "AND topic IN ($6)") || !strings.HasSuffix(query, "LIMIT $7") {
		t.Fatalf("unexpected reserve query: %s", query)
	}
	want := []any{mq.TaskStatusQueued, mq.TaskStatusLeased, now, now, now, "send", 2}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

type reserveConnector struct {
	driver.Connector
	conn *reserveConn
}

func (c reserveConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }

type reserveConn struct {
	attemptConn
	invalidID  bool
	tx         *reserveTx
	expireErr  error
	queryCalls int
}

func (c *reserveConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c.tx, nil }

func (c *reserveConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.expireErr != nil && strings.Contains(query, "expires_at IS NOT NULL") {
		return nil, c.expireErr
	}
	return c.attemptConn.ExecContext(ctx, query, args)
}

func (c *reserveConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.queryCalls++
	if strings.HasPrefix(query, "SELECT id FROM") {
		var id driver.Value = "task"
		if c.invalidID {
			id = nil
		}
		return &attemptRows{cols: []string{"id"}, values: [][]driver.Value{{id}}}, nil
	}
	return &attemptRows{cols: []string{"invalid_task_shape"}, values: [][]driver.Value{{nil}}}, nil
}

type reserveTx struct {
	committed  bool
	rolledBack bool
}

func (t *reserveTx) Commit() error { t.committed = true; return nil }

func (t *reserveTx) Rollback() error { t.rolledBack = true; return nil }

func TestReserveRollsBackOnScanFailure(t *testing.T) {
	for _, invalidID := range []bool{true, false} {
		name := "task scan"
		if invalidID {
			name = "ID scan"
		}
		t.Run(name, func(t *testing.T) {
			tx := new(reserveTx)
			db := sql.OpenDB(reserveConnector{conn: &reserveConn{invalidID: invalidID, tx: tx}})
			defer db.Close()
			_, err := NewSQL(db, DefaultSQLConfig()).Reserve(t.Context(), mq.ReserveOptions{Limit: 1, ConsumerID: "worker"})
			if err == nil || !tx.rolledBack || db.Stats().InUse != 0 {
				t.Fatalf("err = %v, rollback = %v, DB stats = %+v", err, tx.rolledBack, db.Stats())
			}
		})
	}
}

func TestAudit19ReserveReturnsExpiryUpdateFailure(t *testing.T) {
	cause := errors.New("test expiry update failure")
	tx := new(reserveTx)
	conn := &reserveConn{tx: tx, expireErr: cause}
	db := sql.OpenDB(reserveConnector{conn: conn})
	defer db.Close()

	tasks, err := NewSQL(db, DefaultSQLConfig()).Reserve(t.Context(), mq.ReserveOptions{Limit: 1, ConsumerID: "worker"})
	if !errors.Is(err, cause) || len(tasks) != 0 {
		t.Fatalf("Reserve = %v, %v, want no tasks and the expiry update error", tasks, err)
	}
	if conn.queryCalls != 0 || tx.committed || !tx.rolledBack || db.Stats().InUse != 0 {
		t.Fatalf("queries = %d, committed = %t, rolled back = %t, DB stats = %+v",
			conn.queryCalls, tx.committed, tx.rolledBack, db.Stats())
	}
}

type statsConn struct {
	attemptConn
	row []driver.Value
	err error
}

func (c statsConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.err != nil {
		return nil, c.err
	}
	return &attemptRows{cols: []string{"queued", "leased", "dead", "expired"}, values: [][]driver.Value{c.row}}, nil
}

type statsConnector struct {
	driver.Connector
	conn statsConn
}

func (c statsConnector) Connect(context.Context) (driver.Conn, error) { return &c.conn, nil }

func TestSQLStoreStatsHandlesNullCountsAndQueryError(t *testing.T) {
	sentinel := errors.New("test stats failure")
	for _, tc := range []struct {
		row   []driver.Value
		stats mq.Stats
		err   error
	}{
		{[]driver.Value{nil, nil, nil, nil}, mq.Stats{}, nil},
		{[]driver.Value{int64(1), int64(2), int64(3), int64(4)}, mq.Stats{Queued: 1, Leased: 2, Dead: 3, Expired: 4}, nil},
		{nil, mq.Stats{}, sentinel},
	} {
		db := sql.OpenDB(statsConnector{conn: statsConn{row: tc.row, err: tc.err}})
		t.Cleanup(func() { _ = db.Close() })
		got, err := NewSQL(db, DefaultSQLConfig()).Stats(t.Context())
		if got != tc.stats || !errors.Is(err, tc.err) {
			t.Errorf("Stats=%+v, %v; want %+v, %v", got, err, tc.stats, tc.err)
		}
	}
}
