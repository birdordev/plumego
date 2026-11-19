package rw

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrNoReplicasAvailable is returned when no replicas are available for selection
	ErrNoReplicasAvailable = errors.New("rw: no replicas available")

	// ErrNoHealthyReplicas is returned when all replicas are unhealthy
	ErrNoHealthyReplicas = errors.New("rw: no healthy replicas available")

	// ErrInvalidReplicaWeight identifies invalid weights or unrepresentable scores.
	ErrInvalidReplicaWeight = errors.New("rw: replica weights must be positive")
)

// Replica represents a database replica with its metadata
type Replica struct {
	Index     int     // Replica index in the list
	DB        *sql.DB // Database connection
	Weight    int     // Weight for weighted load balancing
	IsHealthy bool    // Health status
}

// LoadBalancer defines the interface for load balancing strategies
type LoadBalancer interface {
	// Next selects the next replica to use
	// Returns the index of the selected replica or an error
	Next(replicas []Replica) (int, error)

	// Reset resets the load balancer state (useful for config changes)
	Reset()

	// Name returns the load balancer strategy name
	Name() string
}

// RoundRobinBalancer implements round-robin load balancing
type RoundRobinBalancer struct {
	counter atomic.Uint64
}

// NewRoundRobinBalancer creates a new round-robin load balancer
func NewRoundRobinBalancer() *RoundRobinBalancer {
	return &RoundRobinBalancer{}
}

// Next returns the next replica using round-robin strategy
func (b *RoundRobinBalancer) Next(replicas []Replica) (int, error) {
	if len(replicas) == 0 {
		return -1, ErrNoReplicasAvailable
	}

	// Try to find a healthy replica using round-robin
	n := uint64(len(replicas))
	start := b.counter.Add(1) % n

	for i := uint64(0); i < n; i++ {
		idx := int((start + i) % n)
		if replicas[idx].IsHealthy {
			return idx, nil
		}
	}

	// No healthy replicas found
	return -1, ErrNoHealthyReplicas
}

// Reset resets the round-robin counter
func (b *RoundRobinBalancer) Reset() {
	b.counter.Store(0)
}

// Name returns the strategy name
func (b *RoundRobinBalancer) Name() string {
	return "round_robin"
}

// RandomBalancer implements random load balancing
type RandomBalancer struct {
	rng *rand.Rand
	mu  sync.Mutex
}

// NewRandomBalancer creates a new random load balancer
func NewRandomBalancer() *RandomBalancer {
	return &RandomBalancer{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Next returns a random healthy replica
func (b *RandomBalancer) Next(replicas []Replica) (int, error) {
	if len(replicas) == 0 {
		return -1, ErrNoReplicasAvailable
	}

	// Collect healthy replicas
	healthy := make([]int, 0, len(replicas))
	for i, r := range replicas {
		if r.IsHealthy {
			healthy = append(healthy, i)
		}
	}

	if len(healthy) == 0 {
		return -1, ErrNoHealthyReplicas
	}

	// Select random healthy replica
	b.mu.Lock()
	idx := healthy[b.rng.Intn(len(healthy))]
	b.mu.Unlock()

	return idx, nil
}

// Reset resets the random number generator
func (b *RandomBalancer) Reset() {
	b.mu.Lock()
	b.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	b.mu.Unlock()
}

// Name returns the strategy name
func (b *RandomBalancer) Name() string {
	return "random"
}

// LeastConnBalancer implements least-connections load balancing
type LeastConnBalancer struct{}

// NewLeastConnBalancer creates a new least-connections load balancer
func NewLeastConnBalancer() *LeastConnBalancer {
	return &LeastConnBalancer{}
}

// Next returns the replica with the least connections
func (b *LeastConnBalancer) Next(replicas []Replica) (int, error) {
	if len(replicas) == 0 {
		return -1, ErrNoReplicasAvailable
	}

	minConns := -1
	minIdx := -1

	for i, r := range replicas {
		if !r.IsHealthy {
			continue
		}

		stats := r.DB.Stats()
		inUse := stats.InUse

		if minIdx == -1 || inUse < minConns {
			minConns = inUse
			minIdx = i
		}
	}

	if minIdx == -1 {
		return -1, ErrNoHealthyReplicas
	}

	return minIdx, nil
}

// Reset is a no-op for least-connections strategy
func (b *LeastConnBalancer) Reset() {
	// No state to reset
}

// Name returns the strategy name
func (b *LeastConnBalancer) Name() string {
	return "least_connections"
}

// WeightedBalancer implements weighted round-robin load balancing
type WeightedBalancer struct {
	weights       []int
	currentWeight []int
	fallback      RoundRobinBalancer
	mu            sync.Mutex
}

// NewWeightedBalancer creates a new weighted load balancer and validates
// weights at construction time.
func NewWeightedBalancer(weights []int) (*WeightedBalancer, error) {
	if len(weights) == 0 {
		return &WeightedBalancer{}, nil
	}

	copied := append([]int(nil), weights...)

	for i, w := range copied {
		if w <= 0 {
			return nil, fmt.Errorf("%w: index %d has weight %d", ErrInvalidReplicaWeight, i, w)
		}
	}

	return &WeightedBalancer{
		weights:       copied,
		currentWeight: make([]int, len(copied)),
	}, nil
}

// Next uses smooth weighted round-robin among healthy replicas.
// If a total or score exceeds the integer range, it returns
// ErrInvalidReplicaWeight without advancing the selection state.
func (b *WeightedBalancer) Next(replicas []Replica) (int, error) {
	if len(replicas) == 0 {
		return -1, ErrNoReplicasAvailable
	}

	if len(b.weights) == 0 {
		return b.fallback.Next(replicas)
	}
	if len(b.weights) != len(replicas) {
		return -1, fmt.Errorf("%w: got %d weights for %d replicas", ErrInvalidReplicaWeight, len(b.weights), len(replicas))
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	totalWeight, bestScore, selected := 0, 0, -1
	for i, replica := range replicas {
		if !replica.IsHealthy {
			continue
		}
		weight := b.weights[i]
		if totalWeight > math.MaxInt-weight || b.currentWeight[i] > math.MaxInt-weight {
			return -1, fmt.Errorf("%w: total or score exceeds integer range", ErrInvalidReplicaWeight)
		}
		totalWeight += weight
		score := b.currentWeight[i] + weight
		if selected < 0 || score > bestScore {
			selected, bestScore = i, score
		}
	}
	if selected < 0 {
		return -1, ErrNoHealthyReplicas
	}
	// Unhealthy replicas retain their scores, so the winning score can be negative.
	if bestScore < math.MinInt+totalWeight {
		return -1, fmt.Errorf("%w: winning score exceeds integer range", ErrInvalidReplicaWeight)
	}
	for i, replica := range replicas {
		if replica.IsHealthy {
			b.currentWeight[i] += b.weights[i]
		}
	}
	b.currentWeight[selected] = bestScore - totalWeight
	return selected, nil
}

// Reset resets the weighted balancer state
func (b *WeightedBalancer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fallback.Reset()
	for i := range b.currentWeight {
		b.currentWeight[i] = 0
	}
}

// Name returns the strategy name
func (b *WeightedBalancer) Name() string {
	return "weighted_round_robin"
}
