package semanticcache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/llmcache"
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

// MockProvider implements provider.Provider for testing
type MockProvider struct {
	callCount    atomic.Int64
	shouldFail   bool
	responseText string
}

func (m *MockProvider) Name() string {
	return "mock"
}

func (m *MockProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	m.callCount.Add(1)

	if m.shouldFail {
		return nil, errors.New("mock provider error")
	}

	return &provider.CompletionResponse{
		ID:    "mock-response",
		Model: req.Model,
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: m.responseText},
		},
		Usage: tokenizer.TokenUsage{InputTokens: 10, OutputTokens: 20},
	}, nil
}

func (m *MockProvider) CallCount() int64 {
	return m.callCount.Load()
}

func (m *MockProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	return nil, errors.New("not implemented")
}

func (m *MockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return nil, nil
}

func (m *MockProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return nil, nil
}

func (m *MockProvider) CountTokens(text string) (int, error) {
	return len(text), nil
}

func newTestSemanticCache() *SemanticCache {
	gen := NewSimpleEmbeddingGenerator(128)
	store := NewMemoryVectorStore(100, time.Hour)
	return NewSemanticCache(gen, store, DefaultSemanticCacheConfig())
}

func TestNewSemanticCachingProvider(t *testing.T) {
	mockProvider := &MockProvider{responseText: "Test response"}
	cache := newTestSemanticCache()

	scp, err := NewSemanticCachingProvider(mockProvider, cache)
	if err != nil {
		t.Fatalf("NewSemanticCachingProvider error: %v", err)
	}
	if scp == nil {
		t.Fatal("expected provider")
	}
	if scp.Name() != "mock" {
		t.Fatalf("Name = %q, want mock", scp.Name())
	}

	if _, err := NewSemanticCachingProvider(nil, cache); !errors.Is(err, ErrProviderRequired) {
		t.Fatalf("nil provider error = %v, want ErrProviderRequired", err)
	}
	if _, err := NewSemanticCachingProvider(mockProvider, nil); !errors.Is(err, ErrSemanticCacheRequired) {
		t.Fatalf("nil cache error = %v, want ErrSemanticCacheRequired", err)
	}

	scp, err = NewSemanticCachingProvider(mockProvider, cache, WithProviderConfig(nil))
	if err != nil {
		t.Fatalf("nil config option should use defaults: %v", err)
	}
	if scp.config == nil {
		t.Fatal("config is nil")
	}
}

func TestNewSemanticCachingProvider_NilInputs(t *testing.T) {
	cache := newTestSemanticCache()

	if _, err := NewSemanticCachingProvider(nil, cache); !errors.Is(err, ErrProviderRequired) {
		t.Fatalf("expected ErrProviderRequired, got %v", err)
	}
	if _, err := NewSemanticCachingProvider(&MockProvider{}, nil); !errors.Is(err, ErrSemanticCacheRequired) {
		t.Fatalf("expected ErrSemanticCacheRequired, got %v", err)
	}
}

func TestSemanticCachingProvider(t *testing.T) {
	ctx := t.Context()

	createProvider := func() (*SemanticCachingProvider, *MockProvider, *SemanticCache) {
		mockProvider := &MockProvider{responseText: "Test response"}
		cache := newTestSemanticCache()

		scp, err := NewSemanticCachingProvider(mockProvider, cache)
		if err != nil {
			t.Fatal(err)
		}
		return scp, mockProvider, cache
	}

	t.Run("CacheMiss", func(t *testing.T) {
		scp, mock, _ := createProvider()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "Hello"),
			},
		}

		resp, err := scp.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		if resp == nil {
			t.Fatal("expected response")
		}

		if mock.CallCount() != 1 {
			t.Errorf("expected 1 provider call, got %d", mock.CallCount())
		}
	})

	t.Run("CacheHit", func(t *testing.T) {
		scp, mock, _ := createProvider()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "What is AI?"),
			},
		}

		// First call - cache miss
		resp1, err := scp.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		if mock.CallCount() != 1 {
			t.Errorf("expected 1 provider call, got %d", mock.CallCount())
		}

		// Second call - should hit cache
		resp2, err := scp.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		if mock.CallCount() != 1 {
			t.Errorf("expected still 1 provider call (cached), got %d", mock.CallCount())
		}

		if resp2.ID != resp1.ID {
			t.Error("cached response should be identical")
		}
	})

	t.Run("WithExactCache", func(t *testing.T) {
		mockProvider := &MockProvider{responseText: "Test response"}
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		semanticCache := NewSemanticCache(gen, store, DefaultSemanticCacheConfig())
		exactCache := llmcache.NewMemoryCache(1*time.Hour, 100)

		scp, err := NewSemanticCachingProvider(
			mockProvider,
			semanticCache,
			WithExactCache(exactCache),
		)
		if err != nil {
			t.Fatal(err)
		}

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test query"),
			},
		}

		// First call
		_, err = scp.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		if mockProvider.CallCount() != 1 {
			t.Errorf("expected 1 provider call, got %d", mockProvider.CallCount())
		}

		// Second call - should hit exact cache
		_, err = scp.Complete(ctx, req)
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}

		// Should still be 1 call (exact cache hit)
		if mockProvider.CallCount() != 1 {
			t.Errorf("expected still 1 provider call, got %d", mockProvider.CallCount())
		}
	})

	t.Run("ProviderError", func(t *testing.T) {
		mockProvider := &MockProvider{shouldFail: true}
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		cache := NewSemanticCache(gen, store, DefaultSemanticCacheConfig())

		scp, err := NewSemanticCachingProvider(mockProvider, cache)
		if err != nil {
			t.Fatal(err)
		}

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test"),
			},
		}

		_, err = scp.Complete(ctx, req)
		if err == nil {
			t.Error("expected error from provider")
		}
	})

	t.Run("Name", func(t *testing.T) {
		scp, _, _ := createProvider()

		if scp.Name() != "mock" {
			t.Errorf("expected name 'mock', got %s", scp.Name())
		}
	})

	t.Run("CountTokens", func(t *testing.T) {
		scp, _, _ := createProvider()

		count, err := scp.CountTokens("hello world")
		if err != nil {
			t.Fatalf("CountTokens failed: %v", err)
		}

		if count != 11 {
			t.Errorf("expected 11 tokens, got %d", count)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		scp, _, _ := createProvider()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test"),
			},
		}

		// Make a call
		_, _ = scp.Complete(ctx, req)

		stats := scp.Stats()
		if stats.Stores != 1 {
			t.Errorf("expected 1 store, got %d", stats.Stores)
		}
	})

	t.Run("ClearCache", func(t *testing.T) {
		scp, mock, _ := createProvider()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test"),
			},
		}

		// First call
		_, _ = scp.Complete(ctx, req)

		// Clear cache
		if err := scp.ClearCache(ctx); err != nil {
			t.Fatalf("ClearCache failed: %v", err)
		}

		// Second call should hit provider again
		_, _ = scp.Complete(ctx, req)

		if mock.CallCount() != 2 {
			t.Errorf("expected 2 provider calls after clear, got %d", mock.CallCount())
		}
	})

	t.Run("CompleteStream", func(t *testing.T) {
		scp, _, _ := createProvider()

		req := &provider.CompletionRequest{
			Model: "test-model",
			Messages: []provider.Message{
				provider.NewTextMessage(provider.RoleUser, "test"),
			},
		}

		// Stream should not use cache
		_, err := scp.CompleteStream(ctx, req)
		if err == nil {
			t.Error("expected error (not implemented)")
		}
	})

	t.Run("ListModels", func(t *testing.T) {
		scp, _, _ := createProvider()

		models, err := scp.ListModels(ctx)
		if err != nil {
			t.Fatalf("ListModels failed: %v", err)
		}

		if models != nil {
			t.Log("Models:", models)
		}
	})

	t.Run("GetModel", func(t *testing.T) {
		scp, _, _ := createProvider()

		model, err := scp.GetModel(ctx, "test-model")
		if err != nil {
			t.Fatalf("GetModel failed: %v", err)
		}

		if model != nil {
			t.Log("Model:", model)
		}
	})
}

func TestProviderConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := DefaultProviderConfig()

		if !config.EnableExactCache {
			t.Error("expected EnableExactCache to be true")
		}

		if config.MinSimilarity != 0.85 {
			t.Errorf("expected MinSimilarity 0.85, got %f", config.MinSimilarity)
		}

		if !config.EnablePassthrough {
			t.Error("expected EnablePassthrough to be true")
		}
	})

	t.Run("WithOptions", func(t *testing.T) {
		mockProvider := &MockProvider{}
		gen := NewSimpleEmbeddingGenerator(128)
		store := NewMemoryVectorStore(100, 1*time.Hour)
		cache := NewSemanticCache(gen, store, DefaultSemanticCacheConfig())

		exactCache := llmcache.NewMemoryCache(1*time.Hour, 100)
		customConfig := &ProviderConfig{
			EnableExactCache:  false,
			MinSimilarity:     0.9,
			EnablePassthrough: false,
		}

		scp, err := NewSemanticCachingProvider(
			mockProvider,
			cache,
			WithExactCache(exactCache),
			WithProviderConfig(customConfig),
		)
		if err != nil {
			t.Fatal(err)
		}

		if scp.exactCache == nil {
			t.Error("expected exact cache to be set")
		}

		if scp.config.MinSimilarity != 0.9 {
			t.Errorf("expected MinSimilarity 0.9, got %f", scp.config.MinSimilarity)
		}
	})
}

func TestSemanticCacheConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := DefaultSemanticCacheConfig()

		if config.SimilarityThreshold != 0.85 {
			t.Errorf("expected SimilarityThreshold 0.85, got %f", config.SimilarityThreshold)
		}

		if config.TopK != 5 {
			t.Errorf("expected TopK 5, got %d", config.TopK)
		}

		if !config.EnableExactMatch {
			t.Error("expected EnableExactMatch to be true")
		}

		if config.TTL != 1*time.Hour {
			t.Errorf("expected TTL 1 hour, got %v", config.TTL)
		}

		if config.MaxSize != 1000 {
			t.Errorf("expected MaxSize 1000, got %d", config.MaxSize)
		}
	})
}
