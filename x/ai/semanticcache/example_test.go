package semanticcache_test

import (
	"context"
	"fmt"
	"time"

	"github.com/spcent/plumego/x/ai/llmcache"
	"github.com/spcent/plumego/x/ai/provider"
	"github.com/spcent/plumego/x/ai/semanticcache"
)

// Example_basicUsage demonstrates basic semantic caching
func Example_basicUsage() {
	ctx := context.Background()

	// Setup: Create a mock provider
	mockProvider := &mockProvider{response: "AI is artificial intelligence..."}

	// 1. Create embedding generator
	generator := semanticcache.NewSimpleEmbeddingGenerator(128)

	// 2. Create vector store
	vectorStore := semanticcache.NewMemoryVectorStore(1000, 1*time.Hour)

	// 3. Create semantic cache
	config := semanticcache.DefaultSemanticCacheConfig()
	semanticCache := semanticcache.NewSemanticCache(generator, vectorStore, config)

	// 4. Create caching provider
	cachingProvider, err := semanticcache.NewSemanticCachingProvider(
		mockProvider,
		semanticCache,
	)
	if err != nil {
		panic(err)
	}

	// First request - cache miss
	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "What is AI?"),
		},
	}

	resp, _ := cachingProvider.Complete(ctx, req)
	fmt.Println("First call:", resp.GetText())

	// Second request - cache hit (exact match)
	resp2, _ := cachingProvider.Complete(ctx, req)
	fmt.Println("Second call:", resp2.GetText())

	// Check stats
	stats := cachingProvider.Stats()
	fmt.Printf("Hit rate: %.0f%%\n", stats.HitRate()*100)

	// Output:
	// First call: AI is artificial intelligence...
	// Second call: AI is artificial intelligence...
	// Hit rate: 50%
}

// Example_withExactCache demonstrates hybrid caching
func Example_withExactCache() {
	ctx := context.Background()
	mockProvider := &mockProvider{response: "Response"}

	// Create both exact and semantic caches
	exactCache := llmcache.NewMemoryCache(1*time.Hour, 1000)

	generator := semanticcache.NewSimpleEmbeddingGenerator(128)
	vectorStore := semanticcache.NewMemoryVectorStore(1000, 1*time.Hour)
	semanticCache := semanticcache.NewSemanticCache(
		generator,
		vectorStore,
		semanticcache.DefaultSemanticCacheConfig(),
	)

	// Create provider with both caches
	cachingProvider, err := semanticcache.NewSemanticCachingProvider(
		mockProvider,
		semanticCache,
		semanticcache.WithExactCache(exactCache),
	)
	if err != nil {
		panic(err)
	}

	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// First call - populates both caches
	_, _ = cachingProvider.Complete(ctx, req)

	// Second call - hits exact cache (faster)
	_, _ = cachingProvider.Complete(ctx, req)

	fmt.Println("Hybrid caching enabled")

	// Output:
	// Hybrid caching enabled
}

// Example_withMetrics demonstrates metrics integration
func Example_withMetrics() {
	ctx := context.Background()
	mockProvider := &mockProvider{response: "Response"}

	// Create metrics collector
	collector := semanticcache.NoopMetricRecorder() // Use PrometheusCollector in production

	// Create instrumented components
	generator := semanticcache.NewSimpleEmbeddingGenerator(128)
	vectorStore := semanticcache.NewMemoryVectorStore(1000, 1*time.Hour)
	instrumentedStore := semanticcache.NewInstrumentedVectorStore(vectorStore, collector)

	semanticCache := semanticcache.NewSemanticCache(
		generator,
		instrumentedStore,
		semanticcache.DefaultSemanticCacheConfig(),
	)

	cachingProvider, err := semanticcache.NewSemanticCachingProvider(
		mockProvider,
		semanticCache,
	)
	if err != nil {
		panic(err)
	}

	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// Make requests - metrics are collected automatically
	_, _ = cachingProvider.Complete(ctx, req)
	_, _ = cachingProvider.Complete(ctx, req)

	stats := cachingProvider.Stats()
	fmt.Printf("Hits: %d, Misses: %d\n", stats.SemanticHits, stats.SemanticMisses)

	// Output:
	// Hits: 1, Misses: 1
}

// Example_customConfiguration shows advanced configuration
func Example_customConfiguration() {
	ctx := context.Background()
	mockProvider := &mockProvider{response: "Response"}

	// Custom semantic cache config
	cacheConfig := &semanticcache.SemanticCacheConfig{
		SimilarityThreshold: 0.90,          // Higher threshold = more conservative
		TopK:                10,            // Consider more results
		EnableExactMatch:    true,          // Enable exact match optimization
		TTL:                 2 * time.Hour, // Longer TTL
		MaxSize:             5000,          // Larger cache
	}

	// Custom provider config
	providerConfig := &semanticcache.ProviderConfig{
		EnableExactCache:  false, // Semantic only
		MinSimilarity:     0.90,  // Match cache config
		EnablePassthrough: true,  // Don't fail on cache errors
	}

	generator := semanticcache.NewSimpleEmbeddingGenerator(256) // More dimensions
	vectorStore := semanticcache.NewMemoryVectorStore(5000, 2*time.Hour)
	semanticCache := semanticcache.NewSemanticCache(generator, vectorStore, cacheConfig)

	cachingProvider, err := semanticcache.NewSemanticCachingProvider(
		mockProvider,
		semanticCache,
		semanticcache.WithProviderConfig(providerConfig),
	)
	if err != nil {
		panic(err)
	}

	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	_, _ = cachingProvider.Complete(ctx, req)
	fmt.Println("Custom configuration applied")

	// Output:
	// Custom configuration applied
}

// Example_concurrentAccess demonstrates thread safety
func Example_concurrentAccess() {
	ctx := context.Background()
	mockProvider := &mockProvider{response: "Response"}

	generator := semanticcache.NewSimpleEmbeddingGenerator(128)
	vectorStore := semanticcache.NewMemoryVectorStore(1000, 1*time.Hour)
	semanticCache := semanticcache.NewSemanticCache(
		generator,
		vectorStore,
		semanticcache.DefaultSemanticCacheConfig(),
	)

	cachingProvider, err := semanticcache.NewSemanticCachingProvider(
		mockProvider,
		semanticCache,
	)
	if err != nil {
		panic(err)
	}

	// Concurrent requests
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			req := &provider.CompletionRequest{
				Model: "test-model",
				Messages: []provider.Message{
					provider.NewTextMessage(provider.RoleUser, "concurrent query"),
				},
			}
			_, _ = cachingProvider.Complete(ctx, req)
			done <- true
		}()
	}

	// Wait for all
	for i := 0; i < 10; i++ {
		<-done
	}

	fmt.Println("Concurrent access completed")

	// Output:
	// Concurrent access completed
}

// mockProvider is a simple mock for examples
type mockProvider struct {
	response string
}

func (m *mockProvider) Name() string {
	return "mock"
}

func (m *mockProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	return &provider.CompletionResponse{
		ID:    "mock-id",
		Model: req.Model,
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: m.response},
		},
	}, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	return nil, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return nil, nil
}

func (m *mockProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return nil, nil
}

func (m *mockProvider) CountTokens(text string) (int, error) {
	return len(text), nil
}
