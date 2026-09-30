package dbinsights

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	opSelect = "SELECT"
	opUpdate = "UPDATE"
	opInsert = "INSERT"
	opDelete = "DELETE"
)

// QueryStats tracks statistics for database queries.
type QueryStats struct {
	TotalQueries   int64         `json:"total_queries"`
	SuccessQueries int64         `json:"success_queries"`
	FailedQueries  int64         `json:"failed_queries"`
	TotalDuration  time.Duration `json:"total_duration"`
	MinDuration    time.Duration `json:"min_duration"`
	MaxDuration    time.Duration `json:"max_duration"`
	AvgDuration    time.Duration `json:"avg_duration"`

	SelectQueries int64 `json:"select_queries"`
	InsertQueries int64 `json:"insert_queries"`
	UpdateQueries int64 `json:"update_queries"`
	DeleteQueries int64 `json:"delete_queries"`

	SlowQueries     int64         `json:"slow_queries"`
	SlowQueryThresh time.Duration `json:"slow_query_threshold"`
	LastUpdate      time.Time     `json:"last_update"`
}

// TableStat represents statistics for a single table.
type TableStat struct {
	Table        string        `json:"table"`
	SlowQueries  int64         `json:"slow_queries"`
	AvgDuration  time.Duration `json:"avg_duration"`
	TotalQueries int64         `json:"total_queries"`
}

// Aggregator aggregates database statistics from metrics.
type Aggregator struct {
	mu sync.RWMutex

	global             QueryStats
	byOperation        map[string]QueryStats
	byTable            map[string]QueryStats
	slowQueryThreshold time.Duration
	startTime          time.Time
}

// NewAggregator creates a new database statistics aggregator.
func NewAggregator(slowQueryThreshold time.Duration) *Aggregator {
	if slowQueryThreshold == 0 {
		slowQueryThreshold = time.Second
	}

	return &Aggregator{
		byOperation:        make(map[string]QueryStats),
		byTable:            make(map[string]QueryStats),
		slowQueryThreshold: slowQueryThreshold,
		startTime:          time.Now(),
	}
}

// RecordQuery records a query execution.
func (a *Aggregator) RecordQuery(operation, query string, duration time.Duration, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	queryType := detectQueryType(query)

	a.updateStats(&a.global, duration, err, now)
	a.updateQueryType(&a.global, queryType)

	opStats := a.byOperation[operation]
	a.updateStats(&opStats, duration, err, now)
	a.updateQueryType(&opStats, queryType)
	a.byOperation[operation] = opStats

	if table := extractTableName(query); table != "" {
		tableStats := a.byTable[table]
		a.updateStats(&tableStats, duration, err, now)
		a.updateQueryType(&tableStats, queryType)
		a.byTable[table] = tableStats
	}
}

func (a *Aggregator) updateStats(stats *QueryStats, duration time.Duration, err error, now time.Time) {
	stats.TotalQueries++
	stats.LastUpdate = now

	if err == nil {
		stats.SuccessQueries++
	} else {
		stats.FailedQueries++
	}

	stats.TotalDuration += duration
	if stats.MinDuration == 0 || duration < stats.MinDuration {
		stats.MinDuration = duration
	}
	if duration > stats.MaxDuration {
		stats.MaxDuration = duration
	}
	if stats.TotalQueries > 0 {
		stats.AvgDuration = stats.TotalDuration / time.Duration(stats.TotalQueries)
	}
	if duration >= a.slowQueryThreshold {
		stats.SlowQueries++
	}
	stats.SlowQueryThresh = a.slowQueryThreshold
}

func (a *Aggregator) updateQueryType(stats *QueryStats, queryType string) {
	switch queryType {
	case opSelect:
		stats.SelectQueries++
	case opInsert:
		stats.InsertQueries++
	case opUpdate:
		stats.UpdateQueries++
	case opDelete:
		stats.DeleteQueries++
	}
}

func detectQueryType(query string) string {
	if query == "" {
		return ""
	}

	query = strings.TrimSpace(strings.ToUpper(query))
	if strings.HasPrefix(query, opSelect) {
		return opSelect
	}
	if strings.HasPrefix(query, opInsert) {
		return opInsert
	}
	if strings.HasPrefix(query, opUpdate) {
		return opUpdate
	}
	if strings.HasPrefix(query, opDelete) {
		return opDelete
	}
	return ""
}

// GetGlobalStats returns global statistics.
func (a *Aggregator) GetGlobalStats() QueryStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.global
}

// GetOperationStats returns statistics for a specific operation.
func (a *Aggregator) GetOperationStats(operation string) (QueryStats, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	stats, ok := a.byOperation[operation]
	return stats, ok
}

// GetAllOperationStats returns statistics for all operations.
func (a *Aggregator) GetAllOperationStats() map[string]QueryStats {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]QueryStats, len(a.byOperation))
	for key, value := range a.byOperation {
		result[key] = value
	}
	return result
}

// GetTableStats returns statistics for a specific table.
func (a *Aggregator) GetTableStats(table string) (QueryStats, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	stats, ok := a.byTable[table]
	return stats, ok
}

// GetAllTableStats returns statistics for all tables.
func (a *Aggregator) GetAllTableStats() map[string]QueryStats {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]QueryStats, len(a.byTable))
	for key, value := range a.byTable {
		result[key] = value
	}
	return result
}

// GetTopSlowTables returns the top N tables by slow query count.
func (a *Aggregator) GetTopSlowTables(n int) []TableStat {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.getTopSlowTablesLocked(n)
}

// Reset resets all statistics.
func (a *Aggregator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.global = QueryStats{}
	a.byOperation = make(map[string]QueryStats)
	a.byTable = make(map[string]QueryStats)
	a.startTime = time.Now()
}

// Summary returns a formatted summary of all statistics.
func (a *Aggregator) Summary() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("=== Database Statistics Summary ===\n\n")
	fmt.Fprintf(&sb, "Uptime: %s\n", time.Since(a.startTime).Round(time.Second))
	fmt.Fprintf(&sb, "Slow Query Threshold: %s\n\n", a.slowQueryThreshold)
	sb.WriteString("Global Stats:\n")
	sb.WriteString(formatQueryStats(a.global))
	sb.WriteString("\n")

	if len(a.byOperation) > 0 {
		sb.WriteString("Stats by Operation:\n")
		for op, stats := range a.byOperation {
			fmt.Fprintf(&sb, "  %s:\n", op)
			sb.WriteString(indentString(formatQueryStats(stats), "    "))
		}
		sb.WriteString("\n")
	}

	if len(a.byTable) > 0 {
		sb.WriteString("Top Slow Tables:\n")
		topTables := a.getTopSlowTablesLocked(5)
		for i, ts := range topTables {
			fmt.Fprintf(&sb, "  %d. %s - %d slow queries (avg: %s)\n",
				i+1, ts.Table, ts.SlowQueries, ts.AvgDuration.Round(time.Millisecond))
		}
	}

	return sb.String()
}

func (a *Aggregator) getTopSlowTablesLocked(n int) []TableStat {
	tables := make([]TableStat, 0, len(a.byTable))
	for name, stats := range a.byTable {
		tables = append(tables, TableStat{
			Table:        name,
			SlowQueries:  stats.SlowQueries,
			AvgDuration:  stats.AvgDuration,
			TotalQueries: stats.TotalQueries,
		})
	}

	for i := 0; i < len(tables); i++ {
		for j := i + 1; j < len(tables); j++ {
			if tables[j].SlowQueries > tables[i].SlowQueries {
				tables[i], tables[j] = tables[j], tables[i]
			}
		}
	}

	if n > len(tables) {
		n = len(tables)
	}
	return tables[:n]
}

func formatQueryStats(stats QueryStats) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Total Queries: %d\n", stats.TotalQueries)
	fmt.Fprintf(&sb, "Success: %d, Failed: %d\n", stats.SuccessQueries, stats.FailedQueries)
	if stats.TotalQueries > 0 {
		successRate := float64(stats.SuccessQueries) / float64(stats.TotalQueries) * 100
		fmt.Fprintf(&sb, "Success Rate: %.2f%%\n", successRate)
	}
	fmt.Fprintf(&sb, "Duration: min=%s, avg=%s, max=%s\n",
		stats.MinDuration.Round(time.Millisecond),
		stats.AvgDuration.Round(time.Millisecond),
		stats.MaxDuration.Round(time.Millisecond))
	fmt.Fprintf(&sb, "Query Types: SELECT=%d, INSERT=%d, UPDATE=%d, DELETE=%d\n",
		stats.SelectQueries, stats.InsertQueries, stats.UpdateQueries, stats.DeleteQueries)
	fmt.Fprintf(&sb, "Slow Queries: %d (>%s)\n", stats.SlowQueries, stats.SlowQueryThresh)
	return sb.String()
}

func indentString(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

func extractTableName(query string) string {
	if query == "" {
		return ""
	}

	query = strings.TrimSpace(strings.ToUpper(query))

	if strings.HasPrefix(query, opSelect) {
		if idx := strings.Index(query, "FROM"); idx != -1 {
			rest := strings.TrimSpace(query[idx+4:])
			parts := strings.Fields(rest)
			if len(parts) > 0 {
				return strings.ToLower(cleanTableName(parts[0]))
			}
		}
	}

	if strings.HasPrefix(query, "INSERT INTO") {
		rest := strings.TrimSpace(query[11:])
		parts := strings.Fields(rest)
		if len(parts) > 0 {
			return strings.ToLower(cleanTableName(parts[0]))
		}
	}

	if strings.HasPrefix(query, opUpdate) {
		rest := strings.TrimSpace(query[6:])
		parts := strings.Fields(rest)
		if len(parts) > 0 {
			return strings.ToLower(cleanTableName(parts[0]))
		}
	}

	if strings.HasPrefix(query, "DELETE FROM") {
		rest := strings.TrimSpace(query[11:])
		parts := strings.Fields(rest)
		if len(parts) > 0 {
			return strings.ToLower(cleanTableName(parts[0]))
		}
	}

	return ""
}

func cleanTableName(name string) string {
	return strings.TrimFunc(name, func(r rune) bool {
		return r == '`' || r == '"' || r == '\'' || r == '(' || r == ','
	})
}

// AggregatingObserver wraps a MetricsObserver and also aggregates statistics.
type AggregatingObserver struct {
	base       MetricsObserver
	aggregator *Aggregator
}

// NewAggregatingObserver creates an observer that both records metrics and aggregates statistics.
func NewAggregatingObserver(base MetricsObserver, slowQueryThreshold time.Duration) *AggregatingObserver {
	return &AggregatingObserver{
		base:       base,
		aggregator: NewAggregator(slowQueryThreshold),
	}
}

// ObserveDB implements MetricsObserver and also aggregates statistics.
func (c *AggregatingObserver) ObserveDB(ctx context.Context, operation, driver, query string, rows int, duration time.Duration, err error) {
	if c.base != nil {
		c.base.ObserveDB(ctx, operation, driver, query, rows, duration, err)
	}
	c.aggregator.RecordQuery(operation, query, duration, err)
}

// GetAggregator returns the underlying statistics aggregator.
func (c *AggregatingObserver) GetAggregator() *Aggregator {
	return c.aggregator
}
