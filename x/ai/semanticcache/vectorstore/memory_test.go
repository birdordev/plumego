package vectorstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestMemoryBackend_AddAndGet(t *testing.T) {
	ctx := t.Context()
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 128,
		MaxSize:    100,
	})

	// Create test entry
	entry := &Entry{
		ID:        "test-1",
		Vector:    make([]float64, 128),
		Text:      "test query",
		Response:  "test response",
		CreatedAt: time.Now(),
	}

	// Initialize vector with some values
	for i := range entry.Vector {
		entry.Vector[i] = float64(i) / 128.0
	}

	// Test Add
	err := backend.Add(ctx, entry)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Test Get
	retrieved, err := backend.Get(ctx, "test-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if retrieved.ID != entry.ID {
		t.Errorf("ID mismatch: got %s, want %s", retrieved.ID, entry.ID)
	}
	if retrieved.Text != entry.Text {
		t.Errorf("Text mismatch: got %s, want %s", retrieved.Text, entry.Text)
	}
}

func TestMemoryBackend_Search(t *testing.T) {
	ctx := t.Context()
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 3,
	})

	// Add test entries
	entries := []*Entry{
		{
			ID:     "entry-1",
			Vector: []float64{1.0, 0.0, 0.0},
			Text:   "first entry",
		},
		{
			ID:     "entry-2",
			Vector: []float64{0.9, 0.1, 0.0},
			Text:   "second entry",
		},
		{
			ID:     "entry-3",
			Vector: []float64{0.0, 1.0, 0.0},
			Text:   "third entry",
		},
	}

	for _, entry := range entries {
		if err := backend.Add(ctx, entry); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Search with similar query
	query := []float64{0.95, 0.05, 0.0}
	results, err := backend.Search(ctx, query, 2, 0.5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	// First result should be most similar
	if results[0].Entry.ID != "entry-1" {
		t.Errorf("Expected first result to be entry-1, got %s", results[0].Entry.ID)
	}
}

func TestMemoryBackend_Delete(t *testing.T) {
	ctx := t.Context()
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 128,
	})

	entry := &Entry{
		ID:     "delete-test",
		Vector: make([]float64, 128),
		Text:   "to be deleted",
	}

	// Add entry
	if err := backend.Add(ctx, entry); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Delete entry
	if err := backend.Delete(ctx, "delete-test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deletion
	_, err := backend.Get(ctx, "delete-test")
	if err == nil {
		t.Error("Expected error when getting deleted entry")
	}
}

func TestMemoryBackend_Count(t *testing.T) {
	ctx := t.Context()
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 128,
	})

	// Initially empty
	count, err := backend.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected count 0, got %d", count)
	}

	// Add entries
	for i := 0; i < 5; i++ {
		entry := &Entry{
			ID:     fmt.Sprintf("entry-%d", i),
			Vector: make([]float64, 128),
		}
		backend.Add(ctx, entry)
	}

	count, err = backend.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 5 {
		t.Errorf("Expected count 5, got %d", count)
	}
}

func TestMemoryBackend_Clear(t *testing.T) {
	ctx := t.Context()
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 128,
	})

	// Add entries
	for i := 0; i < 3; i++ {
		entry := &Entry{
			ID:     fmt.Sprintf("entry-%d", i),
			Vector: make([]float64, 128),
		}
		backend.Add(ctx, entry)
	}

	// Clear
	if err := backend.Clear(ctx); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Verify empty
	count, _ := backend.Count(ctx)
	if count != 0 {
		t.Errorf("Expected count 0 after clear, got %d", count)
	}
}

func TestMemoryBackend_MaxSize(t *testing.T) {
	ctx := t.Context()
	maxSize := 3
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 128,
		MaxSize:    maxSize,
	})

	// Add more entries than max size
	for i := 0; i < 5; i++ {
		entry := &Entry{
			ID:     fmt.Sprintf("entry-%d", i),
			Vector: make([]float64, 128),
			Text:   fmt.Sprintf("text %d", i),
		}
		entry.AccessedAt = time.Now().Add(time.Duration(i) * time.Second)
		if err := backend.Add(ctx, entry); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Count should not exceed max size
	count, _ := backend.Count(ctx)
	if count > int64(maxSize) {
		t.Errorf("Count %d exceeds max size %d", count, maxSize)
	}
}

func TestMemoryBackend_Stats(t *testing.T) {
	ctx := t.Context()
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 128,
	})

	// Add some entries
	for i := 0; i < 3; i++ {
		entry := &Entry{
			ID:     fmt.Sprintf("entry-%d", i),
			Vector: make([]float64, 128),
			Text:   "test text",
		}
		backend.Add(ctx, entry)
	}

	stats, err := backend.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.Name != "memory" {
		t.Errorf("Expected backend name 'memory', got %s", stats.Name)
	}

	if stats.TotalEntries != 3 {
		t.Errorf("Expected 3 entries, got %d", stats.TotalEntries)
	}

	if stats.Dimensions != 128 {
		t.Errorf("Expected dimensions 128, got %d", stats.Dimensions)
	}

	if stats.MemoryUsage == 0 {
		t.Error("Expected non-zero memory usage")
	}
}

func BenchmarkMemoryBackend_Add(b *testing.B) {
	ctx := b.Context()
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 1536,
	})

	entry := &Entry{
		ID:     "bench-entry",
		Vector: make([]float64, 1536),
		Text:   "benchmark text",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry.ID = fmt.Sprintf("entry-%d", i)
		backend.Add(ctx, entry)
	}
}

func BenchmarkMemoryBackend_Search(b *testing.B) {
	ctx := b.Context()
	backend := NewMemoryBackend(MemoryConfig{
		Dimensions: 1536,
	})

	// Populate with 1000 entries
	for i := 0; i < 1000; i++ {
		entry := &Entry{
			ID:     fmt.Sprintf("entry-%d", i),
			Vector: make([]float64, 1536),
		}
		for j := range entry.Vector {
			entry.Vector[j] = float64(j+i) / 1536.0
		}
		backend.Add(ctx, entry)
	}

	query := make([]float64, 1536)
	for i := range query {
		query[i] = float64(i) / 1536.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = backend.Search(ctx, query, 10, 0.8)
	}
}

func TestMemoryBackendReplacementKeepsOtherEntries(t *testing.T) {
	for _, id := range []string{"old", "new"} {
		t.Run(id, func(t *testing.T) {
			ctx := t.Context()
			m := NewMemoryBackend(MemoryConfig{Dimensions: 1, MaxSize: 2})
			base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			for _, e := range []*Entry{{ID: "old", Vector: []float64{1}, CreatedAt: base}, {ID: "new", Vector: []float64{1}, CreatedAt: base.Add(time.Hour)}, {ID: id, Vector: []float64{2}}} {
				if err := m.Add(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			if n, _ := m.Count(ctx); n != 2 {
				t.Fatalf("count after replacement = %d", n)
			}
			got, err := m.Get(ctx, id)
			if err != nil || got.Vector[0] != 2 {
				t.Fatalf("replacement = %v, %v", got, err)
			}
			if err := m.Add(ctx, &Entry{ID: "invalid", Vector: []float64{1, 2}}); err == nil {
				t.Fatal("accepted invalid dimensions")
			}
			if n, _ := m.Count(ctx); n != 2 {
				t.Fatalf("count after rejected entry = %d", n)
			}
			if err := m.Add(ctx, &Entry{ID: "fresh", Vector: []float64{1}}); err != nil {
				t.Fatal(err)
			}
			if n, _ := m.Count(ctx); n != 2 {
				t.Fatalf("count after insertion = %d", n)
			}
		})
	}
}

func TestMemoryBackendOwnsEntrySnapshots(t *testing.T) {
	ctx := t.Context()
	backend := NewMemoryBackend(MemoryConfig{})
	read := func(id string) *Entry {
		t.Helper()
		entry, err := backend.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}

	for _, entry := range []*Entry{
		{ID: "nil"},
		{ID: "empty", Vector: []float64{}, Metadata: map[string]any{}},
	} {
		if err := backend.Add(ctx, entry); err != nil {
			t.Fatal(err)
		}
		got := read(entry.ID)
		if (got.Vector == nil) != (entry.Vector == nil) || (got.Metadata == nil) != (entry.Metadata == nil) ||
			len(got.Vector) != 0 || len(got.Metadata) != 0 {
			t.Errorf("container shape changed: %+v", got)
		}
	}

	input := &Entry{ID: "sample", Text: "original", Vector: []float64{1, 2}, Metadata: map[string]any{"label": "original"}}
	if err := backend.Add(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Text = "after add"
	input.Vector[0] = 7
	input.Metadata["label"] = "after add"
	first := read("sample")
	if first.Text != "original" || first.Vector[0] != 1 || first.Metadata["label"] != "original" ||
		first.AccessCount != 1 || first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() || first.AccessedAt.IsZero() ||
		!input.CreatedAt.IsZero() || !input.UpdatedAt.IsZero() {
		t.Fatalf("stored snapshot = %+v, input = %+v", first, input)
	}

	first.Vector[0] = 8
	first.Metadata["label"] = "after get"
	results, err := backend.Search(ctx, []float64{1, 2}, 1, 0.9)
	if err != nil || len(results) != 1 {
		t.Fatalf("Search = %v, %v", results, err)
	}
	match := results[0].Entry
	if match.Vector[0] != 1 || match.Metadata["label"] != "original" {
		t.Fatalf("Search exposed mutated Get result: %+v", match)
	}
	match.Vector[0] = 9
	match.Metadata["label"] = "after search"
	got := read("sample")
	if got.Vector[0] != 1 || got.Metadata["label"] != "original" || got.AccessCount != 2 {
		t.Fatalf("Get exposed mutated Search result: %+v", got)
	}

	input.UpdatedAt = time.Time{}
	if err := backend.Update(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Vector[0] = 10
	input.Metadata["label"] = "after update"
	got = read("sample")
	if got.Vector[0] != 7 || got.Metadata["label"] != "after add" || got.UpdatedAt.IsZero() || !input.UpdatedAt.IsZero() {
		t.Fatalf("Update snapshot = %+v, input = %+v", got, input)
	}
}

func TestMemoryBackendConcurrentGetOwnsAccessCounts(t *testing.T) {
	ctx := t.Context()
	backend := NewMemoryBackend(MemoryConfig{})
	if err := backend.Add(ctx, &Entry{ID: "sample", Vector: []float64{1}}); err != nil {
		t.Fatal(err)
	}

	const readers = 12
	var wg sync.WaitGroup
	for range readers {
		wg.Go(func() {
			snapshot, err := backend.Get(ctx, "sample")
			if err != nil {
				t.Error(err)
				return
			}
			snapshot.Vector[0] = 42
			snapshot.AccessCount = -1
		})
	}
	wg.Wait()

	got, err := backend.Get(ctx, "sample")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessCount != readers+1 || got.Vector[0] != 1 {
		t.Errorf("Get after concurrent reads = %+v", got)
	}
}

func TestMemoryBackendUpdatePreservesVectorDimensions(t *testing.T) {
	m := NewMemoryBackend(MemoryConfig{Dimensions: 2})
	if err := m.Add(t.Context(), &Entry{ID: "fixture", Vector: []float64{1, 0}, Metadata: map[string]any{"color": "blue"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Update(t.Context(), &Entry{ID: "fixture", Vector: []float64{1}}); err == nil {
		t.Error("dimension mismatch accepted")
	}
	results, err := m.Search(t.Context(), []float64{1, 0}, 1, 0.9)
	if err != nil || len(results) != 1 {
		t.Errorf("last valid vector vanished from search: %v, %v", results, err)
	}
	if len(results) == 1 && (!slices.Equal(results[0].Entry.Vector, []float64{1, 0}) || results[0].Entry.Metadata["color"] != "blue") {
		t.Error("invalid update changed the stored entry")
	}
	stats, err := m.Stats(t.Context())
	if err != nil || stats.TotalEntries != 1 || stats.Dimensions != 2 {
		t.Errorf("stats = %+v, %v", stats, err)
	}
	if err := m.Update(t.Context(), &Entry{ID: "fixture", Vector: []float64{0, 1}}); err != nil {
		t.Fatal(err)
	}
	if found, err := m.Search(t.Context(), []float64{0, 1}, 1, 0.9); err != nil || len(found) != 1 {
		t.Errorf("updated vector search = %v, %v", found, err)
	}
	unrestricted := NewMemoryBackend(MemoryConfig{})
	if err := unrestricted.Add(t.Context(), &Entry{ID: "free", Vector: []float64{1}}); err != nil {
		t.Fatal(err)
	}
	if err := unrestricted.Update(t.Context(), &Entry{ID: "free", Vector: []float64{1, 0}}); err != nil {
		t.Errorf("unrestricted update = %v", err)
	}
}

type cancelBetweenAdds struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelBetweenAdds) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestMemoryBackendCanceledWritesKeepExistingEntries(t *testing.T) {
	m := NewMemoryBackend(MemoryConfig{MaxSize: 1})
	if err := m.Add(t.Context(), &Entry{ID: "original", Vector: []float64{1}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, id := range []string{"original", "replacement"} {
		if err := m.Add(ctx, &Entry{ID: id, Vector: []float64{2}}); !errors.Is(err, context.Canceled) {
			t.Errorf("pre-canceled Add(%q)=%v", id, err)
		}
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	m.mu.Lock()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- m.Add(ctx, &Entry{ID: "waiting", Vector: []float64{2}})
	}()
	<-started
	cancel()
	m.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("canceled waiter=%v", err)
	}
	if got, err := m.Get(t.Context(), "original"); err != nil || !slices.Equal(got.Vector, []float64{1}) {
		t.Errorf("original=%v, %v", got, err)
	}

	batch := NewMemoryBackend(MemoryConfig{})
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	staged := &cancelBetweenAdds{Context: ctx, cancel: cancel}
	if err := batch.AddBatch(staged, []*Entry{{ID: "first"}, {ID: "canceled"}, {ID: "skipped"}}); !errors.Is(err, context.Canceled) {
		t.Errorf("AddBatch=%v", err)
	}
	if got, err := batch.Get(t.Context(), "first"); err != nil || got.ID != "first" {
		t.Errorf("committed entry=%v, %v", got, err)
	}
	if count, err := batch.Count(t.Context()); err != nil || count != 1 {
		t.Errorf("batch count=%d, %v", count, err)
	}
}
