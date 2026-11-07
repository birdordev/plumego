package vectorstore

import (
	"context"
	"fmt"
)

// TieredBackend implements a multi-tier caching strategy.
// L1: Fast, small (memory)
// L2: Medium speed, medium size (Redis/similar)
// L3: Slower, unlimited (vector database)
type TieredBackend struct {
	l1         Backend
	l2         Backend
	l3         Backend
	onError    func(TieredBackgroundError)
	l1MaxSize  int
	l2MaxSize  int
	dimensions int
	stats      *TieredStats
}

// TieredConfig holds tiered backend configuration.
type TieredConfig struct {
	L1         Backend // Required: fast cache (e.g., memory)
	L2         Backend // Optional: medium cache
	L3         Backend // Optional: persistent storage
	OnError    func(TieredBackgroundError)
	L1MaxSize  int
	L2MaxSize  int
	Dimensions int
}

// TieredBackgroundError captures a best-effort tier operation failure.
type TieredBackgroundError struct {
	Tier      string
	Operation string
	Err       error
}

// TieredStats holds statistics for tiered cache.
type TieredStats struct {
	L1Hits       int64
	L2Hits       int64
	L3Hits       int64
	Misses       int64
	Promotions   int64
	Evictions    int64
	TotalQueries int64
}

// NewTieredBackend creates a new tiered vector store backend.
func NewTieredBackend(config TieredConfig) (*TieredBackend, error) {
	if config.L1 == nil {
		return nil, fmt.Errorf("L1 backend is required")
	}

	return &TieredBackend{
		l1:         config.L1,
		l2:         config.L2,
		l3:         config.L3,
		onError:    config.OnError,
		l1MaxSize:  config.L1MaxSize,
		l2MaxSize:  config.L2MaxSize,
		dimensions: config.Dimensions,
		stats:      &TieredStats{},
	}, nil
}

// Name returns the backend name.
func (t *TieredBackend) Name() string {
	return "tiered"
}

// Add adds a vector entry to all tiers.
func (t *TieredBackend) Add(ctx context.Context, entry *Entry) error {
	// Add to all available tiers
	if err := t.l1.Add(ctx, entry); err != nil {
		return fmt.Errorf("L1 add failed: %w", err)
	}

	if t.l2 != nil {
		if err := t.l2.Add(ctx, entry); err != nil {
			t.reportBackgroundError("l2", "add", err)
		}
	}

	if t.l3 != nil {
		if err := t.l3.Add(ctx, entry); err != nil {
			t.reportBackgroundError("l3", "add", err)
		}
	}

	return nil
}

// AddBatch adds multiple vector entries to all tiers.
func (t *TieredBackend) AddBatch(ctx context.Context, entries []*Entry) error {
	// Add to L1
	if err := t.l1.AddBatch(ctx, entries); err != nil {
		return fmt.Errorf("L1 batch add failed: %w", err)
	}

	// Best-effort add to L2 and L3
	if t.l2 != nil {
		if err := t.l2.AddBatch(ctx, entries); err != nil {
			t.reportBackgroundError("l2", "add_batch", err)
		}
	}
	if t.l3 != nil {
		if err := t.l3.AddBatch(ctx, entries); err != nil {
			t.reportBackgroundError("l3", "add_batch", err)
		}
	}

	return nil
}

// Search searches across tiers with automatic promotion.
func (t *TieredBackend) Search(ctx context.Context, query []float64, topK int, threshold float64) ([]*SearchResult, error) {
	t.stats.TotalQueries++

	// Try L1 first (fastest)
	results, err := t.l1.Search(ctx, query, topK, threshold)
	if err == nil && len(results) > 0 {
		t.stats.L1Hits++
		return results, nil
	}

	// Try L2
	if t.l2 != nil {
		results, err = t.l2.Search(ctx, query, topK, threshold)
		if err == nil && len(results) > 0 {
			t.stats.L2Hits++

			// Promote to L1
			for _, result := range results {
				if err := t.l1.Add(ctx, result.Entry); err == nil {
					t.stats.Promotions++
				}
			}

			return results, nil
		}
	}

	// Try L3
	if t.l3 != nil {
		results, err = t.l3.Search(ctx, query, topK, threshold)
		if err == nil && len(results) > 0 {
			t.stats.L3Hits++

			// Promote to L2 and L1
			for _, result := range results {
				if t.l2 != nil {
					_ = t.l2.Add(ctx, result.Entry)
				}
				if err := t.l1.Add(ctx, result.Entry); err == nil {
					t.stats.Promotions++
				}
			}

			return results, nil
		}
	}

	// Cache miss
	t.stats.Misses++
	return []*SearchResult{}, nil
}

// Get retrieves an entry by ID from any tier.
func (t *TieredBackend) Get(ctx context.Context, id string) (*Entry, error) {
	// Try L1
	entry, err := t.l1.Get(ctx, id)
	if err == nil {
		return entry, nil
	}

	// Try L2
	if t.l2 != nil {
		entry, err = t.l2.Get(ctx, id)
		if err == nil {
			// Promote to L1
			_ = t.l1.Add(ctx, entry)
			return entry, nil
		}
	}

	// Try L3
	if t.l3 != nil {
		entry, err = t.l3.Get(ctx, id)
		if err == nil {
			// Promote to L2 and L1
			if t.l2 != nil {
				_ = t.l2.Add(ctx, entry)
			}
			_ = t.l1.Add(ctx, entry)
			return entry, nil
		}
	}

	return nil, fmt.Errorf("entry not found: %s", id)
}

// Delete removes an entry from all tiers.
func (t *TieredBackend) Delete(ctx context.Context, id string) error {
	// Delete from all tiers
	_ = t.l1.Delete(ctx, id)
	if t.l2 != nil {
		_ = t.l2.Delete(ctx, id)
	}
	if t.l3 != nil {
		_ = t.l3.Delete(ctx, id)
	}

	return nil
}

// DeleteBatch removes multiple entries from all tiers.
func (t *TieredBackend) DeleteBatch(ctx context.Context, ids []string) error {
	_ = t.l1.DeleteBatch(ctx, ids)
	if t.l2 != nil {
		_ = t.l2.DeleteBatch(ctx, ids)
	}
	if t.l3 != nil {
		_ = t.l3.DeleteBatch(ctx, ids)
	}

	return nil
}

// Update updates an entry in all tiers.
func (t *TieredBackend) Update(ctx context.Context, entry *Entry) error {
	if err := t.l1.Update(ctx, entry); err != nil {
		return err
	}

	if t.l2 != nil {
		_ = t.l2.Update(ctx, entry)
	}
	if t.l3 != nil {
		_ = t.l3.Update(ctx, entry)
	}

	return nil
}

// Count returns the total count from L3 (or L2, or L1).
func (t *TieredBackend) Count(ctx context.Context) (int64, error) {
	if t.l3 != nil {
		return t.l3.Count(ctx)
	}
	if t.l2 != nil {
		return t.l2.Count(ctx)
	}
	return t.l1.Count(ctx)
}

// Clear removes all entries from all tiers.
func (t *TieredBackend) Clear(ctx context.Context) error {
	_ = t.l1.Clear(ctx)
	if t.l2 != nil {
		_ = t.l2.Clear(ctx)
	}
	if t.l3 != nil {
		_ = t.l3.Clear(ctx)
	}

	t.stats = &TieredStats{}
	return nil
}

// Close closes all backends.
func (t *TieredBackend) Close() error {
	_ = t.l1.Close()
	if t.l2 != nil {
		_ = t.l2.Close()
	}
	if t.l3 != nil {
		_ = t.l3.Close()
	}

	return nil
}

// Stats returns combined statistics.
func (t *TieredBackend) Stats(ctx context.Context) (*BackendStats, error) {
	l1Stats, _ := t.l1.Stats(ctx)

	stats := &BackendStats{
		Name:         "tiered",
		TotalEntries: l1Stats.TotalEntries,
		Dimensions:   t.dimensions,
	}

	// Calculate hit rate
	totalHits := t.stats.L1Hits + t.stats.L2Hits + t.stats.L3Hits
	if t.stats.TotalQueries > 0 {
		stats.HitRate = float64(totalHits) / float64(t.stats.TotalQueries)
	}

	return stats, nil
}

// TieredStats returns the tiered-specific statistics.
func (t *TieredBackend) TieredStats() *TieredStats {
	return t.stats
}

// HitRate returns the overall cache hit rate.
func (t *TieredBackend) HitRate() float64 {
	if t.stats.TotalQueries == 0 {
		return 0
	}

	totalHits := t.stats.L1Hits + t.stats.L2Hits + t.stats.L3Hits
	return float64(totalHits) / float64(t.stats.TotalQueries)
}

// L1HitRate returns the L1 cache hit rate.
func (t *TieredBackend) L1HitRate() float64 {
	if t.stats.TotalQueries == 0 {
		return 0
	}
	return float64(t.stats.L1Hits) / float64(t.stats.TotalQueries)
}

// ResetStats resets all statistics.
func (t *TieredBackend) ResetStats() {
	t.stats = &TieredStats{}
}

func (t *TieredBackend) reportBackgroundError(tier, operation string, err error) {
	if t == nil || err == nil || t.onError == nil {
		return
	}
	t.onError(TieredBackgroundError{
		Tier:      tier,
		Operation: operation,
		Err:       err,
	})
}
