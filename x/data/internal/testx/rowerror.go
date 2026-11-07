// Package testx provides shared test utilities for x/data subpackages.
package testx

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

// QueryRowError returns a *sql.Row that will yield the given error when scanned.
// It constructs a minimal sql.DB backed by a stub driver that always returns err.
func QueryRowError(err error) *sql.Row {
	db := sql.OpenDB(RowErrorConnector{Err: err})
	row := db.QueryRowContext(context.Background(), "")
	_ = db.Close()
	return row
}

// RowErrorConnector is a driver.Connector that returns stub connections.
type RowErrorConnector struct {
	Err error
}

// Connect returns a RowErrorConn configured with the stored error.
func (c RowErrorConnector) Connect(context.Context) (driver.Conn, error) {
	return RowErrorConn(c), nil
}

// Driver returns the RowErrorDriver singleton.
func (c RowErrorConnector) Driver() driver.Driver {
	return RowErrorDriver{}
}

// RowErrorDriver is a stub driver.Driver used for error-injection tests.
type RowErrorDriver struct{}

// Open returns a no-op connection.
func (RowErrorDriver) Open(string) (driver.Conn, error) {
	return RowErrorConn{}, nil
}

// RowErrorConn is a stub driver.Conn that returns the configured error
// from every operation except Close.
type RowErrorConn struct {
	Err error
}

// Prepare returns the configured error.
func (c RowErrorConn) Prepare(string) (driver.Stmt, error) {
	return nil, c.Err
}

// Close returns nil (the connection itself is successfully closed).
func (c RowErrorConn) Close() error {
	return nil
}

// Begin returns the configured error.
func (c RowErrorConn) Begin() (driver.Tx, error) {
	return nil, c.Err
}

// QueryContext returns the configured error.
func (c RowErrorConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, c.Err
}
