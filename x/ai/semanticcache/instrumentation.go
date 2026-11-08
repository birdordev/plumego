package semanticcache

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
)

// MetricRecord holds a single metric observation for this package.
type MetricRecord struct {
	Name     string
	Value    float64
	Labels   map[string]string
	Duration time.Duration
	Error    error
}

// MetricRecorder accepts metric records emitted by instrumented wrappers.
// metrics.Recorder satisfies this interface via a thin adapter at the call site.
type MetricRecorder interface {
	Record(ctx context.Context, record MetricRecord)
}

type noopRecorder struct{}

func (noopRecorder) Record(context.Context, MetricRecord) {}

// NoopMetricRecorder returns a MetricRecorder that silently discards all records.
func NoopMetricRecorder() MetricRecorder { return noopRecorder{} }

// InstrumentedSemanticCache wraps SemanticCache with metrics collection.
type InstrumentedSemanticCache struct {
	cache     *SemanticCache
	collector MetricRecorder
}

// NewInstrumentedSemanticCache creates an instrumented semantic cache.
func NewInstrumentedSemanticCache(cache *SemanticCache, collector MetricRecorder) *InstrumentedSemanticCache {
	return &InstrumentedSemanticCache{
		cache:     cache,
		collector: collector,
	}
}

// Get implements semantic cache get with metrics.
func (c *InstrumentedSemanticCache) Get(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, float64, error) {
	start := time.Now()

	resp, similarity, err := c.cache.Get(ctx, req)

	duration := time.Since(start)

	// Record metrics
	if c.collector != nil {
		tags := map[string]string{
			"model":     req.Model,
			"operation": "semantic_cache_get",
		}

		if err == nil && resp != nil {
			// Cache hit
			tags["result"] = "hit"
			tags["similarity"] = formatSimilarity(similarity)

			c.collector.Record(ctx, MetricRecord{
				Name:     "semantic_cache_hits",
				Value:    1,
				Labels:   tags,
				Duration: duration,
			})
			c.collector.Record(ctx, MetricRecord{
				Name:     "semantic_cache_similarity",
				Value:    similarity,
				Labels:   tags,
				Duration: duration,
			})
		} else {
			// Cache miss
			tags["result"] = "miss"
			c.collector.Record(ctx, MetricRecord{
				Name:     "semantic_cache_misses",
				Value:    1,
				Labels:   tags,
				Duration: duration,
			})
		}

		c.collector.Record(ctx, MetricRecord{
			Name:     "semantic_cache_get_duration",
			Value:    float64(duration.Milliseconds()),
			Labels:   tags,
			Duration: duration,
		})
	}

	return resp, similarity, err
}

// Set implements semantic cache set with metrics.
func (c *InstrumentedSemanticCache) Set(ctx context.Context, req *provider.CompletionRequest, resp *provider.CompletionResponse) error {
	start := time.Now()

	err := c.cache.Set(ctx, req, resp)

	duration := time.Since(start)

	// Record metrics
	if c.collector != nil {
		tags := map[string]string{
			"model":     req.Model,
			"operation": "semantic_cache_set",
		}

		if err != nil {
			tags["result"] = "error"
			c.collector.Record(ctx, MetricRecord{
				Name:     "semantic_cache_set_errors",
				Value:    1,
				Labels:   tags,
				Duration: duration,
				Error:    err,
			})
		} else {
			tags["result"] = "success"
			c.collector.Record(ctx, MetricRecord{
				Name:     "semantic_cache_stores",
				Value:    1,
				Labels:   tags,
				Duration: duration,
			})
		}

		c.collector.Record(ctx, MetricRecord{
			Name:     "semantic_cache_set_duration",
			Value:    float64(duration.Milliseconds()),
			Labels:   tags,
			Duration: duration,
		})
	}

	return err
}

// Stats returns cache statistics.
func (c *InstrumentedSemanticCache) Stats() SemanticCacheStats {
	return c.cache.Stats()
}

// Clear clears the cache.
func (c *InstrumentedSemanticCache) Clear(ctx context.Context) error {
	return c.cache.Clear(ctx)
}

// InstrumentedVectorStore wraps VectorStore with metrics collection.
type InstrumentedVectorStore struct {
	store     VectorStore
	collector MetricRecorder
}

// NewInstrumentedVectorStore creates an instrumented vector store.
func NewInstrumentedVectorStore(store VectorStore, collector MetricRecorder) *InstrumentedVectorStore {
	return &InstrumentedVectorStore{
		store:     store,
		collector: collector,
	}
}

// Add implements VectorStore.
func (s *InstrumentedVectorStore) Add(ctx context.Context, entry *VectorEntry) error {
	start := time.Now()

	err := s.store.Add(ctx, entry)

	duration := time.Since(start)

	// Record metrics
	if s.collector != nil {
		tags := map[string]string{
			"operation": "vector_store_add",
		}

		if err != nil {
			tags["result"] = "error"
		} else {
			tags["result"] = "success"
		}

		s.collector.Record(ctx, MetricRecord{
			Name:     "vector_store_add_duration",
			Value:    float64(duration.Milliseconds()),
			Labels:   tags,
			Duration: duration,
			Error:    err,
		})
		s.collector.Record(ctx, MetricRecord{
			Name:     "vector_store_size",
			Value:    float64(s.store.Size()),
			Labels:   tags,
			Duration: duration,
		})
	}

	return err
}

// Search implements VectorStore.
func (s *InstrumentedVectorStore) Search(ctx context.Context, query *Embedding, topK int, threshold float64) ([]*SimilarityResult, error) {
	start := time.Now()

	results, err := s.store.Search(ctx, query, topK, threshold)

	duration := time.Since(start)

	// Record metrics
	if s.collector != nil {
		tags := map[string]string{
			"operation": "vector_store_search",
		}

		if err != nil {
			tags["result"] = "error"
		} else {
			tags["result"] = "success"
		}

		s.collector.Record(ctx, MetricRecord{
			Name:     "vector_store_search_duration",
			Value:    float64(duration.Milliseconds()),
			Labels:   tags,
			Duration: duration,
			Error:    err,
		})
		s.collector.Record(ctx, MetricRecord{
			Name:     "vector_store_results_count",
			Value:    float64(len(results)),
			Labels:   tags,
			Duration: duration,
		})

		// Record best similarity if results found
		if len(results) > 0 {
			s.collector.Record(ctx, MetricRecord{
				Name:     "vector_store_best_similarity",
				Value:    results[0].Similarity,
				Labels:   tags,
				Duration: duration,
			})
		}
	}

	return results, err
}

// Delete implements VectorStore.
func (s *InstrumentedVectorStore) Delete(ctx context.Context, hash string) error {
	return s.store.Delete(ctx, hash)
}

// Clear implements VectorStore.
func (s *InstrumentedVectorStore) Clear(ctx context.Context) error {
	return s.store.Clear(ctx)
}

// Size implements VectorStore.
func (s *InstrumentedVectorStore) Size() int {
	return s.store.Size()
}

// Stats implements VectorStore.
func (s *InstrumentedVectorStore) Stats() VectorStoreStats {
	return s.store.Stats()
}

// formatSimilarity formats similarity score to string range.
func formatSimilarity(similarity float64) string {
	switch {
	case similarity >= 0.95:
		return "0.95-1.00"
	case similarity >= 0.90:
		return "0.90-0.95"
	case similarity >= 0.85:
		return "0.85-0.90"
	case similarity >= 0.80:
		return "0.80-0.85"
	default:
		return "0.00-0.80"
	}
}
