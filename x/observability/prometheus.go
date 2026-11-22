package observability

import (
	"container/heap"
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/metrics"
)

type labelKey struct {
	method string
	path   string
	status string
}

type latencyStats struct {
	count uint64
	sum   float64
	min   float64
	max   float64
}

type PrometheusCollector struct {
	base *metrics.BaseMetricsCollector

	namespace string
	maxMemory int

	mu            sync.RWMutex
	totalRequests uint64
	requests      map[labelKey]uint64
	durations     map[labelKey]latencyStats
	startTime     time.Time
}

func NewPrometheusCollector(namespace string) *PrometheusCollector {
	if namespace == "" {
		namespace = "plumego"
	}
	return &PrometheusCollector{
		base:      metrics.NewBaseMetricsCollector(),
		namespace: namespace,
		maxMemory: 10000,
		requests:  make(map[labelKey]uint64),
		durations: make(map[labelKey]latencyStats),
		startTime: time.Now(),
	}
}

// WithMaxMemory sets the series limit and immediately evicts excess series.
func (p *PrometheusCollector) WithMaxMemory(max int) *PrometheusCollector {
	if max > 0 {
		p.mu.Lock()
		p.maxMemory = max
		if excess := len(p.requests) - max; excess > 0 {
			p.evictSeries(excess)
		}
		p.mu.Unlock()
	}
	return p
}

func (p *PrometheusCollector) Handler() http.Handler {
	return (&PrometheusExporter{collector: p}).Handler()
}

func (p *PrometheusCollector) Record(ctx context.Context, record metrics.MetricRecord) {
	if record.Name == metrics.MetricHTTPRequest {
		method, _ := record.Labels["method"]
		path, _ := record.Labels["path"]
		statusStr, _ := record.Labels["status"]
		status, _ := strconv.Atoi(statusStr)
		if method != "" && path != "" && status != 0 {
			p.recordHTTP(method, path, status, record.Duration)
			return
		}
	}
	p.base.Record(ctx, record)
}

func (p *PrometheusCollector) ObserveHTTP(_ context.Context, method, path string, status, _ int, duration time.Duration) {
	p.recordHTTP(method, path, status, duration)
}

func (p *PrometheusCollector) GetStats() metrics.CollectorStats {
	p.mu.RLock()
	totalRequests := uint64(0)
	errorRecords := int64(0)
	for key, count := range p.requests {
		totalRequests += count
		status, err := strconv.Atoi(key.status)
		if err == nil && status >= http.StatusBadRequest {
			errorRecords += int64(count)
		}
	}
	startTime := p.startTime
	activeSeries := len(p.requests)
	p.mu.RUnlock()

	stats := metrics.CollectorStats{
		TotalRecords:  int64(totalRequests),
		ErrorRecords:  errorRecords,
		ActiveSeries:  activeSeries,
		StartTime:     startTime,
		NameBreakdown: make(map[string]int64),
	}
	if totalRequests > 0 {
		stats.NameBreakdown[metrics.MetricHTTPRequest] = int64(totalRequests)
	}

	baseStats := p.base.GetStats()
	stats.TotalRecords += baseStats.TotalRecords
	stats.ErrorRecords += baseStats.ErrorRecords
	stats.ActiveSeries += baseStats.ActiveSeries
	if stats.StartTime.IsZero() || (!baseStats.StartTime.IsZero() && baseStats.StartTime.Before(stats.StartTime)) {
		stats.StartTime = baseStats.StartTime
	}
	for key, value := range baseStats.NameBreakdown {
		stats.NameBreakdown[key] += value
	}

	return stats
}

func (p *PrometheusCollector) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.totalRequests = 0
	p.requests = make(map[labelKey]uint64)
	p.durations = make(map[labelKey]latencyStats)
	p.startTime = time.Now()
	p.base.Clear()
}

type prometheusSeries struct {
	key      labelKey
	count    uint64
	duration latencyStats
}

func (p *PrometheusCollector) snapshot() ([]prometheusSeries, uint64, time.Duration) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	series := make([]prometheusSeries, 0, len(p.requests))
	for key, count := range p.requests {
		series = append(series, prometheusSeries{key, count, p.durations[key]})
	}
	return series, p.totalRequests, time.Since(p.startTime)
}

func (p *PrometheusCollector) recordHTTP(method, path string, status int, duration time.Duration) {
	key := labelKey{method: method, path: path, status: strconv.Itoa(status)}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.requests[key]; !exists && p.maxMemory > 0 && len(p.requests) >= p.maxMemory {
		p.evictLeastUsed()
	}

	p.requests[key]++
	p.totalRequests++

	seconds := duration.Seconds()
	stats := p.durations[key]
	stats.count++
	stats.sum += seconds
	if stats.count == 1 || seconds < stats.min {
		stats.min = seconds
	}
	if stats.count == 1 || seconds > stats.max {
		stats.max = seconds
	}
	p.durations[key] = stats
}

func (p *PrometheusCollector) evictLeastUsed() {
	p.evictSeries(max(1, len(p.requests)/10))
}

func (p *PrometheusCollector) evictSeries(count int) {
	if count >= len(p.requests) {
		clear(p.requests)
		clear(p.durations)
		return
	}
	candidates := seriesEvictions{keys: make([]labelKey, 0, count), requests: p.requests}
	for key := range p.requests {
		if len(candidates.keys) < count {
			heap.Push(&candidates, key)
			continue
		}
		if candidates.before(key, candidates.keys[0]) {
			candidates.keys[0] = key
			heap.Fix(&candidates, 0)
		}
	}
	for _, key := range candidates.keys {
		delete(p.requests, key)
		delete(p.durations, key)
	}
}

type seriesEvictions struct {
	keys     []labelKey
	requests map[labelKey]uint64
}

func (s seriesEvictions) before(a, b labelKey) bool {
	if s.requests[a] != s.requests[b] {
		return s.requests[a] < s.requests[b]
	}
	if a.method != b.method {
		return a.method < b.method
	}
	if a.path != b.path {
		return a.path < b.path
	}
	return a.status < b.status
}
func (s seriesEvictions) Len() int           { return len(s.keys) }
func (s seriesEvictions) Less(i, j int) bool { return s.before(s.keys[j], s.keys[i]) }
func (s seriesEvictions) Swap(i, j int)      { s.keys[i], s.keys[j] = s.keys[j], s.keys[i] }
func (s *seriesEvictions) Push(value any)    { s.keys = append(s.keys, value.(labelKey)) }
func (s *seriesEvictions) Pop() any {
	last := len(s.keys) - 1
	key := s.keys[last]
	s.keys[last] = labelKey{}
	s.keys = s.keys[:last]
	return key
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

var _ metrics.Recorder = (*PrometheusCollector)(nil)
var _ metrics.HTTPObserver = (*PrometheusCollector)(nil)
var _ metrics.StatsReader = (*PrometheusCollector)(nil)
var _ metrics.Resetter = (*PrometheusCollector)(nil)
