package vectorstore

import (
	"container/heap"
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"sync"
	"time"
)

// MemoryBackend implements an in-memory vector store. It copies entries,
// vectors and top-level metadata maps on writes and reads. Values nested
// inside metadata are shared and callers must treat them as immutable.
// Write timestamps are applied to the stored copy.
type MemoryBackend struct {
	entries    map[string]*Entry
	norms      map[string]float64
	mu         sync.RWMutex
	dimensions int
	maxSize    int
	stats      *BackendStats
}

// MemoryConfig holds memory backend configuration.
type MemoryConfig struct {
	MaxSize    int // Maximum number of entries (0 = unlimited)
	Dimensions int
}

// NewMemoryBackend creates a new in-memory vector store backend.
func NewMemoryBackend(config MemoryConfig) *MemoryBackend {
	return &MemoryBackend{
		entries:    make(map[string]*Entry),
		norms:      make(map[string]float64),
		dimensions: config.Dimensions,
		maxSize:    config.MaxSize,
		stats: &BackendStats{
			Name: "memory",
		},
	}
}

// Name returns the backend name.
func (m *MemoryBackend) Name() string {
	return "memory"
}

func cloneMemoryEntry(entry *Entry) *Entry {
	copy := *entry
	copy.Vector = slices.Clone(entry.Vector)
	copy.Metadata = maps.Clone(entry.Metadata)
	return &copy
}

func (m *MemoryBackend) validateEntryDimensions(entry *Entry) error {
	if m.dimensions > 0 && len(entry.Vector) != m.dimensions {
		return fmt.Errorf("vector dimension mismatch: got %d, want %d", len(entry.Vector), m.dimensions)
	}
	return nil
}

// Add adds or replaces a vector entry. Replacing an ID uses its existing slot.
// Cancellation is checked under the lock before the entry or capacity changes.
func (m *MemoryBackend) Add(ctx context.Context, entry *Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if err := m.validateEntryDimensions(entry); err != nil {
		return err
	}

	// Check size limit
	if _, exists := m.entries[entry.ID]; !exists && m.maxSize > 0 && len(m.entries) >= m.maxSize {
		// Evict oldest entry
		if err := m.evictOldest(); err != nil {
			return fmt.Errorf("failed to evict entry: %w", err)
		}
	}

	// Set timestamps on the stored copy.
	entry = cloneMemoryEntry(entry)
	now := time.Now()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now

	m.entries[entry.ID] = entry
	m.norms[entry.ID] = vectorNorm(entry.Vector)
	m.stats.TotalEntries = int64(len(m.entries))

	return nil
}

// AddBatch adds entries in order. Earlier writes remain if a later Add fails.
func (m *MemoryBackend) AddBatch(ctx context.Context, entries []*Entry) error {
	for _, entry := range entries {
		if err := m.Add(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

// Search searches for similar vectors using cosine similarity.
func (m *MemoryBackend) Search(ctx context.Context, query []float64, topK int, threshold float64) ([]*SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.dimensions > 0 && len(query) != m.dimensions {
		return nil, fmt.Errorf("query dimension mismatch: got %d, want %d", len(query), m.dimensions)
	}

	limit := len(m.entries)
	bounded := topK > 0 && topK < limit
	if bounded {
		limit = topK
	}
	candidates := vectorCandidates(make([]vectorCandidate, 0, limit))
	queryNorm := vectorNorm(query)
	for id, entry := range m.entries {
		if len(query) != len(entry.Vector) {
			continue
		}
		var similarity float64
		if entryNorm := m.norms[id]; entryNorm != 0 && queryNorm != 0 {
			var dot float64
			for i, value := range query {
				dot += value * entry.Vector[i]
			}
			similarity = dot / (queryNorm * entryNorm)
		}
		if !(similarity >= threshold) {
			continue
		}
		candidate := vectorCandidate{entry: entry, similarity: similarity}
		switch {
		case !bounded:
			candidates = append(candidates, candidate)
		case len(candidates) < limit:
			candidates = append(candidates, candidate)
			if len(candidates) == limit {
				heap.Init(&candidates)
			}
		case similarity > candidates[0].similarity:
			candidates[0] = candidate
			heap.Fix(&candidates, 0)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].similarity > candidates[j].similarity
	})
	results := make([]*SearchResult, len(candidates))
	for i, candidate := range candidates {
		results[i] = &SearchResult{
			Entry:      cloneMemoryEntry(candidate.entry),
			Similarity: candidate.similarity,
			Distance:   1 - candidate.similarity,
		}
	}
	return results, nil
}

type vectorCandidate struct {
	entry      *Entry
	similarity float64
}

type vectorCandidates []vectorCandidate

func (c vectorCandidates) Len() int           { return len(c) }
func (c vectorCandidates) Less(i, j int) bool { return c[i].similarity < c[j].similarity }
func (c vectorCandidates) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c *vectorCandidates) Push(v any)        { *c = append(*c, v.(vectorCandidate)) }
func (c *vectorCandidates) Pop() any {
	last := len(*c) - 1
	v := (*c)[last]
	(*c)[last] = vectorCandidate{}
	*c = (*c)[:last]
	return v
}

func vectorNorm(vector []float64) float64 {
	var squared float64
	for _, value := range vector {
		squared += value * value
	}
	return math.Sqrt(squared)
}

// Get retrieves an entry by ID.
func (m *MemoryBackend) Get(ctx context.Context, id string) (*Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.entries[id]
	if !exists {
		return nil, fmt.Errorf("entry not found: %s", id)
	}

	entry.AccessedAt = time.Now()
	entry.AccessCount++
	return cloneMemoryEntry(entry), nil
}

// Delete removes an entry by ID.
func (m *MemoryBackend) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.entries, id)
	delete(m.norms, id)
	m.stats.TotalEntries = int64(len(m.entries))

	return nil
}

// DeleteBatch removes multiple entries by ID.
func (m *MemoryBackend) DeleteBatch(ctx context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range ids {
		delete(m.entries, id)
		delete(m.norms, id)
	}

	m.stats.TotalEntries = int64(len(m.entries))

	return nil
}

// Update updates an entry.
func (m *MemoryBackend) Update(ctx context.Context, entry *Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.entries[entry.ID]; !exists {
		return fmt.Errorf("entry not found: %s", entry.ID)
	}

	if err := m.validateEntryDimensions(entry); err != nil {
		return err
	}

	entry = cloneMemoryEntry(entry)
	entry.UpdatedAt = time.Now()
	m.entries[entry.ID] = entry
	m.norms[entry.ID] = vectorNorm(entry.Vector)

	return nil
}

// Count returns the number of entries in the store.
func (m *MemoryBackend) Count(ctx context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return int64(len(m.entries)), nil
}

// Clear removes all entries from the store.
func (m *MemoryBackend) Clear(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = make(map[string]*Entry)
	m.norms = make(map[string]float64)
	m.stats.TotalEntries = 0

	return nil
}

// Close closes the backend (no-op for memory).
func (m *MemoryBackend) Close() error {
	return nil
}

// Stats returns backend statistics.
func (m *MemoryBackend) Stats(ctx context.Context) (*BackendStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Calculate memory usage (rough estimate)
	var memoryUsage int64
	for _, entry := range m.entries {
		memoryUsage += int64(len(entry.Vector) * 8) // 8 bytes per float64
		memoryUsage += int64(len(entry.Text))
		memoryUsage += int64(len(entry.Response))
		memoryUsage += 200 // Overhead for struct and metadata
	}

	stats := &BackendStats{
		Name:         "memory",
		TotalEntries: int64(len(m.entries)),
		TotalVectors: int64(len(m.entries)),
		Dimensions:   m.dimensions,
		MemoryUsage:  memoryUsage,
	}

	return stats, nil
}

// evictOldest removes the oldest (least recently accessed) entry.
// Must be called with lock held.
func (m *MemoryBackend) evictOldest() error {
	if len(m.entries) == 0 {
		return nil
	}

	var oldestID string
	var oldestTime time.Time

	for id, entry := range m.entries {
		accessTime := entry.AccessedAt
		if accessTime.IsZero() {
			accessTime = entry.CreatedAt
		}

		if oldestTime.IsZero() || accessTime.Before(oldestTime) {
			oldestTime = accessTime
			oldestID = id
		}
	}

	delete(m.entries, oldestID)
	delete(m.norms, oldestID)
	return nil
}
