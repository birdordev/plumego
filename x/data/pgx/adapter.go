// Package pgx defines a small explicit PostgreSQL-style query and transaction
// surface for x/data callers without importing a concrete driver.
package pgx

import (
	"context"
	"errors"
	"fmt"

	storedb "github.com/birdordev/plumego/store/db"
)

var ErrPoolNil = errors.New("pgx pool is nil")

// Row is the minimal scan surface returned by QueryRow.
type Row interface {
	Scan(...any) error
}

// Rows is the minimal cursor surface returned by Query.
type Rows interface {
	Close()
	Err() error
	Next() bool
	Scan(...any) error
}

// CommandTag reports affected row count for Exec.
type CommandTag interface {
	RowsAffected() int64
}

// TxOptions is an opaque caller-owned transaction option value.
type TxOptions any

// Querier is the query surface exposed by DB and Tx.
type Querier interface {
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
	Exec(ctx context.Context, query string, args ...any) (CommandTag, error)
}

// Transactor starts transactions.
type Transactor interface {
	BeginTx(ctx context.Context, opts TxOptions) (Tx, error)
}

// Tx is a transaction with the same query surface as DB.
type Tx interface {
	Querier
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Pool is the caller-owned database pool surface wrapped by DB.
type Pool interface {
	Querier
	BeginTx(ctx context.Context, opts TxOptions) (Tx, error)
	Ping(ctx context.Context) error
	Close()
}

// DB wraps a caller-provided pool and exposes explicit query and transaction methods.
type DB struct {
	pool Pool
}

var (
	_ Querier    = (*DB)(nil)
	_ Transactor = (*DB)(nil)
)

// New wraps a caller-owned pool and verifies connectivity with Ping.
func New(ctx context.Context, pool Pool) (*DB, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: %w", storedb.ErrInvalidConfig, ErrPoolNil)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("%w: ping pool: %w", storedb.ErrConnectionFailed, err)
	}
	return NewWithPool(pool), nil
}

// NewWithPool wraps an existing pool without pinging it.
func NewWithPool(pool Pool) *DB {
	return &DB{pool: pool}
}

// Pool returns the wrapped pool.
func (d *DB) Pool() Pool {
	if d == nil {
		return nil
	}
	return d.pool
}

// QueryRow executes a query expected to return at most one row.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) Row {
	if d == nil || d.pool == nil {
		return rowError{err: fmt.Errorf("%w: pgx pool is nil", storedb.ErrQueryFailed)}
	}
	return d.pool.QueryRow(ctx, query, args...)
}

// Query executes a query returning rows.
func (d *DB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	if d == nil || d.pool == nil {
		return nil, fmt.Errorf("%w: pgx pool is nil", storedb.ErrQueryFailed)
	}
	return d.pool.Query(ctx, query, args...)
}

// Exec executes a statement and returns a command tag.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (CommandTag, error) {
	if d == nil || d.pool == nil {
		return nil, fmt.Errorf("%w: pgx pool is nil", storedb.ErrQueryFailed)
	}
	return d.pool.Exec(ctx, query, args...)
}

// BeginTx begins a transaction.
func (d *DB) BeginTx(ctx context.Context, opts TxOptions) (Tx, error) {
	if d == nil || d.pool == nil {
		return nil, fmt.Errorf("%w: pgx pool is nil", storedb.ErrTransactionFailed)
	}
	return d.pool.BeginTx(ctx, opts)
}

// Close closes the wrapped pool.
func (d *DB) Close() error {
	if d == nil || d.pool == nil {
		return nil
	}
	d.pool.Close()
	return nil
}

type rowError struct {
	err error
}

func (r rowError) Scan(...any) error {
	return r.err
}
