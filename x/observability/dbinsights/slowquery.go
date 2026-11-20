package dbinsights

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// SlowQuery represents a slow query execution.
type SlowQuery struct {
	Query     string        `json:"query"`
	Operation string        `json:"operation"`
	Duration  time.Duration `json:"duration"`
	Error     error         `json:"error,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
	Driver    string        `json:"driver"`
}

// Detector detects and records slow database queries.
type Detector struct {
	mu sync.RWMutex

	threshold     time.Duration
	maxRecords    int
	slowQueries   []SlowQuery
	totalDetected int64
	onSlowQuery   func(SlowQuery)
}

// DetectorOption configures a slow query detector.
type DetectorOption func(*Detector)

// WithThreshold sets the threshold for slow queries.
func WithThreshold(threshold time.Duration) DetectorOption {
	return func(d *Detector) {
		d.threshold = threshold
	}
}

// WithMaxRecords sets the maximum number of slow queries to keep.
// A nonpositive value preserves the current limit.
func WithMaxRecords(max int) DetectorOption {
	return func(d *Detector) {
		if max > 0 {
			d.maxRecords = max
		}
	}
}

// WithCallback sets a callback to be called when a slow query is detected.
func WithCallback(callback func(SlowQuery)) DetectorOption {
	return func(d *Detector) {
		d.onSlowQuery = callback
	}
}

// NewDetector creates a new slow query detector.
func NewDetector(opts ...DetectorOption) *Detector {
	d := &Detector{
		threshold:   time.Second,
		maxRecords:  100,
		slowQueries: make([]SlowQuery, 0, 100),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Check records a query if it exceeds the configured slow-query threshold.
func (d *Detector) Check(operation, driver, query string, duration time.Duration, err error) bool {
	if duration < d.threshold {
		return false
	}

	slowQuery := SlowQuery{
		Query:     query,
		Operation: operation,
		Duration:  duration,
		Error:     err,
		Timestamp: time.Now(),
		Driver:    driver,
	}

	d.mu.Lock()
	d.totalDetected++
	if len(d.slowQueries) >= d.maxRecords {
		d.slowQueries = d.slowQueries[1:]
	}
	d.slowQueries = append(d.slowQueries, slowQuery)
	d.mu.Unlock()

	if d.onSlowQuery != nil {
		d.onSlowQuery(slowQuery)
	}
	return true
}

// GetSlowQueries returns all recorded slow queries.
func (d *Detector) GetSlowQueries() []SlowQuery {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]SlowQuery, len(d.slowQueries))
	copy(result, d.slowQueries)
	return result
}

// GetRecentSlowQueries returns the most recent N slow queries.
func (d *Detector) GetRecentSlowQueries(n int) []SlowQuery {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if n > len(d.slowQueries) {
		n = len(d.slowQueries)
	}
	if n == 0 {
		return nil
	}

	result := make([]SlowQuery, n)
	start := len(d.slowQueries) - n
	copy(result, d.slowQueries[start:])
	return result
}

// GetTotalDetected returns the total number of slow queries detected.
func (d *Detector) GetTotalDetected() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.totalDetected
}

// Clear clears all recorded slow queries.
func (d *Detector) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.slowQueries = make([]SlowQuery, 0, d.maxRecords)
	d.totalDetected = 0
}

// Summary returns a formatted summary of slow queries.
func (d *Detector) Summary() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.totalDetected == 0 {
		return "No slow queries detected"
	}

	summary := fmt.Sprintf("Slow Query Summary (threshold: %s)\n", d.threshold)
	summary += fmt.Sprintf("Total detected: %d\n", d.totalDetected)
	summary += fmt.Sprintf("Currently stored: %d\n\n", len(d.slowQueries))

	if len(d.slowQueries) > 0 {
		summary += "Recent slow queries:\n"
		start := 0
		if len(d.slowQueries) > 10 {
			start = len(d.slowQueries) - 10
		}
		for i := start; i < len(d.slowQueries); i++ {
			sq := d.slowQueries[i]
			summary += fmt.Sprintf("  [%s] %s (%s) - %s\n",
				sq.Timestamp.Format("15:04:05"),
				sq.Operation,
				sq.Duration.Round(time.Millisecond),
				truncateQuery(sq.Query, 60))
			if sq.Error != nil {
				summary += fmt.Sprintf("    Error: %v\n", sq.Error)
			}
		}
	}

	return summary
}

func truncateQuery(query string, maxLen int) string {
	if len(query) <= maxLen {
		return query
	}
	return query[:maxLen] + "..."
}

// SlowQueryDB wraps InstrumentedDB with slow query detection.
type SlowQueryDB struct {
	*InstrumentedDB
	driver   string
	detector *Detector
}

// NewSlowQueryDB wraps an instrumented DB with slow query detection.
func NewSlowQueryDB(db *InstrumentedDB, driver string, opts ...DetectorOption) *SlowQueryDB {
	return &SlowQueryDB{
		InstrumentedDB: db,
		driver:         driver,
		detector:       NewDetector(opts...),
	}
}

// GetDetector returns the slow query detector.
func (idb *SlowQueryDB) GetDetector() *Detector {
	return idb.detector
}

// ExecContext wraps ExecContext with slow query detection.
func (idb *SlowQueryDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	start := time.Now()
	result, err := idb.InstrumentedDB.ExecContext(ctx, query, args...)
	duration := time.Since(start)

	idb.detector.Check("exec", idb.driver, query, duration, err)
	return result, err
}

// QueryContext wraps QueryContext with slow query detection.
func (idb *SlowQueryDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	start := time.Now()
	rows, err := idb.InstrumentedDB.QueryContext(ctx, query, args...)
	duration := time.Since(start)

	idb.detector.Check("query", idb.driver, query, duration, err)
	return rows, err
}

// QueryRowContext wraps QueryRowContext with slow query detection.
func (idb *SlowQueryDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	start := time.Now()
	row := idb.InstrumentedDB.QueryRowContext(ctx, query, args...)
	duration := time.Since(start)

	idb.detector.Check("query", idb.driver, query, duration, nil)
	return row
}

// Observer wraps a MetricsObserver with slow query detection.
type Observer struct {
	base     MetricsObserver
	detector *Detector
}

// NewObserver creates a metrics observer with slow query detection.
func NewObserver(base MetricsObserver, opts ...DetectorOption) *Observer {
	return &Observer{
		base:     base,
		detector: NewDetector(opts...),
	}
}

// ObserveDB forwards metrics and records slow queries.
func (c *Observer) ObserveDB(ctx context.Context, operation, driver, query string, rows int, duration time.Duration, err error) {
	if c.base != nil {
		c.base.ObserveDB(ctx, operation, driver, query, rows, duration, err)
	}
	c.detector.Check(operation, driver, query, duration, err)
}

// GetDetector returns the slow query detector.
func (c *Observer) GetDetector() *Detector {
	return c.detector
}
