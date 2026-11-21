// Package llmcache provides intelligent caching for LLM responses.
package llmcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
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

type memoryCacheRecord struct {
	CacheEntry
	model string
}

// MemoryCache implements in-memory LLM response caching.
type MemoryCache struct {
	entries map[string]*memoryCacheRecord
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
		entries: make(map[string]*memoryCacheRecord),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// Get implements Cache.
func (c *MemoryCache) Get(ctx context.Context, key *CacheKey) (*CacheEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

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
	copied := entry.CacheEntry
	copied.Response = copyResponse(entry.Response)

	return &copied, nil
}

// Set implements Cache. Only inserting a new key at capacity evicts an entry.
// A zero expiration uses the cache TTL and is filled in on the stored copy.
// Content blocks, JSON-shaped maps and slices, and byte slices are copied.
// Other metadata values are shared and should be treated as immutable.
func (c *MemoryCache) Set(ctx context.Context, key *CacheKey, entry *CacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check size limit
	if _, exists := c.entries[key.Hash]; !exists && len(c.entries) >= c.maxSize {
		// Simple eviction: remove oldest
		c.evictOldest()
	}

	copied := *entry
	if copied.ExpiresAt.IsZero() {
		copied.ExpiresAt = time.Now().Add(c.ttl)
	}
	copied.Response = copyResponse(entry.Response)
	c.entries[key.Hash] = &memoryCacheRecord{CacheEntry: copied, model: key.Model}

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

	c.entries = make(map[string]*memoryCacheRecord)
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
// The hash uses a versioned encoding of the completion request. Tracking
// metadata is excluded. Older hashes miss naturally, and an empty hash means
// the request could not be encoded for caching.
func BuildCacheKey(req *provider.CompletionRequest) *CacheKey {
	if req == nil {
		return &CacheKey{}
	}
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
		toolsJSON, err := json.Marshal(req.Tools)
		if err != nil {
			return key
		}
		key.Tools = string(toolsJSON)
	}

	request := *req
	request.Metadata = nil
	data, err := json.Marshal(request)
	if err != nil {
		return key
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("llmcache:v2\n"))
	_, _ = digest.Write(data)
	key.Hash = hex.EncodeToString(digest.Sum(nil))

	return key
}

// NormalizePrompt collapses whitespace to single spaces and preserves case.
func NormalizePrompt(prompt string) string {
	return strings.Join(strings.Fields(prompt), " ")
}

// copyResponse copies content blocks and JSON-shaped metadata. Other metadata
// values are opaque and must be immutable while shared with the cache.
func copyResponse(resp *provider.CompletionResponse) *provider.CompletionResponse {
	if resp == nil {
		return nil
	}

	copied := *resp
	cloner := make(responseValueCloner)
	copied.Metadata = cloner.cloneMap(resp.Metadata)
	copied.Content = slices.Clone(resp.Content)
	for i := range copied.Content {
		block := &copied.Content[i]
		if block.ToolUse != nil {
			tool := *block.ToolUse
			tool.Input = cloner.cloneMap(tool.Input)
			block.ToolUse = &tool
		}
		if block.ToolResult != nil {
			result := *block.ToolResult
			block.ToolResult = &result
		}
		if block.Image != nil {
			image := *block.Image
			block.Image = &image
		}
	}
	return &copied
}

type responseValueKey struct {
	pointer uintptr
	length  int
	slice   bool
}

// Record each clone before visiting children so cyclic containers are safe.
type responseValueCloner map[responseValueKey]any

func (c responseValueCloner) cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	key := responseValueKey{pointer: reflect.ValueOf(value).Pointer()}
	if existing, ok := c[key]; ok {
		return existing.(map[string]any)
	}
	result := make(map[string]any, len(value))
	c[key] = result
	for name, field := range value {
		result[name] = c.clone(field)
	}
	return result
}

func (c responseValueCloner) clone(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return c.cloneMap(v)
	case []any:
		if len(v) == 0 {
			return slices.Clone(v)
		}
		key := responseValueKey{pointer: reflect.ValueOf(v).Pointer(), length: len(v), slice: true}
		if existing, ok := c[key]; ok {
			return existing.([]any)
		}
		result := make([]any, len(v))
		c[key] = result
		for i, field := range v {
			result[i] = c.clone(field)
		}
		return result
	case []string:
		return slices.Clone(v)
	case []byte:
		return slices.Clone(v)
	case json.RawMessage:
		return slices.Clone(v)
	default:
		return v
	}
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

// Complete caches encodable requests and delegates uncacheable requests directly.
func (p *CachingProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	// Build cache key
	key := BuildCacheKey(req)
	if key.Hash == "" {
		return p.provider.Complete(ctx, req)
	}

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

// InvalidateByModel removes entries whose key model exactly matches model
// and returns the number removed. An empty model matches empty key models.
func (c *MemoryCache) InvalidateByModel(model string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for hash, entry := range c.entries {
		if entry.model == model {
			delete(c.entries, hash)
			count++
		}
	}

	return count
}
