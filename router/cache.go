package router

import (
	"hash/maphash"
	"strings"
	"sync"
)

type matchCacheKey struct{ method, path string }

// matchCache is a read-optimized route-match cache backed by a ring buffer
// for O(1) FIFO eviction. Get acquires only an RLock; promotion is
// intentionally absent so concurrent reads never cause write-lock contention.
// Set acquires a full Lock and evicts the oldest-inserted entry when full.
type matchCache struct {
	capacity  int
	mu        sync.RWMutex
	data      map[matchCacheKey]*matchResult
	ring      []matchCacheKey // circular FIFO insertion-order buffer
	head      int             // index of the oldest entry
	size      int             // number of live entries
	admission *[128]uint64
	seed      maphash.Seed
}

// newMatchCache creates a new route cache with the given capacity.
func newMatchCache(capacity int) *matchCache {
	if capacity <= 0 {
		capacity = defaultCacheCapacity
	}
	return &matchCache{
		capacity: capacity,
		data:     make(map[matchCacheKey]*matchResult, capacity),
		ring:     make([]matchCacheKey, capacity),
	}
}

// Get retrieves a cached route match result under a read lock without changing
// the FIFO order.
func (rc *matchCache) Get(key matchCacheKey) (*matchResult, bool) {
	rc.mu.RLock()
	v, ok := rc.data[key]
	rc.mu.RUnlock()
	return v, ok
}

// Set adds or updates a route match result. When the cache is full the
// oldest-inserted entry is evicted (FIFO).
func (rc *matchCache) Set(key matchCacheKey, value *matchResult) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.setLocked(key, value)
}

// storeMatch uses bounded repeated-use admission for dynamic paths under pressure.
func (rc *matchCache) storeMatch(key matchCacheKey, value *matchResult, dynamic bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if _, exists := rc.data[key]; exists {
		return
	}
	if dynamic && rc.size >= rc.capacity {
		if rc.admission == nil {
			rc.admission = new([128]uint64)
			rc.seed = maphash.MakeSeed()
		}
		hash := (maphash.String(rc.seed, key.path) ^ maphash.String(rc.seed, key.method)) | 1<<63
		slot := &rc.admission[hash%uint64(len(rc.admission))]
		if *slot != hash {
			*slot = hash
			return
		}
		*slot = 0
	}
	key.path = strings.Clone(key.path)
	rc.setLocked(key, value)
}

func (rc *matchCache) setLocked(key matchCacheKey, value *matchResult) {

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
	if rc.admission != nil {
		clear(rc.admission[:])
	}
	rc.head = 0
	rc.size = 0
}
