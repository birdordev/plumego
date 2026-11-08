package testmetrics

import (
	"context"
	"sync"
	"time"

	metrics "github.com/birdordev/plumego/metrics"
)

type (
	AggregateCollector = metrics.AggregateCollector
	CollectorStats     = metrics.CollectorStats
	MetricRecord       = metrics.MetricRecord
	NoopCollector      = metrics.NoopCollector
)

// MockCollector is a configurable mock implementation of AggregateCollector for testing.
// It embeds NoopCollector and allows selective override of methods through function fields.
//
// This collector is useful for:
//   - Unit testing components that use metrics
//   - Verifying that metrics are recorded correctly
//   - Capturing metric calls for assertions
//
// Example - Simple usage with embedding:
//
//	type myMock struct {
//	    *metrics.NoopCollector
//	    httpCalls int
//	}
//
//	func (m *myMock) ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
//	    m.httpCalls++
//	}
//
// Example - Advanced usage with MockCollector:
//
//	mock := testmetrics.NewMockCollector()
//	mock.OnObserveHTTP = func(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
//	    // Verify expected values
//	    if method != "GET" {
//	        t.Errorf("expected GET, got %s", method)
//	    }
//	}
//
//	// Use the mock
//	mock.ObserveHTTP(ctx, "GET", "/api", 200, 100, 10*time.Millisecond)
//
//	// Check captured calls
//	if mock.HTTPCallCount() != 1 {
//	    t.Error("expected 1 HTTP call")
//	}
type MockCollector struct {
	NoopCollector // Embed NoopCollector for default no-op implementations

	mu sync.RWMutex

	// Captured calls
	records     []MetricRecord
	httpCalls   []HTTPCall
	dbCalls     []DBCall
	kvCalls     []KVCall
	ipcCalls    []IPCCall
	mqCalls     []MQCall
	pubsubCalls []PubSubCall

	// Configurable hooks (optional)
	OnRecord        func(ctx context.Context, record MetricRecord)
	OnObserveHTTP   func(ctx context.Context, method, path string, status, bytes int, duration time.Duration)
	OnObserveDB     func(ctx context.Context, operation, driver, query string, rows int, duration time.Duration, err error)
	OnObserveKV     func(ctx context.Context, operation, key string, duration time.Duration, err error, hit bool)
	OnObserveIPC    func(ctx context.Context, operation, addr, transport string, bytes int, duration time.Duration, err error)
	OnObserveMQ     func(ctx context.Context, operation, topic string, duration time.Duration, err error, panicked bool)
	OnObservePubSub func(ctx context.Context, operation, topic string, duration time.Duration, err error)
	OnGetStats      func() CollectorStats
	OnClear         func()
}

// Call types for capturing invocations
type HTTPCall struct {
	Method   string
	Path     string
	Status   int
	Bytes    int
	Duration time.Duration
}

type DBCall struct {
	Operation string
	Driver    string
	Query     string
	Rows      int
	Duration  time.Duration
	Err       error
}

type KVCall struct {
	Operation string
	Key       string
	Duration  time.Duration
	Err       error
	Hit       bool
}

type IPCCall struct {
	Operation string
	Addr      string
	Transport string
	Bytes     int
	Duration  time.Duration
	Err       error
}

type MQCall struct {
	Operation string
	Topic     string
	Duration  time.Duration
	Err       error
	Panicked  bool
}

type PubSubCall struct {
	Operation string
	Topic     string
	Duration  time.Duration
	Err       error
}

// NewMockCollector creates a new mock collector for testing.
//
// Example:
//
//	mock := testmetrics.NewMockCollector()
//	mock.OnObserveHTTP = func(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
//	    // Custom verification logic
//	}
func NewMockCollector() *MockCollector {
	return &MockCollector{
		records:     make([]MetricRecord, 0),
		httpCalls:   make([]HTTPCall, 0),
		dbCalls:     make([]DBCall, 0),
		kvCalls:     make([]KVCall, 0),
		ipcCalls:    make([]IPCCall, 0),
		mqCalls:     make([]MQCall, 0),
		pubsubCalls: make([]PubSubCall, 0),
	}
}

// Record captures the metric record and optionally calls the OnRecord hook.
func (m *MockCollector) Record(ctx context.Context, record MetricRecord) {
	m.mu.Lock()
	m.records = append(m.records, record)
	hook := m.OnRecord
	m.mu.Unlock()

	if hook != nil {
		hook(ctx, record)
	}
}

// ObserveHTTP captures HTTP metrics and optionally calls the OnObserveHTTP hook.
func (m *MockCollector) ObserveHTTP(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
	m.mu.Lock()
	m.httpCalls = append(m.httpCalls, HTTPCall{
		Method:   method,
		Path:     path,
		Status:   status,
		Bytes:    bytes,
		Duration: duration,
	})
	hook := m.OnObserveHTTP
	m.mu.Unlock()

	if hook != nil {
		hook(ctx, method, path, status, bytes, duration)
	}
}

// ObserveDB captures database metrics and optionally calls the OnObserveDB hook.
func (m *MockCollector) ObserveDB(ctx context.Context, operation, driver, query string, rows int, duration time.Duration, err error) {
	m.mu.Lock()
	m.dbCalls = append(m.dbCalls, DBCall{
		Operation: operation,
		Driver:    driver,
		Query:     query,
		Rows:      rows,
		Duration:  duration,
		Err:       err,
	})
	hook := m.OnObserveDB
	m.mu.Unlock()

	if hook != nil {
		hook(ctx, operation, driver, query, rows, duration, err)
	}
}

// ObserveKV captures KV metrics and optionally calls the OnObserveKV hook.
func (m *MockCollector) ObserveKV(ctx context.Context, operation, key string, duration time.Duration, err error, hit bool) {
	m.mu.Lock()
	m.kvCalls = append(m.kvCalls, KVCall{
		Operation: operation,
		Key:       key,
		Duration:  duration,
		Err:       err,
		Hit:       hit,
	})
	hook := m.OnObserveKV
	m.mu.Unlock()

	if hook != nil {
		hook(ctx, operation, key, duration, err, hit)
	}
}

// ObserveIPC captures IPC metrics and optionally calls the OnObserveIPC hook.
func (m *MockCollector) ObserveIPC(ctx context.Context, operation, addr, transport string, bytes int, duration time.Duration, err error) {
	m.mu.Lock()
	m.ipcCalls = append(m.ipcCalls, IPCCall{
		Operation: operation,
		Addr:      addr,
		Transport: transport,
		Bytes:     bytes,
		Duration:  duration,
		Err:       err,
	})
	hook := m.OnObserveIPC
	m.mu.Unlock()

	if hook != nil {
		hook(ctx, operation, addr, transport, bytes, duration, err)
	}
}

// ObserveMQ captures MQ metrics and optionally calls the OnObserveMQ hook.
func (m *MockCollector) ObserveMQ(ctx context.Context, operation, topic string, duration time.Duration, err error, panicked bool) {
	m.mu.Lock()
	m.mqCalls = append(m.mqCalls, MQCall{
		Operation: operation,
		Topic:     topic,
		Duration:  duration,
		Err:       err,
		Panicked:  panicked,
	})
	hook := m.OnObserveMQ
	m.mu.Unlock()

	if hook != nil {
		hook(ctx, operation, topic, duration, err, panicked)
	}
}

// ObservePubSub captures PubSub metrics and optionally calls the OnObservePubSub hook.
func (m *MockCollector) ObservePubSub(ctx context.Context, operation, topic string, duration time.Duration, err error) {
	m.mu.Lock()
	m.pubsubCalls = append(m.pubsubCalls, PubSubCall{
		Operation: operation,
		Topic:     topic,
		Duration:  duration,
		Err:       err,
	})
	hook := m.OnObservePubSub
	m.mu.Unlock()

	if hook != nil {
		hook(ctx, operation, topic, duration, err)
	}
}

// GetStats returns mock stats or calls the OnGetStats hook if configured.
func (m *MockCollector) GetStats() CollectorStats {
	m.mu.RLock()
	hook := m.OnGetStats
	recordCount := len(m.records)
	m.mu.RUnlock()

	if hook != nil {
		return hook()
	}

	return CollectorStats{
		TotalRecords:  int64(recordCount),
		NameBreakdown: make(map[string]int64),
	}
}

// Clear resets all captured calls and optionally calls the OnClear hook.
func (m *MockCollector) Clear() {
	m.mu.Lock()
	m.records = m.records[:0]
	m.httpCalls = m.httpCalls[:0]
	m.dbCalls = m.dbCalls[:0]
	m.kvCalls = m.kvCalls[:0]
	m.ipcCalls = m.ipcCalls[:0]
	m.mqCalls = m.mqCalls[:0]
	m.pubsubCalls = m.pubsubCalls[:0]
	hook := m.OnClear
	m.mu.Unlock()

	if hook != nil {
		hook()
	}
}

// Call count methods for assertions

// RecordCallCount returns the number of Record calls.
func (m *MockCollector) RecordCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.records)
}

// HTTPCallCount returns the number of ObserveHTTP calls.
func (m *MockCollector) HTTPCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.httpCalls)
}

// DBCallCount returns the number of ObserveDB calls.
func (m *MockCollector) DBCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.dbCalls)
}

// KVCallCount returns the number of ObserveKV calls.
func (m *MockCollector) KVCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.kvCalls)
}

// IPCCallCount returns the number of ObserveIPC calls.
func (m *MockCollector) IPCCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.ipcCalls)
}

// MQCallCount returns the number of ObserveMQ calls.
func (m *MockCollector) MQCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.mqCalls)
}

// PubSubCallCount returns the number of ObservePubSub calls.
func (m *MockCollector) PubSubCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pubsubCalls)
}

// Getter methods for captured calls

// GetRecords returns a copy of all captured metric records.
func (m *MockCollector) GetRecords() []MetricRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]MetricRecord, len(m.records))
	copy(result, m.records)
	return result
}

// GetHTTPCalls returns a copy of all captured HTTP calls.
func (m *MockCollector) GetHTTPCalls() []HTTPCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]HTTPCall, len(m.httpCalls))
	copy(result, m.httpCalls)
	return result
}

// GetDBCalls returns a copy of all captured database calls.
func (m *MockCollector) GetDBCalls() []DBCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]DBCall, len(m.dbCalls))
	copy(result, m.dbCalls)
	return result
}

// GetKVCalls returns a copy of all captured KV calls.
func (m *MockCollector) GetKVCalls() []KVCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]KVCall, len(m.kvCalls))
	copy(result, m.kvCalls)
	return result
}

// GetIPCCalls returns a copy of all captured IPC calls.
func (m *MockCollector) GetIPCCalls() []IPCCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]IPCCall, len(m.ipcCalls))
	copy(result, m.ipcCalls)
	return result
}

// GetMQCalls returns a copy of all captured MQ calls.
func (m *MockCollector) GetMQCalls() []MQCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]MQCall, len(m.mqCalls))
	copy(result, m.mqCalls)
	return result
}

// GetPubSubCalls returns a copy of all captured PubSub calls.
func (m *MockCollector) GetPubSubCalls() []PubSubCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PubSubCall, len(m.pubsubCalls))
	copy(result, m.pubsubCalls)
	return result
}

// GetLastHTTPCall returns the most recent HTTP call, or nil if none.
func (m *MockCollector) GetLastHTTPCall() *HTTPCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.httpCalls) == 0 {
		return nil
	}
	call := m.httpCalls[len(m.httpCalls)-1]
	return &call
}

// GetLastDBCall returns the most recent DB call, or nil if none.
func (m *MockCollector) GetLastDBCall() *DBCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.dbCalls) == 0 {
		return nil
	}
	call := m.dbCalls[len(m.dbCalls)-1]
	return &call
}

// Verify that MockCollector implements AggregateCollector at compile time.
var _ AggregateCollector = (*MockCollector)(nil)
