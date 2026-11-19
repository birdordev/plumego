package gateway

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// LoadBalancer defines the interface for load balancing strategies
type LoadBalancer interface {
	// Next selects the next backend from the pool
	Next(pool *BackendPool) (*Backend, error)

	// Reset resets the load balancer state
	Reset()

	// Name returns the strategy name
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

// Next returns the next backend using round-robin strategy
func (b *RoundRobinBalancer) Next(pool *BackendPool) (*Backend, error) {
	backends := pool.HealthyBackends()
	if len(backends) == 0 {
		return nil, ErrNoHealthyBackends
	}

	index := int(b.counter.Add(1) % uint64(len(backends)))
	return backends[index], nil
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

// Next returns a random healthy backend
func (b *RandomBalancer) Next(pool *BackendPool) (*Backend, error) {
	backends := pool.HealthyBackends()
	if len(backends) == 0 {
		return nil, ErrNoHealthyBackends
	}

	b.mu.Lock()
	index := b.rng.Intn(len(backends))
	b.mu.Unlock()

	return backends[index], nil
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

// WeightedRoundRobinBalancer implements weighted round-robin load balancing
type WeightedRoundRobinBalancer struct {
	currentWeight []int
	mu            sync.Mutex
}

// NewWeightedRoundRobinBalancer creates a new weighted round-robin load balancer
func NewWeightedRoundRobinBalancer() *WeightedRoundRobinBalancer {
	return &WeightedRoundRobinBalancer{}
}

// Next returns the next backend using smooth weighted round-robin.
// Zero weights count as one. Negative weights or arithmetic that exceeds the
// integer range return ErrInvalidConfig without advancing the weights.
func (b *WeightedRoundRobinBalancer) Next(pool *BackendPool) (*Backend, error) {
	backends := pool.HealthyBackends()
	if len(backends) == 0 {
		return nil, ErrNoHealthyBackends
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	totalWeight, selected, maxCurrent := 0, 0, 0
	sameSize := len(b.currentWeight) == len(backends)
	for i, backend := range backends {
		weight := backend.Weight
		if weight == 0 {
			weight = 1
		}
		if weight < 0 || totalWeight > math.MaxInt-weight {
			return nil, fmt.Errorf("%w: invalid weighted round-robin total", ErrInvalidConfig)
		}
		totalWeight += weight
		current := 0
		if sameSize {
			current = b.currentWeight[i]
		}
		if current > math.MaxInt-weight {
			return nil, fmt.Errorf("%w: weighted round-robin score exceeds integer range", ErrInvalidConfig)
		}
		score := current + weight
		if i == 0 || score > maxCurrent {
			selected, maxCurrent = i, score
		}
	}

	// Apply the round only after validating all weights and scores.
	if !sameSize {
		b.currentWeight = make([]int, len(backends))
	}
	for i, backend := range backends {
		b.currentWeight[i] += max(backend.Weight, 1)
	}
	// The previous scores sum to zero, so the winning score is nonnegative.
	b.currentWeight[selected] = maxCurrent - totalWeight
	return backends[selected], nil
}

// Reset resets the weighted balancer state
func (b *WeightedRoundRobinBalancer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentWeight = nil
}

// Name returns the strategy name
func (b *WeightedRoundRobinBalancer) Name() string {
	return "weighted_round_robin"
}

// IPHashBalancer implements IP-hash load balancing
// Routes requests from the same client IP to the same backend
type IPHashBalancer struct{}

// NewIPHashBalancer creates a new IP-hash load balancer
func NewIPHashBalancer() *IPHashBalancer {
	return &IPHashBalancer{}
}

// Next returns a backend based on client IP hash
func (b *IPHashBalancer) Next(pool *BackendPool) (*Backend, error) {
	backends := pool.HealthyBackends()
	if len(backends) == 0 {
		return nil, ErrNoHealthyBackends
	}

	// Note: This needs the client IP to be passed through context
	// For now, we'll use a simple round-robin fallback
	// In production, you would extract IP from request context
	index := int(time.Now().UnixNano() % int64(len(backends)))
	return backends[index], nil
}

// NextWithIP returns a backend based on the given IP address
func (b *IPHashBalancer) NextWithIP(pool *BackendPool, ip string) (*Backend, error) {
	backends := pool.HealthyBackends()
	if len(backends) == 0 {
		return nil, ErrNoHealthyBackends
	}

	// Hash the IP address
	h := fnv.New32a()
	h.Write([]byte(ip))
	hash := h.Sum32()

	// Select backend based on hash
	index := int(hash % uint32(len(backends)))
	return backends[index], nil
}

// Reset is a no-op for IP-hash strategy
func (b *IPHashBalancer) Reset() {
	// No state to reset
}

// Name returns the strategy name
func (b *IPHashBalancer) Name() string {
	return "ip_hash"
}

// LeastConnectionsBalancer implements least-connections load balancing
// Note: This is a simplified version that tracks active requests per backend
type LeastConnectionsBalancer struct {
	connections map[string]*atomic.Int64
	mu          sync.RWMutex
}

// NewLeastConnectionsBalancer creates a new least-connections load balancer
func NewLeastConnectionsBalancer() *LeastConnectionsBalancer {
	return &LeastConnectionsBalancer{
		connections: make(map[string]*atomic.Int64),
	}
}

// Next returns the backend with the least connections
func (b *LeastConnectionsBalancer) Next(pool *BackendPool) (*Backend, error) {
	backends := pool.HealthyBackends()
	if len(backends) == 0 {
		return nil, ErrNoHealthyBackends
	}

	b.mu.Lock()
	// Ensure all backends have a counter
	for _, backend := range backends {
		if _, exists := b.connections[backend.URL]; !exists {
			b.connections[backend.URL] = &atomic.Int64{}
		}
	}
	b.mu.Unlock()

	// Find backend with least connections
	var selected *Backend
	minConns := int64(-1)

	b.mu.RLock()
	for _, backend := range backends {
		counter := b.connections[backend.URL]
		conns := counter.Load()

		if minConns == -1 || conns < minConns {
			minConns = conns
			selected = backend
		}
	}
	b.mu.RUnlock()

	if selected == nil {
		return nil, ErrNoHealthyBackends
	}

	return selected, nil
}

// Acquire increments the connection count for a backend
func (b *LeastConnectionsBalancer) Acquire(backend *Backend) {
	b.mu.RLock()
	counter, exists := b.connections[backend.URL]
	b.mu.RUnlock()

	if exists {
		counter.Add(1)
	}
}

// Release decrements the connection count for a backend
func (b *LeastConnectionsBalancer) Release(backend *Backend) {
	b.mu.RLock()
	counter, exists := b.connections[backend.URL]
	b.mu.RUnlock()

	if exists {
		counter.Add(-1)
	}
}

// Reset resets all connection counters
func (b *LeastConnectionsBalancer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connections = make(map[string]*atomic.Int64)
}

// Name returns the strategy name
func (b *LeastConnectionsBalancer) Name() string {
	return "least_connections"
}
