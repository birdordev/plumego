package sqlx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	storedb "github.com/birdordev/plumego/store/db"
)

func init() {
	sql.Register("plumego-sqlx-test", testDriver{})
}

func TestQueryRowReturnsScannedValue(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	var got string
	if err := db.QueryRow(t.Context(), "select single name").Scan(&got); err != nil {
		t.Fatalf("scan row: %v", err)
	}
	if got != "plumego" {
		t.Fatalf("got %q, want plumego", got)
	}
}

func TestQueryReturnsMultipleRows(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	rows, err := db.Query(t.Context(), "select name")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("rows = %#v, want [alpha beta]", got)
	}
}

func TestExecAffectsRowCount(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	result, err := db.Exec(t.Context(), "update widgets set active = true")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got, err := result.RowsAffected(); err != nil || got != 3 {
		t.Fatalf("rows affected = %d, %v; want 3, nil", got, err)
	}
}

func TestBeginTxCommitAndRollbackSucceed(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx, err = db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

func TestNilDBQueryReturnsError(t *testing.T) {
	if _, err := (*DB)(nil).Query(t.Context(), "select 1"); err == nil {
		t.Fatal("Query() error = nil, want error")
	}
}

var pingTestDriverID atomic.Uint64

type pingTestDriver struct{ conn *pingTestConn }

func (d pingTestDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

type pingTestConn struct {
	testConn
	pingErr, closeErr error
	closeCalls        int
}

func (c *pingTestConn) Ping(context.Context) error { return c.pingErr }

func (c *pingTestConn) Close() error {
	c.closeCalls++
	return c.closeErr
}

func TestNewReportsPingCleanupFailure(t *testing.T) {
	pingErr := errors.New("test ping failure")
	closeErr := errors.New("test close failure")
	for _, tt := range []struct {
		name     string
		pingErr  error
		closeErr error
	}{
		{name: "ping succeeds"},
		{name: "ping fails", pingErr: pingErr},
		{name: "ping and cleanup fail", pingErr: pingErr, closeErr: closeErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conn := &pingTestConn{pingErr: tt.pingErr, closeErr: tt.closeErr}
			driverName := fmt.Sprintf("plumego-sqlx-test-ping-%d", pingTestDriverID.Add(1))
			sql.Register(driverName, pingTestDriver{conn: conn})
			db, err := New(driverName, "")
			if tt.pingErr == nil {
				if db == nil || err != nil || conn.closeCalls != 0 {
					t.Fatalf("New = %v, %v, close calls = %d; want open handle", db, err, conn.closeCalls)
				}
				if err := db.Close(); err != nil || conn.closeCalls != 1 {
					t.Fatalf("Close = %v, close calls = %d", err, conn.closeCalls)
				}
				return
			}

			if db != nil || !errors.Is(err, storedb.ErrConnectionFailed) || !errors.Is(err, tt.pingErr) ||
				errors.Is(err, closeErr) != (tt.closeErr != nil) || conn.closeCalls != 1 {
				t.Fatalf("New = %v, %v, close calls = %d; want connection and ping errors and cleanup error %v", db, err, conn.closeCalls, tt.closeErr)
			}
		})
	}
}

func newTestDB(t *testing.T) *DB {
	t.Helper()
	raw, err := sql.Open("plumego-sqlx-test", "")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return NewWithDB(raw)
}

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) {
	return testConn{}, nil
}

type testConn struct{}

func (testConn) Prepare(query string) (driver.Stmt, error) {
	return testStmt{query: query}, nil
}

func (testConn) Close() error {
	return nil
}

func (testConn) Begin() (driver.Tx, error) {
	return testTx{}, nil
}

func (testConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return testTx{}, nil
}

func (testConn) Ping(context.Context) error {
	return nil
}

type testStmt struct {
	query string
}

func (s testStmt) Close() error {
	return nil
}

func (s testStmt) NumInput() int {
	return -1
}

func (s testStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(3), nil
}

func (s testStmt) Query([]driver.Value) (driver.Rows, error) {
	switch s.query {
	case "select name":
		return &testRows{columns: []string{"name"}, values: [][]driver.Value{{"alpha"}, {"beta"}}}, nil
	default:
		return &testRows{columns: []string{"name"}, values: [][]driver.Value{{"plumego"}}}, nil
	}
}

type testTx struct{}

func (testTx) Commit() error {
	return nil
}

func (testTx) Rollback() error {
	return nil
}

type testRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *testRows) Columns() []string {
	return r.columns
}

func (r *testRows) Close() error {
	return nil
}

func (r *testRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	row := r.values[r.index]
	r.index++
	if len(dest) != len(row) {
		return errors.New("destination count mismatch")
	}
	copy(dest, row)
	return nil
}

func TestSQLXTransactionOperationsRespectTerminalState(t *testing.T) {
	db := newTestDB(t)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	for _, mode := range []string{"commit", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tx.Rollback() })
			var value string
			if err := tx.QueryRow(ctx, "select single name").Scan(&value); err != nil || value != "plumego" {
				t.Errorf("transaction Scan = %q, %v", value, err)
			}
			rows, err := tx.Query(ctx, "select name")
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Error("active transaction returned no rows")
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			res, err := tx.Exec(ctx, "update example set enabled = true")
			if err != nil {
				t.Fatal(err)
			}
			if count, err := res.RowsAffected(); count != 3 || err != nil {
				t.Errorf("RowsAffected = %d, %v", count, err)
			}
			if mode == "commit" {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(ctx, "select single name").Scan(&value); !errors.Is(err, sql.ErrTxDone) {
				t.Errorf("terminal Scan = %v", err)
			}
			if rows, err := tx.Query(ctx, "select name"); rows != nil || !errors.Is(err, sql.ErrTxDone) {
				t.Errorf("terminal Query = %v, %v", rows, err)
			}
			if result, err := tx.Exec(ctx, "update example set enabled = true"); result != nil || !errors.Is(err, sql.ErrTxDone) {
				t.Errorf("terminal Exec = %v, %v", result, err)
			}
		})
	}
}

func TestSQLXReturnsInjectedDB(t *testing.T) {
	raw, err := sql.Open("plumego-sqlx-test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if got := NewWithDB(raw).SQL(); got != raw {
		t.Errorf("SQL=%p, want %p", got, raw)
	}
	for _, empty := range []*DB{nil, NewWithDB(nil)} {
		if got := empty.SQL(); got != nil {
			t.Errorf("empty SQL=%p", got)
		}
	}
}
