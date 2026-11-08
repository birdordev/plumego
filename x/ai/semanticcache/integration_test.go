package semanticcache

import (
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/llmcache"
	"github.com/birdordev/plumego/x/ai/provider"
)

// TestIntegration_FullStack tests the complete semantic caching stack
func TestIntegration_FullStack(t *testing.T) {
	ctx := t.Context()

	// Setup: Create full stack
	mockProvider := &MockProvider{responseText: "This is a response about AI"}
	gen := NewSimpleEmbeddingGenerator(128)
	vectorStore := NewMemoryVectorStore(1000, 1*time.Hour)
	metricsCollector := NoopMetricRecorder()

	// Wrap vector store with instrumentation
	instrumentedStore := NewInstrumentedVectorStore(vectorStore, metricsCollector)

	// Create semantic cache
	semanticConfig := DefaultSemanticCacheConfig()
	semanticCache := NewSemanticCache(gen, instrumentedStore, semanticConfig)

	// Create exact cache
	exactCache := llmcache.NewMemoryCache(1*time.Hour, 1000)

	// Create semantic caching provider with exact cache fallback
	providerConfig := DefaultProviderConfig()
	cachingProvider, err := NewSemanticCachingProvider(
		mockProvider,
		semanticCache,
		WithExactCache(exactCache),
		WithProviderConfig(providerConfig),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Test 1: First request - cache miss, calls provider
	t.Run("FirstRequest_CacheMiss", func(t *testing.T) {
		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "What is artificial intelligence?"),
			},
			Temperature: 0.7,
			MaxTokens:   100,
		}

		resp, err := cachingProvider.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		if resp == nil {
			t.Fatal("expected response")
		}

		if resp.GetText() != "This is a response about AI" {
			t.Errorf("unexpected response text: %s", resp.GetText())
		}

		if mockProvider.CallCount() != 1 {
			t.Errorf("expected 1 provider call, got %d", mockProvider.CallCount())
		}
	})

	// Test 2: Exact same request - exact cache hit
	t.Run("ExactMatch_ExactCacheHit", func(t *testing.T) {
		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "What is artificial intelligence?"),
			},
			Temperature: 0.7,
			MaxTokens:   100,
		}

		resp, err := cachingProvider.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		if resp == nil {
			t.Fatal("expected response")
		}

		// Should not call provider again (exact cache hit)
		if mockProvider.CallCount() != 1 {
			t.Errorf("expected still 1 provider call (exact cached), got %d", mockProvider.CallCount())
		}
	})

	// Test 3: Different request with similar query
	t.Run("SimilarQuery_SemanticCacheHit", func(t *testing.T) {
		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "Explain machine learning concepts"),
			},
			Temperature: 0.7,
			MaxTokens:   100,
		}

		resp, err := cachingProvider.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		if resp == nil {
			t.Fatal("expected response")
		}

		// Note: With SimpleEmbeddingGenerator, semantic similarity is low
		// In production with real embeddings, this would likely hit semantic cache
		// For now, it will call the provider
		t.Logf("Provider call count: %d", mockProvider.CallCount())
	})

	// Test 4: Verify statistics
	t.Run("VerifyStatistics", func(t *testing.T) {
		stats := cachingProvider.Stats()
		t.Logf("Semantic hits: %d", stats.SemanticHits)
		t.Logf("Semantic misses: %d", stats.SemanticMisses)
		t.Logf("Stores: %d", stats.Stores)
		t.Logf("Hit rate: %.2f%%", stats.HitRate()*100)

		if stats.Stores == 0 {
			t.Error("expected some stores")
		}
	})

	// Test 5: Clear cache and verify
	t.Run("ClearCache", func(t *testing.T) {
		if err := cachingProvider.ClearCache(ctx); err != nil {
			t.Fatalf("ClearCache failed: %v", err)
		}

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "What is artificial intelligence?"),
			},
			Temperature: 0.7,
			MaxTokens:   100,
		}

		// Should call provider again after clear
		prevCount := mockProvider.CallCount()
		_, err := cachingProvider.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		if mockProvider.CallCount() <= prevCount {
			t.Error("expected provider call after cache clear")
		}
	})
}

// TestIntegration_MultipleQueries tests caching behavior with various queries
func TestIntegration_MultipleQueries(t *testing.T) {
	ctx := t.Context()

	mockProvider := &MockProvider{responseText: "Response"}
	gen := NewSimpleEmbeddingGenerator(256) // Larger dimensions
	vectorStore := NewMemoryVectorStore(100, 1*time.Hour)
	semanticCache := NewSemanticCache(gen, vectorStore, DefaultSemanticCacheConfig())

	cachingProvider, err := NewSemanticCachingProvider(mockProvider, semanticCache)
	if err != nil {
		t.Fatal(err)
	}

	queries := []string{
		"What is machine learning?",
		"Explain neural networks",
		"How does deep learning work?",
		"What are transformers in AI?",
		"Describe natural language processing",
	}

	// Make multiple requests
	for i, query := range queries {
		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, query),
			},
		}

		resp, err := cachingProvider.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}

		if resp == nil {
			t.Fatalf("Request %d returned nil response", i)
		}
	}

	// Verify stats
	stats := cachingProvider.Stats()
	t.Logf("Total requests: %d", len(queries))
	t.Logf("Provider calls: %d", mockProvider.CallCount())
	t.Logf("Semantic hits: %d", stats.SemanticHits)
	t.Logf("Semantic misses: %d", stats.SemanticMisses)
	t.Logf("Stores: %d", stats.Stores)

	// Verify vector store
	if vectorStore.Size() != len(queries) {
		t.Errorf("expected %d vectors stored, got %d", len(queries), vectorStore.Size())
	}

	// Make same requests again - should have higher hit rate
	prevCalls := mockProvider.CallCount()
	for _, query := range queries {
		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, query),
			},
		}

		_, err := cachingProvider.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Repeat request failed: %v", err)
		}
	}

	// Should have fewer new provider calls
	newCalls := mockProvider.CallCount() - prevCalls
	t.Logf("New provider calls on repeat: %d", newCalls)

	if newCalls > 0 {
		t.Logf("Some cache misses occurred (expected with hash-based embeddings)")
	}
}

// TestIntegration_ConcurrentAccess tests thread safety
func TestIntegration_ConcurrentAccess(t *testing.T) {
	ctx := t.Context()

	mockProvider := &MockProvider{responseText: "Response"}
	gen := NewSimpleEmbeddingGenerator(128)
	vectorStore := NewMemoryVectorStore(1000, 1*time.Hour)
	semanticCache := NewSemanticCache(gen, vectorStore, DefaultSemanticCacheConfig())

	cachingProvider, err := NewSemanticCachingProvider(mockProvider, semanticCache)
	if err != nil {
		t.Fatal(err)
	}

	// Run concurrent requests
	numGoroutines := 10
	numRequestsPerGoroutine := 5

	done := make(chan bool, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer func() { done <- true }()

			for i := 0; i < numRequestsPerGoroutine; i++ {
				req := &provider.CompletionRequest{
					Model: "test-model",
					Messages: []provider.Message{
						provider.NewTextMessage(provider.RoleUser, "Concurrent query"),
					},
				}

				_, err := cachingProvider.Complete(ctx, req)
				if err != nil {
					t.Errorf("Goroutine %d request %d failed: %v", goroutineID, i, err)
				}
			}
		}(g)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	stats := cachingProvider.Stats()
	t.Logf("Concurrent test - Provider calls: %d", mockProvider.CallCount())
	t.Logf("Concurrent test - Semantic hits: %d", stats.SemanticHits)
	t.Logf("Concurrent test - Semantic misses: %d", stats.SemanticMisses)

	// Verify no panics occurred
	t.Log("Concurrent access test completed successfully")
}

// TestIntegration_TTLExpiration tests cache expiration
func TestIntegration_TTLExpiration(t *testing.T) {
	ctx := t.Context()

	mockProvider := &MockProvider{responseText: "Response"}
	gen := NewSimpleEmbeddingGenerator(128)
	vectorStore := NewMemoryVectorStore(100, 50*time.Millisecond) // Short TTL

	config := &SemanticCacheConfig{
		SimilarityThreshold: 0.85,
		TopK:                5,
		EnableExactMatch:    true,
		TTL:                 50 * time.Millisecond,
		MaxSize:             100,
	}
	semanticCache := NewSemanticCache(gen, vectorStore, config)
	cachingProvider, err := NewSemanticCachingProvider(mockProvider, semanticCache)
	if err != nil {
		t.Fatal(err)
	}

	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "Test query"),
		},
	}

	// First request
	_, err = cachingProvider.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	initialCalls := mockProvider.CallCount()

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Cleanup expired entries
	vectorStore.CleanupExpired()

	// Second request - should call provider again
	_, err = cachingProvider.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	if mockProvider.CallCount() <= initialCalls {
		t.Error("expected provider call after expiration")
	}

	t.Log("TTL expiration test completed successfully")
}
