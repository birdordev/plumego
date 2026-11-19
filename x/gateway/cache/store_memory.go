package cache

import (
	"container/list"
	"sync"
	"time"
)

// MemoryStore is an in-memory LRU cache store
// Uses Go standard library only (container/list for LRU)
type MemoryStore struct {
	capacity int
	items    map[string]*list.Element
	lruList  *list.List
	mu       sync.RWMutex

	// Statistics
	hits      uint64
	misses    uint64
	evictions uint64
	size      int64
}

// entry represents a cache entry in the LRU list
type entry struct {
	key      string
	response *CachedResponse
}

// NewMemoryStore creates a new in-memory LRU cache
func NewMemoryStore(capacity int) *MemoryStore {
	if capacity <= 0 {
		capacity = 1000
	}

	return &MemoryStore{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		lruList:  list.New(),
	}
}

// Get retrieves a cached response
func (s *MemoryStore) Get(key string) (*CachedResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	elem, found := s.items[key]
	if !found {
		s.misses++
		return nil, false
	}

	// Get entry
	ent := elem.Value.(*entry)

	// Check expiration
	if ent.response.IsExpired() {
		// Remove expired entry
		s.removeElement(elem)
		s.misses++
		return nil, false
	}

	// Move to front (most recently used)
	s.lruList.MoveToFront(elem)

	s.hits++
	return ent.response, true
}

// Set stores a response in cache. A positive TTL sets the expiry when
// resp.ExpiresAt is zero, without modifying the supplied response.
func (s *MemoryStore) Set(key string, resp *CachedResponse, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if resp.ExpiresAt.IsZero() && ttl > 0 {
		owned := *resp
		owned.ExpiresAt = time.Now().Add(ttl)
		resp = &owned
	}

	// Check if key already exists
	if elem, found := s.items[key]; found {
		// Update existing entry
		ent := elem.Value.(*entry)
		oldSize := ent.response.Size
		ent.response = resp
		s.size = s.size - oldSize + resp.Size
		s.lruList.MoveToFront(elem)
		return
	}

	// Create new entry
	ent := &entry{
		key:      key,
		response: resp,
	}

	// Add to front of LRU list
	elem := s.lruList.PushFront(ent)
	s.items[key] = elem
	s.size += resp.Size

	// Check capacity and evict if necessary
	if s.lruList.Len() > s.capacity {
		s.evictOldest()
	}
}

// Delete removes a cached response
func (s *MemoryStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if elem, found := s.items[key]; found {
		s.removeElement(elem)
	}
}

// Clear removes all cached responses
func (s *MemoryStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = make(map[string]*list.Element, s.capacity)
	s.lruList.Init()
	s.size = 0
}

// Stats returns cache statistics
func (s *MemoryStore) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return StoreStats{
		Items:     len(s.items),
		Hits:      s.hits,
		Misses:    s.misses,
		Evictions: s.evictions,
		Size:      s.size,
	}
}

// evictOldest evicts the least recently used entry
func (s *MemoryStore) evictOldest() {
	elem := s.lruList.Back()
	if elem != nil {
		s.removeElement(elem)
		s.evictions++
	}
}

// removeElement removes an element from cache
func (s *MemoryStore) removeElement(elem *list.Element) {
	ent := elem.Value.(*entry)
	s.lruList.Remove(elem)
	delete(s.items, ent.key)
	s.size -= ent.response.Size
}

// CleanupExpired removes all expired entries (can be called periodically)
func (s *MemoryStore) CleanupExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	now := time.Now()

	// Walk through list and remove expired entries
	var next *list.Element
	for elem := s.lruList.Front(); elem != nil; elem = next {
		next = elem.Next()
		ent := elem.Value.(*entry)

		if now.After(ent.response.ExpiresAt) {
			s.removeElement(elem)
			removed++
		}
	}

	return removed
}

// Resize changes the cache capacity and evicts excess entries.
// A non-positive capacity clears the cache and disables retention.
func (s *MemoryStore) Resize(newCapacity int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.capacity = max(0, newCapacity)

	// Evict entries if over capacity
	for s.lruList.Len() > s.capacity {
		s.evictOldest()
	}
}

// Keys returns all cached keys (useful for debugging)
func (s *MemoryStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.items))
	for key := range s.items {
		keys = append(keys, key)
	}
	return keys
}

// Len returns the number of items in cache
func (s *MemoryStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// Size returns the approximate total size of cached responses in bytes
func (s *MemoryStore) Size() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
}

// HitRate returns the cache hit rate (0.0 - 1.0)
func (s *MemoryStore) HitRate() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := s.hits + s.misses
	if total == 0 {
		return 0.0
	}

	return float64(s.hits) / float64(total)
}
