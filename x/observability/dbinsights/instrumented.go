package dbinsights

import (
	"context"
	"database/sql"
	"time"

	storedb "github.com/birdordev/plumego/store/db"
)

// MetricsObserver defines the minimal interface for database metrics collection.
type MetricsObserver interface {
	ObserveDB(ctx context.Context, operation, driver, query string, rows int, duration time.Duration, err error)
}

// InstrumentedDB wraps a sql.DB with observability-owned metrics collection.
type InstrumentedDB struct {
	db       *sql.DB
	observer MetricsObserver
	driver   string
}

// NewInstrumentedDB creates a new instrumented database connection.
func NewInstrumentedDB(db *sql.DB, observer MetricsObserver, driver string) *InstrumentedDB {
	return &InstrumentedDB{
		db:       db,
		observer: observer,
		driver:   driver,
	}
}

func (idb *InstrumentedDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	result, err := idb.db.ExecContext(ctx, query, args...)
	duration := time.Since(start)

	if idb.observer != nil {
		rows := int64(0)
		if result != nil && err == nil {
			if affected, aErr := result.RowsAffected(); aErr == nil {
				rows = affected
			}
		}
		idb.observer.ObserveDB(ctx, "exec", idb.driver, query, int(rows), duration, err)
	}

	return result, err
}

func (idb *InstrumentedDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := idb.db.QueryContext(ctx, query, args...)
	duration := time.Since(start)

	if idb.observer != nil {
		idb.observer.ObserveDB(ctx, "query", idb.driver, query, 0, duration, err)
	}

	return rows, err
}

func (idb *InstrumentedDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := idb.db.QueryRowContext(ctx, query, args...)
	duration := time.Since(start)

	if idb.observer != nil {
		idb.observer.ObserveDB(ctx, "query", idb.driver, query, 1, duration, nil)
	}

	return row
}

func (idb *InstrumentedDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	start := time.Now()
	tx, err := idb.db.BeginTx(ctx, opts)
	duration := time.Since(start)

	if idb.observer != nil {
		idb.observer.ObserveDB(ctx, "transaction", idb.driver, "BEGIN", 0, duration, err)
	}

	return tx, err
}

func (idb *InstrumentedDB) PingContext(ctx context.Context) error {
	start := time.Now()
	err := idb.db.PingContext(ctx)
	duration := time.Since(start)

	if idb.observer != nil {
		idb.observer.ObserveDB(ctx, "ping", idb.driver, "", 0, duration, err)
	}

	return err
}

func (idb *InstrumentedDB) Close() error {
	start := time.Now()
	err := idb.db.Close()
	duration := time.Since(start)

	if idb.observer != nil {
		idb.observer.ObserveDB(context.Background(), "close", idb.driver, "", 0, duration, err)
	}

	return err
}

func (idb *InstrumentedDB) Unwrap() *sql.DB {
	return idb.db
}

func (idb *InstrumentedDB) Stats() sql.DBStats {
	return idb.db.Stats()
}

func (idb *InstrumentedDB) SetMaxOpenConns(n int) {
	idb.db.SetMaxOpenConns(n)
}

func (idb *InstrumentedDB) SetMaxIdleConns(n int) {
	idb.db.SetMaxIdleConns(n)
}

func (idb *InstrumentedDB) SetConnMaxLifetime(d time.Duration) {
	idb.db.SetConnMaxLifetime(d)
}

func (idb *InstrumentedDB) SetConnMaxIdleTime(d time.Duration) {
	idb.db.SetConnMaxIdleTime(d)
}

// RecordPoolStats records current connection pool statistics as metrics.
func (idb *InstrumentedDB) RecordPoolStats(ctx context.Context) {
	if idb.observer == nil {
		return
	}

	stats := idb.db.Stats()

	idb.observer.ObserveDB(ctx, "pool_open_connections", idb.driver, "", int(stats.OpenConnections), 0, nil)
	idb.observer.ObserveDB(ctx, "pool_in_use", idb.driver, "", int(stats.InUse), 0, nil)
	idb.observer.ObserveDB(ctx, "pool_idle", idb.driver, "", int(stats.Idle), 0, nil)
	idb.observer.ObserveDB(ctx, "pool_wait_count", idb.driver, "", int(stats.WaitCount), 0, nil)
	idb.observer.ObserveDB(ctx, "pool_wait_duration", idb.driver, "", 0, stats.WaitDuration, nil)
	idb.observer.ObserveDB(ctx, "pool_max_idle_closed", idb.driver, "", int(stats.MaxIdleClosed), 0, nil)
	idb.observer.ObserveDB(ctx, "pool_max_lifetime_closed", idb.driver, "", int(stats.MaxLifetimeClosed), 0, nil)
}

var _ storedb.DB = (*InstrumentedDB)(nil)
