package llmcache

import (
	"context"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

func TestMemoryCache_SetGet(t *testing.T) {
	cache := NewMemoryCache(1*time.Hour, 100)

	key := &CacheKey{
		Model:  "claude-3-opus",
		Prompt: "test prompt",
		Hash:   "test-hash",
	}

	entry := &CacheEntry{
		Response: &provider.CompletionResponse{
			ID:    "resp-1",
			Model: "claude-3-opus",
			Content: []provider.ContentBlock{
				{Type: provider.ContentTypeText, Text: "test response"},
			},
		},
		Usage:     tokenizer.TokenUsage{TotalTokens: 100},
		CreatedAt: time.Now(),
	}

	// Set
	err := cache.Set(t.Context(), key, entry)
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Get
	cached, err := cache.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if cached.Response.ID != entry.Response.ID {
		t.Errorf("Response ID = %v, want %v", cached.Response.ID, entry.Response.ID)
	}
}

func TestMemoryCache_Miss(t *testing.T) {
	cache := NewMemoryCache(1*time.Hour, 100)

	key := &CacheKey{
		Hash: "nonexistent",
	}

	_, err := cache.Get(t.Context(), key)
	if err == nil {
		t.Error("Get() should return error for cache miss")
	}
}

func TestMemoryCache_Expiration(t *testing.T) {
	cache := NewMemoryCache(10*time.Millisecond, 100)

	key := &CacheKey{
		Hash: "test-hash",
	}

	entry := &CacheEntry{
		Response:  &provider.CompletionResponse{ID: "resp-1"},
		CreatedAt: time.Now(),
	}

	// Set
	cache.Set(t.Context(), key, entry)

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should be expired
	_, err := cache.Get(t.Context(), key)
	if err == nil {
		t.Error("Get() should return error for expired entry")
	}
}

func TestMemoryCache_Eviction(t *testing.T) {
	cache := NewMemoryCache(1*time.Hour, 2) // Max 2 entries

	// Add 3 entries
	for i := 0; i < 3; i++ {
		key := &CacheKey{
			Hash: string(rune('a' + i)),
		}
		entry := &CacheEntry{
			Response:  &provider.CompletionResponse{ID: string(rune('a' + i))},
			CreatedAt: time.Now(),
		}
		cache.Set(t.Context(), key, entry)
		time.Sleep(1 * time.Millisecond) // Ensure different timestamps
	}

	stats := cache.Stats()
	if stats.Evictions != 1 {
		t.Errorf("Evictions = %v, want 1", stats.Evictions)
	}
}

func TestMemoryCache_Stats(t *testing.T) {
	cache := NewMemoryCache(1*time.Hour, 100)

	key := &CacheKey{
		Hash: "test-hash",
	}

	entry := &CacheEntry{
		Response: &provider.CompletionResponse{ID: "resp-1"},
		Usage:    tokenizer.TokenUsage{TotalTokens: 100},
	}

	// Set
	cache.Set(t.Context(), key, entry)

	// Hit
	_, _ = cache.Get(t.Context(), key)

	// Miss
	missingKey := &CacheKey{Hash: "missing"}
	_, _ = cache.Get(t.Context(), missingKey)

	stats := cache.Stats()

	if stats.Hits != 1 {
		t.Errorf("Hits = %v, want 1", stats.Hits)
	}

	if stats.Misses != 1 {
		t.Errorf("Misses = %v, want 1", stats.Misses)
	}

	if stats.TotalTokens != 100 {
		t.Errorf("TotalTokens = %v, want 100", stats.TotalTokens)
	}

	hitRate := stats.HitRate()
	if hitRate != 0.5 {
		t.Errorf("HitRate = %v, want 0.5", hitRate)
	}
}

func TestMemoryCache_Clear(t *testing.T) {
	cache := NewMemoryCache(1*time.Hour, 100)

	// Add entries
	for i := 0; i < 5; i++ {
		key := &CacheKey{
			Hash: string(rune('a' + i)),
		}
		entry := &CacheEntry{
			Response: &provider.CompletionResponse{ID: string(rune('a' + i))},
		}
		cache.Set(t.Context(), key, entry)
	}

	// Clear
	err := cache.Clear(t.Context())
	if err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	// Check stats
	stats := cache.Stats()
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Error("Stats should be reset after Clear()")
	}

	// Check entries
	key := &CacheKey{Hash: "a"}
	_, err = cache.Get(t.Context(), key)
	if err == nil {
		t.Error("Get() should fail after Clear()")
	}
}

func TestBuildCacheKey(t *testing.T) {
	req := &provider.CompletionRequest{
		Model: "claude-3-opus",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleSystem, "You are a helpful assistant"),
			provider.NewTextMessage(provider.RoleUser, "Hello world"),
		},
		Temperature: 0.7,
		MaxTokens:   100,
	}

	key := BuildCacheKey(req)

	if key.Model != "claude-3-opus" {
		t.Errorf("Model = %v, want claude-3-opus", key.Model)
	}

	if key.System == "" {
		t.Error("System should not be empty")
	}

	if key.Prompt == "" {
		t.Error("Prompt should not be empty")
	}

	if key.Hash == "" {
		t.Error("Hash should not be empty")
	}
}

func TestBuildCacheKey_Consistency(t *testing.T) {
	req1 := &provider.CompletionRequest{
		Model: "claude-3-opus",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "Hello world"),
		},
	}

	req2 := &provider.CompletionRequest{
		Model: "claude-3-opus",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "Hello world"),
		},
	}

	key1 := BuildCacheKey(req1)
	key2 := BuildCacheKey(req2)

	if key1.Hash != key2.Hash {
		t.Error("Same requests should produce same hash")
	}
}

func TestNormalizePrompt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "trim spaces",
			input:    "  hello world  ",
			expected: "hello world",
		},
		{
			name:     "multiple spaces",
			input:    "hello    world",
			expected: "hello world",
		},
		{
			name:     "newlines",
			input:    "hello\n\nworld",
			expected: "hello world",
		},
		{
			name:     "tabs",
			input:    "hello\t\tworld",
			expected: "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizePrompt(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizePrompt() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestCachingProvider(t *testing.T) {
	// Create mock provider
	mockProvider := &mockProvider{
		callCount: 0,
	}

	cache := NewMemoryCache(1*time.Hour, 100)
	cachingProvider := NewCachingProvider(mockProvider, cache)

	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	// First call - cache miss
	resp1, err := cachingProvider.Complete(t.Context(), req)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if mockProvider.callCount != 1 {
		t.Errorf("Provider should be called once, got %d", mockProvider.callCount)
	}

	// Second call - cache hit
	resp2, err := cachingProvider.Complete(t.Context(), req)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if mockProvider.callCount != 1 {
		t.Errorf("Provider should not be called again (cache hit), got %d calls", mockProvider.callCount)
	}

	// Responses should match
	if resp1.ID != resp2.ID {
		t.Error("Cached response should match original")
	}
}

func TestCachingProvider_Name(t *testing.T) {
	mockProvider := &mockProvider{}
	cache := NewMemoryCache(1*time.Hour, 100)
	cachingProvider := NewCachingProvider(mockProvider, cache)

	if cachingProvider.Name() != "mock" {
		t.Errorf("Name() = %v, want mock", cachingProvider.Name())
	}
}

// Mock provider for testing
type mockProvider struct {
	callCount int
}

func (m *mockProvider) Name() string {
	return "mock"
}

func (m *mockProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	m.callCount++
	return &provider.CompletionResponse{
		ID:    "resp-1",
		Model: req.Model,
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: "mock response"},
		},
		Usage: tokenizer.TokenUsage{TotalTokens: 50},
	}, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	return nil, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return []provider.Model{}, nil
}

func (m *mockProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return &provider.Model{ID: modelID}, nil
}

func (m *mockProvider) CountTokens(text string) (int, error) {
	return len(text) / 4, nil
}
