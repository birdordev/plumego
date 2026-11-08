package devtools

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/x/observability/featuremetrics"
	"github.com/birdordev/plumego/x/observability/recordbuffer"
	windowmetrics "github.com/birdordev/plumego/x/observability/windowmetrics"
)

// DevCollectorConfig configures the dev metrics collector.
type DevCollectorConfig struct {
	Window      time.Duration // Display window for snapshot retention
	MaxSamples  int           // Maximum recent request samples to retain
	MaxSeries   int           // Maximum distinct route series to retain
	MaxValues   int           // Maximum values retained per series for percentiles
	DBSlowMS    float64       // Slow query threshold in ms
	DBMaxSlow   int           // Maximum slow query samples retained
	DBMaxSeries int           // Maximum distinct DB series retained
}

// DefaultDevCollectorConfig returns defaults for dev metrics.
func DefaultDevCollectorConfig() DevCollectorConfig {
	return DevCollectorConfig{
		Window:      5 * time.Minute,
		MaxSamples:  200,
		MaxSeries:   200,
		MaxValues:   2000,
		DBSlowMS:    200,
		DBMaxSlow:   100,
		DBMaxSeries: 100,
	}
}

// DevRequestSample represents a recent HTTP request sample.
type DevRequestSample struct {
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS float64   `json:"duration_ms"`
	Bytes      int       `json:"bytes"`
	Error      bool      `json:"error"`
	Timestamp  time.Time `json:"timestamp"`
}

// DevHTTPStatusCounts tracks status code classes.
type DevHTTPStatusCounts struct {
	Info        int64 `json:"1xx"`
	Success     int64 `json:"2xx"`
	Redirect    int64 `json:"3xx"`
	ClientError int64 `json:"4xx"`
	ServerError int64 `json:"5xx"`
}

// DevHTTPSeries is the aggregated stats for a route or total series.
type DevHTTPSeries struct {
	Method     string                        `json:"method"`
	Path       string                        `json:"path"`
	Count      int64                         `json:"count"`
	ErrorCount int64                         `json:"error_count"`
	Status     DevHTTPStatusCounts           `json:"status"`
	Duration   windowmetrics.AggregatorStats `json:"duration_ms"`
	Bytes      windowmetrics.AggregatorStats `json:"bytes"`
	LastSeen   time.Time                     `json:"last_seen,omitempty"`
}

// DevHTTPSnapshot is the JSON payload for dev HTTP metrics.
type DevHTTPSnapshot struct {
	WindowSeconds int64              `json:"window_seconds"`
	StartedAt     time.Time          `json:"started_at"`
	Total         DevHTTPSeries      `json:"total"`
	Routes        []DevHTTPSeries    `json:"routes"`
	Recent        []DevRequestSample `json:"recent"`
}

// DevDBQuerySample represents a slow DB query sample.
type DevDBQuerySample struct {
	Operation    string    `json:"operation"`
	Driver       string    `json:"driver"`
	Table        string    `json:"table,omitempty"`
	Query        string    `json:"query"`
	QueryPreview string    `json:"query_preview,omitempty"`
	Redacted     bool      `json:"redacted"`
	DurationMS   float64   `json:"duration_ms"`
	Error        string    `json:"error,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// DevDBSeries is the aggregated stats for DB operations.
type DevDBSeries struct {
	Operation  string                        `json:"operation"`
	Driver     string                        `json:"driver"`
	Table      string                        `json:"table,omitempty"`
	Count      int64                         `json:"count"`
	ErrorCount int64                         `json:"error_count"`
	Duration   windowmetrics.AggregatorStats `json:"duration_ms"`
	Rows       windowmetrics.AggregatorStats `json:"rows"`
	LastSeen   time.Time                     `json:"last_seen,omitempty"`
}

// DevDBSnapshot is the JSON payload for dev DB metrics.
type DevDBSnapshot struct {
	StartedAt time.Time            `json:"started_at"`
	Total     DevDBSeries          `json:"total"`
	Series    []DevDBSeries        `json:"series"`
	Tables    []DevDBSeries        `json:"tables,omitempty"`
	Slow      []DevDBQuerySample   `json:"slow"`
	Redaction DevDBRedactionConfig `json:"redaction"`
}

// DevDBRedactionConfig describes redaction rules used for SQL strings.
type DevDBRedactionConfig struct {
	Enabled bool     `json:"enabled"`
	Rules   []string `json:"rules,omitempty"`
}

// DevCollector is a lightweight in-memory metrics collector for dev dashboards.
// It focuses on HTTP request aggregation while also exposing extension-owned
// helpers for DB, MQ, KV, IPC, and PubSub observations.
type DevCollector struct {
	base *recordbuffer.Collector

	mu         sync.RWMutex
	startedAt  time.Time
	window     time.Duration
	maxSamples int
	maxSeries  int
	maxValues  int
	now        func() time.Time

	total   *httpSeries
	series  map[string]*httpSeries
	samples []DevRequestSample

	dbTotal       *dbSeries
	dbSeries      map[string]*dbSeries
	dbTableSeries map[string]*dbSeries
	dbSlow        []DevDBQuerySample
	dbSlowMS      float64
	dbMaxSlow     int
	dbMaxSeries   int
}

// NewDevCollector creates a new dev metrics collector with the provided config.
func NewDevCollector(cfg DevCollectorConfig) *DevCollector {
	def := DefaultDevCollectorConfig()
	if cfg.Window <= 0 {
		cfg.Window = def.Window
	}
	if cfg.MaxSamples <= 0 {
		cfg.MaxSamples = def.MaxSamples
	}
	if cfg.MaxSeries <= 0 {
		cfg.MaxSeries = def.MaxSeries
	}
	if cfg.MaxValues <= 0 {
		cfg.MaxValues = def.MaxValues
	}
	if cfg.DBSlowMS <= 0 {
		cfg.DBSlowMS = def.DBSlowMS
	}
	if cfg.DBMaxSlow <= 0 {
		cfg.DBMaxSlow = def.DBMaxSlow
	}
	if cfg.DBMaxSeries <= 0 {
		cfg.DBMaxSeries = def.DBMaxSeries
	}

	return &DevCollector{
		base:          recordbuffer.NewCollector(),
		startedAt:     time.Now(),
		window:        cfg.Window,
		maxSamples:    cfg.MaxSamples,
		maxSeries:     cfg.MaxSeries,
		maxValues:     cfg.MaxValues,
		now:           time.Now,
		total:         newHTTPSeries("ALL", "ALL", cfg.MaxValues),
		series:        make(map[string]*httpSeries),
		samples:       make([]DevRequestSample, 0, cfg.MaxSamples),
		dbTotal:       newDBSeries("ALL", "ALL", cfg.MaxValues),
		dbSeries:      make(map[string]*dbSeries),
		dbTableSeries: make(map[string]*dbSeries),
		dbSlow:        make([]DevDBQuerySample, 0, cfg.DBMaxSlow),
		dbSlowMS:      cfg.DBSlowMS,
		dbMaxSlow:     cfg.DBMaxSlow,
		dbMaxSeries:   cfg.DBMaxSeries,
	}
}

// Snapshot returns the current HTTP metrics snapshot.
func (d *DevCollector) Snapshot() DevHTTPSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pruneHTTPLocked(d.now())

	routes := make([]DevHTTPSeries, 0, len(d.series))
	for _, s := range d.series {
		routes = append(routes, s.snapshot())
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Count == routes[j].Count {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Count > routes[j].Count
	})

	recent := make([]DevRequestSample, len(d.samples))
	copy(recent, d.samples)

	return DevHTTPSnapshot{
		WindowSeconds: int64(d.window.Seconds()),
		StartedAt:     d.startedAt,
		Total:         d.total.snapshot(),
		Routes:        routes,
		Recent:        recent,
	}
}

// DBSnapshot returns the current DB metrics snapshot.
func (d *DevCollector) DBSnapshot() DevDBSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pruneDBLocked(d.now())

	series := make([]DevDBSeries, 0, len(d.dbSeries))
	for _, s := range d.dbSeries {
		series = append(series, s.snapshot())
	}

	sort.Slice(series, func(i, j int) bool {
		if series[i].Count == series[j].Count {
			return series[i].Operation < series[j].Operation
		}
		return series[i].Count > series[j].Count
	})

	tables := make([]DevDBSeries, 0, len(d.dbTableSeries))
	for _, s := range d.dbTableSeries {
		tables = append(tables, s.snapshot())
	}

	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Count == tables[j].Count {
			return tables[i].Table < tables[j].Table
		}
		return tables[i].Count > tables[j].Count
	})

	slow := make([]DevDBQuerySample, len(d.dbSlow))
	copy(slow, d.dbSlow)

	snap := DevDBSnapshot{
		StartedAt: d.startedAt,
		Total:     d.dbTotal.snapshot(),
		Series:    series,
		Slow:      slow,
		Redaction: DevDBRedactionConfig{
			Enabled: true,
			Rules:   dbRedactionRules(),
		},
	}
	if len(tables) > 0 {
		snap.Tables = tables
	}
	return snap
}

// Record records a generic metric.
func (d *DevCollector) Record(ctx context.Context, record metrics.MetricRecord) {
	d.base.Record(ctx, record)
}

// ObserveHTTP records an HTTP metric.
func (d *DevCollector) ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
	d.base.ObserveHTTP(ctx, method, path, status, bytes, duration)

	key := seriesKey(method, path)
	now := d.now()
	errorHit := status >= 400
	durationMS := duration.Seconds() * 1000

	d.mu.Lock()
	defer d.mu.Unlock()

	d.total.record(status, bytes, durationMS, now)

	s := d.series[key]
	if s == nil {
		if d.maxSeries > 0 && len(d.series) >= d.maxSeries {
			d.appendSampleLocked(method, path, status, bytes, durationMS, errorHit, now)
			return
		}
		s = newHTTPSeries(method, path, d.maxValues)
		d.series[key] = s
	}

	s.record(status, bytes, durationMS, now)
	d.appendSampleLocked(method, path, status, bytes, durationMS, errorHit, now)
}

// ObservePubSub records a PubSub metric via extension-owned helpers.
func (d *DevCollector) ObservePubSub(ctx context.Context, operation, topic string, duration time.Duration, err error) {
	featuremetrics.ObservePubSub(d.base, ctx, operation, topic, duration, err)
}

// ObserveMQ records an MQ metric via extension-owned helpers.
func (d *DevCollector) ObserveMQ(ctx context.Context, operation, topic string, duration time.Duration, err error, panicked bool) {
	featuremetrics.ObserveMQ(d.base, ctx, operation, topic, duration, err, panicked)
}

// ObserveKV records a KV metric via extension-owned helpers.
func (d *DevCollector) ObserveKV(ctx context.Context, operation, key string, duration time.Duration, err error, hit bool) {
	featuremetrics.ObserveKV(d.base, ctx, operation, key, duration, err, hit)
}

// ObserveIPC records an IPC metric via extension-owned helpers.
func (d *DevCollector) ObserveIPC(ctx context.Context, operation, addr, transport string, bytes int, duration time.Duration, err error) {
	featuremetrics.ObserveIPC(d.base, ctx, operation, addr, transport, bytes, duration, err)
}

// ObserveDB records a DB metric via extension-owned helpers.
func (d *DevCollector) ObserveDB(ctx context.Context, operation, driver, query string, rows int, duration time.Duration, err error) {
	record := featuremetrics.DBRecord(operation, driver, query, rows, duration, err)
	d.base.Record(ctx, record)

	durationMS := duration.Seconds() * 1000
	now := d.now()
	errorHit := err != nil
	table := record.Labels["table"]

	key := dbSeriesKey(operation, driver)

	d.mu.Lock()
	defer d.mu.Unlock()

	d.dbTotal.record(operation, driver, durationMS, float64(rows), errorHit, now)

	series := d.dbSeries[key]
	if series == nil {
		if d.dbMaxSeries > 0 && len(d.dbSeries) >= d.dbMaxSeries {
			d.appendDBSlowLocked(operation, driver, table, query, durationMS, err, now)
			return
		}
		series = newDBSeries(operation, driver, d.maxValues)
		d.dbSeries[key] = series
	}

	series.record(operation, driver, durationMS, float64(rows), errorHit, now)

	// Track per-table aggregation
	if table != "" {
		ts := d.dbTableSeries[table]
		if ts == nil {
			if d.dbMaxSeries <= 0 || len(d.dbTableSeries) < d.dbMaxSeries {
				ts = newDBTableSeries(table, d.maxValues)
				d.dbTableSeries[table] = ts
			}
		}
		if ts != nil {
			ts.record(operation, driver, durationMS, float64(rows), errorHit, now)
		}
	}

	d.appendDBSlowLocked(operation, driver, table, query, durationMS, err, now)
}

// GetStats returns aggregate stats from the underlying base collector.
func (d *DevCollector) GetStats() metrics.CollectorStats {
	stats := d.base.GetStats()
	if stats.ActiveSeries == 0 && len(stats.NameBreakdown) > 0 {
		stats.ActiveSeries = len(stats.NameBreakdown)
	}
	return stats
}

// Clear resets the collector state.
func (d *DevCollector) Clear() {
	d.base.Clear()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.total = newHTTPSeries("ALL", "ALL", d.maxValues)
	d.series = make(map[string]*httpSeries)
	d.samples = d.samples[:0]
	d.dbTotal = newDBSeries("ALL", "ALL", d.maxValues)
	d.dbSeries = make(map[string]*dbSeries)
	d.dbTableSeries = make(map[string]*dbSeries)
	d.dbSlow = d.dbSlow[:0]
	d.startedAt = d.now()
}

func (d *DevCollector) retentionCutoff(now time.Time) time.Time {
	cutoff := now.Add(-d.window)
	if cutoff.Before(d.startedAt) {
		return d.startedAt
	}
	return cutoff
}

func (d *DevCollector) pruneHTTPLocked(now time.Time) {
	cutoff := d.retentionCutoff(now)

	writeIdx := 0
	for _, sample := range d.samples {
		if sample.Timestamp.Before(cutoff) {
			continue
		}
		d.samples[writeIdx] = sample
		writeIdx++
	}
	d.samples = d.samples[:writeIdx]

	for key, series := range d.series {
		if series.lastSeen.Before(cutoff) {
			delete(d.series, key)
		}
	}
}

func (d *DevCollector) pruneDBLocked(now time.Time) {
	cutoff := d.retentionCutoff(now)

	writeIdx := 0
	for _, sample := range d.dbSlow {
		if sample.Timestamp.Before(cutoff) {
			continue
		}
		d.dbSlow[writeIdx] = sample
		writeIdx++
	}
	d.dbSlow = d.dbSlow[:writeIdx]

	for key, series := range d.dbSeries {
		if series.lastSeen.Before(cutoff) {
			delete(d.dbSeries, key)
		}
	}

	for key, series := range d.dbTableSeries {
		if series.lastSeen.Before(cutoff) {
			delete(d.dbTableSeries, key)
		}
	}
}

func (d *DevCollector) appendSampleLocked(method, path string, status, bytes int, durationMS float64, errorHit bool, ts time.Time) {
	if d.maxSamples <= 0 {
		return
	}
	sample := DevRequestSample{
		Method:     method,
		Path:       path,
		Status:     status,
		DurationMS: durationMS,
		Bytes:      bytes,
		Error:      errorHit,
		Timestamp:  ts,
	}

	if len(d.samples) >= d.maxSamples {
		copy(d.samples, d.samples[1:])
		d.samples[len(d.samples)-1] = sample
		return
	}
	d.samples = append(d.samples, sample)
}

func seriesKey(method, path string) string {
	method = strings.TrimSpace(method)
	path = strings.TrimSpace(path)
	if method == "" {
		method = "UNKNOWN"
	}
	if path == "" {
		path = "/"
	}
	return method + " " + path
}

func dbSeriesKey(operation, driver string) string {
	operation = strings.TrimSpace(operation)
	driver = strings.TrimSpace(driver)
	if operation == "" {
		operation = "unknown"
	}
	if driver == "" {
		driver = "unknown"
	}
	return operation + "|" + driver
}

type httpSeries struct {
	method     string
	path       string
	count      int64
	errorCount int64
	status     DevHTTPStatusCounts
	duration   *statBucket
	bytes      *statBucket
	lastSeen   time.Time
}

func newHTTPSeries(method, path string, maxValues int) *httpSeries {
	return &httpSeries{
		method:   method,
		path:     path,
		duration: newStatBucket(maxValues),
		bytes:    newStatBucket(maxValues),
	}
}

func (s *httpSeries) record(status, bytes int, durationMS float64, now time.Time) {
	s.count++
	if status >= 400 {
		s.errorCount++
	}
	s.status.add(status)
	s.duration.record(durationMS)
	s.bytes.record(float64(bytes))
	s.lastSeen = now
}

func (s *httpSeries) snapshot() DevHTTPSeries {
	return DevHTTPSeries{
		Method:     s.method,
		Path:       s.path,
		Count:      s.count,
		ErrorCount: s.errorCount,
		Status:     s.status,
		Duration:   s.duration.snapshot(),
		Bytes:      s.bytes.snapshot(),
		LastSeen:   s.lastSeen,
	}
}

func (c *DevHTTPStatusCounts) add(status int) {
	switch {
	case status >= 500:
		c.ServerError++
	case status >= 400:
		c.ClientError++
	case status >= 300:
		c.Redirect++
	case status >= 200:
		c.Success++
	default:
		c.Info++
	}
}

type statBucket struct {
	count     int64
	sum       float64
	min       float64
	max       float64
	values    []float64
	maxValues int
	sorted    []float64
	sortedOK  bool
}

func newStatBucket(maxValues int) *statBucket {
	return &statBucket{
		values:    make([]float64, 0, maxValues),
		maxValues: maxValues,
	}
}

func (b *statBucket) record(value float64) {
	if b.count == 0 {
		b.min = value
		b.max = value
	} else {
		if value < b.min {
			b.min = value
		}
		if value > b.max {
			b.max = value
		}
	}

	b.count++
	b.sum += value

	if b.maxValues > 0 && len(b.values) < b.maxValues {
		b.values = append(b.values, value)
		b.sortedOK = false
	}
}

func (b *statBucket) snapshot() windowmetrics.AggregatorStats {
	if b.count == 0 {
		return windowmetrics.AggregatorStats{}
	}

	stats := windowmetrics.AggregatorStats{
		Count: b.count,
		Sum:   b.sum,
		Min:   b.min,
		Max:   b.max,
	}

	stats.Mean = b.sum / float64(b.count)

	if len(b.values) > 0 {
		sorted := b.sortedValues()
		stats.P50 = percentileSorted(sorted, 0.50)
		stats.P95 = percentileSorted(sorted, 0.95)
		stats.P99 = percentileSorted(sorted, 0.99)
	}

	return stats
}

func (b *statBucket) sortedValues() []float64 {
	if b.sortedOK {
		return b.sorted
	}

	b.sorted = make([]float64, len(b.values))
	copy(b.sorted, b.values)
	sort.Float64s(b.sorted)
	b.sortedOK = true

	return b.sorted
}

func percentileSorted(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * p)
	return values[index]
}

func (d *DevCollector) appendDBSlowLocked(operation, driver, table, query string, durationMS float64, err error, now time.Time) {
	if d.dbSlowMS <= 0 || durationMS < d.dbSlowMS {
		return
	}

	if d.dbMaxSlow <= 0 {
		return
	}

	redacted := redactSQL(query)
	preview := truncateQuery(redacted, 140)
	sample := DevDBQuerySample{
		Operation:    operation,
		Driver:       driver,
		Table:        table,
		Query:        redacted,
		QueryPreview: preview,
		Redacted:     true,
		DurationMS:   durationMS,
		Timestamp:    now,
	}
	if err != nil {
		sample.Error = err.Error()
	}

	if len(d.dbSlow) >= d.dbMaxSlow {
		copy(d.dbSlow, d.dbSlow[1:])
		d.dbSlow[len(d.dbSlow)-1] = sample
		return
	}
	d.dbSlow = append(d.dbSlow, sample)
}

func truncateQuery(query string, maxLen int) string {
	if maxLen <= 0 || len(query) <= maxLen {
		return query
	}
	return query[:maxLen] + "..."
}

func dbRedactionRules() []string {
	return []string{
		"Single-quoted string literals replaced with '?'",
		"Double-quoted strings replaced with \"?\"",
		"Backtick-quoted identifiers replaced with `?`",
		"SQL comments redacted",
		"Numeric literals replaced with ?",
		"Excess whitespace collapsed",
	}
}

func redactSQL(query string) string {
	if query == "" {
		return query
	}

	var out strings.Builder
	out.Grow(len(query))

	for i := 0; i < len(query); i++ {
		ch := query[i]

		// Single-quoted string literal: 'value' -> '?'
		if ch == '\'' {
			out.WriteString("'?'")
			i = consumeQuoted(query, i, '\'')
			continue
		}

		// Double-quoted string/identifier: "value" -> "?"
		if ch == '"' {
			out.WriteString("\"?\"")
			i = consumeQuoted(query, i, '"')
			continue
		}

		// Backtick-quoted identifier: `table` -> `?`
		if ch == '`' {
			out.WriteString("`?`")
			i = consumeQuoted(query, i, '`')
			continue
		}

		// Single-line comment: -- ... \n
		if ch == '-' && i+1 < len(query) && query[i+1] == '-' {
			out.WriteString("-- ?")
			for i+1 < len(query) && query[i+1] != '\n' {
				i++
			}
			continue
		}

		// Block comment: /* ... */
		if ch == '/' && i+1 < len(query) && query[i+1] == '*' {
			out.WriteString("/* ? */")
			i += 2
			for i < len(query) {
				if query[i] == '*' && i+1 < len(query) && query[i+1] == '/' {
					i++
					break
				}
				i++
			}
			continue
		}

		// Numeric literal
		if isNumericStart(query, i) {
			out.WriteByte('?')
			i = consumeNumber(query, i) - 1
			continue
		}

		out.WriteByte(ch)
	}

	return strings.Join(strings.Fields(out.String()), " ")
}

// consumeQuoted advances past a quoted string starting at position idx.
// It handles backslash escaping and returns the index of the closing quote,
// or len(s)-1 if the string is unterminated.
func consumeQuoted(s string, idx int, quote byte) int {
	escapeNext := false
	for i := idx + 1; i < len(s); i++ {
		if escapeNext {
			escapeNext = false
			continue
		}
		if s[i] == '\\' {
			escapeNext = true
			continue
		}
		if s[i] == quote {
			return i
		}
	}
	return len(s) - 1
}

func isNumericStart(s string, idx int) bool {
	ch := s[idx]
	if ch >= '0' && ch <= '9' {
		return true
	}
	if ch == '-' && idx+1 < len(s) {
		next := s[idx+1]
		return next >= '0' && next <= '9'
	}
	if ch == '.' && idx+1 < len(s) {
		next := s[idx+1]
		return next >= '0' && next <= '9'
	}
	return false
}

func consumeNumber(s string, idx int) int {
	i := idx
	if s[i] == '-' {
		i++
	}
	for i < len(s) {
		ch := s[i]
		if (ch >= '0' && ch <= '9') || ch == '.' || ch == 'e' || ch == 'E' || ch == '+' || ch == '-' {
			i++
			continue
		}
		break
	}
	return i
}

type dbSeries struct {
	operation  string
	driver     string
	table      string
	count      int64
	errorCount int64
	duration   *statBucket
	rows       *statBucket
	lastSeen   time.Time
}

func newDBSeries(operation, driver string, maxValues int) *dbSeries {
	return &dbSeries{
		operation: operation,
		driver:    driver,
		duration:  newStatBucket(maxValues),
		rows:      newStatBucket(maxValues),
	}
}

func newDBTableSeries(table string, maxValues int) *dbSeries {
	return &dbSeries{
		table:    table,
		duration: newStatBucket(maxValues),
		rows:     newStatBucket(maxValues),
	}
}

func (s *dbSeries) record(operation, driver string, durationMS, rows float64, errorHit bool, now time.Time) {
	s.count++
	if errorHit {
		s.errorCount++
	}
	if s.operation == "" {
		s.operation = operation
	}
	if s.driver == "" {
		s.driver = driver
	}
	s.duration.record(durationMS)
	s.rows.record(rows)
	s.lastSeen = now
}

func (s *dbSeries) snapshot() DevDBSeries {
	return DevDBSeries{
		Operation:  s.operation,
		Driver:     s.driver,
		Table:      s.table,
		Count:      s.count,
		ErrorCount: s.errorCount,
		Duration:   s.duration.snapshot(),
		Rows:       s.rows.snapshot(),
		LastSeen:   s.lastSeen,
	}
}
