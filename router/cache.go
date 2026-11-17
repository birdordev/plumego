package router

import (
	"sync"
)

// matchCache is a read-optimized route-match cache backed by a ring buffer
// for O(1) FIFO eviction. Get acquires only an RLock; promotion is
// intentionally absent so concurrent reads never cause write-lock contention.
// Set acquires a full Lock and evicts the oldest-inserted entry when full.
type matchCache struct {
	capacity int
	mu       sync.RWMutex
	data     map[string]*matchResult
	ring     []string // circular FIFO insertion-order buffer
	head     int      // index of the oldest entry
	size     int      // number of live entries
}

// newMatchCache creates a new route cache with the given capacity.
func newMatchCache(capacity int) *matchCache {
	if capacity <= 0 {
		capacity = defaultCacheCapacity
	}
	return &matchCache{
		capacity: capacity,
		data:     make(map[string]*matchResult, capacity),
		ring:     make([]string, capacity),
	}
}

// Get retrieves a cached route match result. It acquires only an RLock so
// concurrent readers never block each other.
func (rc *matchCache) Get(key string) (*matchResult, bool) {
	rc.mu.RLock()
	v, ok := rc.data[key]
	rc.mu.RUnlock()
	return v, ok
}

// Set adds or updates a route match result. When the cache is full the
// oldest-inserted entry is evicted (FIFO).
func (rc *matchCache) Set(key string, value *matchResult) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if _, exists := rc.data[key]; exists {
		rc.data[key] = value
		return
	}

	if rc.size >= rc.capacity {
		// Evict the oldest entry via the ring-buffer head.
		oldest := rc.ring[rc.head]
		delete(rc.data, oldest)
		rc.ring[rc.head] = key
		rc.head = (rc.head + 1) % rc.capacity
		rc.data[key] = value
		return
	}

	tail := (rc.head + rc.size) % rc.capacity
	rc.ring[tail] = key
	rc.size++
	rc.data[key] = value
}

// Clear removes all entries from the cache.
func (rc *matchCache) Clear() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.size == 0 {
		return
	}
	clear(rc.data)
	clear(rc.ring)
	rc.head = 0
	rc.size = 0
}
