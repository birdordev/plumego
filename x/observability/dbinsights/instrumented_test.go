package dbinsights

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	testmetrics "github.com/birdordev/plumego/x/observability/testmetrics"
)

type mockDB struct {
	pingErr      error
	execErr      error
	queryErr     error
	beginErr     error
	closeErr     error
	rowsAffected int64
}

func (c *mockDB) Prepare(query string) (driver.Stmt, error) {
	return &mockStmt{
		execErr:      c.execErr,
		queryErr:     c.queryErr,
		rowsAffected: c.rowsAffected,
	}, nil
}

func (c *mockDB) Close() error { return c.closeErr }

func (c *mockDB) Begin() (driver.Tx, error) {
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	return stubTx{}, nil
}

func (c *mockDB) Ping(ctx context.Context) error { return c.pingErr }

type mockStmt struct {
	execErr      error
	queryErr     error
	rowsAffected int64
}

func (s *mockStmt) Close() error  { return nil }
func (s *mockStmt) NumInput() int { return -1 }

func (s *mockStmt) Exec(args []driver.Value) (driver.Result, error) {
	if s.execErr != nil {
		return nil, s.execErr
	}
	return mockResult{rowsAffected: s.rowsAffected}, nil
}

func (s *mockStmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.queryErr != nil {
		return nil, s.queryErr
	}
	return &mockRows{}, nil
}

type mockResult struct {
	rowsAffected int64
}

func (r mockResult) LastInsertId() (int64, error) { return 0, nil }
func (r mockResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

type mockRows struct{}

func (r *mockRows) Columns() []string              { return []string{} }
func (r *mockRows) Close() error                   { return nil }
func (r *mockRows) Next(dest []driver.Value) error { return io.EOF }

type mockConnector struct {
	conn driver.Conn
}

func (c *mockConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.conn, nil
}

func (c *mockConnector) Driver() driver.Driver { return stubDriver{} }

func newMockDB(opts ...func(*mockDB)) *sql.DB {
	conn := &mockDB{rowsAffected: 1}
	for _, opt := range opts {
		opt(conn)
	}
	return sql.OpenDB(&mockConnector{conn: conn})
}

func TestNewInstrumentedDB(t *testing.T) {
	db := newMockDB()
	defer db.Close()
	collector := testmetrics.NewMockCollector()

	idb := NewInstrumentedDB(db, collector, "postgres")

	if idb == nil {
		t.Fatal("expected non-nil InstrumentedDB")
	}
	if idb.db != db {
		t.Error("expected db to be set")
	}
	if idb.observer != collector {
		t.Error("expected observer to be set")
	}
	if idb.driver != "postgres" {
		t.Errorf("expected driver 'postgres', got %q", idb.driver)
	}
}

func TestInstrumentedDB_ExecContext(t *testing.T) {
	db := newMockDB(func(m *mockDB) { m.rowsAffected = 3 })
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "mysql")

	result, err := idb.ExecContext(t.Context(), "INSERT INTO users (name) VALUES (?)", "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows, _ := result.RowsAffected()
	if rows != 3 {
		t.Errorf("expected 3 rows affected, got %d", rows)
	}
	if collector.DBCallCount() != 1 {
		t.Fatalf("expected 1 DB call, got %d", collector.DBCallCount())
	}
}

func TestInstrumentedDB_ExecContext_Error(t *testing.T) {
	execErr := errors.New("exec failed")
	db := newMockDB(func(m *mockDB) { m.execErr = execErr })
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	if _, err := idb.ExecContext(t.Context(), "INSERT INTO users (name) VALUES (?)", "alice"); err == nil {
		t.Fatal("expected error")
	}
	if collector.DBCallCount() != 1 {
		t.Fatalf("expected 1 DB call, got %d", collector.DBCallCount())
	}
}

func TestInstrumentedDB_QueryContext(t *testing.T) {
	db := newMockDB()
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	rows, err := idb.QueryContext(t.Context(), "SELECT * FROM users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rows.Close()

	if collector.DBCallCount() != 1 {
		t.Fatalf("expected 1 DB call, got %d", collector.DBCallCount())
	}
}

func TestInstrumentedDB_QueryContext_Error(t *testing.T) {
	queryErr := errors.New("query failed")
	db := newMockDB(func(m *mockDB) { m.queryErr = queryErr })
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	if _, err := idb.QueryContext(t.Context(), "SELECT * FROM invalid"); err == nil {
		t.Fatal("expected error")
	}
	if collector.DBCallCount() != 1 {
		t.Fatalf("expected 1 DB call, got %d", collector.DBCallCount())
	}
}

func TestInstrumentedDB_QueryRowContext(t *testing.T) {
	db := newMockDB()
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	row := idb.QueryRowContext(t.Context(), "SELECT * FROM users WHERE id = ?", 1)
	if row == nil {
		t.Fatal("expected non-nil row")
	}
	if calls := collector.GetDBCalls(); len(calls) != 1 || calls[0].Rows != 1 || calls[0].Err != nil {
		t.Fatalf("unexpected DB calls: %+v", calls)
	}
	if err := row.Scan(); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Scan error = %v, want sql.ErrNoRows", err)
	}
}

func TestAudit08QueryRowError(t *testing.T) {
	driverErr := errors.New("query failed")
	for _, tc := range []struct {
		name     string
		canceled bool
		want     error
	}{
		{"canceled", true, context.Canceled},
		{"driver", false, driverErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newMockDB(func(db *mockDB) { db.queryErr = driverErr })
			defer db.Close()
			collector := testmetrics.NewMockCollector()
			idb := NewInstrumentedDB(db, collector, "postgres")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			row := idb.QueryRowContext(ctx, "SELECT 1")
			if err := row.Err(); !errors.Is(err, tc.want) {
				t.Fatalf("Row.Err = %v, want %v", err, tc.want)
			}
			if calls := collector.GetDBCalls(); len(calls) != 1 || calls[0].Rows != 0 || !errors.Is(calls[0].Err, tc.want) {
				t.Fatalf("observations = %+v, want one with zero rows and %v", calls, tc.want)
			}
			if err := row.Scan(); !errors.Is(err, tc.want) {
				t.Fatalf("Scan error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestInstrumentedDB_BeginTx(t *testing.T) {
	db := newMockDB()
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	tx, err := idb.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tx == nil {
		t.Fatal("expected transaction")
	}
	if collector.DBCallCount() != 1 {
		t.Fatalf("expected 1 DB call, got %d", collector.DBCallCount())
	}
}

func TestInstrumentedDB_PingContext(t *testing.T) {
	db := newMockDB()
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	if err := idb.PingContext(t.Context()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if collector.DBCallCount() != 1 {
		t.Fatalf("expected 1 DB call, got %d", collector.DBCallCount())
	}
}

func TestInstrumentedDB_Close(t *testing.T) {
	db := newMockDB()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	if err := idb.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if collector.DBCallCount() != 1 {
		t.Fatalf("expected 1 DB call, got %d", collector.DBCallCount())
	}
}

func TestInstrumentedDB_RecordPoolStats(t *testing.T) {
	db := newMockDB()
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	idb.RecordPoolStats(t.Context())

	if collector.DBCallCount() != 7 {
		t.Fatalf("expected 7 DB calls, got %d", collector.DBCallCount())
	}
}

func TestInstrumentedDB_RecordPoolStats_NilObserver(t *testing.T) {
	db := newMockDB()
	defer db.Close()
	idb := NewInstrumentedDB(db, nil, "postgres")

	idb.RecordPoolStats(t.Context())
}

func TestInstrumentedDB_ConfigForwarders(t *testing.T) {
	db := newMockDB()
	defer db.Close()
	collector := testmetrics.NewMockCollector()
	idb := NewInstrumentedDB(db, collector, "postgres")

	idb.SetMaxOpenConns(11)
	idb.SetMaxIdleConns(7)
	idb.SetConnMaxLifetime(2 * time.Second)
	idb.SetConnMaxIdleTime(time.Second)

	stats := idb.Stats()
	if stats.MaxOpenConnections != 11 {
		t.Fatalf("expected max open connections 11, got %d", stats.MaxOpenConnections)
	}
	if idb.Unwrap() != db {
		t.Fatal("expected unwrap to return original db")
	}
}

type stubDriver struct{}

func (stubDriver) Open(name string) (driver.Conn, error) { return nil, errors.New("not used") }

type stubTx struct{}

func (stubTx) Commit() error   { return nil }
func (stubTx) Rollback() error { return nil }

func TestSlowQueryDBQueryContextObservesAndPreservesErrors(t *testing.T) {
	sentinel := errors.New("test query failure")
	for _, wantErr := range []error{nil, sentinel} {
		db := newMockDB(func(m *mockDB) { m.queryErr = wantErr })
		t.Cleanup(func() { _ = db.Close() })
		base := testmetrics.NewMockCollector()
		w := NewSlowQueryDB(NewInstrumentedDB(db, base, "test"), "test", WithThreshold(0))
		rows, err := w.QueryContext(t.Context(), "SELECT id FROM items")
		if !errors.Is(err, wantErr) {
			t.Errorf("query error = %v, want %v", err, wantErr)
		}
		if wantErr == nil {
			if rows == nil {
				t.Fatal("query did not return rows")
			}
			if rows.Next() {
				t.Fatal("mock unexpectedly returned a row")
			}
			if err := rows.Close(); err != nil {
				t.Error(err)
			}
		}
		if got := w.GetDetector().GetTotalDetected(); got != 1 || base.DBCallCount() != 1 {
			t.Errorf("detected=%d, observed=%d", got, base.DBCallCount())
		}
	}
}
