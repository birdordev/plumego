package vectorstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type failingBackend struct {
	name        string
	addErr      error
	addBatchErr error
}

func (b *failingBackend) Add(context.Context, *Entry) error { return b.addErr }

func (b *failingBackend) AddBatch(context.Context, []*Entry) error { return b.addBatchErr }

func (b *failingBackend) Search(context.Context, []float64, int, float64) ([]*SearchResult, error) {
	return nil, nil
}

func (b *failingBackend) Get(context.Context, string) (*Entry, error) {
	return nil, errors.New("not found")
}

func (b *failingBackend) Delete(context.Context, string) error { return nil }

func (b *failingBackend) DeleteBatch(context.Context, []string) error { return nil }

func (b *failingBackend) Update(context.Context, *Entry) error { return nil }

func (b *failingBackend) Count(context.Context) (int64, error) { return 0, nil }

func (b *failingBackend) Clear(context.Context) error { return nil }

func (b *failingBackend) Close() error { return nil }

func (b *failingBackend) Name() string { return b.name }

func (b *failingBackend) Stats(context.Context) (*BackendStats, error) {
	return &BackendStats{Name: b.name}, nil
}

func TestTieredBackendReportsBackgroundAddFailures(t *testing.T) {
	ctx := t.Context()
	l1 := NewMemoryBackend(MemoryConfig{Dimensions: 2, MaxSize: 10})
	l2Err := errors.New("l2 add failed")
	l3Err := errors.New("l3 add failed")
	var got []TieredBackgroundError

	backend, err := NewTieredBackend(TieredConfig{
		L1: l1,
		L2: &failingBackend{name: "l2", addErr: l2Err},
		L3: &failingBackend{name: "l3", addErr: l3Err},
		OnError: func(entry TieredBackgroundError) {
			got = append(got, entry)
		},
		Dimensions: 2,
	})
	if err != nil {
		t.Fatalf("NewTieredBackend: %v", err)
	}

	entry := &Entry{ID: "e1", Vector: []float64{1, 0}}
	if err := backend.Add(ctx, entry); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("background error count = %d, want 2", len(got))
	}
	if got[0].Tier != "l2" || got[0].Operation != "add" || !errors.Is(got[0].Err, l2Err) {
		t.Fatalf("unexpected first background error: %+v", got[0])
	}
	if got[1].Tier != "l3" || got[1].Operation != "add" || !errors.Is(got[1].Err, l3Err) {
		t.Fatalf("unexpected second background error: %+v", got[1])
	}
}

func TestTieredBackendReportsBackgroundBatchFailures(t *testing.T) {
	ctx := t.Context()
	l1 := NewMemoryBackend(MemoryConfig{Dimensions: 2, MaxSize: 10})
	l2Err := errors.New("l2 add batch failed")
	var got []TieredBackgroundError

	backend, err := NewTieredBackend(TieredConfig{
		L1: l1,
		L2: &failingBackend{name: "l2", addBatchErr: l2Err},
		OnError: func(entry TieredBackgroundError) {
			got = append(got, entry)
		},
		Dimensions: 2,
	})
	if err != nil {
		t.Fatalf("NewTieredBackend: %v", err)
	}

	entries := []*Entry{
		{ID: "e1", Vector: []float64{1, 0}},
		{ID: "e2", Vector: []float64{0, 1}},
	}
	if err := backend.AddBatch(ctx, entries); err != nil {
		t.Fatalf("AddBatch: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("background error count = %d, want 1", len(got))
	}
	if got[0].Tier != "l2" || got[0].Operation != "add_batch" || !errors.Is(got[0].Err, l2Err) {
		t.Fatalf("unexpected background error: %+v", got[0])
	}
}

type controlledDeleteBackend struct {
	*MemoryBackend
	err   error
	calls int
}

func (b *controlledDeleteBackend) Delete(ctx context.Context, id string) error {
	b.calls++
	if b.err != nil {
		return b.err
	}
	return b.MemoryBackend.Delete(ctx, id)
}

func (b *controlledDeleteBackend) DeleteBatch(ctx context.Context, ids []string) error {
	b.calls++
	if b.err != nil {
		return b.err
	}
	return b.MemoryBackend.DeleteBatch(ctx, ids)
}

func TestTieredDeleteJoinsFailuresAndCanRetry(t *testing.T) {
	for _, operation := range []string{"delete", "delete batch"} {
		t.Run(operation, func(t *testing.T) {
			ctx := t.Context()
			hotErr, coldErr := errors.New("hot deletion failure"), errors.New("cold deletion failure")
			l1 := &controlledDeleteBackend{MemoryBackend: NewMemoryBackend(MemoryConfig{}), err: hotErr}
			l2 := &controlledDeleteBackend{MemoryBackend: NewMemoryBackend(MemoryConfig{})}
			l3 := &controlledDeleteBackend{MemoryBackend: NewMemoryBackend(MemoryConfig{}), err: coldErr}
			store, err := NewTieredBackend(TieredConfig{L1: l1, L2: l2, L3: l3})
			if err != nil {
				t.Fatal(err)
			}
			entry := &Entry{ID: "sample", Vector: []float64{1}}
			if err := store.Add(ctx, entry); err != nil {
				t.Fatal(err)
			}
			remove := func() error { return store.Delete(ctx, entry.ID) }
			if operation == "delete batch" {
				remove = func() error { return store.DeleteBatch(ctx, []string{entry.ID}) }
			}
			if err := remove(); !errors.Is(err, hotErr) || !errors.Is(err, coldErr) ||
				!strings.Contains(err.Error(), operation+" on L1") || !strings.Contains(err.Error(), operation+" on L3") {
				t.Fatalf("%s returned %v", operation, err)
			}
			if l1.calls != 1 || l2.calls != 1 || l3.calls != 1 {
				t.Fatal("a failure prevented attempting another tier")
			}
			if _, err := l2.Get(ctx, entry.ID); err == nil {
				t.Fatal("successful deletion was rolled back")
			}
			l1.err, l3.err = nil, nil
			if err := remove(); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(ctx, entry.ID); err == nil {
				t.Fatal("deleted entry is still available")
			}
			if results, err := store.Search(ctx, entry.Vector, 1, 0.9); err != nil || len(results) != 0 {
				t.Fatalf("Search after retry = %v, %v", results, err)
			}
		})
	}
}

func TestTieredSearchCommitsConcurrentOutcomes(t *testing.T) {
	ctx := t.Context()
	store, err := NewTieredBackend(TieredConfig{L1: NewMemoryBackend(MemoryConfig{})})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(ctx, &Entry{ID: "sample", Vector: []float64{1}}); err != nil {
		t.Fatal(err)
	}

	const searches = 32
	var wg sync.WaitGroup
	for i := range searches {
		wg.Go(func() {
			vector := []float64{1}
			if i%2 == 0 {
				vector[0] = -1
			}
			if _, err := store.Search(ctx, vector, 1, 0.9); err != nil {
				t.Error(err)
			}
			stats := store.TieredStats()
			if stats.TotalQueries != stats.L1Hits+stats.Misses {
				t.Errorf("incomplete outcome in snapshot: %+v", stats)
			}
			stats.TotalQueries = -1
		})
	}
	wg.Wait()
	if got := store.TieredStats(); got.TotalQueries != searches || got.L1Hits != searches/2 || got.Misses != searches/2 {
		t.Errorf("final statistics = %+v", got)
	}
	if got, err := store.Stats(ctx); err != nil || got.HitRate != 0.5 || store.HitRate() != 0.5 || store.L1HitRate() != 0.5 {
		t.Errorf("unexpected hit rates: stats=%+v err=%v", got, err)
	}
}

type blockedTierSearch struct {
	*MemoryBackend
	entered, release chan struct{}
}

func (b *blockedTierSearch) Search(ctx context.Context, vector []float64, count int, threshold float64) ([]*SearchResult, error) {
	if vector[0] < 0 {
		close(b.entered)
		<-b.release
	}
	return b.MemoryBackend.Search(ctx, vector, count, threshold)
}

func TestTieredResetCountsInFlightSearchAtCompletion(t *testing.T) {
	for _, operation := range []string{"reset", "clear"} {
		t.Run(operation, func(t *testing.T) {
			ctx := t.Context()
			l1 := &blockedTierSearch{NewMemoryBackend(MemoryConfig{}), make(chan struct{}), make(chan struct{})}
			store, err := NewTieredBackend(TieredConfig{L1: l1})
			if err != nil {
				t.Fatal(err)
			}
			if store.HitRate() != 0 || store.L1HitRate() != 0 {
				t.Fatal("empty hit rates must be zero")
			}
			if _, err := store.Search(ctx, []float64{1}, 1, 0.9); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := store.Search(ctx, []float64{-1}, 1, 0.9)
				done <- err
			}()
			<-l1.entered
			var resetErr error
			if operation == "clear" {
				resetErr = store.Clear(ctx)
			} else {
				store.ResetStats()
			}
			reset := *store.TieredStats()
			close(l1.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if resetErr != nil || reset != (TieredStats{}) {
				t.Fatalf("%s returned %v, stats=%+v", operation, resetErr, reset)
			}
			if got := store.TieredStats(); *got != (TieredStats{TotalQueries: 1, Misses: 1}) {
				t.Errorf("stats after in-flight search = %+v", got)
			}
		})
	}
}

func TestTieredSearchCountsPromotionWithColdHit(t *testing.T) {
	ctx := t.Context()
	cold := NewMemoryBackend(MemoryConfig{})
	entry := &Entry{ID: "sample", Vector: []float64{1}}
	if err := cold.Add(ctx, entry); err != nil {
		t.Fatal(err)
	}
	store, err := NewTieredBackend(TieredConfig{L1: NewMemoryBackend(MemoryConfig{}), L3: cold})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		hits, err := store.Search(ctx, entry.Vector, 1, 0.9)
		if err != nil || len(hits) != 1 {
			t.Fatalf("Search = %v, %v", hits, err)
		}
	}
	want := TieredStats{L1Hits: 1, L3Hits: 1, Promotions: 1, TotalQueries: 2}
	if got := store.TieredStats(); *got != want || store.HitRate() != 1 || store.L1HitRate() != 0.5 {
		t.Errorf("promotion statistics = %+v, want %+v", got, want)
	}
}
