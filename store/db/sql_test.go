package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "missing driver",
			config:  Config{DSN: "dsn"},
			wantErr: true,
		},
		{
			name:    "missing dsn",
			config:  Config{Driver: "driver"},
			wantErr: true,
		},
		{
			name:    "negative max open",
			config:  Config{Driver: "driver", DSN: "dsn", MaxOpenConns: -1},
			wantErr: true,
		},
		{
			name:    "valid",
			config:  Config{Driver: "driver", DSN: "dsn", MaxOpenConns: 5},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		if err := tc.config.Validate(); (err != nil) != tc.wantErr {
			t.Fatalf("%s: Validate() error = %v", tc.name, err)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig("mysql", "user:pass@tcp(localhost:3306)/db")

	if config.Driver != "mysql" {
		t.Fatalf("expected driver mysql, got %s", config.Driver)
	}
	if config.DSN != "user:pass@tcp(localhost:3306)/db" {
		t.Fatalf("expected DSN, got %s", config.DSN)
	}
	if config.MaxOpenConns != 10 {
		t.Fatalf("expected MaxOpenConns 10, got %d", config.MaxOpenConns)
	}
	if config.MaxIdleConns != 5 {
		t.Fatalf("expected MaxIdleConns 5, got %d", config.MaxIdleConns)
	}
}

func TestApplyConfigMaxOpenConns(t *testing.T) {
	conn := &stubConn{}
	connector := &stubConnector{conn: conn}
	db := sql.OpenDB(connector)
	defer db.Close()

	ApplyConfig(db, Config{MaxOpenConns: 4, MaxIdleConns: 10})
	stats := db.Stats()
	if stats.MaxOpenConnections != 4 {
		t.Fatalf("expected MaxOpenConnections 4, got %d", stats.MaxOpenConnections)
	}
}

func TestApplyConfigResetsPoolLimitsToZero(t *testing.T) {
	conn := &stubConn{}
	connector := &stubConnector{conn: conn}
	db := sql.OpenDB(connector)
	defer db.Close()

	ApplyConfig(db, Config{MaxOpenConns: 4, MaxIdleConns: 2})
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("PingContext: %v", err)
	}
	if stats := db.Stats(); stats.MaxOpenConnections != 4 || stats.Idle == 0 {
		t.Fatalf("expected configured pool with idle connection, got max open %d idle %d", stats.MaxOpenConnections, stats.Idle)
	}

	ApplyConfig(db, Config{MaxOpenConns: 0, MaxIdleConns: 0})
	stats := db.Stats()
	if stats.MaxOpenConnections != 0 {
		t.Fatalf("expected MaxOpenConnections reset to 0, got %d", stats.MaxOpenConnections)
	}
	if stats.Idle != 0 {
		t.Fatalf("expected idle connections closed after MaxIdleConns reset, got %d", stats.Idle)
	}
}

func TestOpenWithDoesNotPingDuringOpen(t *testing.T) {
	conn := &stubConn{}
	connector := &stubConnector{conn: conn}

	db, err := OpenWith(Config{
		Driver: "stub",
		DSN:    "dsn",
	}, func(driver, dsn string) (*sql.DB, error) {
		return sql.OpenDB(connector), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer db.Close()
	if got := conn.pingCount; got != 0 {
		t.Fatalf("expected open to avoid implicit ping, got %d ping calls", got)
	}
}

func TestOpenWithNilDatabase(t *testing.T) {
	_, err := OpenWith(Config{Driver: "stub", DSN: "dsn"}, func(driver, dsn string) (*sql.DB, error) {
		return nil, nil
	})
	if err == nil || !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("expected ErrConnectionFailed, got %v", err)
	}
}

func TestExecContext(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	_, err := ExecContext(ctx, db, "INSERT INTO test VALUES (?)", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecContextNilDB(t *testing.T) {
	_, err := ExecContext(t.Context(), nil, "INSERT INTO test VALUES (?)", 1)
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

func TestExecContextUsesCallerContext(t *testing.T) {
	ctx := context.WithValue(t.Context(), testContextKey{}, "request")
	db := &contextRecorderDB{}

	if _, err := ExecContext(ctx, db, "INSERT INTO test VALUES (?)", 1); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	if db.execCtx != ctx {
		t.Fatal("expected ExecContext to receive caller context")
	}
}

func TestExecContextWrapsUnderlyingError(t *testing.T) {
	execErr := errors.New("exec failed")
	db := &contextRecorderDB{execErr: execErr}

	_, err := ExecContext(t.Context(), db, "INSERT INTO test VALUES (?)", 1)
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
	if !errors.Is(err, execErr) {
		t.Fatalf("expected exec error in chain, got %v", err)
	}
}

func TestQueryContext(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	rows, err := QueryContext(ctx, db, "SELECT * FROM test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows == nil {
		t.Fatal("expected rows")
	}
	rows.Close()
}

func TestQueryContextNilDB(t *testing.T) {
	_, err := QueryContext(t.Context(), nil, "SELECT * FROM test")
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

func TestQueryContextUsesCallerContext(t *testing.T) {
	ctx := context.WithValue(t.Context(), testContextKey{}, "request")
	queryErr := errors.New("query failed")
	db := &contextRecorderDB{queryErr: queryErr}

	if _, err := QueryContext(ctx, db, "SELECT * FROM test"); !errors.Is(err, queryErr) {
		t.Fatalf("expected query error in chain, got %v", err)
	}
	if db.queryCtx != ctx {
		t.Fatal("expected QueryContext to receive caller context")
	}
}

func TestQueryContextNilRows(t *testing.T) {
	db := &contextRecorderDB{}

	rows, err := QueryContext(t.Context(), db, "SELECT * FROM test")
	if rows != nil {
		t.Fatalf("expected nil rows, got %v", rows)
	}
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

func TestQueryContextWrapsUnderlyingError(t *testing.T) {
	queryErr := errors.New("query failed")
	db := &contextRecorderDB{queryErr: queryErr}

	_, err := QueryContext(t.Context(), db, "SELECT * FROM test")
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
	if !errors.Is(err, queryErr) {
		t.Fatalf("expected query error in chain, got %v", err)
	}
}

func TestQueryRowContext(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	row, err := QueryRowContext(ctx, db, "SELECT * FROM test WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row == nil {
		t.Fatal("expected row")
	}
}

func TestQueryRowContextNilDB(t *testing.T) {
	row, err := QueryRowContext(t.Context(), nil, "SELECT * FROM test WHERE id = ?", 1)
	if row != nil {
		t.Fatal("expected nil row")
	}
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

func TestQueryRowContextUsesCallerContext(t *testing.T) {
	ctx := context.WithValue(t.Context(), testContextKey{}, "request")
	db := &contextRecorderDB{}

	row, err := QueryRowContext(ctx, db, "SELECT * FROM test WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("QueryRowContext: %v", err)
	}
	if row == nil {
		t.Fatal("expected row")
	}
	if db.queryRowCtx != ctx {
		t.Fatal("expected QueryRowContext to receive caller context")
	}
}

func TestWithTransaction(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	err := WithTransaction(ctx, db, nil, func(tx *sql.Tx) error {
		// Simulate transaction work
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithTransactionNilDB(t *testing.T) {
	err := WithTransaction(t.Context(), nil, nil, func(tx *sql.Tx) error {
		return nil
	})
	if err == nil || !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed, got %v", err)
	}
}

func TestWithTransactionNilFunction(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	err := WithTransaction(t.Context(), db, nil, nil)
	if err == nil || !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed, got %v", err)
	}
}

func TestWithTransactionError(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	txErr := errors.New("transaction error")
	err := WithTransaction(ctx, db, nil, func(tx *sql.Tx) error {
		return txErr
	})
	if err == nil || !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed, got %v", err)
	}
	if !errors.Is(err, txErr) {
		t.Fatalf("expected transaction error in chain, got %v", err)
	}
}

func TestWithTransactionRollbackError(t *testing.T) {
	txErr := errors.New("transaction error")
	rollbackErr := errors.New("rollback failed")
	connector := &stubConnector{conn: &stubConn{tx: stubTx{rollbackErr: rollbackErr}}}
	db := sql.OpenDB(connector)
	defer db.Close()

	err := WithTransaction(t.Context(), db, nil, func(tx *sql.Tx) error {
		return txErr
	})
	if err == nil || !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed, got %v", err)
	}
	if !errors.Is(err, txErr) {
		t.Fatalf("expected transaction error in chain, got %v", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("expected rollback error in chain, got %v", err)
	}
}

func TestWithTransactionUsesCallerContext(t *testing.T) {
	ctx := context.WithValue(t.Context(), testContextKey{}, "request")
	beginErr := errors.New("begin failed")
	db := &contextRecorderDB{beginErr: beginErr}

	err := WithTransaction(ctx, db, nil, func(tx *sql.Tx) error {
		t.Fatal("function should not run when begin fails")
		return nil
	})
	if err == nil || !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed, got %v", err)
	}
	if db.beginCtx != ctx {
		t.Fatal("expected BeginTx to receive caller context")
	}
}

func TestWithTransactionNilTransaction(t *testing.T) {
	db := &contextRecorderDB{}

	err := WithTransaction(t.Context(), db, nil, func(tx *sql.Tx) error {
		t.Fatal("function should not run when transaction is nil")
		return nil
	})
	if err == nil || !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed, got %v", err)
	}
}

func TestScanRow(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	row, err := QueryRowContext(ctx, db, "SELECT * FROM test WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("QueryRowContext: %v", err)
	}

	var id int
	err = ScanRow(row, &id)
	// The stub returns no rows, so we expect ErrNoRows
	if err != nil && !errors.Is(err, ErrNoRows) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScanRowNil(t *testing.T) {
	err := ScanRow(nil, nil)
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

func TestScanRows(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	rows, err := QueryContext(ctx, db, "SELECT * FROM test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	scanFunc := func(rows *sql.Rows) (int, error) {
		var id int
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}

	results, err := ScanRows(rows, scanFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The stub returns no rows, so results should be nil or empty slice
	// Since rows.Next() returns false immediately, results will be nil
	if results == nil {
		// This is expected when no rows are returned
		return
	}
	// Empty slice is also acceptable
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestScanRowsNil(t *testing.T) {
	_, err := ScanRows(nil, func(rows *sql.Rows) (int, error) {
		return 0, nil
	})
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

func TestScanRowsNilScanFunc(t *testing.T) {
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:   []string{"id"},
		values: [][]driver.Value{{1}},
	}})
	defer db.Close()

	rows, err := QueryContext(t.Context(), db, "SELECT id FROM test")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	_, err = ScanRows[int](rows, nil)
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

func TestScanRowsCloseError(t *testing.T) {
	closeErr := errors.New("close rows failed")
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:     []string{"id"},
		values:   [][]driver.Value{{1}},
		closeErr: closeErr,
	}})
	defer db.Close()

	rows, err := QueryContext(t.Context(), db, "SELECT id FROM test")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	_, err = ScanRows(rows, func(rows *sql.Rows) (int, error) {
		var id int
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	})
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("expected close error in chain, got %v", err)
	}
}

func TestPing(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	err := Ping(ctx, db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPingNilDB(t *testing.T) {
	err := Ping(t.Context(), nil)
	if err == nil || !errors.Is(err, ErrPingFailed) {
		t.Fatalf("expected ErrPingFailed, got %v", err)
	}
}

func TestPingUsesCallerTimeout(t *testing.T) {
	pingErr := errors.New("ping failed")
	connector := &stubConnector{conn: &stubConn{pingErr: pingErr}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	err := Ping(ctx, db)
	if err == nil || !errors.Is(err, ErrPingFailed) {
		t.Fatalf("expected ErrPingFailed, got %v", err)
	}
	if !errors.Is(err, pingErr) {
		t.Fatalf("expected ping error in chain, got %v", err)
	}
}

func TestQueryRow(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	ctx := t.Context()
	row, err := QueryRow(ctx, db, "SELECT * FROM test WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row == nil {
		t.Fatal("expected row")
	}
}

func TestQueryRowNilDB(t *testing.T) {
	_, err := QueryRow(t.Context(), nil, "SELECT * FROM test WHERE id = ?", 1)
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

func TestQueryRowUsesCallerContext(t *testing.T) {
	ctx := context.WithValue(t.Context(), testContextKey{}, "request")
	db := &contextRecorderDB{}

	row, err := QueryRow(ctx, db, "SELECT * FROM test WHERE id = ?", 1)
	if err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if row == nil {
		t.Fatal("expected row")
	}
	if db.queryRowCtx != ctx {
		t.Fatal("expected QueryRow to receive caller context")
	}
}

func TestQueryRowStrictNoRows(t *testing.T) {
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{cols: []string{"id"}}})
	defer db.Close()

	err := QueryRowStrict(t.Context(), db, "SELECT id FROM test", func(rows *sql.Rows) error {
		var id int
		return rows.Scan(&id)
	})
	if err == nil || !errors.Is(err, ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}

func TestQueryRowStrictMultipleRows(t *testing.T) {
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:   []string{"id"},
		values: [][]driver.Value{{1}, {2}},
	}})
	defer db.Close()

	err := QueryRowStrict(t.Context(), db, "SELECT id FROM test", func(rows *sql.Rows) error {
		var id int
		return rows.Scan(&id)
	})
	if err == nil || !errors.Is(err, ErrMultipleRows) {
		t.Fatalf("expected ErrMultipleRows, got %v", err)
	}
}

func TestQueryRowStrictCloseError(t *testing.T) {
	closeErr := errors.New("close rows failed")
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:     []string{"id"},
		values:   [][]driver.Value{{1}},
		closeErr: closeErr,
	}})
	defer db.Close()

	err := QueryRowStrict(t.Context(), db, "SELECT id FROM test", func(rows *sql.Rows) error {
		var id int
		return rows.Scan(&id)
	})
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("expected close error in chain, got %v", err)
	}
}

// Stub implementations for testing
type stubConnector struct {
	conn *stubConn
}

func (c *stubConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.conn, nil
}

func (c *stubConnector) Driver() driver.Driver {
	return stubDriver{}
}

type stubDriver struct{}

func (d stubDriver) Open(name string) (driver.Conn, error) {
	return nil, errors.New("not supported")
}

type stubConn struct {
	pingErr   error
	pingCount int
	tx        driver.Tx
}

func (c *stubConn) Prepare(query string) (driver.Stmt, error) {
	return stubStmt{}, nil
}

func (c *stubConn) Close() error {
	return nil
}

func (c *stubConn) Begin() (driver.Tx, error) {
	if c.tx != nil {
		return c.tx, nil
	}
	return stubTx{}, nil
}

func (c *stubConn) Ping(ctx context.Context) error {
	c.pingCount++
	// Add a small delay to ensure measurable latency in tests
	time.Sleep(1 * time.Millisecond)
	return c.pingErr
}

type stubStmt struct{}

func (s stubStmt) Close() error {
	return nil
}

func (s stubStmt) NumInput() int {
	return -1
}

func (s stubStmt) Exec(args []driver.Value) (driver.Result, error) {
	return stubResult{}, nil
}

func (s stubStmt) Query(args []driver.Value) (driver.Rows, error) {
	return stubRows{}, nil
}

type stubTx struct {
	commitErr   error
	rollbackErr error
}

func (t stubTx) Commit() error {
	return t.commitErr
}

func (t stubTx) Rollback() error {
	return t.rollbackErr
}

type stubResult struct{}

func (r stubResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r stubResult) RowsAffected() (int64, error) {
	return 0, nil
}

type stubRows struct{}

func (r stubRows) Columns() []string {
	return nil
}

func (r stubRows) Close() error {
	return nil
}

func (r stubRows) Next(dest []driver.Value) error {
	return io.EOF
}

type rowsConnector struct {
	rows driver.Rows
}

func (c *rowsConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return &rowsConn{rows: c.rows}, nil
}

func (c *rowsConnector) Driver() driver.Driver {
	return rowsDriver{}
}

type rowsDriver struct{}

func (d rowsDriver) Open(name string) (driver.Conn, error) {
	return nil, errors.New("not supported")
}

type rowsConn struct {
	rows driver.Rows
}

func (c *rowsConn) Prepare(query string) (driver.Stmt, error) {
	return rowsStmt{rows: c.rows}, nil
}

func (c *rowsConn) Close() error {
	return nil
}

func (c *rowsConn) Begin() (driver.Tx, error) {
	return stubTx{}, nil
}

func (c *rowsConn) Ping(ctx context.Context) error {
	return nil
}

type rowsStmt struct {
	rows driver.Rows
}

func (s rowsStmt) Close() error {
	return nil
}

func (s rowsStmt) NumInput() int {
	return -1
}

func (s rowsStmt) Exec(args []driver.Value) (driver.Result, error) {
	return stubResult{}, nil
}

func (s rowsStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.rows, nil
}

type fixedRows struct {
	cols     []string
	values   [][]driver.Value
	idx      int
	closeErr error
}

func (r *fixedRows) Columns() []string {
	return r.cols
}

func (r *fixedRows) Close() error {
	return r.closeErr
}

func (r *fixedRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.idx])
	r.idx++
	return nil
}

// ---------------------------------------------------------------------------
// Additional coverage tests
// ---------------------------------------------------------------------------

// TestOpenWithInvalidConfig verifies that OpenWith rejects a bad config early.
func TestOpenWithInvalidConfig(t *testing.T) {
	_, err := OpenWith(Config{}, func(driver, dsn string) (*sql.DB, error) {
		t.Fatal("open function should not be called for invalid config")
		return nil, nil
	})
	if err == nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

// TestOpenWithNilOpenFunc verifies that OpenWith rejects a nil open function.
func TestOpenWithNilOpenFunc(t *testing.T) {
	_, err := OpenWith(Config{Driver: "stub", DSN: "dsn"}, nil)
	if err == nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for nil open func, got %v", err)
	}
}

// TestOpenWithOpenError verifies that an error from the open function is wrapped.
func TestOpenWithOpenError(t *testing.T) {
	openErr := errors.New("open failed")
	_, err := OpenWith(Config{Driver: "stub", DSN: "dsn"}, func(driver, dsn string) (*sql.DB, error) {
		return nil, openErr
	})
	if err == nil || !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("expected ErrConnectionFailed, got %v", err)
	}
	if !errors.Is(err, openErr) {
		t.Fatalf("expected open error in chain, got %v", err)
	}
}

// TestOpenWithInvalidConfigReturnsErrInvalidConfig exercises Open through the
// validation failure path.  Open calls OpenWith(config, sql.Open); an invalid
// config must return ErrInvalidConfig before sql.Open is ever called.
func TestOpenWithInvalidConfigReturnsErrInvalidConfig(t *testing.T) {
	_, err := Open(Config{}) // missing Driver and DSN
	if err == nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Open with empty config: expected ErrInvalidConfig, got %v", err)
	}
}

// TestOpenUnknownDriverReturnsErrConnectionFailed exercises Open with a valid
// config but an unregistered driver, so sql.Open returns an error that Open
// wraps as ErrConnectionFailed.
func TestOpenUnknownDriverReturnsErrConnectionFailed(t *testing.T) {
	_, err := Open(Config{
		Driver: "unknown-driver-that-does-not-exist-xyz",
		DSN:    "whatever",
	})
	if err == nil || !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("Open with unknown driver: expected ErrConnectionFailed, got %v", err)
	}
}

// TestConfigValidateNegativeMaxIdleConns covers the negative MaxIdleConns branch.
func TestConfigValidateNegativeMaxIdleConns(t *testing.T) {
	cfg := Config{Driver: "d", DSN: "dsn", MaxIdleConns: -1}
	if err := cfg.Validate(); err == nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for negative MaxIdleConns, got %v", err)
	}
}

// TestConfigValidateNegativeConnMaxLifetime covers the negative ConnMaxLifetime branch.
func TestConfigValidateNegativeConnMaxLifetime(t *testing.T) {
	cfg := Config{Driver: "d", DSN: "dsn", ConnMaxLifetime: -time.Second}
	if err := cfg.Validate(); err == nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for negative ConnMaxLifetime, got %v", err)
	}
}

// TestConfigValidateNegativeConnMaxIdleTime covers the negative ConnMaxIdleTime branch.
func TestConfigValidateNegativeConnMaxIdleTime(t *testing.T) {
	cfg := Config{Driver: "d", DSN: "dsn", ConnMaxIdleTime: -time.Second}
	if err := cfg.Validate(); err == nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for negative ConnMaxIdleTime, got %v", err)
	}
}

// TestJoinRowsCloseErrorBothNonNil covers the path where both err and closeErr are non-nil.
func TestJoinRowsCloseErrorBothNonNil(t *testing.T) {
	rowErr := errors.New("row error")
	closeErr := errors.New("close error")
	joined := joinRowsCloseError(rowErr, closeErr)
	if joined == nil {
		t.Fatal("expected non-nil joined error")
	}
	if !errors.Is(joined, rowErr) {
		t.Fatalf("expected row error in chain, got %v", joined)
	}
	if !errors.Is(joined, closeErr) {
		t.Fatalf("expected close error in chain, got %v", joined)
	}
	if !errors.Is(joined, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed in chain, got %v", joined)
	}
}

// TestJoinRowsCloseErrorOnlyCloseErr covers the path where only closeErr is non-nil.
func TestJoinRowsCloseErrorOnlyCloseErr(t *testing.T) {
	closeErr := errors.New("close error")
	joined := joinRowsCloseError(nil, closeErr)
	if joined == nil {
		t.Fatal("expected non-nil joined error")
	}
	if !errors.Is(joined, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed in chain, got %v", joined)
	}
	if !errors.Is(joined, closeErr) {
		t.Fatalf("expected close error in chain, got %v", joined)
	}
}

// TestQueryRowStrictNilDB covers the nil-DB path (0% branch in QueryRowStrict).
func TestQueryRowStrictNilDB(t *testing.T) {
	err := QueryRowStrict(t.Context(), nil, "SELECT 1", func(rows *sql.Rows) error {
		return nil
	})
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed for nil db, got %v", err)
	}
}

// TestQueryRowStrictNilScanFunc covers the nil-scan-function path.
func TestQueryRowStrictNilScanFunc(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	err := QueryRowStrict(t.Context(), db, "SELECT 1", nil)
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed for nil scan func, got %v", err)
	}
}

// TestQueryRowStrictSingleRowSuccess exercises the happy-path single-row scan.
func TestQueryRowStrictSingleRowSuccess(t *testing.T) {
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:   []string{"id"},
		values: [][]driver.Value{{42}},
	}})
	defer db.Close()

	var got int64
	err := QueryRowStrict(t.Context(), db, "SELECT id FROM test", func(rows *sql.Rows) error {
		return rows.Scan(&got)
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

// TestQueryRowContextNilRowReturned exercises the nil-row guard in QueryRowContext.
// contextRecorderDB.QueryRowContext returns a non-nil *sql.Row by construction, so
// we need a DB that returns a nil *sql.Row to reach the nil-row branch.
func TestQueryRowContextNilRowReturned(t *testing.T) {
	db := &nilRowDB{}
	_, err := QueryRowContext(t.Context(), db, "SELECT 1")
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed for nil row, got %v", err)
	}
}

// TestScanRowNoRows exercises the sql.ErrNoRows path inside ScanRow.
func TestScanRowNoRows(t *testing.T) {
	// A query against the stub returns no rows, so QueryRowContext returns a
	// *sql.Row that will produce sql.ErrNoRows on Scan.
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	row, err := QueryRowContext(t.Context(), db, "SELECT id FROM test WHERE id = ?", 999)
	if err != nil {
		t.Fatalf("QueryRowContext: %v", err)
	}

	var id int
	err = ScanRow(row, &id)
	if err == nil || !errors.Is(err, ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}

// TestApplyConfigNilDB ensures ApplyConfig with nil db is a no-op (covers nil guard).
func TestApplyConfigNilDB(t *testing.T) {
	// Applying configuration to a nil DB is a safe no-op: it must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ApplyConfig with nil DB panicked: %v", r)
		}
	}()
	ApplyConfig(nil, Config{MaxOpenConns: 5})
}

// TestWithTransactionPanic verifies that the defer/recover inside
// WithTransaction re-panics after rolling back the transaction.
func TestWithTransactionPanic(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate")
		}
		if r != "test panic" {
			t.Fatalf("expected 'test panic', got %v", r)
		}
	}()

	_ = WithTransaction(t.Context(), db, nil, func(tx *sql.Tx) error {
		panic("test panic")
	})
}

// TestWithTransactionCommitError exercises the commit-failure path.
func TestWithTransactionCommitError(t *testing.T) {
	commitErr := errors.New("commit failed")
	connector := &stubConnector{conn: &stubConn{tx: stubTx{commitErr: commitErr}}}
	db := sql.OpenDB(connector)
	defer db.Close()

	err := WithTransaction(t.Context(), db, nil, func(tx *sql.Tx) error {
		return nil // success — triggers commit
	})
	if err == nil || !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed on commit error, got %v", err)
	}
	if !errors.Is(err, commitErr) {
		t.Fatalf("expected commit error in chain, got %v", err)
	}
}

// TestScanRowNonNoRowsError exercises the non-ErrNoRows scan error path in ScanRow.
func TestScanRowNonNoRowsError(t *testing.T) {
	// Query a row with mismatched destination type to produce a non-ErrNoRows scan error.
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:   []string{"id"},
		values: [][]driver.Value{{"not-an-int"}},
	}})
	defer db.Close()

	row, err := QueryRowContext(t.Context(), db, "SELECT id FROM test")
	if err != nil {
		t.Fatalf("QueryRowContext: %v", err)
	}

	var id int
	err = ScanRow(row, &id)
	// Scanning a string into an int should return a scan error (not ErrNoRows).
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}
	if errors.Is(err, ErrNoRows) {
		t.Fatalf("expected non-ErrNoRows error, got ErrNoRows")
	}
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

// TestQueryRowStrictScanError exercises the scan-function-error path in QueryRowStrict.
func TestQueryRowStrictScanError(t *testing.T) {
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:   []string{"id"},
		values: [][]driver.Value{{42}},
	}})
	defer db.Close()

	scanErr := errors.New("scan failed")
	err := QueryRowStrict(t.Context(), db, "SELECT id FROM test", func(rows *sql.Rows) error {
		return scanErr
	})
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
	if !errors.Is(err, scanErr) {
		t.Fatalf("expected scanErr in chain, got %v", err)
	}
}

// TestScanRowSuccess exercises the happy-path return nil in ScanRow.
func TestScanRowSuccess(t *testing.T) {
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:   []string{"id"},
		values: [][]driver.Value{{int64(7)}},
	}})
	defer db.Close()

	row, err := QueryRowContext(t.Context(), db, "SELECT id FROM test")
	if err != nil {
		t.Fatalf("QueryRowContext: %v", err)
	}

	var id int64
	if err := ScanRow(row, &id); err != nil {
		t.Fatalf("ScanRow: %v", err)
	}
	if id != 7 {
		t.Fatalf("expected id=7, got %d", id)
	}
}

// TestScanRowsWithScanError exercises the scan-function-error path in ScanRows.
func TestScanRowsWithScanError(t *testing.T) {
	db := sql.OpenDB(&rowsConnector{rows: &fixedRows{
		cols:   []string{"id"},
		values: [][]driver.Value{{int64(1)}, {int64(2)}},
	}})
	defer db.Close()

	rows, err := QueryContext(t.Context(), db, "SELECT id FROM test")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}

	scanErr := errors.New("scan failed during iteration")
	_, err = ScanRows(rows, func(rows *sql.Rows) (int, error) {
		return 0, scanErr
	})
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
	if !errors.Is(err, scanErr) {
		t.Fatalf("expected scanErr in chain, got %v", err)
	}
}

// TestQueryRowStrictQueryContextError exercises the path in QueryRowStrict
// where QueryContext itself fails (lines 292-294).
func TestQueryRowStrictQueryContextError(t *testing.T) {
	queryErr := errors.New("query failed")
	db := &contextRecorderDB{queryErr: queryErr}

	err := QueryRowStrict(t.Context(), db, "SELECT id FROM test", func(rows *sql.Rows) error {
		return nil
	})
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

// TestQueryRowStrictRowsErrOnEmpty exercises the path where rows.Err() returns
// non-nil when rows.Next() is false (lines 300-302 in QueryRowStrict).
// This is hard to trigger with standard sql drivers since rows.Err() is
// typically nil after iteration. We cover the reachable part with a custom rows.
func TestQueryRowStrictRowsErrOnEmpty(t *testing.T) {
	rowErr := errors.New("rows iteration error")
	db := sql.OpenDB(&rowsConnector{rows: &errorOnNextRows{err: rowErr}})
	defer db.Close()

	err := QueryRowStrict(t.Context(), db, "SELECT id FROM test", func(rows *sql.Rows) error {
		return nil
	})
	if err == nil || !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("expected ErrQueryFailed, got %v", err)
	}
}

// errorOnNextRows is a driver.Rows that returns an error from Next.
type errorOnNextRows struct {
	err error
}

func (r *errorOnNextRows) Columns() []string { return []string{"id"} }
func (r *errorOnNextRows) Close() error      { return nil }
func (r *errorOnNextRows) Next(dest []driver.Value) error {
	return r.err
}

// nilRowDB is a DB implementation whose QueryRowContext always returns nil.
type nilRowDB struct{}

func (db *nilRowDB) ExecContext(ctx context.Context, _ string, _ ...any) (sql.Result, error) {
	return stubResult{}, nil
}
func (db *nilRowDB) QueryContext(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (db *nilRowDB) QueryRowContext(_ context.Context, _ string, _ ...any) *sql.Row {
	return nil
}
func (db *nilRowDB) BeginTx(_ context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	return nil, nil
}
func (db *nilRowDB) PingContext(context.Context) error { return nil }
func (db *nilRowDB) Close() error                      { return nil }

type testContextKey struct{}

type contextRecorderDB struct {
	execCtx     context.Context
	queryCtx    context.Context
	queryRowCtx context.Context
	beginCtx    context.Context
	execErr     error
	queryErr    error
	beginErr    error
	pingErr     error
}

func (db *contextRecorderDB) ExecContext(ctx context.Context, _ string, _ ...any) (sql.Result, error) {
	db.execCtx = ctx
	return stubResult{}, db.execErr
}

func (db *contextRecorderDB) QueryContext(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	db.queryCtx = ctx
	return nil, db.queryErr
}

func (db *contextRecorderDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	db.queryRowCtx = ctx
	return &sql.Row{}
}

func (db *contextRecorderDB) BeginTx(ctx context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	db.beginCtx = ctx
	return nil, db.beginErr
}

func (db *contextRecorderDB) PingContext(context.Context) error {
	return db.pingErr
}

func (db *contextRecorderDB) Close() error {
	return nil
}
