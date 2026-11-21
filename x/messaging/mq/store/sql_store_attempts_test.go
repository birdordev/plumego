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

type leaseTestTx struct{ commits, rollbacks int }

func (tx *leaseTestTx) Commit() error   { tx.commits++; return nil }
func (tx *leaseTestTx) Rollback() error { tx.rollbacks++; return nil }

type leaseTestConn struct {
	attemptConn
	tx      leaseTestTx
	rows    int64
	args    []driver.NamedValue
	execErr error
}

func (c *leaseTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &c.tx, nil
}
func (*leaseTestConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	now := time.Unix(100, 0).UTC()
	return &attemptRows{cols: make([]string, 15), values: [][]driver.Value{{
		"task", "fixture", nil, []byte("sample"), nil, int64(0), nil, now, nil, int64(1), int64(2), "consumer", now, now, now,
	}}}, nil
}

func (c *leaseTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(query, "UPDATE mq_tasks SET") {
		c.args = slices.Clone(args)
		return attemptResult{rows: c.rows}, c.execErr
	}
	return attemptResult{rows: 1}, nil
}

type leaseTestConnector struct {
	driver.Connector
	conn *leaseTestConn
}

func (c leaseTestConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }

func TestSQLStoreLostLeaseRollsBack(t *testing.T) {
	for _, name := range []string{"ack", "release", "move_to_dlq", "move_to_dlq_success"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			conn := &leaseTestConn{}
			if name == "move_to_dlq_success" {
				conn.rows = 1
			}
			db := sql.OpenDB(leaseTestConnector{conn: conn})
			defer db.Close()
			defer cancel()
			cfg := DefaultSQLConfig()
			cfg.EnableAttemptLog = true
			s := NewSQL(db, cfg)
			var err error
			switch name {
			case "ack":
				err = s.Ack(ctx, "task", "consumer", s.nowFunc())
			case "release":
				err = s.Release(ctx, "task", "consumer", mq.ReleaseOptions{})
			case "move_to_dlq", "move_to_dlq_success":
				err = s.MoveToDLQ(ctx, "task", "consumer", "fixture", s.nowFunc())
			}
			wantErr, commits, rollbacks := mq.ErrLeaseLost, 0, 1
			if name == "move_to_dlq_success" {
				wantErr, commits, rollbacks = nil, 1, 0
			}
			if !errors.Is(err, wantErr) || db.Stats().InUse != 0 || conn.tx.commits != commits || conn.tx.rollbacks != rollbacks {
				t.Errorf("err=%v in_use=%d commits=%d rollbacks=%d", err, db.Stats().InUse, conn.tx.commits, conn.tx.rollbacks)
			}
		})
	}
}

func TestSQLStoreExtendLeaseContract(t *testing.T) {
	now := time.Unix(1234, 0).UTC()
	sentinel := errors.New("database unavailable")
	for _, tc := range []struct {
		name             string
		lease, wantLease time.Duration
		rows             int64
		execErr, wantErr error
	}{
		{name: "default", lease: -time.Second, wantLease: mq.DefaultLeaseDuration, rows: 1},
		{name: "explicit", lease: time.Minute, wantLease: time.Minute, rows: 1},
		{name: "lost", lease: time.Minute, wantLease: time.Minute, wantErr: mq.ErrLeaseLost},
		{name: "driver failure", lease: time.Minute, wantLease: time.Minute, execErr: sentinel, wantErr: sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &leaseTestConn{rows: tc.rows, execErr: tc.execErr}
			db := sql.OpenDB(leaseTestConnector{conn: conn})
			defer db.Close()
			s := NewSQL(db, DefaultSQLConfig())
			if err := s.ExtendLease(t.Context(), "task", "consumer", tc.lease, now); !errors.Is(err, tc.wantErr) {
				t.Errorf("ExtendLease = %v, want %v", err, tc.wantErr)
			}
			want := []any{now.Add(tc.wantLease), now, "task", "consumer", string(mq.TaskStatusLeased), now}
			got := make([]any, len(conn.args))
			for i, arg := range conn.args {
				got[i] = arg.Value
			}
			if !slices.Equal(got, want) {
				t.Errorf("renewal args = %v, want %v", got, want)
			}
		})
	}
}

type replayTestConn struct {
	attemptConn
	leaseTestTx
	total, affected        int64
	execErr, commitErr     error
	selectArgs, updateArgs []driver.NamedValue
	updateSQL              string
}

func (c *replayTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c, nil }
func (c *replayTestConn) Commit() error {
	c.commits++
	return c.commitErr
}
func (c *replayTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.HasPrefix(query, "SELECT COUNT"):
		return &attemptRows{cols: []string{"count"}, values: [][]driver.Value{{c.total}}}, nil
	case strings.HasPrefix(query, "SELECT id"):
		c.selectArgs = slices.Clone(args)
		return &attemptRows{cols: []string{"id"}, values: [][]driver.Value{{"oldest"}}}, nil
	default:
		return nil, errors.New("unexpected replay query")
	}
}
func (c *replayTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.updateSQL = query
	c.updateArgs = slices.Clone(args)
	if c.execErr != nil {
		return nil, c.execErr
	}
	return attemptResult{rows: c.affected}, nil
}

type replayTestConnector struct {
	driver.Connector
	conn *replayTestConn
}

func (c replayTestConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }

func TestSQLStoreReplayDLQTransaction(t *testing.T) {
	now := time.Unix(123, 0).UTC()
	available := now.Add(time.Minute)
	sentinel := errors.New("replay driver failed")
	for _, tc := range []struct {
		name               string
		total, affected    int64
		max                int
		execErr, commitErr error
		want               ReplayResult
		commits, rollbacks int
	}{
		{name: "empty", commits: 1},
		{name: "limited", total: 3, affected: 1, max: 1, want: ReplayResult{Replayed: 1, Remaining: 2}, commits: 1},
		{name: "all", total: 3, affected: 3, want: ReplayResult{Replayed: 3}, commits: 1},
		{name: "exec failure", total: 3, max: 1, execErr: sentinel, rollbacks: 1},
		{name: "commit failure", total: 3, affected: 3, commitErr: sentinel, commits: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &replayTestConn{total: tc.total, affected: tc.affected, execErr: tc.execErr, commitErr: tc.commitErr}
			db := sql.OpenDB(replayTestConnector{conn: conn})
			defer db.Close()
			got, err := NewSQL(db, DefaultSQLConfig()).ReplayDLQ(t.Context(), ReplayOptions{Now: now, AvailableAt: available, Max: tc.max, ResetAttempts: true})
			if got != tc.want {
				t.Errorf("ReplayDLQ=%+v, %v", got, err)
			}
			if tc.execErr != nil || tc.commitErr != nil {
				if !errors.Is(err, sentinel) {
					t.Errorf("driver error lost: %v", err)
				}
			} else if err != nil {
				t.Errorf("unexpected replay error: %v", err)
			}
			if conn.commits != tc.commits || conn.rollbacks != tc.rollbacks || db.Stats().InUse != 0 {
				t.Errorf("commits=%d rollbacks=%d database=%+v", conn.commits, conn.rollbacks, db.Stats())
			}
			if tc.total == 0 {
				if conn.updateSQL != "" {
					t.Errorf("empty replay issued %q", conn.updateSQL)
				}
				return
			}
			wantArgs := []any{string(mq.TaskStatusQueued), available, now, int64(0), string(mq.TaskStatusDead)}
			if tc.max != 0 {
				wantArgs[4] = "oldest"
				wantSelect := []driver.NamedValue{{Ordinal: 1, Value: string(mq.TaskStatusDead)}, {Ordinal: 2, Value: int64(1)}}
				if !slices.Equal(conn.selectArgs, wantSelect) || !strings.Contains(conn.updateSQL, "WHERE id IN") {
					t.Errorf("unexpected limited replay: %v %q", conn.selectArgs, conn.updateSQL)
				}
			} else if len(conn.selectArgs) != 0 || !strings.Contains(conn.updateSQL, "WHERE status =") {
				t.Errorf("unexpected full replay: %v %q", conn.selectArgs, conn.updateSQL)
			}
			gotArgs := make([]any, len(conn.updateArgs))
			for i, arg := range conn.updateArgs {
				gotArgs[i] = arg.Value
			}
			if !slices.Equal(gotArgs, wantArgs) {
				t.Errorf("update args=%v, want %v", gotArgs, wantArgs)
			}
		})
	}
}
