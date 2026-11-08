// Package sqlx defines a small explicit database/sql query and transaction
// surface for x/data callers.
package sqlx

import (
	"context"
	"database/sql"
	"fmt"

	storedb "github.com/birdordev/plumego/store/db"
)

// Querier is the context-aware query surface exposed by DB and Tx.
type Querier interface {
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Transactor starts SQL transactions.
type Transactor interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error)
}

// Tx is a SQL transaction with the same query surface as DB.
type Tx interface {
	Querier
	Commit() error
	Rollback() error
}

// DB wraps a database/sql DB and exposes explicit query and transaction methods.
type DB struct {
	db *sql.DB
}

var (
	_ Querier    = (*DB)(nil)
	_ Transactor = (*DB)(nil)
	_ Tx         = (*tx)(nil)
)

// New opens a SQL database and verifies connectivity with Ping.
func New(driverName, dataSourceName string) (*DB, error) {
	opened, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("%w: open sql database: %w", storedb.ErrConnectionFailed, err)
	}
	if err := opened.Ping(); err != nil {
		_ = opened.Close()
		return nil, fmt.Errorf("%w: ping sql database: %w", storedb.ErrConnectionFailed, err)
	}
	return NewWithDB(opened), nil
}

// NewWithDB wraps an existing database/sql DB.
func NewWithDB(db *sql.DB) *DB {
	return &DB{db: db}
}

// SQL returns the wrapped database/sql DB.
func (d *DB) SQL() *sql.DB {
	if d == nil {
		return nil
	}
	return d.db
}

// QueryRow executes a query expected to return at most one row.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.QueryRowContext(ctx, query, args...)
}

// Query executes a query returning rows.
func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if d == nil || d.db == nil {
		return nil, fmt.Errorf("%w: sql database is nil", storedb.ErrQueryFailed)
	}
	return d.db.QueryContext(ctx, query, args...)
}

// Exec executes a statement.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if d == nil || d.db == nil {
		return nil, fmt.Errorf("%w: sql database is nil", storedb.ErrQueryFailed)
	}
	return d.db.ExecContext(ctx, query, args...)
}

// BeginTx begins a SQL transaction.
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error) {
	if d == nil || d.db == nil {
		return nil, fmt.Errorf("%w: sql database is nil", storedb.ErrTransactionFailed)
	}
	started, err := d.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("%w: begin sql transaction: %w", storedb.ErrTransactionFailed, err)
	}
	return &tx{tx: started}, nil
}

// Close closes the wrapped database.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

type tx struct {
	tx *sql.Tx
}

func (t *tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if t == nil || t.tx == nil {
		return nil
	}
	return t.tx.QueryRowContext(ctx, query, args...)
}

func (t *tx) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if t == nil || t.tx == nil {
		return nil, fmt.Errorf("%w: sql transaction is nil", storedb.ErrQueryFailed)
	}
	return t.tx.QueryContext(ctx, query, args...)
}

func (t *tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if t == nil || t.tx == nil {
		return nil, fmt.Errorf("%w: sql transaction is nil", storedb.ErrQueryFailed)
	}
	return t.tx.ExecContext(ctx, query, args...)
}

func (t *tx) Commit() error {
	if t == nil || t.tx == nil {
		return fmt.Errorf("%w: sql transaction is nil", storedb.ErrTransactionFailed)
	}
	return t.tx.Commit()
}

func (t *tx) Rollback() error {
	if t == nil || t.tx == nil {
		return fmt.Errorf("%w: sql transaction is nil", storedb.ErrTransactionFailed)
	}
	return t.tx.Rollback()
}
