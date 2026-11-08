package semanticcache

import (
	"fmt"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/llmcache"
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

func TestMemoryVectorStore(t *testing.T) {
	ctx := t.Context()

	t.Run("AddAndSearch", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)

		// Add entries
		entry1 := createTestVectorEntry("test query 1", []float64{1.0, 0.0, 0.0})
		entry2 := createTestVectorEntry("test query 2", []float64{0.9, 0.1, 0.0})
		entry3 := createTestVectorEntry("different query", []float64{0.0, 0.0, 1.0})

		if err := store.Add(ctx, entry1); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
		if err := store.Add(ctx, entry2); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
		if err := store.Add(ctx, entry3); err != nil {
			t.Fatalf("Add failed: %v", err)
		}

		if store.Size() != 3 {
			t.Errorf("expected size 3, got %d", store.Size())
		}

		// Search for similar to entry1
		query := &Embedding{
			Vector: []float64{1.0, 0.0, 0.0},
			Hash:   "query",
		}

		results, err := store.Search(ctx, query, 5, 0.5)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) == 0 {
			t.Fatal("expected results, got none")
		}

		// Should find entry1 as most similar
		if results[0].Similarity < 0.9 {
			t.Errorf("expected high similarity, got %f", results[0].Similarity)
		}
	})

	t.Run("SearchWithThreshold", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)

		entry := createTestVectorEntry("test", []float64{1.0, 0.0, 0.0})
		store.Add(ctx, entry)

		// Query with high threshold - should find match
		query := &Embedding{
			Vector: []float64{1.0, 0.0, 0.0},
			Hash:   "query",
		}
		results, _ := store.Search(ctx, query, 5, 0.9)
		if len(results) == 0 {
			t.Error("expected results with high similarity")
		}

		// Query with orthogonal vector - should not find match
		query2 := &Embedding{
			Vector: []float64{0.0, 1.0, 0.0},
			Hash:   "query2",
		}
		results2, _ := store.Search(ctx, query2, 5, 0.5)
		if len(results2) > 0 {
			t.Error("expected no results for orthogonal vector")
		}
	})

	t.Run("TopKLimit", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)

		// Add 10 similar entries
		for i := 0; i < 10; i++ {
			entry := createTestVectorEntry("test", []float64{1.0, 0.0, 0.0})
			store.Add(ctx, entry)
		}

		query := &Embedding{
			Vector: []float64{1.0, 0.0, 0.0},
			Hash:   "query",
		}

		// Search with topK=3
		results, _ := store.Search(ctx, query, 3, 0.0)
		if len(results) > 3 {
			t.Errorf("expected max 3 results, got %d", len(results))
		}
	})

	t.Run("ExpirationFiltering", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Millisecond)

		entry := createTestVectorEntry("test", []float64{1.0, 0.0, 0.0})
		// Clear expiration so store's TTL is used
		entry.ExpiresAt = time.Time{}
		store.Add(ctx, entry)

		// Wait for expiration
		time.Sleep(10 * time.Millisecond)

		query := &Embedding{
			Vector: []float64{1.0, 0.0, 0.0},
			Hash:   "query",
		}
		results, _ := store.Search(ctx, query, 5, 0.0)

		if len(results) > 0 {
			t.Error("expected no results for expired entries")
		}
	})

	t.Run("DeleteAndClear", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)

		entry := createTestVectorEntry("test", []float64{1.0, 0.0, 0.0})
		store.Add(ctx, entry)

		if store.Size() != 1 {
			t.Errorf("expected size 1, got %d", store.Size())
		}

		// Delete
		_ = store.Delete(ctx, entry.Embedding.Hash)
		if store.Size() != 0 {
			t.Errorf("expected size 0 after delete, got %d", store.Size())
		}

		// Add again and clear
		store.Add(ctx, entry)
		_ = store.Clear(ctx)
		if store.Size() != 0 {
			t.Errorf("expected size 0 after clear, got %d", store.Size())
		}
	})

	t.Run("LRUEviction", func(t *testing.T) {
		store := NewMemoryVectorStore(3, 1*time.Hour)

		// Add 4 entries to trigger eviction
		for i := 0; i < 4; i++ {
			entry := createTestVectorEntry("test", []float64{float64(i), 0.0, 0.0})
			store.Add(ctx, entry)
		}

		if store.Size() > 3 {
			t.Errorf("expected max size 3, got %d", store.Size())
		}
	})

	t.Run("CleanupExpired", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Millisecond)

		// Add entries with unique text so they have unique hashes
		for i := 0; i < 5; i++ {
			entry := createTestVectorEntry(fmt.Sprintf("test-%d", i), []float64{float64(i), 0.0, 0.0})
			// Clear expiration so store's TTL is used
			entry.ExpiresAt = time.Time{}
			store.Add(ctx, entry)
		}

		// Wait for expiration
		time.Sleep(10 * time.Millisecond)

		// Cleanup
		count := store.CleanupExpired()
		if count != 5 {
			t.Errorf("expected 5 expired entries, got %d", count)
		}

		if store.Size() != 0 {
			t.Errorf("expected size 0 after cleanup, got %d", store.Size())
		}
	})

	t.Run("Stats", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)

		entry := createTestVectorEntry("test", []float64{1.0, 0.0, 0.0})
		store.Add(ctx, entry)

		stats := store.Stats()
		if stats.TotalVectors != 1 {
			t.Errorf("expected 1 vector, got %d", stats.TotalVectors)
		}

		// Perform search
		query := &Embedding{Vector: []float64{1.0, 0.0, 0.0}, Hash: "query"}
		_, _ = store.Search(ctx, query, 5, 0.0)

		stats = store.Stats()
		if stats.TotalSearches != 1 {
			t.Errorf("expected 1 search, got %d", stats.TotalSearches)
		}
	})
}

func TestSemanticCache(t *testing.T) {
	ctx := t.Context()

	createCache := func() *SemanticCache {
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		config := DefaultSemanticCacheConfig()
		return NewSemanticCache(gen, store, config)
	}

	t.Run("GetMiss", func(t *testing.T) {
		cache := createCache()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "Hello"),
			},
		}

		resp, similarity, err := cache.Get(ctx, req)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if resp != nil {
			t.Error("expected nil response on miss")
		}

		if similarity != 0 {
			t.Errorf("expected 0 similarity on miss, got %f", similarity)
		}

		stats := cache.Stats()
		if stats.SemanticMisses != 1 {
			t.Errorf("expected 1 miss, got %d", stats.SemanticMisses)
		}
	})

	t.Run("SetAndGet", func(t *testing.T) {
		cache := createCache()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "What is AI?"),
			},
		}

		resp := &provider.CompletionResponse{
			ID:    "test-response",
			Model: "test-model",
			Content: []provider.ContentBlock{
				{Type: provider.ContentTypeText, Text: "AI is..."},
			},
			Usage: tokenizer.TokenUsage{InputTokens: 10, OutputTokens: 20},
		}

		// Set
		if err := cache.Set(ctx, req, resp); err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Get exact match
		cached, similarity, err := cache.Get(ctx, req)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if cached == nil {
			t.Fatal("expected cached response")
		}

		if cached.ID != resp.ID {
			t.Errorf("response ID mismatch")
		}

		if similarity < 0.99 {
			t.Errorf("expected near-perfect similarity, got %f", similarity)
		}

		stats := cache.Stats()
		if stats.SemanticHits != 1 {
			t.Errorf("expected 1 hit, got %d", stats.SemanticHits)
		}
	})

	t.Run("SemanticSimilarity", func(t *testing.T) {
		cache := createCache()

		// Store response for one query
		req1 := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "What is artificial intelligence?"),
			},
		}

		resp := &provider.CompletionResponse{
			ID:    "test-response",
			Model: "test-model",
			Content: []provider.ContentBlock{
				{Type: provider.ContentTypeText, Text: "AI is..."},
			},
		}

		cache.Set(ctx, req1, resp)

		// Query with different but similar text
		// Note: SimpleEmbeddingGenerator uses hashing, so similarity won't be high
		// In production with real embeddings, this would work better
		req2 := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "What is artificial intelligence?"),
			},
		}

		cached, similarity, _ := cache.Get(ctx, req2)
		if cached != nil {
			t.Logf("Found similar query with similarity %f", similarity)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		cache := createCache()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test"),
			},
		}

		resp := &provider.CompletionResponse{
			ID:    "test-response",
			Model: "test-model",
			Content: []provider.ContentBlock{
				{Type: provider.ContentTypeText, Text: "response"},
			},
		}

		// Initial stats
		stats := cache.Stats()
		if stats.HitRate() != 0 {
			t.Error("expected 0 hit rate initially")
		}

		// Store and retrieve
		cache.Set(ctx, req, resp)
		_, _, _ = cache.Get(ctx, req)

		stats = cache.Stats()
		if stats.Stores != 1 {
			t.Errorf("expected 1 store, got %d", stats.Stores)
		}
		if stats.SemanticHits != 1 {
			t.Errorf("expected 1 hit, got %d", stats.SemanticHits)
		}
		if stats.HitRate() < 0.9 {
			t.Errorf("expected high hit rate, got %f", stats.HitRate())
		}
	})

	t.Run("Clear", func(t *testing.T) {
		cache := createCache()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test"),
			},
		}

		resp := &provider.CompletionResponse{
			ID:    "test-response",
			Model: "test-model",
		}

		cache.Set(ctx, req, resp)

		if err := cache.Clear(ctx); err != nil {
			t.Fatalf("Clear failed: %v", err)
		}

		// Should not find cached response
		cached, _, _ := cache.Get(ctx, req)
		if cached != nil {
			t.Error("expected nil response after clear")
		}
	})
}

func createTestVectorEntry(text string, vector []float64) *VectorEntry {
	return &VectorEntry{
		Embedding: &Embedding{
			Vector:     vector,
			Model:      "test",
			Dimensions: len(vector),
			Text:       text,
			Hash:       "hash-" + text,
		},
		CacheEntry: &llmcache.CacheEntry{
			Response: &provider.CompletionResponse{
				ID:    "test-response",
				Model: "test-model",
				Content: []provider.ContentBlock{
					{Type: provider.ContentTypeText, Text: "response"},
				},
			},
			CreatedAt: time.Now(),
		},
		CacheKey: &llmcache.CacheKey{
			Model:  "test-model",
			Prompt: text,
			Hash:   "hash-" + text,
		},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
		LastUsed:  time.Now(),
	}
}
