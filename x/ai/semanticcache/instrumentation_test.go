package semanticcache

import (
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

func TestInstrumentedSemanticCache(t *testing.T) {
	ctx := t.Context()

	t.Run("InstrumentedGet", func(t *testing.T) {
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		cache := NewSemanticCache(gen, store, DefaultSemanticCacheConfig())
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedSemanticCache(cache, collector)

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test"),
			},
		}

		// Get on empty cache (miss)
		resp, similarity, err := instrumented.Get(ctx, req)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if resp != nil {
			t.Error("expected nil response on miss")
		}

		if similarity != 0 {
			t.Errorf("expected 0 similarity, got %f", similarity)
		}
	})

	t.Run("InstrumentedSet", func(t *testing.T) {
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		cache := NewSemanticCache(gen, store, DefaultSemanticCacheConfig())
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedSemanticCache(cache, collector)

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
			Usage: tokenizer.TokenUsage{InputTokens: 10, OutputTokens: 20},
		}

		err := instrumented.Set(ctx, req, resp)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	})

	t.Run("GetAfterSet", func(t *testing.T) {
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		cache := NewSemanticCache(gen, store, DefaultSemanticCacheConfig())
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedSemanticCache(cache, collector)

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test query"),
			},
		}

		resp := &provider.CompletionResponse{
			ID:    "test-response",
			Model: "test-model",
			Content: []provider.ContentBlock{
				{Type: provider.ContentTypeText, Text: "response"},
			},
			Usage: tokenizer.TokenUsage{InputTokens: 10, OutputTokens: 20},
		}

		// Set
		instrumented.Set(ctx, req, resp)

		// Get
		cached, similarity, err := instrumented.Get(ctx, req)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if cached == nil {
			t.Fatal("expected cached response")
		}

		if similarity < 0.99 {
			t.Errorf("expected high similarity, got %f", similarity)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		cache := NewSemanticCache(gen, store, DefaultSemanticCacheConfig())
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedSemanticCache(cache, collector)

		stats := instrumented.Stats()
		if stats.SemanticHits != 0 {
			t.Error("expected 0 hits initially")
		}
	})

	t.Run("Clear", func(t *testing.T) {
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		cache := NewSemanticCache(gen, store, DefaultSemanticCacheConfig())
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedSemanticCache(cache, collector)

		if err := instrumented.Clear(ctx); err != nil {
			t.Fatalf("Clear failed: %v", err)
		}
	})
}

func TestInstrumentedVectorStore(t *testing.T) {
	ctx := t.Context()

	t.Run("InstrumentedAdd", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedVectorStore(store, collector)

		entry := createTestVectorEntry("test", []float64{1.0, 0.0, 0.0})

		err := instrumented.Add(ctx, entry)
		if err != nil {
			t.Fatalf("Add failed: %v", err)
		}

		if instrumented.Size() != 1 {
			t.Errorf("expected size 1, got %d", instrumented.Size())
		}
	})

	t.Run("InstrumentedSearch", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedVectorStore(store, collector)

		entry := createTestVectorEntry("test", []float64{1.0, 0.0, 0.0})
		instrumented.Add(ctx, entry)

		query := &Embedding{
			Vector: []float64{1.0, 0.0, 0.0},
			Hash:   "query",
		}

		results, err := instrumented.Search(ctx, query, 5, 0.5)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) == 0 {
			t.Error("expected results")
		}
	})

	t.Run("DeleteAndClear", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedVectorStore(store, collector)

		entry := createTestVectorEntry("test", []float64{1.0, 0.0, 0.0})
		instrumented.Add(ctx, entry)

		// Delete
		err := instrumented.Delete(ctx, entry.Embedding.Hash)
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		if instrumented.Size() != 0 {
			t.Errorf("expected size 0 after delete, got %d", instrumented.Size())
		}

		// Add and clear
		instrumented.Add(ctx, entry)
		err = instrumented.Clear(ctx)
		if err != nil {
			t.Fatalf("Clear failed: %v", err)
		}

		if instrumented.Size() != 0 {
			t.Errorf("expected size 0 after clear, got %d", instrumented.Size())
		}
	})

	t.Run("Stats", func(t *testing.T) {
		store := NewMemoryVectorStore(100, 1*time.Hour)
		collector := NoopMetricRecorder()

		instrumented := NewInstrumentedVectorStore(store, collector)

		stats := instrumented.Stats()
		if stats.TotalVectors != 0 {
			t.Error("expected 0 vectors initially")
		}
	})
}

func TestFormatSimilarity(t *testing.T) {
	tests := []struct {
		similarity float64
		expected   string
	}{
		{0.99, "0.95-1.00"},
		{0.95, "0.95-1.00"},
		{0.92, "0.90-0.95"},
		{0.90, "0.90-0.95"},
		{0.87, "0.85-0.90"},
		{0.85, "0.85-0.90"},
		{0.82, "0.80-0.85"},
		{0.80, "0.80-0.85"},
		{0.75, "0.00-0.80"},
		{0.50, "0.00-0.80"},
		{0.00, "0.00-0.80"},
	}

	for _, tt := range tests {
		result := formatSimilarity(tt.similarity)
		if result != tt.expected {
			t.Errorf("formatSimilarity(%f) = %s, expected %s", tt.similarity, result, tt.expected)
		}
	}
}
