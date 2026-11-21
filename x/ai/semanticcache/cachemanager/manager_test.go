package cachemanager_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

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

func TestExportUnsupportedLeavesDestinationUntouched(t *testing.T) {
	manager, backend := newTestManager()
	ctx := t.Context()
	if err := backend.Add(ctx, &vectorstore.Entry{ID: "sample", Vector: []float64{1, 0, 0, 0}}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "cache.json")
	if err := manager.Export(ctx, path); !errors.Is(err, cachemanager.ErrUnsupportedMaintenance) {
		t.Fatalf("Export = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("export created the destination: %v", err)
	}

	const existing = "existing file"
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Export(ctx, path); !errors.Is(err, cachemanager.ErrUnsupportedMaintenance) {
		t.Fatalf("Export over existing file = %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != existing {
		t.Fatalf("destination = %q, %v", data, err)
	}
}

func TestImportStillRestoresExistingFileWithEntries(t *testing.T) {
	manager, backend := newTestManager()
	path := filepath.Join(t.TempDir(), "cache.json")
	data := []byte(`{"version":"1.0","dimensions":4,"total_entries":1,"entries":[{"id":"sample","vector":[1,0,0,0]}]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Import(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if got, err := backend.Get(t.Context(), "sample"); err != nil || got.ID != "sample" || len(got.Vector) != 4 {
		t.Fatalf("imported entry = %+v, %v", got, err)
	}
}

type warmingBatchFixture struct {
	embedding.Provider
	batchSize int
	generate  func(context.Context, []string) ([]*embedding.Embedding, error)
}

func (p warmingBatchFixture) MaxBatchSize() int { return p.batchSize }
func (p warmingBatchFixture) GenerateBatch(ctx context.Context, texts []string) ([]*embedding.Embedding, error) {
	return p.generate(ctx, texts)
}

func TestWarmerRejectsInvalidEmbeddingBatches(t *testing.T) {
	ctx := t.Context()
	valid := &embedding.Embedding{Vector: []float64{1, 0}}
	sentinel := errors.New("embedding failed")
	for _, operation := range []struct {
		name string
		warm func(*cachemanager.Warmer) error
	}{
		{"queries", func(w *cachemanager.Warmer) error {
			return w.WarmFromQueries(ctx, []cachemanager.QueryResponse{{Query: "one"}})
		}},
		{"documents", func(w *cachemanager.Warmer) error {
			return w.WarmFromDocuments(ctx, []cachemanager.Document{{Content: "one"}})
		}},
	} {
		for _, tc := range []struct {
			name    string
			results []*embedding.Embedding
			err     error
		}{
			{name: "short"},
			{name: "long", results: []*embedding.Embedding{valid, valid}},
			{name: "nil result", results: []*embedding.Embedding{nil}},
			{name: "provider error", results: []*embedding.Embedding{valid}, err: sentinel},
		} {
			t.Run(operation.name+"/"+tc.name, func(t *testing.T) {
				store := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{})
				w := cachemanager.NewWarmer(store, warmingBatchFixture{batchSize: 1, generate: func(context.Context, []string) ([]*embedding.Embedding, error) { return tc.results, tc.err }})
				if err := operation.warm(w); err == nil || (tc.err != nil && !errors.Is(err, tc.err)) {
					t.Errorf("warming error=%v", err)
				}
				if n, err := store.Count(ctx); err != nil || n != 0 {
					t.Errorf("count=%d, %v", n, err)
				}
			})
		}
	}
	store := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{})
	var batches [][]string
	w := cachemanager.NewWarmer(store, warmingBatchFixture{batchSize: 2, generate: func(_ context.Context, texts []string) ([]*embedding.Embedding, error) {
		batches = append(batches, slices.Clone(texts))
		if len(batches) == 2 {
			return nil, sentinel
		}
		return []*embedding.Embedding{{Vector: []float64{1, 0}}, {Vector: []float64{0, 1}}}, nil
	}})
	if err := w.WarmFromDocuments(ctx, []cachemanager.Document{{Content: "first"}, {Content: "second"}, {Content: "failed"}}); !errors.Is(err, sentinel) {
		t.Errorf("later batch error=%v", err)
	}
	if len(batches) != 2 || !slices.Equal(batches[0], []string{"first", "second"}) || !slices.Equal(batches[1], []string{"failed"}) {
		t.Errorf("batches=%v", batches)
	}
	if count, err := store.Count(ctx); err != nil || count != 2 {
		t.Errorf("committed count=%d, %v", count, err)
	}
	for i, query := range [][]float64{{1, 0}, {0, 1}} {
		got, err := store.Search(ctx, query, 1, 0.9)
		want := []string{"first", "second"}[i]
		if err != nil || len(got) != 1 || got[0].Entry.Text != want {
			t.Errorf("search %q=%v, %v", want, got, err)
		}
	}
}

func TestWarmerSelectsQueriesByStrategyMetadata(t *testing.T) {
	early := cachemanager.QueryResponse{Query: "early", AccessCount: 9, Timestamp: time.Unix(1, 0)}
	late := cachemanager.QueryResponse{Query: "late", AccessCount: 1, Timestamp: time.Unix(2, 0)}
	for _, tc := range []struct {
		name     string
		strategy cachemanager.StrategyType
		limit    int
		input    []cachemanager.QueryResponse
		want     []string
	}{
		{"frequent", cachemanager.StrategyMostFrequent, 1, []cachemanager.QueryResponse{late, early}, []string{"early"}},
		{"recent", cachemanager.StrategyRecent, 1, []cachemanager.QueryResponse{late, early}, []string{"late"}},
		{"frequent tie", cachemanager.StrategyMostFrequent, 1, []cachemanager.QueryResponse{{Query: "first", AccessCount: 5}, {Query: "second", AccessCount: 5}}, []string{"first"}},
		{"recent tie", cachemanager.StrategyRecent, 1, []cachemanager.QueryResponse{{Query: "first", Timestamp: early.Timestamp}, {Query: "second", Timestamp: early.Timestamp}}, []string{"second"}},
		{"absent metadata", cachemanager.StrategyRecent, 1, []cachemanager.QueryResponse{{Query: "first"}, {Query: "second"}}, []string{"second"}},
		{"unlimited", cachemanager.StrategyMostFrequent, 0, []cachemanager.QueryResponse{late, early}, []string{"late", "early"}},
		{"all", cachemanager.StrategyAll, 1, []cachemanager.QueryResponse{late, early}, []string{"late", "early"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.input)
			var texts []string
			store := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{})
			w := cachemanager.NewWarmer(store, warmingBatchFixture{generate: func(_ context.Context, inputs []string) ([]*embedding.Embedding, error) {
				texts = slices.Clone(inputs)
				result := make([]*embedding.Embedding, len(inputs))
				for i := range result {
					result[i] = &embedding.Embedding{Vector: []float64{1, 0}}
				}
				return result, nil
			}})
			if err := w.WarmWithStrategy(t.Context(), cachemanager.WarmingStrategy{Type: tc.strategy, Queries: tc.input, Limit: tc.limit}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(original, tc.input) || !slices.Equal(texts, tc.want) {
				t.Errorf("input=%v, selected=%v, want=%v", tc.input, texts, tc.want)
			}
		})
	}
}

func TestWarmWithProgressReportsBatchFailures(t *testing.T) {
	offline := errors.New("embedding unavailable")
	busy := errors.New("embedding busy")
	stop := errors.New("stop warming")
	for _, tc := range []struct {
		name       string
		stopEarly  bool
		wantErrors []error
		wantStored []string
		wantCalls  int
		wantSteps  []int
	}{
		{"continue after failure", false, []error{offline, busy}, []string{"first", "last"}, 4, []int{1, 4}},
		{"stop after cancellation", true, []error{stop}, []string{"first"}, 1, []int{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			backend := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{})
			calls := 0
			provider := warmingBatchFixture{batchSize: 1, generate: func(_ context.Context, texts []string) ([]*embedding.Embedding, error) {
				calls++
				switch texts[0] {
				case "offline":
					return nil, offline
				case "busy":
					return nil, busy
				default:
					return []*embedding.Embedding{{Vector: []float64{1, 0}}}, nil
				}
			}}
			queries := []cachemanager.QueryResponse{{Query: "first"}, {Query: "offline"}, {Query: "busy"}, {Query: "last"}}
			var steps []int
			err := cachemanager.NewWarmer(backend, provider).WarmWithProgress(ctx, queries,
				func(current, total int, stats *cachemanager.WarmingStats) {
					steps = append(steps, current)
					if total != len(queries) || stats.QueriesWarmed != len(steps) {
						t.Errorf("unexpected progress: current=%d total=%d stats=%+v", current, total, stats)
					}
					if tc.stopEarly {
						cancel(stop)
					}
				})
			for _, want := range tc.wantErrors {
				if !errors.Is(err, want) {
					t.Errorf("WarmWithProgress error = %v; missing %v", err, want)
				}
			}
			if calls != tc.wantCalls || !slices.Equal(steps, tc.wantSteps) {
				t.Errorf("calls=%d progress=%v; want %d, %v", calls, steps, tc.wantCalls, tc.wantSteps)
			}
			results, err := backend.Search(t.Context(), []float64{1, 0}, 4, 0)
			if err != nil {
				t.Fatal(err)
			}
			var stored []string
			for _, result := range results {
				stored = append(stored, result.Entry.Text)
			}
			slices.Sort(stored)
			if !slices.Equal(stored, tc.wantStored) {
				t.Errorf("stored = %v; want %v", stored, tc.wantStored)
			}
		})
	}
}

func TestWarmerNormalizesProviderBatchSize(t *testing.T) {
	const entries = 101
	docs := make([]cachemanager.Document, entries)
	queries := make([]cachemanager.QueryResponse, entries)
	for i := range entries {
		text := fmt.Sprintf("entry-%d", i)
		docs[i] = cachemanager.Document{ID: text, Content: text}
		queries[i] = cachemanager.QueryResponse{Query: text}
	}
	for _, operation := range []struct {
		name string
		run  func(context.Context, *cachemanager.Warmer) error
	}{
		{"documents", func(ctx context.Context, w *cachemanager.Warmer) error { return w.WarmFromDocuments(ctx, docs) }},
		{"progress", func(ctx context.Context, w *cachemanager.Warmer) error { return w.WarmWithProgress(ctx, queries, nil) }},
	} {
		for _, tc := range []struct {
			size int
			want []int
		}{
			{-1, []int{100, 1}},
			{0, []int{100, 1}},
			{50, []int{50, 50, 1}},
		} {
			t.Run(fmt.Sprintf("%s/%d", operation.name, tc.size), func(t *testing.T) {
				ctx := t.Context()
				store := vectorstore.NewMemoryBackend(vectorstore.MemoryConfig{})
				var batches []int
				provider := warmingBatchFixture{batchSize: tc.size, generate: func(_ context.Context, texts []string) ([]*embedding.Embedding, error) {
					batches = append(batches, len(texts))
					result := make([]*embedding.Embedding, len(texts))
					for i := range result {
						result[i] = &embedding.Embedding{Vector: []float64{1, 0}}
					}
					return result, nil
				}}
				if err := operation.run(ctx, cachemanager.NewWarmer(store, provider)); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(batches, tc.want) {
					t.Errorf("batches = %v; want %v", batches, tc.want)
				}
				if count, err := store.Count(ctx); err != nil || count != entries {
					t.Errorf("stored %d entries, error = %v; want %d", count, err, entries)
				}
			})
		}
	}
}
