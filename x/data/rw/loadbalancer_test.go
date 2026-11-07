package rw

import (
	"errors"
	"testing"
)

func TestRoundRobinBalancer(t *testing.T) {
	lb := NewRoundRobinBalancer()

	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
		{Index: 2, IsHealthy: true},
	}

	// Test round-robin distribution
	// Note: counter starts at 0, so Add(1) returns 1, then 1 % 3 = 1
	expected := []int{1, 2, 0, 1, 2, 0}
	for i, want := range expected {
		got, err := lb.Next(replicas)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if got != want {
			t.Errorf("iteration %d: got %d, want %d", i, got, want)
		}
	}
}

func TestRoundRobinBalancerSkipsUnhealthy(t *testing.T) {
	lb := NewRoundRobinBalancer()

	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: false}, // Unhealthy
		{Index: 2, IsHealthy: true},
	}

	// Should skip unhealthy replica
	for i := 0; i < 6; i++ {
		idx, err := lb.Next(replicas)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if idx == 1 {
			t.Errorf("iteration %d: selected unhealthy replica 1", i)
		}
	}
}

func TestRoundRobinBalancerNoReplicas(t *testing.T) {
	lb := NewRoundRobinBalancer()

	_, err := lb.Next([]Replica{})
	if err != ErrNoReplicasAvailable {
		t.Errorf("got error %v, want %v", err, ErrNoReplicasAvailable)
	}
}

func TestRoundRobinBalancerAllUnhealthy(t *testing.T) {
	lb := NewRoundRobinBalancer()

	replicas := []Replica{
		{Index: 0, IsHealthy: false},
		{Index: 1, IsHealthy: false},
	}

	_, err := lb.Next(replicas)
	if err != ErrNoHealthyReplicas {
		t.Errorf("got error %v, want %v", err, ErrNoHealthyReplicas)
	}
}

func TestRandomBalancer(t *testing.T) {
	lb := NewRandomBalancer()

	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
		{Index: 2, IsHealthy: true},
	}

	// Test that it returns valid indices
	seen := make(map[int]bool)
	for i := 0; i < 100; i++ {
		idx, err := lb.Next(replicas)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if idx < 0 || idx >= len(replicas) {
			t.Errorf("invalid index %d", idx)
		}
		seen[idx] = true
	}

	// With 100 iterations, we should see all replicas
	if len(seen) != 3 {
		t.Errorf("expected to see all 3 replicas, saw %d", len(seen))
	}
}

func TestRandomBalancerSkipsUnhealthy(t *testing.T) {
	lb := NewRandomBalancer()

	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: false},
		{Index: 2, IsHealthy: true},
	}

	// Should never select unhealthy replica
	for i := 0; i < 50; i++ {
		idx, err := lb.Next(replicas)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if idx == 1 {
			t.Errorf("selected unhealthy replica")
		}
	}
}

func TestLeastConnBalancer(t *testing.T) {
	// Note: This test uses nil DB pointers, which would panic in real usage
	// In practice, these would be real *sql.DB connections
	// For now, we test the logic with a simple case

	lb := NewLeastConnBalancer()

	// Test with no replicas
	_, err := lb.Next([]Replica{})
	if err != ErrNoReplicasAvailable {
		t.Errorf("got error %v, want %v", err, ErrNoReplicasAvailable)
	}

	// Test with all unhealthy
	replicas := []Replica{
		{Index: 0, IsHealthy: false},
		{Index: 1, IsHealthy: false},
	}

	_, err = lb.Next(replicas)
	if err != ErrNoHealthyReplicas {
		t.Errorf("got error %v, want %v", err, ErrNoHealthyReplicas)
	}
}

func TestWeightedBalancer(t *testing.T) {
	weights := []int{1, 2, 3}
	lb, err := NewWeightedBalancer(weights)
	if err != nil {
		t.Fatal(err)
	}

	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
		{Index: 2, IsHealthy: true},
	}

	// Count how many times each replica is selected
	counts := make(map[int]int)
	iterations := 600 // Multiple of sum of weights (1+2+3=6)

	for i := 0; i < iterations; i++ {
		idx, err := lb.Next(replicas)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		counts[idx]++
	}

	// Check distribution roughly matches weights ratio (1:2:3)
	// With weighted round-robin, exact distribution may vary
	// Check that higher weights get more selections
	if counts[2] <= counts[1] || counts[1] <= counts[0] {
		t.Errorf("expected counts[2] > counts[1] > counts[0], got %v", counts)
	}

	// Total should be 600
	total := counts[0] + counts[1] + counts[2]
	if total != iterations {
		t.Errorf("total selections %d, want %d", total, iterations)
	}
}

func TestWeightedBalancerSkipsUnhealthy(t *testing.T) {
	weights := []int{1, 2, 3}
	lb, err := NewWeightedBalancer(weights)
	if err != nil {
		t.Fatal(err)
	}

	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: false}, // Unhealthy
		{Index: 2, IsHealthy: true},
	}

	// Should skip unhealthy replica
	for i := 0; i < 50; i++ {
		idx, err := lb.Next(replicas)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if idx == 1 {
			t.Errorf("selected unhealthy replica")
		}
	}
}

func TestWeightedBalancerNoWeights(t *testing.T) {
	lb, err := NewWeightedBalancer([]int{})
	if err != nil {
		t.Fatal(err)
	}

	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
	}

	// Should fallback to round-robin (which starts at 1)
	idx, err := lb.Next(replicas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Round-robin Add(1) returns 1, 1 % 2 = 1
	if idx != 1 {
		t.Errorf("got %d, want 1", idx)
	}

	idx, err = lb.Next(replicas)
	if err != nil {
		t.Fatalf("second Next unexpected error: %v", err)
	}
	if idx != 0 {
		t.Errorf("second Next got %d, want 0", idx)
	}
}

func TestNewWeightedBalancer_RejectsNonPositiveWeights(t *testing.T) {
	for _, weights := range [][]int{{0, 1}, {-1, 1}, {0, 0}} {
		if _, err := NewWeightedBalancer(weights); !errors.Is(err, ErrInvalidReplicaWeight) {
			t.Fatalf("weights %v constructor error = %v, want ErrInvalidReplicaWeight", weights, err)
		}
	}
}

func TestWeightedBalancerRejectsReplicaWeightMismatch(t *testing.T) {
	lb, err := NewWeightedBalancer([]int{1})
	if err != nil {
		t.Fatal(err)
	}
	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
	}
	if _, err := lb.Next(replicas); !errors.Is(err, ErrInvalidReplicaWeight) {
		t.Fatalf("Next() error = %v, want ErrInvalidReplicaWeight", err)
	}
}

func TestLoadBalancerReset(t *testing.T) {
	t.Run("RoundRobin", func(t *testing.T) {
		lb := NewRoundRobinBalancer()
		replicas := []Replica{{Index: 0, IsHealthy: true}, {Index: 1, IsHealthy: true}}

		// Advance counter
		_, _ = lb.Next(replicas)
		_, _ = lb.Next(replicas)

		// Reset
		lb.Reset()

		// Should start from beginning (Add(1) % 2 = 1)
		idx, _ := lb.Next(replicas)
		if idx != 1 {
			t.Errorf("after reset, got %d, want 1", idx)
		}
	})

	t.Run("Weighted", func(t *testing.T) {
		lb, err := NewWeightedBalancer([]int{1, 2})
		if err != nil {
			t.Fatal(err)
		}
		replicas := []Replica{{Index: 0, IsHealthy: true}, {Index: 1, IsHealthy: true}}

		// Advance state
		for i := 0; i < 10; i++ {
			_, _ = lb.Next(replicas)
		}

		// Reset
		lb.Reset()

		// State should be reset (hard to test exact behavior, but shouldn't panic)
		_, err = lb.Next(replicas)
		if err != nil {
			t.Errorf("after reset: unexpected error: %v", err)
		}
	})

	t.Run("WeightedNoWeights", func(t *testing.T) {
		lb, err := NewWeightedBalancer(nil)
		if err != nil {
			t.Fatal(err)
		}
		replicas := []Replica{{Index: 0, IsHealthy: true}, {Index: 1, IsHealthy: true}}

		_, _ = lb.Next(replicas)
		_, _ = lb.Next(replicas)
		lb.Reset()

		idx, err := lb.Next(replicas)
		if err != nil {
			t.Errorf("after reset: unexpected error: %v", err)
		}
		if idx != 1 {
			t.Errorf("after reset, got %d, want 1", idx)
		}
	})
}

func BenchmarkRoundRobinBalancer(b *testing.B) {
	lb := NewRoundRobinBalancer()
	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
		{Index: 2, IsHealthy: true},
		{Index: 3, IsHealthy: true},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lb.Next(replicas)
	}
}

func BenchmarkRandomBalancer(b *testing.B) {
	lb := NewRandomBalancer()
	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
		{Index: 2, IsHealthy: true},
		{Index: 3, IsHealthy: true},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lb.Next(replicas)
	}
}

func BenchmarkWeightedBalancer(b *testing.B) {
	lb, err := NewWeightedBalancer([]int{1, 2, 3, 4})
	if err != nil {
		b.Fatal(err)
	}
	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
		{Index: 2, IsHealthy: true},
		{Index: 3, IsHealthy: true},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lb.Next(replicas)
	}
}
