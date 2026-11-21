package pubsub

import (
	"container/list"
	"sync"
	"time"
)

// Deduplicator handles message deduplication.
type Deduplicator struct {
	mu         sync.RWMutex
	seen       map[string]*list.Element // msgID -> list element
	order      *list.List               // LRU order
	window     time.Duration
	maxSize    int
	cleanupInt time.Duration
	stopCh     chan struct{}
	closeOnce  sync.Once
}

// dedupEntry stores dedup entry data.
type dedupEntry struct {
	msgID    string
	seenAt   time.Time
	expireAt time.Time
}

// DeduplicatorConfig configures the deduplicator.
type DeduplicatorConfig struct {
	// Window is the time window for deduplication (default: 5 minutes)
	Window time.Duration

	// MaxSize is the maximum number of entries to track (default: 100000)
	MaxSize int

	// CleanupInterval is how often to clean expired entries (default: 1 minute)
	CleanupInterval time.Duration
}

// DefaultDeduplicatorConfig returns default config.
func DefaultDeduplicatorConfig() DeduplicatorConfig {
	return DeduplicatorConfig{
		Window:          5 * time.Minute,
		MaxSize:         100000,
		CleanupInterval: time.Minute,
	}
}

// NewDeduplicator creates a new deduplicator.
func NewDeduplicator(config DeduplicatorConfig) *Deduplicator {
	if config.Window <= 0 {
		config.Window = 5 * time.Minute
	}
	if config.MaxSize <= 0 {
		config.MaxSize = 100000
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = time.Minute
	}

	d := &Deduplicator{
		seen:       make(map[string]*list.Element),
		order:      list.New(),
		window:     config.Window,
		maxSize:    config.MaxSize,
		cleanupInt: config.CleanupInterval,
		stopCh:     make(chan struct{}),
	}

	go d.cleanupLoop()

	return d
}

// IsDuplicate checks if a message ID has been seen within the window.
// Returns true if duplicate, false if new.
// This also marks the message as seen.
func (d *Deduplicator) IsDuplicate(msgID string) bool {
	if msgID == "" {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// Check if already seen
	if elem, ok := d.seen[msgID]; ok {
		entry := elem.Value.(*dedupEntry)
		if now.Before(entry.expireAt) {
			// Move to front (most recently seen)
			d.order.MoveToFront(elem)
			entry.seenAt = now
			return true
		}
		// Expired, remove and treat as new
		d.order.Remove(elem)
		delete(d.seen, msgID)
	}

	// New message, add to tracking
	entry := &dedupEntry{
		msgID:    msgID,
		seenAt:   now,
		expireAt: now.Add(d.window),
	}
	elem := d.order.PushFront(entry)
	d.seen[msgID] = elem

	// Evict if over capacity
	for len(d.seen) > d.maxSize {
		oldest := d.order.Back()
		if oldest == nil {
			break
		}
		oldEntry := oldest.Value.(*dedupEntry)
		d.order.Remove(oldest)
		delete(d.seen, oldEntry.msgID)
	}

	return false
}

// Check checks if a message ID would be a duplicate without marking it.
func (d *Deduplicator) Check(msgID string) bool {
	if msgID == "" {
		return false
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	if elem, ok := d.seen[msgID]; ok {
		entry := elem.Value.(*dedupEntry)
		return time.Now().Before(entry.expireAt)
	}
	return false
}

// Mark marks a message ID as seen without checking.
func (d *Deduplicator) Mark(msgID string) {
	if msgID == "" {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	if elem, ok := d.seen[msgID]; ok {
		entry := elem.Value.(*dedupEntry)
		entry.seenAt = now
		entry.expireAt = now.Add(d.window)
		d.order.MoveToFront(elem)
		return
	}

	entry := &dedupEntry{
		msgID:    msgID,
		seenAt:   now,
		expireAt: now.Add(d.window),
	}
	elem := d.order.PushFront(entry)
	d.seen[msgID] = elem

	// Evict if over capacity
	for len(d.seen) > d.maxSize {
		oldest := d.order.Back()
		if oldest == nil {
			break
		}
		oldEntry := oldest.Value.(*dedupEntry)
		d.order.Remove(oldest)
		delete(d.seen, oldEntry.msgID)
	}
}

// Remove removes a message ID from tracking.
func (d *Deduplicator) Remove(msgID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if elem, ok := d.seen[msgID]; ok {
		d.order.Remove(elem)
		delete(d.seen, msgID)
	}
}

// Size returns the current number of tracked entries.
func (d *Deduplicator) Size() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.seen)
}

// Clear removes all entries.
func (d *Deduplicator) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.seen = make(map[string]*list.Element)
	d.order.Init()
}

// Close stops the deduplicator. Safe to call multiple times.
func (d *Deduplicator) Close() {
	d.closeOnce.Do(func() { close(d.stopCh) })
}

// cleanupLoop periodically removes expired entries.
func (d *Deduplicator) cleanupLoop() {
	ticker := time.NewTicker(d.cleanupInt)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.cleanup()
		}
	}
}

// cleanup removes expired entries.
func (d *Deduplicator) cleanup() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// A duplicate read can move an entry without extending its expiration.
	for elem := d.order.Back(); elem != nil; {
		previous := elem.Prev()
		entry := elem.Value.(*dedupEntry)
		if !now.Before(entry.expireAt) {
			d.order.Remove(elem)
			delete(d.seen, entry.msgID)
		}
		elem = previous
	}
}

// Stats returns deduplicator statistics.
func (d *Deduplicator) Stats() DedupStats {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := DedupStats{
		Size:    len(d.seen),
		MaxSize: d.maxSize,
		Window:  d.window,
	}

	if d.order.Len() > 0 {
		oldest := d.order.Back()
		if oldest != nil {
			stats.OldestEntry = oldest.Value.(*dedupEntry).seenAt
		}
		newest := d.order.Front()
		if newest != nil {
			stats.NewestEntry = newest.Value.(*dedupEntry).seenAt
		}
	}

	return stats
}

// DedupStats contains deduplicator statistics.
type DedupStats struct {
	Size        int
	MaxSize     int
	Window      time.Duration
	OldestEntry time.Time
	NewestEntry time.Time
}
