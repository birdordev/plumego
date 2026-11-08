package gateway

import (
	"errors"
	"sync"
	"testing"

	"github.com/birdordev/plumego/x/resilience/circuitbreaker"
)

// TestNewBackend verifies backend creation with URL parsing.
func TestNewBackend(t *testing.T) {
	b, err := NewBackend("http://localhost:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.URL != "http://localhost:8080" {
		t.Errorf("URL = %q, want %q", b.URL, "http://localhost:8080")
	}
	if b.ParsedURL == nil {
		t.Fatal("ParsedURL is nil")
	}
	if b.ParsedURL.Host != "localhost:8080" {
		t.Errorf("Host = %q, want %q", b.ParsedURL.Host, "localhost:8080")
	}
	if b.Weight != 1 {
		t.Errorf("Weight = %d, want 1", b.Weight)
	}
	if !b.IsHealthy() {
		t.Error("new backend should be healthy by default")
	}
}

func TestNewBackendInvalidURL(t *testing.T) {
	_, err := NewBackend("://invalid")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestBackendSetHealthy(t *testing.T) {
	b, _ := NewBackend("http://localhost:8080")

	b.SetHealthy(false)
	if b.IsHealthy() {
		t.Error("expected unhealthy")
	}

	// RecordFailure to build up consecutive count
	b.consecutiveFailures.Store(5)
	b.SetHealthy(true)
	if !b.IsHealthy() {
		t.Error("expected healthy after SetHealthy(true)")
	}
	if b.consecutiveFailures.Load() != 0 {
		t.Error("consecutive failures should be reset on recovery")
	}
}

func TestBackendRecordSuccess(t *testing.T) {
	b, _ := NewBackend("http://localhost:8080")
	b.SetHealthy(false)
	b.consecutiveFailures.Store(3)

	b.RecordSuccess()

	if b.totalRequests.Load() != 1 {
		t.Errorf("totalRequests = %d, want 1", b.totalRequests.Load())
	}
	if b.totalSuccesses.Load() != 1 {
		t.Errorf("totalSuccesses = %d, want 1", b.totalSuccesses.Load())
	}
	if b.consecutiveFailures.Load() != 0 {
		t.Error("consecutiveFailures should be reset after success")
	}
	if !b.IsHealthy() {
		t.Error("backend should auto-recover to healthy after success")
	}
}

func TestBackendRecordFailure(t *testing.T) {
	b, _ := NewBackend("http://localhost:8080")

	// Below threshold: stays healthy
	b.RecordFailure(3)
	if !b.IsHealthy() {
		t.Error("should still be healthy below threshold")
	}
	if b.totalRequests.Load() != 1 {
		t.Errorf("totalRequests = %d, want 1", b.totalRequests.Load())
	}
	if b.totalFailures.Load() != 1 {
		t.Errorf("totalFailures = %d, want 1", b.totalFailures.Load())
	}

	// Reach threshold: becomes unhealthy
	b.RecordFailure(3)
	b.RecordFailure(3)
	if b.IsHealthy() {
		t.Error("should be unhealthy at threshold")
	}
}

func TestBackendStats(t *testing.T) {
	b, _ := NewBackend("http://localhost:9090")
	b.Weight = 5
	b.RecordSuccess()
	b.RecordFailure(10)

	s := b.Stats()
	if s.URL != "http://localhost:9090" {
		t.Errorf("Stats URL = %q, want %q", s.URL, "http://localhost:9090")
	}
	if s.Weight != 5 {
		t.Errorf("Stats Weight = %d, want 5", s.Weight)
	}
	if s.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2", s.TotalRequests)
	}
	if s.TotalSuccesses != 1 {
		t.Errorf("TotalSuccesses = %d, want 1", s.TotalSuccesses)
	}
	if s.TotalFailures != 1 {
		t.Errorf("TotalFailures = %d, want 1", s.TotalFailures)
	}
}

func TestBackendCircuitBreaker(t *testing.T) {
	b, _ := NewBackend("http://localhost:8080")

	if b.HasCircuitBreaker() {
		t.Error("new backend should have no circuit breaker")
	}

	// Execute without circuit breaker
	called := false
	err := b.Execute(func() error { called = true; return nil })
	if err != nil || !called {
		t.Errorf("Execute without CB: err=%v called=%v", err, called)
	}

	// Set a stub circuit breaker
	stub := &stubCB{err: errors.New("circuit open")}
	b.SetCircuitBreaker(stub)
	if !b.HasCircuitBreaker() {
		t.Error("expected circuit breaker to be set")
	}

	err = b.Execute(func() error { return nil })
	if err == nil || err.Error() != "circuit open" {
		t.Errorf("expected circuit open error, got %v", err)
	}
}

// stubCB implements CircuitBreaker for testing.
type stubCB struct {
	err error
}

func (s *stubCB) Call(fn func() error) error {
	if s.err != nil {
		return s.err
	}
	return fn()
}

func (s *stubCB) State() circuitbreaker.State { return circuitbreaker.StateClosed }
func (s *stubCB) Stats() circuitbreaker.Stats { return circuitbreaker.Stats{} }
func (s *stubCB) Reset()                      {}
func (s *stubCB) Trip()                       {}

// TestBackendConcurrentStats verifies atomic counters under concurrent load.
func TestBackendConcurrentStats(t *testing.T) {
	b, _ := NewBackend("http://localhost:8080")
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			b.RecordSuccess()
		}()
		go func() {
			defer wg.Done()
			b.RecordFailure(1000)
		}()
	}
	wg.Wait()

	s := b.Stats()
	if s.TotalRequests != uint64(goroutines*2) {
		t.Errorf("TotalRequests = %d, want %d", s.TotalRequests, goroutines*2)
	}
	if s.TotalSuccesses != uint64(goroutines) {
		t.Errorf("TotalSuccesses = %d, want %d", s.TotalSuccesses, goroutines)
	}
	if s.TotalFailures != uint64(goroutines) {
		t.Errorf("TotalFailures = %d, want %d", s.TotalFailures, goroutines)
	}
}

// --- BackendPool tests ---

func TestNewBackendPool(t *testing.T) {
	urls := []string{"http://a:8080", "http://b:8080", "http://c:8080"}
	pool, err := NewBackendPool(urls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pool.Count() != 3 {
		t.Errorf("Count = %d, want 3", pool.Count())
	}
	if pool.HealthyCount() != 3 {
		t.Errorf("HealthyCount = %d, want 3", pool.HealthyCount())
	}
}

func TestNewBackendPoolInvalidURL(t *testing.T) {
	_, err := NewBackendPool([]string{"://bad"})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestBackendPoolHealthyBackends(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://a:8080", "http://b:8080", "http://c:8080"})

	pool.Backends()[1].SetHealthy(false)

	healthy := pool.HealthyBackends()
	if len(healthy) != 2 {
		t.Errorf("HealthyBackends = %d, want 2", len(healthy))
	}
	if pool.HealthyCount() != 2 {
		t.Errorf("HealthyCount = %d, want 2", pool.HealthyCount())
	}
}

func TestBackendPoolAddRemove(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://a:8080"})

	b, _ := NewBackend("http://b:8080")
	pool.AddBackend(b)
	if pool.Count() != 2 {
		t.Errorf("after Add: Count = %d, want 2", pool.Count())
	}

	removed := pool.RemoveBackend("http://a:8080")
	if !removed {
		t.Error("expected removal to succeed")
	}
	if pool.Count() != 1 {
		t.Errorf("after Remove: Count = %d, want 1", pool.Count())
	}

	// Remove non-existent
	if pool.RemoveBackend("http://missing:8080") {
		t.Error("expected false for non-existent backend")
	}
}

func TestBackendPoolGetBackend(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://a:8080", "http://b:8080"})

	got := pool.GetBackend("http://a:8080")
	if got == nil {
		t.Fatal("expected backend for http://a:8080")
	}
	if got.URL != "http://a:8080" {
		t.Errorf("URL = %q", got.URL)
	}

	if pool.GetBackend("http://missing:8080") != nil {
		t.Error("expected nil for missing backend")
	}
}

func TestBackendPoolUpdateBackends(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://a:8080"})

	err := pool.UpdateBackends([]string{"http://x:9090", "http://y:9090"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pool.Count() != 2 {
		t.Errorf("Count after update = %d, want 2", pool.Count())
	}
	if pool.GetBackend("http://a:8080") != nil {
		t.Error("old backend should be removed")
	}
}

func TestBackendPoolStats(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://a:8080", "http://b:8080"})
	pool.Backends()[0].RecordSuccess()
	pool.Backends()[1].RecordFailure(10)

	stats := pool.Stats()
	if len(stats) != 2 {
		t.Fatalf("Stats len = %d, want 2", len(stats))
	}
	if stats[0].TotalSuccesses != 1 {
		t.Errorf("stats[0].TotalSuccesses = %d, want 1", stats[0].TotalSuccesses)
	}
	if stats[1].TotalFailures != 1 {
		t.Errorf("stats[1].TotalFailures = %d, want 1", stats[1].TotalFailures)
	}
}

func TestBackendPoolBackendsCopy(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://a:8080", "http://b:8080"})

	got := pool.Backends()
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	// Mutate the returned slice — should not affect pool
	got[0] = nil
	if pool.Backends()[0] == nil {
		t.Error("pool internal slice should not be mutated via returned copy")
	}
}

func TestBackendPoolConcurrentAddRemove(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://a:8080"})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _ := NewBackend("http://tmp:8080")
			pool.AddBackend(b)
			pool.RemoveBackend("http://tmp:8080")
		}()
	}
	wg.Wait()
}
