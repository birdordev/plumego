package semanticcache

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/ai/llmcache"
	"github.com/birdordev/plumego/x/ai/provider"
)

// VectorEntry represents a stored vector with metadata.
type VectorEntry struct {
	// Embedding vector
	Embedding *Embedding

	// Cache entry
	CacheEntry *llmcache.CacheEntry

	// Cache key for exact matching
	CacheKey *llmcache.CacheKey

	// Timestamp
	CreatedAt time.Time
	ExpiresAt time.Time
	LastUsed  time.Time

	// Usage statistics
	HitCount int
}

// SimilarityResult represents a similarity search result.
type SimilarityResult struct {
	// Matched entry
	Entry *VectorEntry

	// Similarity score (0.0 to 1.0, higher is better)
	Similarity float64

	// Distance metric (lower is better)
	Distance float64
}

// VectorStore stores and retrieves vectors with similarity search.
type VectorStore interface {
	// Add stores a vector entry.
	Add(ctx context.Context, entry *VectorEntry) error

	// Search finds similar vectors.
	Search(ctx context.Context, query *Embedding, topK int, threshold float64) ([]*SimilarityResult, error)

	// Delete removes a vector entry.
	Delete(ctx context.Context, hash string) error

	// Clear removes all entries.
	Clear(ctx context.Context) error

	// Size returns the number of stored vectors.
	Size() int

	// Stats returns store statistics.
	Stats() VectorStoreStats
}

// VectorStoreStats tracks vector store performance.
type VectorStoreStats struct {
	TotalVectors      int64
	TotalSearches     int64
	AverageSimilarity float64
	LastCleanupAt     time.Time
}

// MemoryVectorStore implements in-memory vector storage with linear scan.
// For production with large datasets, consider using a specialized vector DB.
type MemoryVectorStore struct {
	entries map[string]*VectorEntry
	stats   VectorStoreStats
	maxSize int
	ttl     time.Duration
	mu      sync.RWMutex
}

// NewMemoryVectorStore creates a new in-memory vector store.
func NewMemoryVectorStore(maxSize int, ttl time.Duration) *MemoryVectorStore {
	if maxSize == 0 {
		maxSize = 1000
	}
	if ttl == 0 {
		ttl = 1 * time.Hour
	}

	return &MemoryVectorStore{
		entries: make(map[string]*VectorEntry),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Add implements VectorStore.
func (s *MemoryVectorStore) Add(ctx context.Context, entry *VectorEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check size limit
	if len(s.entries) >= s.maxSize {
		s.evictLRU()
	}

	// Set expiration
	if entry.ExpiresAt.IsZero() {
		entry.ExpiresAt = time.Now().Add(s.ttl)
	}

	// Store entry
	s.entries[entry.Embedding.Hash] = entry
	s.stats.TotalVectors = int64(len(s.entries))

	return nil
}

// Search implements VectorStore using linear scan with cosine similarity.
func (s *MemoryVectorStore) Search(ctx context.Context, query *Embedding, topK int, threshold float64) ([]*SimilarityResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stats.TotalSearches++

	var results []*SimilarityResult
	now := time.Now()

	// Linear scan - compute similarity for all entries
	for _, entry := range s.entries {
		// Skip expired entries
		if now.After(entry.ExpiresAt) {
			continue
		}

		// Compute cosine similarity
		similarity, err := CosineSimilarity(query.Vector, entry.Embedding.Vector)
		if err != nil {
			continue
		}

		// Filter by threshold
		if similarity < threshold {
			continue
		}

		// Compute Euclidean distance for additional metric
		distance, _ := EuclideanDistance(query.Vector, entry.Embedding.Vector)

		results = append(results, &SimilarityResult{
			Entry:      entry,
			Similarity: similarity,
			Distance:   distance,
		})
	}

	// Sort by similarity (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	// Limit to topK
	if len(results) > topK {
		results = results[:topK]
	}

	return results, nil
}

// MarkUsed updates access metadata for an existing entry.
func (s *MemoryVectorStore) MarkUsed(hash string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[hash]
	if !ok || entry == nil {
		return
	}
	entry.LastUsed = now
	entry.HitCount++
}

// Delete implements VectorStore.
func (s *MemoryVectorStore) Delete(ctx context.Context, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, hash)
	s.stats.TotalVectors = int64(len(s.entries))

	return nil
}

// Clear implements VectorStore.
func (s *MemoryVectorStore) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = make(map[string]*VectorEntry)
	s.stats = VectorStoreStats{}

	return nil
}

// Size implements VectorStore.
func (s *MemoryVectorStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.entries)
}

// Stats implements VectorStore.
func (s *MemoryVectorStore) Stats() VectorStoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.stats
}

// evictLRU removes the least recently used entry.
func (s *MemoryVectorStore) evictLRU() {
	var oldestHash string
	var oldestTime time.Time

	for hash, entry := range s.entries {
		if oldestHash == "" || entry.LastUsed.Before(oldestTime) {
			oldestHash = hash
			oldestTime = entry.LastUsed
		}
	}

	if oldestHash != "" {
		delete(s.entries, oldestHash)
	}
}

// CleanupExpired removes expired entries.
func (s *MemoryVectorStore) CleanupExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	count := 0

	for hash, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			delete(s.entries, hash)
			count++
		}
	}

	s.stats.TotalVectors = int64(len(s.entries))
	s.stats.LastCleanupAt = now

	return count
}

// SemanticCacheConfig configures semantic cache behavior.
type SemanticCacheConfig struct {
	// Similarity threshold (0.0 to 1.0)
	SimilarityThreshold float64

	// Maximum results to consider
	TopK int

	// Enable exact match fallback
	EnableExactMatch bool

	// TTL for cache entries
	TTL time.Duration

	// Maximum cache size
	MaxSize int
}

// DefaultSemanticCacheConfig returns default configuration.
func DefaultSemanticCacheConfig() *SemanticCacheConfig {
	return &SemanticCacheConfig{
		SimilarityThreshold: 0.85,
		TopK:                5,
		EnableExactMatch:    true,
		TTL:                 1 * time.Hour,
		MaxSize:             1000,
	}
}

// SemanticCache implements semantic caching with embedding-based similarity.
type SemanticCache struct {
	vectorStore VectorStore
	generator   EmbeddingGenerator
	config      *SemanticCacheConfig
	stats       SemanticCacheStats
	mu          sync.RWMutex
}

// SemanticCacheStats tracks semantic cache performance.
type SemanticCacheStats struct {
	SemanticHits   int64
	SemanticMisses int64
	Stores         int64
	Errors         int64
	AvgSimilarity  float64
}

// NewSemanticCache creates a new semantic cache.
func NewSemanticCache(generator EmbeddingGenerator, vectorStore VectorStore, config *SemanticCacheConfig) *SemanticCache {
	if config == nil {
		config = DefaultSemanticCacheConfig()
	}

	return &SemanticCache{
		vectorStore: vectorStore,
		generator:   generator,
		config:      config,
	}
}

// Get retrieves a cached response by semantic similarity.
func (c *SemanticCache) Get(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, float64, error) {
	// Generate embedding for query
	queryText := buildQueryText(req)
	embedding, err := c.generator.Generate(ctx, queryText)
	if err != nil {
		c.incrementErrors()
		return nil, 0, fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Search for similar vectors
	results, err := c.vectorStore.Search(ctx, embedding, c.config.TopK, c.config.SimilarityThreshold)
	if err != nil {
		c.incrementErrors()
		return nil, 0, fmt.Errorf("failed to search vectors: %w", err)
	}

	// No results found
	if len(results) == 0 {
		c.incrementMisses()
		return nil, 0, nil
	}

	// Return best match
	bestMatch := results[0]
	c.incrementHits(bestMatch.Similarity)

	// Update usage stats when using the in-memory store.
	if store, ok := c.vectorStore.(*MemoryVectorStore); ok && bestMatch.Entry != nil && bestMatch.Entry.Embedding != nil {
		store.MarkUsed(bestMatch.Entry.Embedding.Hash, time.Now())
	}

	return bestMatch.Entry.CacheEntry.Response, bestMatch.Similarity, nil
}

// Set stores a response with its embedding.
func (c *SemanticCache) Set(ctx context.Context, req *provider.CompletionRequest, resp *provider.CompletionResponse) error {
	// Generate embedding
	queryText := buildQueryText(req)
	embedding, err := c.generator.Generate(ctx, queryText)
	if err != nil {
		c.incrementErrors()
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Create cache entry
	cacheKey := llmcache.BuildCacheKey(req)
	cacheEntry := &llmcache.CacheEntry{
		Response:    resp,
		Usage:       resp.Usage,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(c.config.TTL),
		RequestHash: cacheKey.Hash,
	}

	// Create vector entry
	vectorEntry := &VectorEntry{
		Embedding:  embedding,
		CacheEntry: cacheEntry,
		CacheKey:   cacheKey,
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(c.config.TTL),
		LastUsed:   time.Now(),
	}

	// Store in vector store
	if err := c.vectorStore.Add(ctx, vectorEntry); err != nil {
		c.incrementErrors()
		return fmt.Errorf("failed to store vector: %w", err)
	}

	c.incrementStores()
	return nil
}

// Stats returns cache statistics.
func (c *SemanticCache) Stats() SemanticCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.stats
}

// Clear clears the cache.
func (c *SemanticCache) Clear(ctx context.Context) error {
	return c.vectorStore.Clear(ctx)
}

// HitRate returns the semantic cache hit rate.
func (s *SemanticCacheStats) HitRate() float64 {
	total := s.SemanticHits + s.SemanticMisses
	if total == 0 {
		return 0.0
	}
	return float64(s.SemanticHits) / float64(total)
}

// incrementHits increments hit counter and updates average similarity.
func (c *SemanticCache) incrementHits(similarity float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.SemanticHits++
	// Update running average
	total := c.stats.SemanticHits
	c.stats.AvgSimilarity = (c.stats.AvgSimilarity*float64(total-1) + similarity) / float64(total)
}

// incrementMisses increments miss counter.
func (c *SemanticCache) incrementMisses() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.SemanticMisses++
}

// incrementStores increments store counter.
func (c *SemanticCache) incrementStores() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.Stores++
}

// incrementErrors increments error counter.
func (c *SemanticCache) incrementErrors() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.Errors++
}

// buildQueryText builds query text from request for embedding.
func buildQueryText(req *provider.CompletionRequest) string {
	var text string

	// Add system prompt if present
	if req.System != "" {
		text += req.System + "\n"
	}

	// Add messages
	for _, msg := range req.Messages {
		text += msg.GetText() + "\n"
	}

	return text
}
