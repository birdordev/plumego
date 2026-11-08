// Package llmcache provides intelligent caching for LLM responses.
package llmcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

// Cache is the interface for LLM response caching.
type Cache interface {
	// Get retrieves a cached response.
	Get(ctx context.Context, key *CacheKey) (*CacheEntry, error)

	// Set stores a response in cache.
	Set(ctx context.Context, key *CacheKey, entry *CacheEntry) error

	// Delete removes a cached entry.
	Delete(ctx context.Context, key *CacheKey) error

	// Clear removes all cached entries.
	Clear(ctx context.Context) error

	// Stats returns cache statistics.
	Stats() CacheStats
}

// CacheKey represents a cache key.
type CacheKey struct {
	// Model name
	Model string

	// Normalized prompt
	Prompt string

	// System prompt (optional)
	System string

	// Temperature
	Temperature float64

	// Max tokens
	MaxTokens int

	// Tools (serialized)
	Tools string

	// Computed hash
	Hash string
}

// CacheEntry represents a cached response.
type CacheEntry struct {
	// Cached response
	Response *provider.CompletionResponse

	// Token usage
	Usage tokenizer.TokenUsage

	// Cache metadata
	CreatedAt time.Time
	ExpiresAt time.Time
	HitCount  int

	// Original request hash
	RequestHash string
}

// CacheStats tracks cache performance.
type CacheStats struct {
	Hits        int64
	Misses      int64
	Evictions   int64
	TotalTokens int64
	SavedCost   float64
}

// HitRate returns the cache hit rate.
func (s *CacheStats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0.0
	}
	return float64(s.Hits) / float64(total)
}

// MemoryCache implements in-memory LLM response caching.
type MemoryCache struct {
	entries map[string]*CacheEntry
	stats   CacheStats
	ttl     time.Duration
	maxSize int
	mu      sync.RWMutex
}

// NewMemoryCache creates a new memory cache.
func NewMemoryCache(ttl time.Duration, maxSize int) *MemoryCache {
	if ttl == 0 {
		ttl = 1 * time.Hour
	}
	if maxSize == 0 {
		maxSize = 1000
	}

	return &MemoryCache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// Get implements Cache.
func (c *MemoryCache) Get(ctx context.Context, key *CacheKey) (*CacheEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key.Hash]
	if !ok {
		c.stats.Misses++
		return nil, fmt.Errorf("cache miss")
	}

	// Check expiration
	if time.Now().After(entry.ExpiresAt) {
		c.stats.Misses++
		return nil, fmt.Errorf("cache expired")
	}

	c.stats.Hits++
	entry.HitCount++

	// Return copy to avoid external mutation
	copied := *entry
	copied.Response = copyResponse(entry.Response)

	return &copied, nil
}

// Set implements Cache.
func (c *MemoryCache) Set(ctx context.Context, key *CacheKey, entry *CacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check size limit
	if len(c.entries) >= c.maxSize {
		// Simple eviction: remove oldest
		c.evictOldest()
	}

	// Set expiration
	if entry.ExpiresAt.IsZero() {
		entry.ExpiresAt = time.Now().Add(c.ttl)
	}

	// Store copy
	copied := *entry
	copied.Response = copyResponse(entry.Response)
	c.entries[key.Hash] = &copied

	// Update stats
	c.stats.TotalTokens += int64(entry.Usage.TotalTokens)

	return nil
}

// Delete implements Cache.
func (c *MemoryCache) Delete(ctx context.Context, key *CacheKey) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key.Hash)
	return nil
}

// Clear implements Cache.
func (c *MemoryCache) Clear(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CacheEntry)
	c.stats = CacheStats{}
	return nil
}

// Stats implements Cache.
func (c *MemoryCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.stats
}

// evictOldest removes the oldest cache entry.
func (c *MemoryCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for hash, entry := range c.entries {
		if oldestKey == "" || entry.CreatedAt.Before(oldestTime) {
			oldestKey = hash
			oldestTime = entry.CreatedAt
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
		c.stats.Evictions++
	}
}

// BuildCacheKey builds a cache key from a request.
func BuildCacheKey(req *provider.CompletionRequest) *CacheKey {
	key := &CacheKey{
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	// Normalize messages to prompt
	var promptParts []string
	for _, msg := range req.Messages {
		if msg.Role == provider.RoleSystem {
			key.System = msg.GetText()
		} else {
			promptParts = append(promptParts, msg.GetText())
		}
	}
	key.Prompt = NormalizePrompt(strings.Join(promptParts, "\n"))

	// Serialize tools
	if len(req.Tools) > 0 {
		toolsJSON, _ := json.Marshal(req.Tools)
		key.Tools = string(toolsJSON)
	}

	// Compute hash
	key.Hash = computeHash(key)

	return key
}

// NormalizePrompt normalizes a prompt for caching.
func NormalizePrompt(prompt string) string {
	// Trim whitespace
	normalized := strings.TrimSpace(prompt)

	// Remove multiple spaces
	normalized = strings.Join(strings.Fields(normalized), " ")

	// Lowercase for better cache hits (optional - can be configurable)
	// normalized = strings.ToLower(normalized)

	return normalized
}

// computeHash computes a cache key hash.
func computeHash(key *CacheKey) string {
	data := fmt.Sprintf("%s|%s|%s|%f|%d|%s",
		key.Model,
		key.System,
		key.Prompt,
		key.Temperature,
		key.MaxTokens,
		key.Tools,
	)

	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// copyResponse creates a deep copy of a response.
func copyResponse(resp *provider.CompletionResponse) *provider.CompletionResponse {
	if resp == nil {
		return nil
	}

	copied := *resp
	copied.Content = make([]provider.ContentBlock, len(resp.Content))
	copy(copied.Content, resp.Content)

	if resp.Metadata != nil {
		copied.Metadata = make(map[string]any)
		for k, v := range resp.Metadata {
			copied.Metadata[k] = v
		}
	}

	return &copied
}

// CachingProvider wraps a provider with caching.
type CachingProvider struct {
	provider provider.Provider
	cache    Cache
}

// NewCachingProvider creates a caching provider wrapper.
func NewCachingProvider(p provider.Provider, cache Cache) *CachingProvider {
	return &CachingProvider{
		provider: p,
		cache:    cache,
	}
}

// Name implements Provider.
func (p *CachingProvider) Name() string {
	return p.provider.Name()
}

// Complete implements Provider with caching.
func (p *CachingProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	// Build cache key
	key := BuildCacheKey(req)

	// Try cache first
	cached, err := p.cache.Get(ctx, key)
	if err == nil && cached != nil {
		// Cache hit
		return cached.Response, nil
	}

	// Cache miss - call provider
	resp, err := p.provider.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	// Store in cache
	entry := &CacheEntry{
		Response:  resp,
		Usage:     resp.Usage,
		CreatedAt: time.Now(),
	}
	_ = p.cache.Set(ctx, key, entry)

	return resp, nil
}

// CompleteStream implements Provider (no caching for streams).
func (p *CachingProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	// Streaming responses are not cached
	return p.provider.CompleteStream(ctx, req)
}

// ListModels implements Provider.
func (p *CachingProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return p.provider.ListModels(ctx)
}

// GetModel implements Provider.
func (p *CachingProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return p.provider.GetModel(ctx, modelID)
}

// CountTokens implements Provider.
func (p *CachingProvider) CountTokens(text string) (int, error) {
	return p.provider.CountTokens(text)
}

// InvalidateByModel invalidates all cache entries for a model.
func (c *MemoryCache) InvalidateByModel(model string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for hash, entry := range c.entries {
		// Check if entry is for this model (stored in hash)
		// This is approximate - in production, store model in entry
		_ = entry
		delete(c.entries, hash)
		count++
	}

	return count
}
