package cachemanager_test

import (
	"context"
	"errors"
	"testing"

	"github.com/birdordev/plumego/x/ai/semanticcache/cachemanager"
	"github.com/birdordev/plumego/x/ai/semanticcache/embedding"
	"github.com/birdordev/plumego/x/ai/semanticcache/vectorstore"
)

// stubProvider is a minimal embedding.Provider for tests.
type stubProvider struct{ dims int }

func (s *stubProvider) Name() string          { return "stub" }
func (s *stubProvider) Model() string         { return "stub-model" }
func (s *stubProvider) CostPerToken() float64 { return 0 }
func (s *stubProvider) MaxBatchSize() int     { return 100 }
func (s *stubProvider) SupportsAsync() bool   { return false }
func (s *stubProvider) Dimensions() int       { return s.dims }

func (s *stubProvider) Generate(ctx context.Context, text string) (*embedding.Embedding, error) {
	vec := make([]float64, s.dims)
	return &embedding.Embedding{Vector: vec, Provider: "stub", Model: "stub-model"}, nil
}

func (s *stubProvider) GenerateBatch(ctx context.Context, texts []string) ([]*embedding.Embedding, error) {
	out := make([]*embedding.Embedding, len(texts))
	for i := range texts {
		vec := make([]float64, s.dims)
		out[i] = &embedding.Embedding{Vector: vec, Provider: "stub", Model: "stub-model"}
	}
	return out, nil
}

func newTestManager() (*cachemanager.Manager, vectorstore.Backend) {
	backend := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{Dimensions: 4})
	provider := &stubProvider{dims: 4}
	mgr := cachemanager.NewManager(backend, provider)
	return mgr, backend
}

func TestNewManager_NotNil(t *testing.T) {
	mgr, _ := newTestManager()
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}
}

func TestStats_EmptyBackend(t *testing.T) {
	mgr, _ := newTestManager()
	stats, err := mgr.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats error: %v", err)
	}
	if stats == nil {
		t.Fatal("Stats returned nil")
	}
	if stats.TotalEntries != 0 {
		t.Errorf("expected 0 entries, got %d", stats.TotalEntries)
	}
}

func TestCleanup_AllPolicy(t *testing.T) {
	mgr, backend := newTestManager()
	ctx := context.Background()

	// Seed one entry directly
	_ = backend.AddBatch(ctx, []*vectorstore.Entry{
		{ID: "e1", Vector: []float64{0, 0, 0, 0}, Text: "hello"},
	})

	if err := mgr.Cleanup(ctx, cachemanager.CleanupPolicy{Type: cachemanager.CleanupTypeAll}); err != nil {
		t.Fatalf("Cleanup(all) error: %v", err)
	}

	count, err := backend.Count(ctx)
	if err != nil {
		t.Fatalf("Count error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 entries after CleanupTypeAll, got %d", count)
	}
}

func TestCleanup_UnknownPolicy(t *testing.T) {
	mgr, _ := newTestManager()
	err := mgr.Cleanup(context.Background(), cachemanager.CleanupPolicy{Type: "unknown_policy"})
	if err == nil {
		t.Error("expected error for unknown policy, got nil")
	}
}

func TestMaintenanceUnsupportedContracts(t *testing.T) {
	mgr, _ := newTestManager()
	ctx := context.Background()

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "compact",
			run:  func() error { return mgr.Compact(ctx) },
		},
		{
			name: "cleanup oldest",
			run: func() error {
				return mgr.Cleanup(ctx, cachemanager.CleanupPolicy{Type: cachemanager.CleanupTypeOldest})
			},
		},
		{
			name: "cleanup least used",
			run: func() error {
				return mgr.Cleanup(ctx, cachemanager.CleanupPolicy{Type: cachemanager.CleanupTypeLeastUsed})
			},
		},
		{
			name: "cleanup expired",
			run: func() error {
				return mgr.Cleanup(ctx, cachemanager.CleanupPolicy{Type: cachemanager.CleanupTypeExpired})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if !errors.Is(err, cachemanager.ErrUnsupportedMaintenance) {
				t.Fatalf("error=%v, want ErrUnsupportedMaintenance", err)
			}
		})
	}
}

func TestNewWarmer_NotNil(t *testing.T) {
	backend := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{Dimensions: 4})
	provider := &stubProvider{dims: 4}
	w := cachemanager.NewWarmer(backend, provider)
	if w == nil {
		t.Fatal("NewWarmer returned nil")
	}
}

func TestWarmer_WarmFromQueries_Empty(t *testing.T) {
	backend := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{Dimensions: 4})
	provider := &stubProvider{dims: 4}
	w := cachemanager.NewWarmer(backend, provider)

	if err := w.WarmFromQueries(context.Background(), nil); err != nil {
		t.Fatalf("WarmFromQueries(nil) should be no-op, got error: %v", err)
	}
}

func TestWarmer_WarmFromQueries_AddsEntries(t *testing.T) {
	backend := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{Dimensions: 4})
	provider := &stubProvider{dims: 4}
	w := cachemanager.NewWarmer(backend, provider)
	ctx := context.Background()

	queries := []cachemanager.QueryResponse{
		{Query: "what is Go?", Response: "A systems language."},
		{Query: "what is HTTP?", Response: "Hypertext Transfer Protocol."},
	}

	if err := w.WarmFromQueries(ctx, queries); err != nil {
		t.Fatalf("WarmFromQueries error: %v", err)
	}

	count, err := backend.Count(ctx)
	if err != nil {
		t.Fatalf("Count error: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 warmed entries, got %d", count)
	}
}

func TestWarmer_WarmFromFileUnsupportedContract(t *testing.T) {
	backend := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{Dimensions: 4})
	provider := &stubProvider{dims: 4}
	w := cachemanager.NewWarmer(backend, provider)

	err := w.WarmFromFile(context.Background(), "queries.json")
	if !errors.Is(err, cachemanager.ErrUnsupportedMaintenance) {
		t.Fatalf("error=%v, want ErrUnsupportedMaintenance", err)
	}
}
