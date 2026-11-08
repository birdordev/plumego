package semanticcache

import (
	"context"
	"errors"
	"fmt"

	"github.com/birdordev/plumego/x/ai/llmcache"
	"github.com/birdordev/plumego/x/ai/provider"
)

var (
	// ErrProviderRequired is returned when a semantic caching provider is built without an upstream provider.
	ErrProviderRequired = errors.New("semanticcache: provider is required")

	// ErrSemanticCacheRequired is returned when a semantic caching provider is built without a semantic cache.
	ErrSemanticCacheRequired = errors.New("semanticcache: semantic cache is required")
)

// SemanticCachingProvider wraps a provider with semantic caching.
type SemanticCachingProvider struct {
	provider      provider.Provider
	semanticCache *SemanticCache
	exactCache    llmcache.Cache // Optional fallback to exact cache
	config        *ProviderConfig
}

// ProviderConfig configures the semantic caching provider.
type ProviderConfig struct {
	// Enable exact cache fallback
	EnableExactCache bool

	// Minimum similarity score to use cached response
	MinSimilarity float64

	// Enable passthrough on semantic cache errors
	EnablePassthrough bool
}

// DefaultProviderConfig returns default provider configuration.
func DefaultProviderConfig() *ProviderConfig {
	return &ProviderConfig{
		EnableExactCache:  true,
		MinSimilarity:     0.85,
		EnablePassthrough: true,
	}
}

// Option configures the semantic caching provider.
type Option func(*SemanticCachingProvider)

// WithExactCache enables exact cache as fallback.
func WithExactCache(cache llmcache.Cache) Option {
	return func(p *SemanticCachingProvider) {
		p.exactCache = cache
	}
}

// WithProviderConfig sets the provider configuration.
func WithProviderConfig(config *ProviderConfig) Option {
	return func(p *SemanticCachingProvider) {
		p.config = config
	}
}

// NewSemanticCachingProvider creates a semantic caching provider wrapper and
// returns validation errors for missing required dependencies.
func NewSemanticCachingProvider(
	p provider.Provider,
	semanticCache *SemanticCache,
	opts ...Option,
) (*SemanticCachingProvider, error) {
	if p == nil {
		return nil, ErrProviderRequired
	}
	if semanticCache == nil {
		return nil, ErrSemanticCacheRequired
	}

	scp := &SemanticCachingProvider{
		provider:      p,
		semanticCache: semanticCache,
		config:        DefaultProviderConfig(),
	}

	for _, opt := range opts {
		opt(scp)
	}
	if scp.config == nil {
		scp.config = DefaultProviderConfig()
	}

	return scp, nil
}

// Name implements Provider.
func (p *SemanticCachingProvider) Name() string {
	return p.provider.Name()
}

// Complete implements Provider with semantic caching.
func (p *SemanticCachingProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	// Try exact cache first if enabled
	if p.config.EnableExactCache && p.exactCache != nil {
		cacheKey := llmcache.BuildCacheKey(req)
		if cached, err := p.exactCache.Get(ctx, cacheKey); err == nil && cached != nil {
			return cached.Response, nil
		}
	}

	// Try semantic cache
	cached, similarity, err := p.semanticCache.Get(ctx, req)
	if err == nil && cached != nil && similarity >= p.config.MinSimilarity {
		// Semantic cache hit
		return cached, nil
	}

	// If semantic cache failed but passthrough is disabled, return error
	if err != nil && !p.config.EnablePassthrough {
		return nil, fmt.Errorf("semantic cache error: %w", err)
	}

	// Cache miss - call provider
	resp, err := p.provider.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	// Store in semantic cache (best effort)
	if storeErr := p.semanticCache.Set(ctx, req, resp); storeErr != nil {
		// Log error but don't fail the request
		// In production, use proper logging
		_ = storeErr
	}

	// Store in exact cache if enabled (best effort)
	if p.config.EnableExactCache && p.exactCache != nil {
		cacheKey := llmcache.BuildCacheKey(req)
		cacheEntry := &llmcache.CacheEntry{
			Response:    resp,
			Usage:       resp.Usage,
			RequestHash: cacheKey.Hash,
		}
		if storeErr := p.exactCache.Set(ctx, cacheKey, cacheEntry); storeErr != nil {
			_ = storeErr
		}
	}

	return resp, nil
}

// CompleteStream implements Provider (no caching for streams).
func (p *SemanticCachingProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	// Streaming responses are not cached
	return p.provider.CompleteStream(ctx, req)
}

// ListModels implements Provider.
func (p *SemanticCachingProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return p.provider.ListModels(ctx)
}

// GetModel implements Provider.
func (p *SemanticCachingProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return p.provider.GetModel(ctx, modelID)
}

// CountTokens implements Provider.
func (p *SemanticCachingProvider) CountTokens(text string) (int, error) {
	return p.provider.CountTokens(text)
}

// Stats returns semantic cache statistics.
func (p *SemanticCachingProvider) Stats() SemanticCacheStats {
	return p.semanticCache.Stats()
}

// ClearCache clears both semantic and exact caches.
func (p *SemanticCachingProvider) ClearCache(ctx context.Context) error {
	// Clear semantic cache
	if err := p.semanticCache.Clear(ctx); err != nil {
		return err
	}

	// Clear exact cache if enabled
	if p.exactCache != nil {
		if err := p.exactCache.Clear(ctx); err != nil {
			return err
		}
	}

	return nil
}
