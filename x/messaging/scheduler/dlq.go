package scheduler

import (
	"sync"
	"time"
)

// DeadLetterEntry represents a job that failed after all retries.
type DeadLetterEntry struct {
	JobID        JobID
	Error        error  `json:"-"`
	ErrorMessage string // 固定失败摘要
	Attempts     int
	FirstFailed  time.Time
	LastFailed   time.Time
	TaskName     string
	Group        string
	Tags         []string
}

// DeadLetterQueue manages failed jobs for inspection and requeuing.
// Internally it maintains a map for O(1) lookup and an ordered slice for O(1)
// FIFO eviction (oldest entry removed when capacity is exceeded).
type DeadLetterQueue struct {
	mu      sync.RWMutex
	entries map[JobID]*DeadLetterEntry
	order   []JobID // insertion-order list for O(1) FIFO eviction
	maxSize int
}

// NewDeadLetterQueue creates a new dead letter queue.
// maxSize limits the number of entries (0 = unlimited).
func NewDeadLetterQueue(maxSize int) *DeadLetterQueue {
	return &DeadLetterQueue{
		entries: make(map[JobID]*DeadLetterEntry),
		maxSize: maxSize,
	}
}

// Add adds a failed job to the dead letter queue.
func (d *DeadLetterQueue) Add(entry DeadLetterEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Update existing entry in-place (preserve FirstFailed, order unchanged).
	if existing, exists := d.entries[entry.JobID]; exists {
		existing.Error = entry.Error
		existing.ErrorMessage = entry.ErrorMessage
		existing.Attempts = entry.Attempts
		existing.LastFailed = entry.LastFailed
		existing.TaskName = entry.TaskName
		existing.Group = entry.Group
		existing.Tags = append([]string(nil), entry.Tags...)
		return
	}

	// Evict the oldest entry when at capacity (O(1) using the order slice).
	if d.maxSize > 0 && len(d.entries) >= d.maxSize {
		for len(d.order) > 0 {
			oldest := d.order[0]
			d.order = d.order[1:]
			if _, exists := d.entries[oldest]; exists {
				delete(d.entries, oldest)
				break
			}
		}
	}

	// Add new entry.
	entry.Tags = append([]string(nil), entry.Tags...)
	d.entries[entry.JobID] = &entry
	d.order = append(d.order, entry.JobID)
}

// Get retrieves a dead letter entry by job ID.
func (d *DeadLetterQueue) Get(jobID JobID) (*DeadLetterEntry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry, ok := d.entries[jobID]
	if !ok {
		return nil, false
	}
	// Return a deep copy to prevent external modification.
	entryCopy := cloneDeadLetterEntry(*entry)
	return &entryCopy, true
}

// List returns all dead letter entries.
func (d *DeadLetterQueue) List() []DeadLetterEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]DeadLetterEntry, 0, len(d.entries))
	for _, id := range d.order {
		entry, exists := d.entries[id]
		if !exists || entry == nil {
			continue
		}
		result = append(result, cloneDeadLetterEntry(*entry))
	}
	return result
}

// Delete removes a dead letter entry.
func (d *DeadLetterQueue) Delete(jobID JobID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.entries[jobID]; !exists {
		return false
	}
	delete(d.entries, jobID)
	// Remove from the order slice so it does not accumulate stale entries that
	// would hold memory and slow down the FIFO eviction scan in Add.
	for i, id := range d.order {
		if id == jobID {
			d.order = append(d.order[:i], d.order[i+1:]...)
			break
		}
	}
	return true
}

// Clear removes all dead letter entries.
func (d *DeadLetterQueue) Clear() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := len(d.entries)
	d.entries = make(map[JobID]*DeadLetterEntry)
	d.order = d.order[:0]
	return count
}

// Size returns the current number of entries in the queue.
func (d *DeadLetterQueue) Size() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.entries)
}

func cloneDeadLetterEntry(entry DeadLetterEntry) DeadLetterEntry {
	entry.Tags = append([]string(nil), entry.Tags...)
	return entry
}
