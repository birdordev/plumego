package cache

// Stats is a point-in-time MemoryCache state snapshot.
type Stats struct {
	// Entries is the number of entries currently tracked by the cache.
	Entries int

	// MemoryUsage is the tracked payload byte count.
	MemoryUsage uint64

	// Closed reports whether the cache lifecycle is closed.
	Closed bool
}

// Stats returns tracked entries, payload bytes, and lifecycle state. After
// Close returns, the counters reflect final usage before the data was released.
func (mc *MemoryCache) Stats() Stats {
	if mc == nil {
		return Stats{Closed: true}
	}
	mc.stateMu.RLock()
	defer mc.stateMu.RUnlock()
	return Stats{
		Entries:     mc.size,
		MemoryUsage: mc.memory,
		Closed:      mc.closed.Load() || mc.stopChan == nil,
	}
}
