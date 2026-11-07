package vectorstore

import (
	"fmt"
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
