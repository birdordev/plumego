package testmetrics

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	metrics "github.com/spcent/plumego/metrics"
)

// TestMockCollector_BasicUsage demonstrates basic MockCollector usage
func TestMockCollector_BasicUsage(t *testing.T) {
	mock := NewMockCollector()

	// Make some calls
	ctx := t.Context()
	mock.ObserveHTTP(ctx, "GET", "/api/users", 200, 100, 50*time.Millisecond)
	mock.ObserveHTTP(ctx, "POST", "/api/users", 201, 200, 100*time.Millisecond)

	// Verify call counts
	if got := mock.HTTPCallCount(); got != 2 {
		t.Errorf("HTTPCallCount() = %d, want 2", got)
	}

	// Verify captured calls
	calls := mock.GetHTTPCalls()
	if len(calls) != 2 {
		t.Fatalf("GetHTTPCalls() returned %d calls, want 2", len(calls))
	}

	// Check first call
	if calls[0].Method != "GET" {
		t.Errorf("First call method = %s, want GET", calls[0].Method)
	}
	if calls[0].Path != "/api/users" {
		t.Errorf("First call path = %s, want /api/users", calls[0].Path)
	}
	if calls[0].Status != 200 {
		t.Errorf("First call status = %d, want 200", calls[0].Status)
	}

	// Check last call
	lastCall := mock.GetLastHTTPCall()
	if lastCall == nil {
		t.Fatal("GetLastHTTPCall() returned nil")
	}
	if lastCall.Method != "POST" {
		t.Errorf("Last call method = %s, want POST", lastCall.Method)
	}
}

// TestMockCollector_WithHooks demonstrates using custom hooks
func TestMockCollector_WithHooks(t *testing.T) {
	mock := NewMockCollector()

	// Set up custom hook
	var capturedMethod string
	var capturedPath string
	mock.OnObserveHTTP = func(ctx context.Context, method, path string, status, bytes int, duration time.Duration) {
		capturedMethod = method
		capturedPath = path
	}

	// Make call
	ctx := t.Context()
	mock.ObserveHTTP(ctx, "GET", "/api/health", 200, 50, 10*time.Millisecond)

	// Verify hook was called
	if capturedMethod != "GET" {
		t.Errorf("Hook captured method = %s, want GET", capturedMethod)
	}
	if capturedPath != "/api/health" {
		t.Errorf("Hook captured path = %s, want /api/health", capturedPath)
	}

	// Verify call was also captured
	if got := mock.HTTPCallCount(); got != 1 {
		t.Errorf("HTTPCallCount() = %d, want 1", got)
	}
}

// TestMockCollector_DatabaseCalls demonstrates DB metrics capturing
func TestMockCollector_DatabaseCalls(t *testing.T) {
	mock := NewMockCollector()
	ctx := t.Context()

	// Simulate successful query
	mock.ObserveDB(ctx, "query", "postgres", "SELECT * FROM users WHERE id = $1", 1, 30*time.Millisecond, nil)

	// Simulate failed exec
	mock.ObserveDB(ctx, "exec", "postgres", "INSERT INTO users (name) VALUES ($1)", 0, 20*time.Millisecond, errors.New("constraint violation"))

	// Verify counts
	if got := mock.DBCallCount(); got != 2 {
		t.Errorf("DBCallCount() = %d, want 2", got)
	}

	// Verify captured calls
	calls := mock.GetDBCalls()
	if len(calls) != 2 {
		t.Fatalf("GetDBCalls() returned %d calls, want 2", len(calls))
	}

	// Check successful query
	if calls[0].Operation != "query" {
		t.Errorf("First call operation = %s, want query", calls[0].Operation)
	}
	if calls[0].Driver != "postgres" {
		t.Errorf("First call driver = %s, want postgres", calls[0].Driver)
	}
	if calls[0].Rows != 1 {
		t.Errorf("First call rows = %d, want 1", calls[0].Rows)
	}
	if calls[0].Err != nil {
		t.Errorf("First call err = %v, want nil", calls[0].Err)
	}

	// Check failed exec
	if calls[1].Operation != "exec" {
		t.Errorf("Second call operation = %s, want exec", calls[1].Operation)
	}
	if calls[1].Err == nil {
		t.Error("Second call err = nil, want error")
	}

	// Check last call
	lastCall := mock.GetLastDBCall()
	if lastCall == nil {
		t.Fatal("GetLastDBCall() returned nil")
	}
	if lastCall.Operation != "exec" {
		t.Errorf("Last call operation = %s, want exec", lastCall.Operation)
	}
}

// TestMockCollector_Clear demonstrates clearing captured calls
func TestMockCollector_Clear(t *testing.T) {
	mock := NewMockCollector()
	ctx := t.Context()

	// Make some calls
	mock.ObserveHTTP(ctx, "GET", "/api", 200, 100, 10*time.Millisecond)
	mock.ObserveDB(ctx, "query", "postgres", "SELECT 1", 1, 5*time.Millisecond, nil)

	// Verify calls were captured
	if got := mock.HTTPCallCount(); got != 1 {
		t.Errorf("HTTPCallCount() before clear = %d, want 1", got)
	}
	if got := mock.DBCallCount(); got != 1 {
		t.Errorf("DBCallCount() before clear = %d, want 1", got)
	}

	// Clear
	mock.Clear()

	// Verify calls were cleared
	if got := mock.HTTPCallCount(); got != 0 {
		t.Errorf("HTTPCallCount() after clear = %d, want 0", got)
	}
	if got := mock.DBCallCount(); got != 0 {
		t.Errorf("DBCallCount() after clear = %d, want 0", got)
	}
}

// TestMockCollector_KVCalls demonstrates KV metrics capturing
func TestMockCollector_KVCalls(t *testing.T) {
	mock := NewMockCollector()
	ctx := t.Context()

	// Simulate cache hit
	mock.ObserveKV(ctx, "get", "user:123", 5*time.Millisecond, nil, true)

	// Simulate cache miss
	mock.ObserveKV(ctx, "get", "user:456", 3*time.Millisecond, nil, false)

	// Verify counts
	if got := mock.KVCallCount(); got != 2 {
		t.Errorf("KVCallCount() = %d, want 2", got)
	}

	// Verify hit/miss
	calls := mock.GetKVCalls()
	if !calls[0].Hit {
		t.Error("First call should be a hit")
	}
	if calls[1].Hit {
		t.Error("Second call should be a miss")
	}
}

// TestMockCollector_AllMetricTypes demonstrates all metric types
func TestMockCollector_AllMetricTypes(t *testing.T) {
	mock := NewMockCollector()
	ctx := t.Context()

	// Record different metric types
	mock.ObserveHTTP(ctx, "GET", "/", 200, 100, 10*time.Millisecond)
	mock.ObserveDB(ctx, "query", "postgres", "SELECT 1", 1, 5*time.Millisecond, nil)
	mock.ObserveKV(ctx, "get", "key", 2*time.Millisecond, nil, true)
	mock.ObserveIPC(ctx, "read", "/tmp/sock", "unix", 256, 3*time.Millisecond, nil)
	mock.ObserveMQ(ctx, "publish", "events", 1*time.Millisecond, nil, false)
	mock.ObservePubSub(ctx, "publish", "notifications", 2*time.Millisecond, nil)

	// Verify all counts
	if got := mock.HTTPCallCount(); got != 1 {
		t.Errorf("HTTPCallCount() = %d, want 1", got)
	}
	if got := mock.DBCallCount(); got != 1 {
		t.Errorf("DBCallCount() = %d, want 1", got)
	}
	if got := mock.KVCallCount(); got != 1 {
		t.Errorf("KVCallCount() = %d, want 1", got)
	}
	if got := mock.IPCCallCount(); got != 1 {
		t.Errorf("IPCCallCount() = %d, want 1", got)
	}
	if got := mock.MQCallCount(); got != 1 {
		t.Errorf("MQCallCount() = %d, want 1", got)
	}
	if got := mock.PubSubCallCount(); got != 1 {
		t.Errorf("PubSubCallCount() = %d, want 1", got)
	}
}

// TestMockCollector_ConcurrentAccess tests thread-safety
func TestMockCollector_ConcurrentAccess(t *testing.T) {
	mock := NewMockCollector()
	ctx := t.Context()

	// Spawn multiple goroutines
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				mock.ObserveHTTP(ctx, "GET", "/", 200, 100, 10*time.Millisecond)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify count
	if got := mock.HTTPCallCount(); got != 1000 {
		t.Errorf("HTTPCallCount() = %d, want 1000", got)
	}
}

// Example_mockCollector_embedding demonstrates embedding NoopCollector
func Example_mockCollector_embedding() {
	// Create a custom mock by embedding NoopCollector and overriding only the
	// methods you care about. Everything else falls through to the embedded
	// no-op collector.
	type customMock struct {
		*NoopCollector
	}

	mock := &customMock{
		NoopCollector: metrics.NewNoopCollector(),
	}

	// The embedded NoopCollector keeps default metric calls safe no-ops.
	mock.ObserveHTTP(context.Background(), "GET", "/", 200, 0, 0)

	// Output:
}

// Example_mockCollector_hooks demonstrates using hooks
func Example_mockCollector_hooks() {
	mock := NewMockCollector()

	// Set up a hook to observe the operation without a panic.
	mock.OnObserveDB = func(ctx context.Context, operation, driver, query string, rows int, duration time.Duration, err error) {
		fmt.Printf("observed %s on %s\n", operation, driver)
	}

	// Use the mock
	ctx := context.Background()
	mock.ObserveDB(ctx, "query", "postgres", "SELECT 1", 1, 5*time.Millisecond, nil)

	// Output:
	// observed query on postgres
}
