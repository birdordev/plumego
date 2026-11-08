// Package db provides small stdlib-shaped SQL helpers.
//
// This package wraps database/sql with connection setup, context-driven query
// and transaction helpers, and scan utilities. Topology, retry policy, schema
// ownership, and health or observability ownership belong outside the stable
// store root. Operation deadlines are caller-owned through context.Context.
//
// Example usage:
//
//	import (
//		"context"
//		"database/sql"
//
//		"github.com/birdordev/plumego/store/db"
//	)
//
//	func example(ctx context.Context) error {
//		cfg := db.DefaultConfig("postgres", "postgres://user:pass@localhost:5432/myapp?sslmode=disable")
//		database, err := db.Open(cfg)
//		if err != nil {
//			return err
//		}
//		defer database.Close()
//
//		rows, err := db.QueryContext(ctx, database, "SELECT id FROM users WHERE active = ?", true)
//		if err != nil {
//			return err
//		}
//		defer rows.Close()
//
//		return db.WithTransaction(ctx, database, nil, func(tx *sql.Tx) error {
//			_, err := tx.ExecContext(ctx, "UPDATE users SET last_seen_at = CURRENT_TIMESTAMP")
//			return err
//		})
//	}
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrInvalidConfig is returned when configuration is invalid
	ErrInvalidConfig = errors.New("db: invalid config")

	// ErrConnectionFailed is returned when connection fails
	ErrConnectionFailed = errors.New("db: connection failed")

	// ErrPingFailed is returned when ping fails
	ErrPingFailed = errors.New("db: ping failed")

	// ErrTransactionFailed is returned when transaction operations fail
	ErrTransactionFailed = errors.New("db: transaction failed")

	// ErrQueryFailed is returned when query execution fails
	ErrQueryFailed = errors.New("db: query failed")

	// ErrNoRows is returned when query returns no rows
	ErrNoRows = errors.New("db: no rows returned")

	// ErrMultipleRows is returned when query returns multiple rows
	ErrMultipleRows = errors.New("db: multiple rows returned")
)

// Config defines the parameters for opening and tuning a sql.DB.
type Config struct {
	// Driver is the database driver name (e.g., "mysql", "postgres", "sqlite3")
	Driver string

	// DSN is the data source name
	DSN string

	// MaxOpenConns is the maximum number of open connections to the database.
	// Zero leaves the connection count unlimited.
	MaxOpenConns int

	// MaxIdleConns is the maximum number of idle connections in the pool.
	// Zero retains no idle connections.
	MaxIdleConns int

	// ConnMaxLifetime is the maximum amount of time a connection may be reused.
	// Zero leaves reused connections without a maximum lifetime.
	ConnMaxLifetime time.Duration

	// ConnMaxIdleTime is the maximum amount of time a connection may be idle.
	// Zero leaves idle connections without a maximum idle time.
	ConnMaxIdleTime time.Duration
}

// DB defines the minimal behavior required by SQL consumers.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	PingContext(ctx context.Context) error
	Close() error
}

// OpenFunc allows swapping sql.Open for tests.
type OpenFunc func(driver, dsn string) (*sql.DB, error)

// Validate ensures the configuration is usable.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Driver) == "" {
		return fmt.Errorf("%w: driver is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(c.DSN) == "" {
		return fmt.Errorf("%w: dsn is required", ErrInvalidConfig)
	}
	if c.MaxOpenConns < 0 {
		return fmt.Errorf("%w: MaxOpenConns must be non-negative", ErrInvalidConfig)
	}
	if c.MaxIdleConns < 0 {
		return fmt.Errorf("%w: MaxIdleConns must be non-negative", ErrInvalidConfig)
	}
	if c.ConnMaxLifetime < 0 {
		return fmt.Errorf("%w: ConnMaxLifetime must be non-negative", ErrInvalidConfig)
	}
	if c.ConnMaxIdleTime < 0 {
		return fmt.Errorf("%w: ConnMaxIdleTime must be non-negative", ErrInvalidConfig)
	}
	return nil
}

// DefaultConfig returns a default configuration for common use cases.
func DefaultConfig(driver, dsn string) Config {
	return Config{
		Driver:          driver,
		DSN:             dsn,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

// Open opens a database connection using the standard sql.Open.
func Open(config Config) (*sql.DB, error) {
	return OpenWith(config, sql.Open)
}

// OpenWith opens a database connection with an injected opener.
func OpenWith(config Config, open OpenFunc) (*sql.DB, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if open == nil {
		return nil, fmt.Errorf("%w: open function is required", ErrInvalidConfig)
	}

	db, err := open(config.Driver, config.DSN)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConnectionFailed, err)
	}
	if db == nil {
		return nil, fmt.Errorf("%w: open function returned nil database", ErrConnectionFailed)
	}

	ApplyConfig(db, config)

	return db, nil
}

// ApplyConfig updates pooling and lifetime settings on an existing sql.DB.
// Zero values are applied because database/sql treats them as explicit reset or
// no-limit settings for these knobs.
func ApplyConfig(db *sql.DB, config Config) {
	if db == nil {
		return
	}

	maxOpen := config.MaxOpenConns
	maxIdle := config.MaxIdleConns
	if maxOpen > 0 && maxIdle > maxOpen {
		maxIdle = maxOpen
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)
}

// ExecContext executes a query using the caller-provided context.
func ExecContext(ctx context.Context, db DB, query string, args ...any) (sql.Result, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is nil", ErrQueryFailed)
	}

	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQueryFailed, err)
	}
	return result, nil
}

// QueryContext executes a query using the caller-provided context.
func QueryContext(ctx context.Context, db DB, query string, args ...any) (*sql.Rows, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is nil", ErrQueryFailed)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQueryFailed, err)
	}
	if rows == nil {
		return nil, fmt.Errorf("%w: query returned nil rows", ErrQueryFailed)
	}
	return rows, nil
}

// QueryRowContext executes a query using the caller-provided context.
func QueryRowContext(ctx context.Context, db DB, query string, args ...any) (*sql.Row, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is nil", ErrQueryFailed)
	}

	row := db.QueryRowContext(ctx, query, args...)
	if row == nil {
		return nil, fmt.Errorf("%w: query returned nil row", ErrQueryFailed)
	}
	return row, nil
}

// WithTransaction executes a function within a transaction.
// If the function returns an error, the transaction is rolled back.
// Otherwise, the transaction is committed.
// Panics and goroutine exits also roll back the transaction.
func WithTransaction(ctx context.Context, db DB, txOpts *sql.TxOptions, fn func(*sql.Tx) error) error {
	if db == nil {
		return fmt.Errorf("%w: database is nil", ErrTransactionFailed)
	}
	if fn == nil {
		return fmt.Errorf("%w: transaction function is nil", ErrTransactionFailed)
	}

	tx, err := db.BeginTx(ctx, txOpts)
	if err != nil {
		return fmt.Errorf("%w: begin transaction failed: %w", ErrTransactionFailed, err)
	}
	if tx == nil {
		return fmt.Errorf("%w: begin transaction returned nil transaction", ErrTransactionFailed)
	}

	// Rollback is a no-op after Commit or explicit Rollback. Keeping it
	// unconditional also covers runtime.Goexit, which does not raise a panic.
	defer func() {
		_ = tx.Rollback()
	}()

	err = fn(tx)
	if err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%w: %w", ErrTransactionFailed, errors.Join(err, fmt.Errorf("rollback failed: %w", rollbackErr)))
		}
		return fmt.Errorf("%w: %w", ErrTransactionFailed, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit failed: %w", ErrTransactionFailed, err)
	}

	return nil
}

// QueryRow executes a query and returns the first row.
// Use QueryRowStrict when you need single-row enforcement.
func QueryRow(ctx context.Context, db DB, query string, args ...any) (*sql.Row, error) {
	return QueryRowContext(ctx, db, query, args...)
}

// QueryRowStrict executes a query and enforces single-row semantics.
// Returns ErrNoRows if no rows are returned.
// Returns ErrMultipleRows if multiple rows are returned.
func QueryRowStrict(ctx context.Context, db DB, query string, scan func(*sql.Rows) error, args ...any) (err error) {
	if db == nil {
		return fmt.Errorf("%w: database is nil", ErrQueryFailed)
	}
	if scan == nil {
		return fmt.Errorf("%w: scan function is nil", ErrQueryFailed)
	}

	rows, err := QueryContext(ctx, db, query, args...)
	if err != nil {
		return err
	}
	defer func() {
		err = joinRowsCloseError(err, rows.Close())
	}()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrQueryFailed, err)
		}
		return fmt.Errorf("%w: %w", ErrNoRows, sql.ErrNoRows)
	}

	if err := scan(rows); err != nil {
		return fmt.Errorf("%w: %w", ErrQueryFailed, err)
	}

	if rows.Next() {
		return ErrMultipleRows
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrQueryFailed, err)
	}

	return nil
}

// ScanRow is a helper function to scan a single row into a destination.
func ScanRow(row *sql.Row, dest ...any) error {
	if row == nil {
		return fmt.Errorf("%w: row is nil", ErrQueryFailed)
	}

	err := row.Scan(dest...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %w", ErrNoRows, err)
		}
		return fmt.Errorf("%w: %w", ErrQueryFailed, err)
	}

	return nil
}

// ScanRows is a helper function to scan multiple rows into a slice.
func ScanRows[T any](rows *sql.Rows, scanFunc func(*sql.Rows) (T, error)) (results []T, err error) {
	if rows == nil {
		return nil, fmt.Errorf("%w: rows is nil", ErrQueryFailed)
	}
	if scanFunc == nil {
		return nil, fmt.Errorf("%w: scan function is nil", ErrQueryFailed)
	}
	defer func() {
		err = joinRowsCloseError(err, rows.Close())
	}()

	for rows.Next() {
		result, err := scanFunc(rows)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrQueryFailed, err)
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrQueryFailed, err)
	}

	return results, nil
}

// Ping tests the database connection using the caller-provided context.
func Ping(ctx context.Context, db DB) error {
	if db == nil {
		return fmt.Errorf("%w: database is nil", ErrPingFailed)
	}

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrPingFailed, err)
	}

	return nil
}

func joinRowsCloseError(err, closeErr error) error {
	if closeErr == nil {
		return err
	}
	wrappedCloseErr := fmt.Errorf("%w: close rows failed: %w", ErrQueryFailed, closeErr)
	if err == nil {
		return wrappedCloseErr
	}
	return errors.Join(err, wrappedCloseErr)
}
