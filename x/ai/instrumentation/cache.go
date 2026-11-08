package instrumentation

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/ai/llmcache"
	"github.com/birdordev/plumego/x/ai/metrics"
)

// InstrumentedCache wraps a Cache with metrics collection.
type InstrumentedCache struct {
	cache     llmcache.Cache
	collector metrics.Collector
	cacheType string
}

// NewInstrumentedCache creates a new instrumented cache.
func NewInstrumentedCache(cache llmcache.Cache, collector metrics.Collector, cacheType string) *InstrumentedCache {
	return &InstrumentedCache{
		cache:     cache,
		collector: collector,
		cacheType: cacheType,
	}
}

// Get implements llmcache.Cache
func (ic *InstrumentedCache) Get(ctx context.Context, key *llmcache.CacheKey) (*llmcache.CacheEntry, error) {
	start := time.Now()

	tags, _ := metrics.Tags("cache_type", ic.cacheType)

	entry, err := ic.cache.Get(ctx, key)

	duration := time.Since(start)
	ic.collector.Timing("ai_cache_get_duration_seconds", duration, tags...)

	if err == nil && entry != nil {
		// Cache hit
		ic.collector.Counter("ai_cache_hits_total", 1, tags...)
		ic.collector.Histogram("ai_cache_hit_tokens", float64(entry.Usage.TotalTokens), tags...)
	} else {
		// Cache miss
		ic.collector.Counter("ai_cache_misses_total", 1, tags...)
	}

	return entry, err
}

// Set implements llmcache.Cache
func (ic *InstrumentedCache) Set(ctx context.Context, key *llmcache.CacheKey, entry *llmcache.CacheEntry) error {
	start := time.Now()

	tags, _ := metrics.Tags("cache_type", ic.cacheType)

	err := ic.cache.Set(ctx, key, entry)

	duration := time.Since(start)
	ic.collector.Timing("ai_cache_set_duration_seconds", duration, tags...)

	if err == nil {
		ic.collector.Counter("ai_cache_writes_total", 1, tags...)
		ic.collector.Histogram("ai_cache_entry_tokens", float64(entry.Usage.TotalTokens), tags...)
	} else {
		ic.collector.Counter("ai_cache_write_errors_total", 1, tags...)
	}

	return err
}

// Delete implements llmcache.Cache
func (ic *InstrumentedCache) Delete(ctx context.Context, key *llmcache.CacheKey) error {
	start := time.Now()

	tags, _ := metrics.Tags("cache_type", ic.cacheType)

	err := ic.cache.Delete(ctx, key)

	duration := time.Since(start)
	ic.collector.Timing("ai_cache_delete_duration_seconds", duration, tags...)

	if err == nil {
		ic.collector.Counter("ai_cache_deletions_total", 1, tags...)
	}

	return err
}

// Clear implements llmcache.Cache
func (ic *InstrumentedCache) Clear(ctx context.Context) error {
	start := time.Now()

	tags, _ := metrics.Tags("cache_type", ic.cacheType)

	err := ic.cache.Clear(ctx)

	duration := time.Since(start)
	ic.collector.Timing("ai_cache_clear_duration_seconds", duration, tags...)

	if err == nil {
		ic.collector.Counter("ai_cache_clears_total", 1, tags...)
	}

	return err
}

// InstrumentedMemoryCache wraps MemoryCache with additional metrics.
type InstrumentedMemoryCache struct {
	*InstrumentedCache
	memCache *llmcache.MemoryCache
}

// NewInstrumentedMemoryCache creates an instrumented memory cache.
func NewInstrumentedMemoryCache(memCache *llmcache.MemoryCache, collector metrics.Collector) *InstrumentedMemoryCache {
	return &InstrumentedMemoryCache{
		InstrumentedCache: NewInstrumentedCache(memCache, collector, "memory"),
		memCache:          memCache,
	}
}

// PublishStats publishes cache statistics as metrics.
// Call this periodically (e.g., every 10 seconds) to update stats.
func (imc *InstrumentedMemoryCache) PublishStats() {
	stats := imc.memCache.Stats()

	tags, _ := metrics.Tags("cache_type", "memory")

	// Publish as gauges
	imc.collector.Gauge("ai_cache_hits", float64(stats.Hits), tags...)
	imc.collector.Gauge("ai_cache_misses", float64(stats.Misses), tags...)
	imc.collector.Gauge("ai_cache_evictions", float64(stats.Evictions), tags...)
	imc.collector.Gauge("ai_cache_total_tokens", float64(stats.TotalTokens), tags...)

	// Calculate and publish hit rate
	hitRate := stats.HitRate()
	imc.collector.Gauge("ai_cache_hit_rate", hitRate, tags...)
}
