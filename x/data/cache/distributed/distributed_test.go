package distributed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/store/cache"
)

type flakyGetCache struct {
	cache.Cache
	failures atomic.Int32
	err      error
}

type failingSetCache struct {
	cache.Cache
	err error
}

type failingClearCache struct {
	cache.Cache
	err error
}

type failingDeleteCache struct {
	cache.Cache
	err error
}

type failingMutationCache struct {
	cache.Cache
	err error
}

type failingGetCache struct {
	cache.Cache
	err   error
	calls atomic.Int32
}

type blockingSetCache struct {
	cache.Cache
	started chan struct{}
	once    sync.Once
}

type retainingValueCache struct {
	cache.Cache
	mu     sync.Mutex
	values map[string][]byte
}

type flakyRetainingValueCache struct {
	*retainingValueCache
	failures atomic.Int32
	err      error
}

type trackingSetCache struct {
	cache.Cache
	active *atomic.Int32
	max    *atomic.Int32
	delay  time.Duration
}

func newFlakyGetCache(failures int32, err error) *flakyGetCache {
	fc := &flakyGetCache{
		Cache: cache.NewMemoryCache(),
		err:   err,
	}
	fc.failures.Store(failures)
	return fc
}

func (fc *flakyGetCache) Get(ctx context.Context, key string) ([]byte, error) {
	if fc.failures.Load() > 0 {
		fc.failures.Add(-1)
		return nil, fc.err
	}
	return fc.Cache.Get(ctx, key)
}

func (fc *failingSetCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return fc.err
}

func (fc *failingClearCache) Clear(ctx context.Context) error {
	return fc.err
}

func (fc *failingDeleteCache) Delete(ctx context.Context, key string) error {
	return fc.err
}

func (fc *failingMutationCache) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	return 0, fc.err
}

func (fc *failingMutationCache) Decr(ctx context.Context, key string, delta int64) (int64, error) {
	return 0, fc.err
}

func (fc *failingGetCache) Get(ctx context.Context, key string) ([]byte, error) {
	fc.calls.Add(1)
	return nil, fc.err
}

func (fc *failingMutationCache) Append(ctx context.Context, key string, data []byte) error {
	return fc.err
}

func (bc *blockingSetCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if bc.started != nil {
		bc.once.Do(func() {
			close(bc.started)
		})
	}
	<-ctx.Done()
	return ctx.Err()
}

func newRetainingValueCache() *retainingValueCache {
	return &retainingValueCache{
		Cache:  cache.NewMemoryCache(),
		values: make(map[string][]byte),
	}
}

func (rc *retainingValueCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.values[key] = value
	return nil
}

func (rc *retainingValueCache) Get(ctx context.Context, key string) ([]byte, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	value, ok := rc.values[key]
	if !ok {
		return nil, cache.ErrNotFound
	}
	return value, nil
}

func (rc *retainingValueCache) Append(ctx context.Context, key string, data []byte) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if _, ok := rc.values[key]; !ok {
		rc.values[key] = data
		return nil
	}
	rc.values[key] = append(rc.values[key], data...)
	return nil
}

func newFlakyRetainingValueCache(failures int32, err error) *flakyRetainingValueCache {
	frc := &flakyRetainingValueCache{
		retainingValueCache: newRetainingValueCache(),
		err:                 err,
	}
	frc.failures.Store(failures)
	return frc
}

func (frc *flakyRetainingValueCache) Get(ctx context.Context, key string) ([]byte, error) {
	if frc.failures.Load() > 0 {
		frc.failures.Add(-1)
		return nil, frc.err
	}
	return frc.retainingValueCache.Get(ctx, key)
}

func (tc *trackingSetCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	active := tc.active.Add(1)
	for {
		max := tc.max.Load()
		if active <= max || tc.max.CompareAndSwap(max, active) {
			break
		}
	}
	if tc.delay > 0 {
		time.Sleep(tc.delay)
	}
	tc.active.Add(-1)
	return tc.Cache.Set(ctx, key, value, ttl)
}

func findReplicaOrderKey(t *testing.T, dc *DistributedCache, prefix string, firstID string, secondID string) string {
	t.Helper()
	for i := 0; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", prefix, i)
		replicas, err := dc.ring.GetN(candidate, 2)
		if err != nil {
			t.Fatalf("GetN failed: %v", err)
		}
		if replicas[0].ID() == firstID && replicas[1].ID() == secondID {
			return candidate
		}
	}
	t.Fatalf("failed to find key with replica order %s, %s", firstID, secondID)
	return ""
}

func TestConsistentHashRingBasicOperations(t *testing.T) {
	ring := NewConsistentHashRing(nil)

	// Create test nodes
	node1 := NewNode("node1", cache.NewMemoryCache())
	node2 := NewNode("node2", cache.NewMemoryCache())
	node3 := NewNode("node3", cache.NewMemoryCache())

	// Test Add
	if err := ring.Add(node1); err != nil {
		t.Errorf("failed to add node1: %v", err)
	}

	if err := ring.Add(node2); err != nil {
		t.Errorf("failed to add node2: %v", err)
	}

	if err := ring.Add(node3); err != nil {
		t.Errorf("failed to add node3: %v", err)
	}

	// Test Size
	if ring.Size() != 3 {
		t.Errorf("expected 3 nodes, got %d", ring.Size())
	}

	// Test duplicate add
	err := ring.Add(node1)
	if err != ErrNodeAlreadyExists {
		t.Errorf("expected ErrNodeAlreadyExists, got %v", err)
	}

	// Test Remove
	if err := ring.Remove("node2"); err != nil {
		t.Errorf("failed to remove node2: %v", err)
	}

	if ring.Size() != 2 {
		t.Errorf("expected 2 nodes after removal, got %d", ring.Size())
	}

	// Test remove non-existent
	err = ring.Remove("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestConsistentHashRingGet(t *testing.T) {
	ring := NewConsistentHashRing(nil)

	// Create and add nodes
	node1 := NewNode("node1", cache.NewMemoryCache())
	node2 := NewNode("node2", cache.NewMemoryCache())
	node3 := NewNode("node3", cache.NewMemoryCache())

	ring.Add(node1)
	ring.Add(node2)
	ring.Add(node3)

	// Test Get - same key should always go to same node
	key := "test-key"
	firstNode, err := ring.Get(key)
	if err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	for i := 0; i < 10; i++ {
		node, err := ring.Get(key)
		if err != nil {
			t.Errorf("failed to get node: %v", err)
		}

		if node.ID() != firstNode.ID() {
			t.Errorf("inconsistent routing: expected %s, got %s", firstNode.ID(), node.ID())
		}
	}
}

func TestConsistentHashRingGetN(t *testing.T) {
	ring := NewConsistentHashRing(nil)

	// Create and add nodes
	nodes := make([]CacheNode, 5)
	for i := 0; i < 5; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
		ring.Add(nodes[i])
	}

	// Test GetN
	key := "test-key"
	replicas, err := ring.GetN(key, 3)
	if err != nil {
		t.Fatalf("failed to get replicas: %v", err)
	}

	if len(replicas) != 3 {
		t.Errorf("expected 3 replicas, got %d", len(replicas))
	}

	// Check for unique nodes
	seen := make(map[string]bool)
	for _, node := range replicas {
		if seen[node.ID()] {
			t.Errorf("duplicate node in replicas: %s", node.ID())
		}
		seen[node.ID()] = true
	}
}

func TestConsistentHashRingGetNRequiresRequestedReplicas(t *testing.T) {
	ring := NewConsistentHashRing(nil)
	if err := ring.Add(NewNode("node1", cache.NewMemoryCache())); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	_, err := ring.GetN("key", 2)
	if !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("expected ErrInsufficientReplicas, got %v", err)
	}
}

func TestConsistentHashRingDoesNotMutateConfig(t *testing.T) {
	config := &ConsistentHashRingConfig{}
	ring := NewConsistentHashRing(config)

	if config.VirtualNodes != 0 {
		t.Fatalf("caller VirtualNodes = %d, want unchanged zero", config.VirtualNodes)
	}
	if config.HashFunc != nil {
		t.Fatal("caller HashFunc was mutated")
	}
	if ring.virtualNodes <= 0 {
		t.Fatal("expected ring to normalize internal virtual nodes")
	}
	if ring.hashFunc == nil {
		t.Fatal("expected ring to normalize internal hash function")
	}
}

func TestConsistentHashRingResolvesHashCollisions(t *testing.T) {
	config := &ConsistentHashRingConfig{
		VirtualNodes: 1,
		HashFunc: func(data []byte) uint32 {
			return 7
		},
	}
	ring := NewConsistentHashRing(config)

	if err := ring.Add(NewNode("node1", cache.NewMemoryCache())); err != nil {
		t.Fatalf("Add node1 failed: %v", err)
	}
	if err := ring.Add(NewNode("node2", cache.NewMemoryCache())); err != nil {
		t.Fatalf("Add node2 failed: %v", err)
	}
	if err := ring.Add(NewNode("node3", cache.NewMemoryCache())); err != nil {
		t.Fatalf("Add node3 failed: %v", err)
	}

	if ring.CollisionCount() == 0 {
		t.Fatal("expected collision count to increase")
	}

	replicas, err := ring.GetN("key", 3)
	if err != nil {
		t.Fatalf("GetN failed: %v", err)
	}
	if len(replicas) != 3 {
		t.Fatalf("expected 3 unique replicas, got %d", len(replicas))
	}
}

func TestConsistentHashRingBoundsVirtualNodeCollisionProbes(t *testing.T) {
	config := &ConsistentHashRingConfig{
		VirtualNodes: maxVirtualNodeHashProbes + 1,
		HashFunc: func(data []byte) uint32 {
			return 0
		},
	}
	ring := NewConsistentHashRing(config)

	err := ring.Add(NewNode("node1", cache.NewMemoryCache()))
	if !errors.Is(err, ErrHashRingSaturated) {
		t.Fatalf("expected ErrHashRingSaturated, got %v", err)
	}
	if ring.Size() != 0 {
		t.Fatalf("ring size = %d, want 0 after failed add", ring.Size())
	}
	if len(ring.sortedHashes) != 0 {
		t.Fatalf("sorted hashes = %d, want 0 after rollback", len(ring.sortedHashes))
	}
}

func TestConsistentHashRingRejectsExcessiveWeightedVirtualNodes(t *testing.T) {
	ring := NewConsistentHashRing(&ConsistentHashRingConfig{VirtualNodes: 2})
	node := NewNode("huge", cache.NewMemoryCache(), WithWeight(maxVirtualNodePlacementsPerNode))

	if err := ring.Add(node); !errors.Is(err, ErrHashRingNodeTooLarge) {
		t.Fatalf("expected ErrHashRingNodeTooLarge, got %v", err)
	}
	if ring.Size() != 0 {
		t.Fatalf("ring size = %d, want 0 after failed add", ring.Size())
	}
	if len(ring.sortedHashes) != 0 {
		t.Fatalf("sorted hashes = %d, want 0 after failed add", len(ring.sortedHashes))
	}
}

func TestConsistentHashRingUsesNodeWeightForVirtualNodes(t *testing.T) {
	config := &ConsistentHashRingConfig{
		VirtualNodes: 10,
	}
	ring := NewConsistentHashRing(config)

	light := NewNode("light", cache.NewMemoryCache())
	heavy := NewNode("heavy", cache.NewMemoryCache(), WithWeight(3))

	if err := ring.Add(light); err != nil {
		t.Fatalf("Add light failed: %v", err)
	}
	if err := ring.Add(heavy); err != nil {
		t.Fatalf("Add heavy failed: %v", err)
	}

	if got := len(ring.nodeHashes["light"]); got != 10 {
		t.Fatalf("light virtual nodes = %d, want 10", got)
	}
	if got := len(ring.nodeHashes["heavy"]); got != 30 {
		t.Fatalf("heavy virtual nodes = %d, want 30", got)
	}
}

func TestConsistentHashRingDistribution(t *testing.T) {
	// Use more virtual nodes for better distribution
	config := &ConsistentHashRingConfig{
		VirtualNodes: 300,
		HashFunc:     nil,
	}
	ring := NewConsistentHashRing(config)

	// Add nodes
	numNodes := 3
	for i := 0; i < numNodes; i++ {
		node := NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
		ring.Add(node)
	}

	// Test distribution
	distribution := make(map[string]int)
	numKeys := 10000

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key%d", i)
		node, err := ring.Get(key)
		if err != nil {
			t.Fatalf("failed to get node: %v", err)
		}

		distribution[node.ID()]++
	}

	// Check distribution variance
	expectedPerNode := numKeys / numNodes
	for nodeID, count := range distribution {
		variance := float64(count-expectedPerNode) / float64(expectedPerNode) * 100
		t.Logf("Node %s: %d keys (%.2f%% variance)", nodeID, count, variance)

		// Allow up to 30% variance for small node count
		// With more nodes and virtual nodes, variance decreases
		if variance > 30 || variance < -30 {
			t.Errorf("node %s has too much variance: %.2f%%", nodeID, variance)
		}
	}
}

func TestLocalCacheNode(t *testing.T) {
	cacheInstance := cache.NewMemoryCache()
	node := NewNode("test-node", cacheInstance, WithWeight(2))

	// Test ID
	if node.ID() != "test-node" {
		t.Errorf("expected ID 'test-node', got %s", node.ID())
	}

	// Test Weight
	if node.Weight() != 2 {
		t.Errorf("expected weight 2, got %d", node.Weight())
	}

	// Test initial health
	if !node.IsHealthy() {
		t.Error("expected node to be initially healthy")
	}

	if node.HealthStatus() != HealthStatusHealthy {
		t.Errorf("expected HealthStatusHealthy, got %v", node.HealthStatus())
	}

	// Test UpdateHealth
	node.UpdateHealth(HealthStatusUnhealthy)
	if node.IsHealthy() {
		t.Error("expected node to be unhealthy")
	}

	if node.HealthStatus() != HealthStatusUnhealthy {
		t.Errorf("expected HealthStatusUnhealthy, got %v", node.HealthStatus())
	}
}

func TestDistributedCacheBasicOperations(t *testing.T) {
	// Create nodes
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	// Create distributed cache
	config := DefaultConfig()
	config.ReplicationFactor = 1
	dc := New(nodes, config)
	defer dc.Close()

	ctx := t.Context()

	// Test Set
	err := dc.Set(ctx, "key1", []byte("value1"), time.Minute)
	if err != nil {
		t.Errorf("Set failed: %v", err)
	}

	// Test Get
	value, err := dc.Get(ctx, "key1")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}

	if string(value) != "value1" {
		t.Errorf("expected value1, got %s", string(value))
	}

	// Test Exists
	exists, err := dc.Exists(ctx, "key1")
	if err != nil {
		t.Errorf("Exists failed: %v", err)
	}

	if !exists {
		t.Error("expected key to exist")
	}

	// Test Delete
	err = dc.Delete(ctx, "key1")
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}

	// Verify deletion
	exists, err = dc.Exists(ctx, "key1")
	if err != nil {
		t.Errorf("Exists failed: %v", err)
	}

	if exists {
		t.Error("expected key to not exist after deletion")
	}
}

func TestDistributedCacheGetOwnsReturnedBytes(t *testing.T) {
	primaryCache := newRetainingValueCache()
	node := NewNode("primary", primaryCache)

	config := DefaultConfig()
	config.ReplicationMode = ReplicationNone
	dc := New([]CacheNode{node}, config)
	defer dc.Close()

	if err := primaryCache.Set(t.Context(), "owned", []byte("before"), time.Minute); err != nil {
		t.Fatalf("primary Set failed: %v", err)
	}

	got, err := dc.Get(t.Context(), "owned")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	copy(got, "after!")

	stored, err := primaryCache.Get(t.Context(), "owned")
	if err != nil {
		t.Fatalf("primary Get failed: %v", err)
	}
	if string(stored) != "before" {
		t.Fatalf("stored value = %q, want before", stored)
	}
}

func TestDistributedCacheFailoverGetOwnsReturnedBytes(t *testing.T) {
	primaryCache := &failingGetCache{
		Cache: cache.NewMemoryCache(),
		err:   errors.New("primary read failed"),
	}
	secondaryCache := newRetainingValueCache()
	nodes := []CacheNode{
		NewNode("primary", primaryCache),
		NewNode("secondary", secondaryCache),
	}

	config := DefaultConfig()
	config.ReplicationMode = ReplicationSync
	config.ReplicationFactor = 2
	config.FailoverStrategy = FailoverNextNode
	dc := New(nodes, config)
	defer dc.Close()

	key := findReplicaOrderKey(t, dc, "failover-get-owned", "primary", "secondary")
	if err := secondaryCache.Set(t.Context(), key, []byte("before"), time.Minute); err != nil {
		t.Fatalf("secondary Set failed: %v", err)
	}

	got, err := dc.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	copy(got, "after!")

	stored, err := secondaryCache.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("secondary Get failed: %v", err)
	}
	if string(stored) != "before" {
		t.Fatalf("stored value = %q, want before", stored)
	}
}

func TestDistributedCacheRetryGetOwnsReturnedBytes(t *testing.T) {
	primaryCache := newFlakyRetainingValueCache(1, errors.New("temporary read failed"))
	node := NewNode("primary", primaryCache)

	config := DefaultConfig()
	config.ReplicationMode = ReplicationNone
	config.FailoverStrategy = FailoverRetry
	config.FailoverRetryAttempts = 2
	dc := New([]CacheNode{node}, config)
	defer dc.Close()

	if err := primaryCache.Set(t.Context(), "owned", []byte("before"), time.Minute); err != nil {
		t.Fatalf("primary Set failed: %v", err)
	}

	got, err := dc.Get(t.Context(), "owned")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	copy(got, "after!")

	stored, err := primaryCache.Get(t.Context(), "owned")
	if err != nil {
		t.Fatalf("primary Get failed: %v", err)
	}
	if string(stored) != "before" {
		t.Fatalf("stored value = %q, want before", stored)
	}
}

func TestDistributedCacheSetOwnsCallerBytes(t *testing.T) {
	tests := []struct {
		name string
		mode ReplicationMode
	}{
		{name: "none", mode: ReplicationNone},
		{name: "sync", mode: ReplicationSync},
		{name: "async primary", mode: ReplicationAsync},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			primaryCache := newRetainingValueCache()
			nodes := []CacheNode{NewNode("primary", primaryCache)}

			config := DefaultConfig()
			config.ReplicationMode = tc.mode
			config.ReplicationFactor = 1
			var secondaryCache *retainingValueCache
			if tc.mode == ReplicationSync {
				secondaryCache = newRetainingValueCache()
				nodes = append(nodes, NewNode("secondary", secondaryCache))
				config.ReplicationFactor = 2
			}

			dc := New(nodes, config)
			defer dc.Close()

			value := []byte("before")
			if err := dc.Set(t.Context(), "owned", value, time.Minute); err != nil {
				t.Fatalf("Set failed: %v", err)
			}
			copy(value, "after!")

			got, err := primaryCache.Get(t.Context(), "owned")
			if err != nil {
				t.Fatalf("primary Get failed: %v", err)
			}
			if string(got) != "before" {
				t.Fatalf("primary stored value = %q, want before", got)
			}

			if tc.mode == ReplicationSync {
				got, err := secondaryCache.Get(t.Context(), "owned")
				if err != nil {
					t.Fatalf("secondary Get failed: %v", err)
				}
				if string(got) != "before" {
					t.Fatalf("secondary stored value = %q, want before", got)
				}
			}
		})
	}
}

func TestDistributedCacheAppendOwnsCallerBytes(t *testing.T) {
	primaryCache := newRetainingValueCache()
	secondaryCache := newRetainingValueCache()
	nodes := []CacheNode{
		NewNode("primary", primaryCache),
		NewNode("secondary", secondaryCache),
	}

	config := DefaultConfig()
	config.ReplicationMode = ReplicationSync
	config.ReplicationFactor = 2
	dc := New(nodes, config)
	defer dc.Close()

	data := []byte("before")
	if err := dc.Append(t.Context(), "owned", data); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	copy(data, "after!")

	for name, nodeCache := range map[string]*retainingValueCache{
		"primary":   primaryCache,
		"secondary": secondaryCache,
	} {
		got, err := nodeCache.Get(t.Context(), "owned")
		if err != nil {
			t.Fatalf("%s Get failed: %v", name, err)
		}
		if string(got) != "before" {
			t.Fatalf("%s stored value = %q, want before", name, got)
		}
	}
}

func TestDistributedCacheSetSyncReplicationBoundsFanout(t *testing.T) {
	nodes := make([]CacheNode, 0, 6)
	var active atomic.Int32
	var maxActive atomic.Int32
	for i := 0; i < 6; i++ {
		nodeCache := &trackingSetCache{
			Cache:  cache.NewMemoryCache(),
			active: &active,
			max:    &maxActive,
			delay:  20 * time.Millisecond,
		}
		nodes = append(nodes, NewNode(fmt.Sprintf("node-%d", i), nodeCache))
	}

	config := DefaultConfig()
	config.ReplicationMode = ReplicationSync
	config.ReplicationFactor = len(nodes)
	config.AsyncReplicationMaxConcurrency = 2
	dc := New(nodes, config)
	defer dc.Close()

	if err := dc.Set(t.Context(), "bounded", []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	max := maxActive.Load()
	if max > 2 {
		t.Fatalf("max concurrent Set calls = %d, want <= 2", max)
	}
	if max < 2 {
		t.Fatalf("max concurrent Set calls = %d, want fanout to use configured bound", max)
	}
}

func TestDistributedCacheSetSyncReplicationUsesSyncFanoutConfig(t *testing.T) {
	nodes := make([]CacheNode, 0, 6)
	var active atomic.Int32
	var maxActive atomic.Int32
	for i := 0; i < 6; i++ {
		nodeCache := &trackingSetCache{
			Cache:  cache.NewMemoryCache(),
			active: &active,
			max:    &maxActive,
			delay:  20 * time.Millisecond,
		}
		nodes = append(nodes, NewNode(fmt.Sprintf("node-%d", i), nodeCache))
	}

	config := DefaultConfig()
	config.ReplicationMode = ReplicationSync
	config.ReplicationFactor = len(nodes)
	config.AsyncReplicationMaxConcurrency = 2
	config.SyncReplicationMaxConcurrency = 3
	dc := New(nodes, config)
	defer dc.Close()

	if err := dc.Set(t.Context(), "bounded", []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	max := maxActive.Load()
	if max > 3 {
		t.Fatalf("max concurrent Set calls = %d, want <= 3", max)
	}
	if max < 3 {
		t.Fatalf("max concurrent Set calls = %d, want explicit sync bound", max)
	}
}

func TestDistributedCacheSetSyncReplicationWritesPrimaryFirst(t *testing.T) {
	primaryCache := &failingSetCache{
		Cache: cache.NewMemoryCache(),
		err:   errors.New("primary write failed"),
	}
	secondaryCache := newRetainingValueCache()
	nodes := []CacheNode{
		NewNode("primary", primaryCache),
		NewNode("secondary", secondaryCache),
	}

	config := DefaultConfig()
	config.ReplicationMode = ReplicationSync
	config.ReplicationFactor = 2
	dc := New(nodes, config)
	defer dc.Close()

	key := findReplicaOrderKey(t, dc, "sync-primary-first", "primary", "secondary")
	if err := dc.Set(t.Context(), key, []byte("value"), time.Minute); err == nil {
		t.Fatal("expected primary write error")
	}
	if _, err := secondaryCache.Get(t.Context(), key); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("secondary Get error = %v, want ErrNotFound", err)
	}
}

func TestDistributedConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name:   "negative virtual nodes",
			config: Config{VirtualNodes: -1, ReplicationFactor: 1},
		},
		{
			name:   "zero replication factor",
			config: Config{ReplicationFactor: 0},
		},
		{
			name:   "invalid replication mode",
			config: Config{ReplicationFactor: 1, ReplicationMode: ReplicationMode(99)},
		},
		{
			name:   "invalid failover strategy",
			config: Config{ReplicationFactor: 1, FailoverStrategy: FailoverStrategy(99)},
		},
		{
			name:   "negative health interval",
			config: Config{ReplicationFactor: 1, HealthCheckInterval: -time.Second},
		},
		{
			name:   "negative health timeout",
			config: Config{ReplicationFactor: 1, HealthCheckTimeout: -time.Second},
		},
		{
			name:   "negative failover retry attempts",
			config: Config{ReplicationFactor: 1, FailoverRetryAttempts: -1},
		},
		{
			name:   "negative failover retry backoff",
			config: Config{ReplicationFactor: 1, FailoverRetryBackoff: -time.Second},
		},
		{
			name:   "negative async replication timeout",
			config: Config{ReplicationFactor: 1, AsyncReplicationTimeout: -time.Second},
		},
		{
			name:   "negative async replication max concurrency",
			config: Config{ReplicationFactor: 1, AsyncReplicationMaxConcurrency: -1},
		},
		{
			name:   "negative sync replication max concurrency",
			config: Config{ReplicationFactor: 1, SyncReplicationMaxConcurrency: -1},
		},
		{
			name:   "negative async replication queue size",
			config: Config{ReplicationFactor: 1, AsyncReplicationQueueSize: -1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.config.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDistributedCacheNewWithConfigRejectsInvalidNodes(t *testing.T) {
	config := DefaultConfig()

	tests := []struct {
		name  string
		nodes []CacheNode
	}{
		{
			name:  "nil node",
			nodes: []CacheNode{nil},
		},
		{
			name:  "empty node id",
			nodes: []CacheNode{NewNode("", cache.NewMemoryCache())},
		},
		{
			name:  "nil node cache",
			nodes: []CacheNode{NewNode("node1", nil)},
		},
		{
			name: "duplicate node",
			nodes: []CacheNode{
				NewNode("node1", cache.NewMemoryCache()),
				NewNode("node1", cache.NewMemoryCache()),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dc, err := NewWithConfig(tc.nodes, config)
			if err == nil {
				if dc != nil {
					_ = dc.Close()
				}
				t.Fatal("expected construction error")
			}
			if dc != nil {
				t.Fatal("expected nil cache on construction error")
			}
		})
	}
}

func TestDistributedCacheAddNodeRejectsNilCache(t *testing.T) {
	dc := New([]CacheNode{NewNode("node1", cache.NewMemoryCache())}, DefaultConfig())
	defer dc.Close()

	err := dc.AddNode(NewNode("nil-cache", nil))
	if !errors.Is(err, errNodeCacheNil) {
		t.Fatalf("expected errNodeCacheNil, got %v", err)
	}
}

func TestDistributedCacheNewReturnsNilOnConstructionError(t *testing.T) {
	dc := New([]CacheNode{nil}, DefaultConfig())
	if dc != nil {
		t.Fatal("expected nil cache for invalid compatibility constructor input")
	}
}

func TestDistributedCacheCloseIdempotent(t *testing.T) {
	dc, err := NewWithConfig([]CacheNode{
		NewNode("node1", cache.NewMemoryCache()),
	}, DefaultConfig())
	if err != nil {
		t.Fatalf("NewWithConfig failed: %v", err)
	}

	if err := dc.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := dc.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestDistributedCacheOperationsFailAfterClose(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node0", cache.NewMemoryCache()),
		NewNode("node1", cache.NewMemoryCache()),
	}
	config := DefaultConfig()
	config.ReplicationFactor = 1
	dc := New(nodes, config)
	if err := dc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	ctx := t.Context()
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "Get",
			run: func() error {
				_, err := dc.Get(ctx, "key")
				return err
			},
		},
		{
			name: "Set",
			run: func() error {
				return dc.Set(ctx, "key", []byte("value"), time.Minute)
			},
		},
		{
			name: "Delete",
			run: func() error {
				return dc.Delete(ctx, "key")
			},
		},
		{
			name: "Exists",
			run: func() error {
				_, err := dc.Exists(ctx, "key")
				return err
			},
		},
		{
			name: "Clear",
			run: func() error {
				return dc.Clear(ctx)
			},
		},
		{
			name: "Incr",
			run: func() error {
				_, err := dc.Incr(ctx, "key", 1)
				return err
			},
		},
		{
			name: "Decr",
			run: func() error {
				_, err := dc.Decr(ctx, "key", 1)
				return err
			},
		},
		{
			name: "Append",
			run: func() error {
				return dc.Append(ctx, "key", []byte("value"))
			},
		},
		{
			name: "AddNode",
			run: func() error {
				return dc.AddNode(NewNode("node2", cache.NewMemoryCache()))
			},
		},
		{
			name: "RemoveNode",
			run: func() error {
				return dc.RemoveNode("node0")
			},
		},
		{
			name: "NodeHealth",
			run: func() error {
				_, err := dc.NodeHealth("node0")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrClosed) {
				t.Fatalf("error = %v, want ErrClosed", err)
			}
		})
	}

	if got := len(dc.Nodes()); got != 2 {
		t.Fatalf("Nodes after close = %d, want snapshot access", got)
	}
	if metrics := dc.GetMetrics(); metrics == nil {
		t.Fatal("expected metrics snapshot after close")
	}
}

func TestDistributedCacheReplication(t *testing.T) {
	// Create nodes
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	// Create distributed cache with replication
	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	dc := New(nodes, config)
	defer dc.Close()

	ctx := t.Context()

	// Set a key
	err := dc.Set(ctx, "replicated-key", []byte("replicated-value"), time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify the key is on multiple nodes
	// Note: We can't directly verify this without exposing internals,
	// but we can test failover
}

func TestDistributedCacheFailover(t *testing.T) {
	// Create nodes
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	// Create distributed cache with replication
	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	dc := New(nodes, config)
	defer dc.Close()

	ctx := t.Context()

	// Set a key
	key := "failover-key"
	value := []byte("failover-value")
	err := dc.Set(ctx, key, value, time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get the primary node for this key
	primaryNode, err := dc.ring.Get(key)
	if err != nil {
		t.Fatalf("failed to get primary node: %v", err)
	}

	// Mark primary node as unhealthy
	primaryNode.UpdateHealth(HealthStatusUnhealthy)

	// Try to get the value (should failover to replica)
	retrievedValue, err := dc.Get(ctx, key)
	if err != nil {
		t.Errorf("Get with failover failed: %v", err)
	}

	if string(retrievedValue) != string(value) {
		t.Errorf("expected %s, got %s", string(value), string(retrievedValue))
	}

	// Verify failover metric was incremented
	metrics := dc.GetMetrics()
	if metrics.FailoverCount == 0 {
		t.Error("expected failover count > 0")
	}
}

func TestDistributedCacheFailoverAllNodes(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node0", cache.NewMemoryCache()),
		NewNode("node1", cache.NewMemoryCache()),
		NewNode("node2", cache.NewMemoryCache()),
	}

	config := DefaultConfig()
	config.ReplicationFactor = 1
	config.FailoverStrategy = FailoverAllNodes
	dc := New(nodes, config)
	defer dc.Close()

	ctx := t.Context()
	key := "all-node-failover-key"
	primary, err := dc.ring.Get(key)
	if err != nil {
		t.Fatalf("ring get failed: %v", err)
	}

	var fallback CacheNode
	for _, node := range nodes {
		if node.ID() != primary.ID() {
			fallback = node
			break
		}
	}
	if fallback == nil {
		t.Fatal("expected fallback node")
	}

	if err := fallback.Cache().Set(ctx, key, []byte("fallback-value"), time.Minute); err != nil {
		t.Fatalf("fallback Set failed: %v", err)
	}
	primary.UpdateHealth(HealthStatusUnhealthy)

	got, err := dc.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(got) != "fallback-value" {
		t.Fatalf("value = %q, want fallback-value", got)
	}
}

func TestDistributedCacheFailoverRetry(t *testing.T) {
	transientErr := errors.New("temporary cache read failure")
	node := NewNode("node0", newFlakyGetCache(1, transientErr))

	config := DefaultConfig()
	config.ReplicationFactor = 1
	config.FailoverStrategy = FailoverRetry
	dc := New([]CacheNode{node}, config)
	defer dc.Close()

	ctx := t.Context()
	if err := dc.Set(ctx, "retry-key", []byte("retry-value"), time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, err := dc.Get(ctx, "retry-key")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(got) != "retry-value" {
		t.Fatalf("value = %q, want retry-value", got)
	}
}

func TestDistributedCacheFailoverNextNodeReturnsCauseWithoutReplica(t *testing.T) {
	node := NewNode("node0", cache.NewMemoryCache())
	config := DefaultConfig()
	config.ReplicationFactor = 1
	config.FailoverStrategy = FailoverNextNode
	dc := New([]CacheNode{node}, config)
	defer dc.Close()

	node.UpdateHealth(HealthStatusUnhealthy)
	_, err := dc.Get(t.Context(), "key")
	if !errors.Is(err, ErrNodeUnhealthy) {
		t.Fatalf("expected ErrNodeUnhealthy, got %v", err)
	}
}

func TestDistributedCacheSyncSetFailsWhenAllReplicasUnhealthy(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node0", cache.NewMemoryCache()),
		NewNode("node1", cache.NewMemoryCache()),
	}

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	dc := New(nodes, config)
	defer dc.Close()

	for _, node := range nodes {
		node.UpdateHealth(HealthStatusUnhealthy)
	}

	err := dc.Set(t.Context(), "key", []byte("value"), time.Minute)
	if !errors.Is(err, ErrNodeUnhealthy) {
		t.Fatalf("expected ErrNodeUnhealthy, got %v", err)
	}
}

func TestDistributedCacheSetFailsWhenReplicationUnderfilled(t *testing.T) {
	config := DefaultConfig()
	config.ReplicationFactor = 2
	dc := New([]CacheNode{NewNode("node0", cache.NewMemoryCache())}, config)
	defer dc.Close()

	err := dc.Set(t.Context(), "key", []byte("value"), time.Minute)
	if !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("expected ErrInsufficientReplicas, got %v", err)
	}
}

func TestDistributedCacheReplicationNoneUsesPrimaryOnly(t *testing.T) {
	node := NewNode("node0", cache.NewMemoryCache())
	config := DefaultConfig()
	config.ReplicationFactor = 3
	config.ReplicationMode = ReplicationNone
	dc := New([]CacheNode{node}, config)
	defer dc.Close()

	ctx := t.Context()
	if err := dc.Set(ctx, "key", []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	exists, err := dc.Exists(ctx, "key")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Fatal("expected key to exist")
	}
	if got, err := dc.Incr(ctx, "counter", 2); err != nil || got != 2 {
		t.Fatalf("Incr = %d, %v; want 2, nil", got, err)
	}
	if err := dc.Append(ctx, "append", []byte("a")); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := dc.Delete(ctx, "key"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	exists, err = dc.Exists(ctx, "key")
	if err != nil {
		t.Fatalf("Exists after delete failed: %v", err)
	}
	if exists {
		t.Fatal("expected key to be deleted")
	}
}

func TestDistributedCacheReplicationNoneFailoverNextNodeReturnsCauseWithoutReplica(t *testing.T) {
	node := NewNode("node0", cache.NewMemoryCache())
	config := DefaultConfig()
	config.ReplicationFactor = 3
	config.ReplicationMode = ReplicationNone
	config.FailoverStrategy = FailoverNextNode
	dc := New([]CacheNode{node}, config)
	defer dc.Close()

	node.UpdateHealth(HealthStatusUnhealthy)
	_, err := dc.Get(t.Context(), "key")
	if !errors.Is(err, ErrNodeUnhealthy) {
		t.Fatalf("expected ErrNodeUnhealthy, got %v", err)
	}
}

func TestDistributedCacheExistsUsesFailoverReplicas(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node0", cache.NewMemoryCache()),
		NewNode("node1", cache.NewMemoryCache()),
		NewNode("node2", cache.NewMemoryCache()),
	}
	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	config.FailoverStrategy = FailoverNextNode
	dc := New(nodes, config)
	defer dc.Close()

	ctx := t.Context()
	key := "exists-failover-key"
	if err := dc.Set(ctx, key, []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	primary, err := dc.ring.Get(key)
	if err != nil {
		t.Fatalf("ring Get failed: %v", err)
	}
	primary.UpdateHealth(HealthStatusUnhealthy)

	exists, err := dc.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Fatal("expected failover replica to report key existence")
	}
}

func TestDistributedCacheFailoverRetryUsesConfiguredAttempts(t *testing.T) {
	getErr := errors.New("transient get failure")
	client := &failingGetCache{
		Cache: cache.NewMemoryCache(),
		err:   getErr,
	}
	node := NewNode("node0", client)
	config := DefaultConfig()
	config.ReplicationMode = ReplicationNone
	config.FailoverStrategy = FailoverRetry
	config.FailoverRetryAttempts = 2
	config.FailoverRetryBackoff = time.Nanosecond
	dc := New([]CacheNode{node}, config)
	defer dc.Close()

	_, err := dc.Get(t.Context(), "key")
	if !errors.Is(err, getErr) {
		t.Fatalf("expected get error, got %v", err)
	}
	if got, want := client.calls.Load(), int32(3); got != want {
		t.Fatalf("Get calls = %d, want %d", got, want)
	}
}

func TestDistributedCacheMutationsReplicateSynchronously(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node0", cache.NewMemoryCache()),
		NewNode("node1", cache.NewMemoryCache()),
		NewNode("node2", cache.NewMemoryCache()),
	}

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	dc := New(nodes, config)
	defer dc.Close()

	ctx := t.Context()
	replicas, err := dc.ring.GetN("counter", 2)
	if err != nil {
		t.Fatalf("GetN failed: %v", err)
	}

	if got, err := dc.Incr(ctx, "counter", 5); err != nil || got != 5 {
		t.Fatalf("Incr = %d, %v; want 5, nil", got, err)
	}
	replicaCounter, ok := replicas[1].Cache().(cache.CounterCache)
	if !ok {
		t.Fatalf("replica cache does not implement CounterCache")
	}
	replicaValue, err := replicaCounter.Incr(ctx, "counter", 0)
	if err != nil {
		t.Fatalf("replica Incr read failed: %v", err)
	}
	if replicaValue != 5 {
		t.Fatalf("replica counter = %d, want 5", replicaValue)
	}

	appendReplicas, err := dc.ring.GetN("append-key", 2)
	if err != nil {
		t.Fatalf("GetN append failed: %v", err)
	}
	if err := dc.Append(ctx, "append-key", []byte("hello")); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	got, err := appendReplicas[1].Cache().Get(ctx, "append-key")
	if err != nil {
		t.Fatalf("replica Get failed: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("replica append value = %q, want hello", got)
	}
}

func TestDistributedCacheSyncSetReportsReplicaFailureAfterPartialWrite(t *testing.T) {
	replicaErr := errors.New("replica set failed")
	primary := NewNode("primary", cache.NewMemoryCache())
	secondary := NewNode("secondary", &failingSetCache{
		Cache: cache.NewMemoryCache(),
		err:   replicaErr,
	})

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	dc := New([]CacheNode{primary, secondary}, config)
	defer dc.Close()

	ctx := t.Context()
	key := findReplicaOrderKey(t, dc, "partial-set", "primary", "secondary")
	err := dc.Set(ctx, key, []byte("primary-visible"), time.Minute)
	if !errors.Is(err, replicaErr) {
		t.Fatalf("expected replica error, got %v", err)
	}
	value, err := primary.Cache().Get(ctx, key)
	if err != nil {
		t.Fatalf("primary read failed: %v", err)
	}
	if string(value) != "primary-visible" {
		t.Fatalf("primary value = %q, want primary-visible", value)
	}
}

func TestDistributedCacheSyncIncrReportsReplicaFailureAfterPrimaryMutation(t *testing.T) {
	replicaErr := errors.New("replica incr failed")
	primary := NewNode("primary", cache.NewMemoryCache())
	secondary := NewNode("secondary", &failingMutationCache{
		Cache: cache.NewMemoryCache(),
		err:   replicaErr,
	})

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	dc := New([]CacheNode{primary, secondary}, config)
	defer dc.Close()

	ctx := t.Context()
	key := findReplicaOrderKey(t, dc, "partial-incr", "primary", "secondary")
	value, err := dc.Incr(ctx, key, 5)
	if !errors.Is(err, replicaErr) {
		t.Fatalf("expected replica error, got %v", err)
	}
	if value != 5 {
		t.Fatalf("returned primary value = %d, want 5", value)
	}
	primaryCounter, ok := primary.Cache().(cache.CounterCache)
	if !ok {
		t.Fatalf("primary cache does not implement CounterCache")
	}
	primaryValue, err := primaryCounter.Incr(ctx, key, 0)
	if err != nil {
		t.Fatalf("primary read failed: %v", err)
	}
	if primaryValue != 5 {
		t.Fatalf("primary value = %d, want 5", primaryValue)
	}
}

func TestDistributedCacheDeleteReportsReplicaFailureAfterPartialDelete(t *testing.T) {
	replicaErr := errors.New("replica delete failed")
	primary := NewNode("primary", cache.NewMemoryCache())
	secondary := NewNode("secondary", &failingDeleteCache{
		Cache: cache.NewMemoryCache(),
		err:   replicaErr,
	})

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	dc := New([]CacheNode{primary, secondary}, config)
	defer dc.Close()

	ctx := t.Context()
	key := findReplicaOrderKey(t, dc, "partial-delete", "primary", "secondary")
	if err := primary.Cache().Set(ctx, key, []byte("value"), time.Minute); err != nil {
		t.Fatalf("primary seed failed: %v", err)
	}
	if err := secondary.Cache().Set(ctx, key, []byte("value"), time.Minute); err != nil {
		t.Fatalf("secondary seed failed: %v", err)
	}

	err := dc.Delete(ctx, key)
	if !errors.Is(err, replicaErr) {
		t.Fatalf("expected replica error, got %v", err)
	}
	if _, err := primary.Cache().Get(ctx, key); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("primary Get after delete = %v, want ErrNotFound", err)
	}
	if _, err := secondary.Cache().Get(ctx, key); err != nil {
		t.Fatalf("secondary value should remain after failed delete, got %v", err)
	}
}

func TestDistributedCacheSyncAppendReportsReplicaFailureAfterPrimaryMutation(t *testing.T) {
	replicaErr := errors.New("replica append failed")
	primary := NewNode("primary", cache.NewMemoryCache())
	secondary := NewNode("secondary", &failingMutationCache{
		Cache: cache.NewMemoryCache(),
		err:   replicaErr,
	})

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationSync
	dc := New([]CacheNode{primary, secondary}, config)
	defer dc.Close()

	ctx := t.Context()
	key := findReplicaOrderKey(t, dc, "partial-append", "primary", "secondary")
	err := dc.Append(ctx, key, []byte("primary-visible"))
	if !errors.Is(err, replicaErr) {
		t.Fatalf("expected replica error, got %v", err)
	}
	value, err := primary.Cache().Get(ctx, key)
	if err != nil {
		t.Fatalf("primary read failed: %v", err)
	}
	if string(value) != "primary-visible" {
		t.Fatalf("primary value = %q, want primary-visible", value)
	}
}

func TestDistributedCacheNodeManagement(t *testing.T) {
	// Create initial nodes
	nodes := make([]CacheNode, 2)
	for i := 0; i < 2; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	// Verify initial node count
	if len(dc.Nodes()) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(dc.Nodes()))
	}

	// Add a new node
	newNode := NewNode("node2", cache.NewMemoryCache())
	err := dc.AddNode(newNode)
	if err != nil {
		t.Errorf("AddNode failed: %v", err)
	}

	if len(dc.Nodes()) != 3 {
		t.Errorf("expected 3 nodes after add, got %d", len(dc.Nodes()))
	}

	// Remove a node
	err = dc.RemoveNode("node1")
	if err != nil {
		t.Errorf("RemoveNode failed: %v", err)
	}

	if len(dc.Nodes()) != 2 {
		t.Errorf("expected 2 nodes after removal, got %d", len(dc.Nodes()))
	}

	// Verify rebalance events
	metrics := dc.GetMetrics()
	if metrics.RebalanceEvents != 2 {
		t.Errorf("expected 2 rebalance events, got %d", metrics.RebalanceEvents)
	}
}

func TestDistributedCacheClearFailsClosed(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node0", cache.NewMemoryCache()),
		NewNode("node1", cache.NewMemoryCache()),
	}
	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	for _, node := range nodes {
		node.UpdateHealth(HealthStatusUnhealthy)
	}

	err := dc.Clear(t.Context())
	if !errors.Is(err, ErrNodeUnhealthy) {
		t.Fatalf("expected ErrNodeUnhealthy, got %v", err)
	}
}

func TestDistributedCacheClearReportsPartialFailure(t *testing.T) {
	clearErr := errors.New("clear failed")
	nodes := []CacheNode{
		NewNode("node0", cache.NewMemoryCache()),
		NewNode("node1", &failingClearCache{
			Cache: cache.NewMemoryCache(),
			err:   clearErr,
		}),
	}
	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	err := dc.Clear(t.Context())
	if !errors.Is(err, clearErr) {
		t.Fatalf("expected wrapped clear error, got %v", err)
	}
}

func TestDistributedCacheIncr(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node1", cache.NewMemoryCache()),
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := t.Context()

	// Test Incr
	val, err := dc.Incr(ctx, "counter", 5)
	if err != nil {
		t.Errorf("Incr failed: %v", err)
	}

	if val != 5 {
		t.Errorf("expected 5, got %d", val)
	}

	// Incr again
	val, err = dc.Incr(ctx, "counter", 3)
	if err != nil {
		t.Errorf("Incr failed: %v", err)
	}

	if val != 8 {
		t.Errorf("expected 8, got %d", val)
	}
}

func TestDistributedCacheDecr(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node1", cache.NewMemoryCache()),
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := t.Context()

	// Test Decr
	val, err := dc.Decr(ctx, "counter", 5)
	if err != nil {
		t.Errorf("Decr failed: %v", err)
	}

	if val != -5 {
		t.Errorf("expected -5, got %d", val)
	}
}

func TestDistributedCacheConcurrency(t *testing.T) {
	nodes := make([]CacheNode, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewNode(fmt.Sprintf("node%d", i), cache.NewMemoryCache())
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := t.Context()
	const numGoroutines = 100
	const numOpsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numOpsPerGoroutine; j++ {
				key := fmt.Sprintf("key%d-%d", id, j)
				value := []byte(fmt.Sprintf("value%d-%d", id, j))

				// Set
				_ = dc.Set(ctx, key, value, time.Minute)

				// Get
				_, _ = dc.Get(ctx, key)

				// Delete
				_ = dc.Delete(ctx, key)
			}
		}(i)
	}

	wg.Wait()

	// Verify metrics
	metrics := dc.GetMetrics()
	expectedRequests := uint64(numGoroutines * numOpsPerGoroutine * 3) // Set + Get + Delete
	if metrics.TotalRequests != expectedRequests {
		t.Errorf("expected %d requests, got %d", expectedRequests, metrics.TotalRequests)
	}
}

func TestDistributedCacheMetrics(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node1", cache.NewMemoryCache()),
		NewNode("node2", cache.NewMemoryCache()),
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := t.Context()

	// Perform operations
	_ = dc.Set(ctx, "key1", []byte("value1"), time.Minute)
	_, _ = dc.Get(ctx, "key1")
	_ = dc.Delete(ctx, "key1")

	// Get metrics
	metrics := dc.GetMetrics()

	if metrics.TotalRequests != 3 {
		t.Errorf("expected 3 requests, got %d", metrics.TotalRequests)
	}

	if metrics.HealthyNodes != 2 {
		t.Errorf("expected 2 healthy nodes, got %d", metrics.HealthyNodes)
	}

	if metrics.UnhealthyNodes != 0 {
		t.Errorf("expected 0 unhealthy nodes, got %d", metrics.UnhealthyNodes)
	}
}

func TestDistributedCacheMetricsExposeHashCollisions(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node1", cache.NewMemoryCache()),
		NewNode("node2", cache.NewMemoryCache()),
	}
	config := DefaultConfig()
	config.VirtualNodes = 1
	config.HashFunc = func(data []byte) uint32 {
		return 1
	}

	dc := New(nodes, config)
	defer dc.Close()

	metrics := dc.GetMetrics()
	if metrics.HashCollisions == 0 {
		t.Fatal("expected hash collisions in metrics")
	}
}

func TestDistributedCacheAsyncReplicationFailureMetrics(t *testing.T) {
	replicaErr := errors.New("replica write failed")
	primary := NewNode("primary", cache.NewMemoryCache())
	secondary := NewNode("secondary", &failingSetCache{
		Cache: cache.NewMemoryCache(),
		err:   replicaErr,
	})

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationAsync
	dc := New([]CacheNode{primary, secondary}, config)
	defer dc.Close()

	key := ""
	for i := 0; i < 1000; i++ {
		candidate := fmt.Sprintf("replica-failure-%d", i)
		replicas, err := dc.ring.GetN(candidate, 2)
		if err != nil {
			t.Fatalf("GetN failed: %v", err)
		}
		if replicas[0].ID() == "primary" && replicas[1].ID() == "secondary" {
			key = candidate
			break
		}
	}
	if key == "" {
		t.Fatal("failed to find key with primary before failing secondary")
	}

	if err := dc.Set(t.Context(), key, []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set returned primary error: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		metrics := dc.GetMetrics()
		if metrics.ReplicationFailures > 0 {
			if metrics.ReplicationLag <= 0 {
				t.Fatal("expected replication lag to be recorded")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for async replication failure metric")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDistributedCacheAsyncReplicationTimeoutMetrics(t *testing.T) {
	primary := NewNode("primary", cache.NewMemoryCache())
	secondary := NewNode("secondary", &blockingSetCache{
		Cache: cache.NewMemoryCache(),
	})

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationAsync
	config.AsyncReplicationTimeout = 20 * time.Millisecond
	dc := New([]CacheNode{primary, secondary}, config)
	defer dc.Close()

	key := ""
	for i := 0; i < 1000; i++ {
		candidate := fmt.Sprintf("replica-timeout-%d", i)
		replicas, err := dc.ring.GetN(candidate, 2)
		if err != nil {
			t.Fatalf("GetN failed: %v", err)
		}
		if replicas[0].ID() == "primary" && replicas[1].ID() == "secondary" {
			key = candidate
			break
		}
	}
	if key == "" {
		t.Fatal("failed to find key with primary before blocking secondary")
	}

	if err := dc.Set(t.Context(), key, []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set returned primary error: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		metrics := dc.GetMetrics()
		if metrics.ReplicationFailures > 0 && metrics.ReplicationLag > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for async replication timeout metric")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDistributedCacheAsyncReplicationDropsWhenQueueFull(t *testing.T) {
	primary := NewNode("primary", cache.NewMemoryCache())
	blocking := &blockingSetCache{
		Cache:   cache.NewMemoryCache(),
		started: make(chan struct{}),
	}
	secondary := NewNode("secondary", blocking)
	drops := make(chan AsyncReplicationDrop, 1)

	config := DefaultConfig()
	config.ReplicationFactor = 2
	config.ReplicationMode = ReplicationAsync
	config.AsyncReplicationMaxConcurrency = 1
	config.AsyncReplicationQueueSize = 1
	config.AsyncReplicationTimeout = 200 * time.Millisecond
	config.AsyncReplicationDropHandler = func(drop AsyncReplicationDrop) {
		select {
		case drops <- drop:
		default:
		}
	}
	dc := New([]CacheNode{primary, secondary}, config)
	defer dc.Close()

	key1 := findReplicaOrderKey(t, dc, "replica-queue-running", "primary", "secondary")
	if err := dc.Set(t.Context(), key1, []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set returned primary error: %v", err)
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async worker to start")
	}

	key2 := findReplicaOrderKey(t, dc, "replica-queue-buffered", "primary", "secondary")
	if err := dc.Set(t.Context(), key2, []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set returned primary error: %v", err)
	}
	key3 := findReplicaOrderKey(t, dc, "replica-queue-drop", "primary", "secondary")
	if err := dc.Set(t.Context(), key3, []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set returned primary error: %v", err)
	}

	var drop AsyncReplicationDrop
	select {
	case drop = <-drops:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async replication drop callback")
	}
	if drop.Operation != "set" || drop.Key != key3 || drop.NodeID != "secondary" || drop.Reason != AsyncReplicationDropQueueFull {
		t.Fatalf("unexpected drop callback: %#v", drop)
	}

	metrics := dc.GetMetrics()
	if metrics.ReplicationFailures == 0 {
		t.Fatal("expected dropped secondary write to increment ReplicationFailures")
	}
	if _, err := secondary.Cache().Get(t.Context(), key3); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("secondary value error = %v, want ErrNotFound", err)
	}
}

func TestDistributedCacheCloseDropsQueuedAsyncReplication(t *testing.T) {
	drops := make(chan AsyncReplicationDrop, 1)
	queuedKey := "queued-key"
	dc := &DistributedCache{
		healthChecker: NewHealthChecker(nil),
		asyncQueue:    make(chan asyncReplicationJob, 1),
		asyncStop:     make(chan struct{}),
		asyncDropHandler: func(drop AsyncReplicationDrop) {
			drops <- drop
		},
		metrics: &DistributedMetrics{},
	}
	dc.asyncQueue <- asyncReplicationJob{
		operation: "set",
		key:       queuedKey,
		nodeID:    "secondary",
		run: func(ctx context.Context) error {
			t.Fatal("queued job should be dropped during close drain")
			return nil
		},
	}

	if err := dc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	select {
	case drop := <-drops:
		if drop.Operation != "set" || drop.Key != queuedKey || drop.NodeID != "secondary" || drop.Reason != AsyncReplicationDropClosed {
			t.Fatalf("unexpected drop callback: %#v", drop)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for close-time async replication drop callback")
	}
}

func TestDistributedCacheAsyncDropHandlerPanicIsRecovered(t *testing.T) {
	dc := &DistributedCache{
		asyncQueue: make(chan asyncReplicationJob, 1),
		asyncStop:  make(chan struct{}),
		asyncDropHandler: func(drop AsyncReplicationDrop) {
			panic("drop handler failed")
		},
		metrics: &DistributedMetrics{},
	}
	dc.asyncQueue <- asyncReplicationJob{}

	dc.scheduleAsyncReplication(asyncReplicationJob{
		operation: "set",
		key:       "key",
		nodeID:    "node",
	})

	if got := atomic.LoadUint64(&dc.metrics.ReplicationFailures); got != 2 {
		t.Fatalf("ReplicationFailures = %d, want drop and callback panic failures", got)
	}
}

func TestDistributedCacheAsyncReplicationContextDefaultsInvalidInternalTimeout(t *testing.T) {
	dc := &DistributedCache{}
	ctx, cancel := dc.asyncReplicationContext()
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected fallback async replication deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > defaultAsyncTimeout {
		t.Fatalf("fallback deadline remaining = %v, want within %v", remaining, defaultAsyncTimeout)
	}
}

func TestHealthCheckerUsesCustomProbe(t *testing.T) {
	probeErr := errors.New("probe failed")
	node := NewNode("node", cache.NewMemoryCache())
	hc := NewHealthChecker(&HealthCheckerConfig{
		CheckInterval: time.Hour,
		Probe: func(ctx context.Context, node CacheNode) error {
			return probeErr
		},
	})

	hc.checkNode(node)
	if node.HealthStatus() != HealthStatusUnhealthy {
		t.Fatalf("status = %v, want unhealthy", node.HealthStatus())
	}
}

func TestHealthCheckerFailureCallbackPanicIsRecovered(t *testing.T) {
	probeErr := errors.New("probe failed")
	node := NewNode("node", cache.NewMemoryCache())
	var calls atomic.Int32
	hc := NewHealthChecker(&HealthCheckerConfig{
		CheckInterval: time.Hour,
		Probe: func(ctx context.Context, node CacheNode) error {
			return probeErr
		},
		FailureCallback: func(nodeID string, err error) {
			calls.Add(1)
			panic("failure callback failed")
		},
	})

	hc.checkNode(node)
	if calls.Load() != 1 {
		t.Fatalf("callback calls = %d, want 1", calls.Load())
	}
	if node.HealthStatus() != HealthStatusUnhealthy {
		t.Fatalf("status = %v, want unhealthy", node.HealthStatus())
	}
}

func TestHealthCheckerDoesNotMutateConfig(t *testing.T) {
	config := &HealthCheckerConfig{}
	hc := NewHealthChecker(config)

	if config.CheckInterval != 0 {
		t.Fatalf("caller CheckInterval = %v, want unchanged zero", config.CheckInterval)
	}
	if config.CheckTimeout != 0 {
		t.Fatalf("caller CheckTimeout = %v, want unchanged zero", config.CheckTimeout)
	}
	if hc.checkInterval <= 0 {
		t.Fatal("expected health checker to normalize internal interval")
	}
	if hc.checkTimeout <= 0 {
		t.Fatal("expected health checker to normalize internal timeout")
	}
}

func TestDistributedCacheConfigPassesHealthProbe(t *testing.T) {
	probeErr := errors.New("probe failed")
	node := NewNode("node", cache.NewMemoryCache())
	config := DefaultConfig()
	config.HealthCheckInterval = time.Hour
	config.HealthProbe = func(ctx context.Context, node CacheNode) error {
		return probeErr
	}

	dc, err := NewWithConfig([]CacheNode{node}, config)
	if err != nil {
		t.Fatalf("NewWithConfig failed: %v", err)
	}
	defer dc.Close()

	dc.healthChecker.checkNode(node)
	if node.HealthStatus() != HealthStatusUnhealthy {
		t.Fatalf("status = %v, want unhealthy", node.HealthStatus())
	}
}

func TestHealthChecker(t *testing.T) {
	config := DefaultHealthCheckerConfig()
	config.CheckInterval = 100 * time.Millisecond

	hc := NewHealthChecker(config)

	// Create test node
	node := NewNode("test-node", cache.NewMemoryCache())
	if err := hc.AddNode(node); err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}

	// Start health checking
	hc.Start()
	defer hc.Stop()

	// Wait for a health check
	time.Sleep(200 * time.Millisecond)

	// Node should still be healthy
	status, err := hc.GetNodeStatus("test-node")
	if err != nil {
		t.Errorf("failed to get node status: %v", err)
	}

	if status != HealthStatusHealthy {
		t.Errorf("expected healthy status, got %v", status)
	}
}

func TestHealthCheckerLifecycleAndValidation(t *testing.T) {
	config := DefaultHealthCheckerConfig()
	config.CheckInterval = time.Hour
	hc := NewHealthChecker(config)

	if err := hc.AddNode(nil); err == nil {
		t.Fatal("expected nil node error")
	}
	if err := hc.AddNode(NewNode("", cache.NewMemoryCache())); err == nil {
		t.Fatal("expected empty node id error")
	}
	if err := hc.AddNode(NewNode("node1", cache.NewMemoryCache())); err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}

	hc.Start()
	hc.Start()
	hc.Stop()
	hc.Stop()
}

func TestDistributedCacheAppend(t *testing.T) {
	nodes := []CacheNode{
		NewNode("node1", cache.NewMemoryCache()),
	}

	dc := New(nodes, DefaultConfig())
	defer dc.Close()

	ctx := t.Context()

	// Append to non-existent key
	err := dc.Append(ctx, "append-key", []byte("hello"))
	if err != nil {
		t.Errorf("Append failed: %v", err)
	}

	// Append again
	err = dc.Append(ctx, "append-key", []byte(" world"))
	if err != nil {
		t.Errorf("Append failed: %v", err)
	}

	// Verify result
	value, err := dc.Get(ctx, "append-key")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}

	expected := "hello world"
	if string(value) != expected {
		t.Errorf("expected %s, got %s", expected, string(value))
	}
}
