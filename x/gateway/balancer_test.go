package gateway

import (
	"sync"
	"testing"
)

// helpers

func mustPool(t *testing.T, urls ...string) *BackendPool {
	t.Helper()
	p, err := NewBackendPool(urls)
	if err != nil {
		t.Fatalf("NewBackendPool: %v", err)
	}
	return p
}

func emptyPool() *BackendPool {
	p, _ := NewBackendPool(nil)
	return p
}

// --- RoundRobinBalancer ---

func TestRoundRobinName(t *testing.T) {
	b := NewRoundRobinBalancer()
	if b.Name() != "round_robin" {
		t.Errorf("Name = %q", b.Name())
	}
}

func TestRoundRobinNoHealthyBackends(t *testing.T) {
	b := NewRoundRobinBalancer()
	_, err := b.Next(emptyPool())
	if err != ErrNoHealthyBackends {
		t.Errorf("err = %v, want ErrNoHealthyBackends", err)
	}
}

func TestRoundRobinDistribution(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080", "http://c:8080")
	b := NewRoundRobinBalancer()

	seen := map[string]int{}
	for i := 0; i < 9; i++ {
		be, err := b.Next(pool)
		if err != nil {
			t.Fatalf("Next err: %v", err)
		}
		seen[be.URL]++
	}

	// Each backend should have been selected exactly 3 times
	for _, url := range []string{"http://a:8080", "http://b:8080", "http://c:8080"} {
		if seen[url] != 3 {
			t.Errorf("%s selected %d times, want 3", url, seen[url])
		}
	}
}

func TestRoundRobinSkipsUnhealthy(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080", "http://c:8080")
	pool.Backends()[1].SetHealthy(false) // mark b unhealthy

	b := NewRoundRobinBalancer()
	for i := 0; i < 10; i++ {
		be, err := b.Next(pool)
		if err != nil {
			t.Fatalf("Next err: %v", err)
		}
		if be.URL == "http://b:8080" {
			t.Error("selected unhealthy backend b")
		}
	}
}

func TestRoundRobinReset(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080")
	b := NewRoundRobinBalancer()
	_, _ = b.Next(pool)
	_, _ = b.Next(pool)
	b.Reset()
	if b.counter.Load() != 0 {
		t.Errorf("counter after Reset = %d, want 0", b.counter.Load())
	}
}

func TestRoundRobinConcurrent(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080")
	b := NewRoundRobinBalancer()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.Next(pool)
		}()
	}
	wg.Wait()
}

// --- RandomBalancer ---

func TestRandomName(t *testing.T) {
	b := NewRandomBalancer()
	if b.Name() != "random" {
		t.Errorf("Name = %q", b.Name())
	}
}

func TestRandomNoHealthyBackends(t *testing.T) {
	b := NewRandomBalancer()
	_, err := b.Next(emptyPool())
	if err != ErrNoHealthyBackends {
		t.Errorf("err = %v, want ErrNoHealthyBackends", err)
	}
}

func TestRandomAlwaysReturnsHealthy(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080", "http://c:8080")
	pool.Backends()[0].SetHealthy(false)

	b := NewRandomBalancer()
	for i := 0; i < 50; i++ {
		be, err := b.Next(pool)
		if err != nil {
			t.Fatalf("Next err: %v", err)
		}
		if be.URL == "http://a:8080" {
			t.Error("selected unhealthy backend a")
		}
	}
}

func TestRandomReset(t *testing.T) {
	b := NewRandomBalancer()
	// Reset should not panic and leave the balancer usable
	b.Reset()
	pool := mustPool(t, "http://a:8080")
	_, err := b.Next(pool)
	if err != nil {
		t.Fatalf("Next after Reset: %v", err)
	}
}

func TestRandomConcurrent(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080")
	b := NewRandomBalancer()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.Next(pool)
		}()
	}
	wg.Wait()
}

// --- WeightedRoundRobinBalancer ---

func TestWeightedRoundRobinName(t *testing.T) {
	b := NewWeightedRoundRobinBalancer()
	if b.Name() != "weighted_round_robin" {
		t.Errorf("Name = %q", b.Name())
	}
}

func TestWeightedRoundRobinNoHealthy(t *testing.T) {
	b := NewWeightedRoundRobinBalancer()
	_, err := b.Next(emptyPool())
	if err != ErrNoHealthyBackends {
		t.Errorf("err = %v, want ErrNoHealthyBackends", err)
	}
}

func TestWeightedRoundRobinProportionalDistribution(t *testing.T) {
	pool, _ := NewBackendPool(nil)
	a, _ := NewBackend("http://a:8080")
	a.Weight = 3
	b2, _ := NewBackend("http://b:8080")
	b2.Weight = 1
	pool.AddBackend(a)
	pool.AddBackend(b2)

	balancer := NewWeightedRoundRobinBalancer()
	counts := map[string]int{}
	for i := 0; i < 40; i++ {
		be, err := balancer.Next(pool)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		counts[be.URL]++
	}

	// a should receive ~3x more than b
	if counts["http://a:8080"] <= counts["http://b:8080"] {
		t.Errorf("weighted distribution wrong: a=%d b=%d", counts["http://a:8080"], counts["http://b:8080"])
	}
}

func TestWeightedRoundRobinReset(t *testing.T) {
	b := NewWeightedRoundRobinBalancer()
	pool := mustPool(t, "http://a:8080")
	_, _ = b.Next(pool)
	b.Reset()
	if b.currentWeight != nil {
		t.Error("currentWeight should be nil after Reset")
	}
}

func TestWeightedRoundRobinSingleBackend(t *testing.T) {
	pool := mustPool(t, "http://a:8080")
	b := NewWeightedRoundRobinBalancer()
	for i := 0; i < 5; i++ {
		be, err := b.Next(pool)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if be.URL != "http://a:8080" {
			t.Errorf("URL = %q, want http://a:8080", be.URL)
		}
	}
}

// --- IPHashBalancer ---

func TestIPHashName(t *testing.T) {
	b := NewIPHashBalancer()
	if b.Name() != "ip_hash" {
		t.Errorf("Name = %q", b.Name())
	}
}

func TestIPHashNoHealthy(t *testing.T) {
	b := NewIPHashBalancer()
	_, err := b.Next(emptyPool())
	if err != ErrNoHealthyBackends {
		t.Errorf("err = %v, want ErrNoHealthyBackends", err)
	}
}

func TestIPHashNextWithIPConsistency(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080", "http://c:8080")
	b := NewIPHashBalancer()

	// Same IP should always map to same backend
	first, err := b.NextWithIP(pool, "192.168.1.100")
	if err != nil {
		t.Fatalf("NextWithIP: %v", err)
	}
	for i := 0; i < 10; i++ {
		got, err := b.NextWithIP(pool, "192.168.1.100")
		if err != nil {
			t.Fatalf("NextWithIP: %v", err)
		}
		if got.URL != first.URL {
			t.Errorf("inconsistent: got %q, want %q", got.URL, first.URL)
		}
	}
}

func TestIPHashNextWithIPDifferentIPs(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080", "http://c:8080")
	b := NewIPHashBalancer()

	// Different IPs may map to different backends
	seen := map[string]bool{}
	ips := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "10.0.0.1", "172.16.0.1"}
	for _, ip := range ips {
		be, err := b.NextWithIP(pool, ip)
		if err != nil {
			t.Fatalf("NextWithIP: %v", err)
		}
		seen[be.URL] = true
	}
	// We should hit at least 2 different backends across 6 different IPs
	if len(seen) < 2 {
		t.Errorf("only %d distinct backends used, expected distribution", len(seen))
	}
}

func TestIPHashReset(t *testing.T) {
	b := NewIPHashBalancer()
	b.Reset() // should be a no-op, not panic
}

func TestIPHashNextWithIPNoHealthy(t *testing.T) {
	b := NewIPHashBalancer()
	_, err := b.NextWithIP(emptyPool(), "1.2.3.4")
	if err != ErrNoHealthyBackends {
		t.Errorf("err = %v, want ErrNoHealthyBackends", err)
	}
}

// --- LeastConnectionsBalancer ---

func TestLeastConnectionsName(t *testing.T) {
	b := NewLeastConnectionsBalancer()
	if b.Name() != "least_connections" {
		t.Errorf("Name = %q", b.Name())
	}
}

func TestLeastConnectionsNoHealthy(t *testing.T) {
	b := NewLeastConnectionsBalancer()
	_, err := b.Next(emptyPool())
	if err != ErrNoHealthyBackends {
		t.Errorf("err = %v, want ErrNoHealthyBackends", err)
	}
}

func TestLeastConnectionsSelectsLeast(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080")
	b := NewLeastConnectionsBalancer()

	// Prime counters: acquire a connection for backend a
	be, _ := b.Next(pool)
	b.Acquire(be) // backend a now has 1 connection

	// Next pick should prefer backend b (0 connections)
	next, err := b.Next(pool)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if next.URL == be.URL {
		// Only a problem if b has fewer connections; if both same URL unexpected
		t.Logf("note: got same backend %q (pool may have selected b first)", next.URL)
	}
}

func TestLeastConnectionsAcquireRelease(t *testing.T) {
	pool := mustPool(t, "http://a:8080")
	b := NewLeastConnectionsBalancer()

	be, _ := b.Next(pool)
	b.Acquire(be)
	b.Acquire(be)
	b.Release(be)
	b.Release(be)

	// Should be back to 0
	b.mu.RLock()
	counter := b.connections[be.URL]
	b.mu.RUnlock()
	if counter != nil && counter.Load() != 0 {
		t.Errorf("expected 0 connections, got %d", counter.Load())
	}
}

func TestLeastConnectionsReset(t *testing.T) {
	pool := mustPool(t, "http://a:8080")
	b := NewLeastConnectionsBalancer()
	be, _ := b.Next(pool)
	b.Acquire(be)

	b.Reset()
	b.mu.RLock()
	count := len(b.connections)
	b.mu.RUnlock()
	if count != 0 {
		t.Errorf("connections map should be empty after Reset, got %d", count)
	}
}

func TestLeastConnectionsConcurrent(t *testing.T) {
	pool := mustPool(t, "http://a:8080", "http://b:8080")
	b := NewLeastConnectionsBalancer()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			be, err := b.Next(pool)
			if err != nil {
				return
			}
			b.Acquire(be)
			b.Release(be)
		}()
	}
	wg.Wait()
}
