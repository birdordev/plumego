package vectorstore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

type failingBackend struct {
	name        string
	addErr      error
	addBatchErr error
	searchErr   error
	updateErr   error
	updateCalls int
}

func (b *failingBackend) Add(context.Context, *Entry) error { return b.addErr }

func (b *failingBackend) AddBatch(context.Context, []*Entry) error { return b.addBatchErr }

func (b *failingBackend) Search(context.Context, []float64, int, float64) ([]*SearchResult, error) {
	return nil, b.searchErr
}

func (b *failingBackend) Get(context.Context, string) (*Entry, error) {
	return nil, errors.New("not found")
}

func (b *failingBackend) Delete(context.Context, string) error { return nil }

func (b *failingBackend) DeleteBatch(context.Context, []string) error { return nil }

func (b *failingBackend) Update(context.Context, *Entry) error {
	b.updateCalls++
	return b.updateErr
}

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

func TestTieredBackendReportsBackgroundUpdateFailures(t *testing.T) {
	l1Err := errors.New("l1 update failed")
	l2Err := errors.New("l2 update failed")
	l3Err := errors.New("l3 update failed")
	for _, tt := range []struct {
		name      string
		tierErrs  [3]error
		wantCalls [3]int
		want      []TieredBackgroundError
	}{
		{
			name:      "optional tier failures",
			tierErrs:  [3]error{nil, l2Err, l3Err},
			wantCalls: [3]int{1, 1, 1},
			want: []TieredBackgroundError{
				{Tier: "l2", Operation: "update", Err: l2Err},
				{Tier: "l3", Operation: "update", Err: l3Err},
			},
		},
		{name: "L1 failure", tierErrs: [3]error{l1Err, l2Err, l3Err}, wantCalls: [3]int{1, 0, 0}},
		{name: "success", wantCalls: [3]int{1, 1, 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			l1 := &failingBackend{updateErr: tt.tierErrs[0]}
			l2 := &failingBackend{updateErr: tt.tierErrs[1]}
			l3 := &failingBackend{updateErr: tt.tierErrs[2]}
			var got []TieredBackgroundError
			backend, err := NewTieredBackend(TieredConfig{
				L1: l1, L2: l2, L3: l3,
				OnError: func(event TieredBackgroundError) { got = append(got, event) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.Update(t.Context(), &Entry{ID: "sample"}); !errors.Is(err, tt.tierErrs[0]) {
				t.Errorf("Update = %v, want %v", err, tt.tierErrs[0])
			}
			if calls := [3]int{l1.updateCalls, l2.updateCalls, l3.updateCalls}; calls != tt.wantCalls {
				t.Errorf("Update calls = %v, want %v", calls, tt.wantCalls)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("diagnostics = %+v, want %+v", got, tt.want)
			}
			for i, want := range tt.want {
				if got[i].Tier != want.Tier || got[i].Operation != want.Operation || !errors.Is(got[i].Err, want.Err) {
					t.Errorf("diagnostic %d = %+v, want %+v", i, got[i], want)
				}
			}
		})
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

type controlledStatsBackend struct {
	*MemoryBackend
	stats func(context.Context) (*BackendStats, error)
}

func (b *controlledStatsBackend) Stats(ctx context.Context) (*BackendStats, error) {
	return b.stats(ctx)
}

func TestTieredStatsHandlesL1Failures(t *testing.T) {
	cause := errors.New("test statistics failure")
	hot := &controlledStatsBackend{MemoryBackend: NewMemoryBackend(MemoryConfig{})}
	store, err := NewTieredBackend(TieredConfig{L1: hot, Dimensions: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	hot.stats = func(context.Context) (*BackendStats, error) { return nil, cause }
	if stats, err := store.Stats(ctx); stats != nil || !errors.Is(err, cause) || !strings.Contains(err.Error(), "L1") {
		t.Fatalf("Stats with failed L1 = %+v, %v", stats, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	hot.stats = func(ctx context.Context) (*BackendStats, error) { return nil, ctx.Err() }
	if stats, err := store.Stats(cancelled); stats != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Stats with cancelled context = %+v, %v", stats, err)
	}

	hot.stats = func(context.Context) (*BackendStats, error) { return nil, nil }
	if stats, err := store.Stats(ctx); stats != nil || err == nil {
		t.Fatalf("Stats with missing L1 data = %+v, %v", stats, err)
	}

	store.ResetStats()
	entry := &Entry{ID: "sample", Vector: []float64{1}}
	if err := store.Add(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if hits, err := store.Search(ctx, entry.Vector, 1, 0.9); err != nil || len(hits) != 1 {
		t.Fatalf("Search after failed Stats = %v, %v", hits, err)
	}
	hot.stats = hot.MemoryBackend.Stats
	if stats, err := store.Stats(ctx); err != nil || stats.TotalEntries != 1 || stats.HitRate != 1 || stats.Dimensions != 1 {
		t.Fatalf("Stats after recovery = %+v, %v", stats, err)
	}
}

func TestTieredSearchReportsAllTierFailures(t *testing.T) {
	l1Err := errors.New("hot backend failure")
	l2Err := errors.New("intermediate backend failure")
	store, err := NewTieredBackend(TieredConfig{
		L1: &failingBackend{searchErr: l1Err},
		L2: &failingBackend{searchErr: l2Err},
		L3: &failingBackend{searchErr: context.Canceled},
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := store.Search(t.Context(), []float64{1}, 1, 0)
	if results != nil || !errors.Is(err, l1Err) || !errors.Is(err, l2Err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Search = %v, %v, want all tier errors", results, err)
	}
	for _, tier := range []string{"L1", "L2", "L3"} {
		if !strings.Contains(err.Error(), tier+" search failed") {
			t.Errorf("Search error %q lacks context for %s", err, tier)
		}
	}
	if stats := store.TieredStats(); *stats != (TieredStats{}) {
		t.Errorf("failed search updated counters: %+v", stats)
	}
}

func TestTieredSearchCountsSuccessfulEmptyTierAsMiss(t *testing.T) {
	store, err := NewTieredBackend(TieredConfig{
		L1: &failingBackend{searchErr: errors.New("hot backend failure")},
		L3: NewMemoryBackend(MemoryConfig{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := store.Search(t.Context(), []float64{1}, 1, 0)
	if err != nil || results == nil || len(results) != 0 {
		t.Fatalf("Search = %v, %v, want successful empty result", results, err)
	}
	if stats := store.TieredStats(); *stats != (TieredStats{Misses: 1, TotalQueries: 1}) {
		t.Errorf("miss counters = %+v", stats)
	}
}

type controlledClearBackend struct {
	*MemoryBackend
	err error
}

func (b *controlledClearBackend) Clear(ctx context.Context) error {
	if b.err != nil {
		return b.err
	}
	return b.MemoryBackend.Clear(ctx)
}

func TestAudit19ClearDoesNotReportSuccessWithRemainingEntries(t *testing.T) {
	hotErr := errors.New("test hot clear failure")
	coldErr := errors.New("test cold clear failure")
	hot := &controlledClearBackend{MemoryBackend: NewMemoryBackend(MemoryConfig{}), err: hotErr}
	warm := NewMemoryBackend(MemoryConfig{})
	cold := &controlledClearBackend{MemoryBackend: NewMemoryBackend(MemoryConfig{}), err: coldErr}
	store, err := NewTieredBackend(TieredConfig{L1: hot, L2: warm, L3: cold})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	entry := &Entry{ID: "sample", Vector: []float64{1}}
	if err := store.Add(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if hits, err := store.Search(ctx, entry.Vector, 1, 0.9); err != nil || len(hits) != 1 {
		t.Fatalf("Search before clear = %v, %v", hits, err)
	}
	before := *store.TieredStats()

	err = store.Clear(ctx)
	if !errors.Is(err, hotErr) || !errors.Is(err, coldErr) ||
		!strings.Contains(err.Error(), "clear on L1") || !strings.Contains(err.Error(), "clear on L3") {
		t.Fatalf("Clear = %v, want both tier errors", err)
	}
	if stats := *store.TieredStats(); stats != before {
		t.Errorf("Clear changed statistics on failure: %+v, want %+v", stats, before)
	}
	if count, err := warm.Count(ctx); err != nil || count != 0 {
		t.Errorf("successful tier Count = %d, %v, want 0, nil", count, err)
	}

	hot.err = nil
	if err := store.Clear(ctx); !errors.Is(err, coldErr) {
		t.Fatalf("Clear with only cold tier down = %v, want %v", err, coldErr)
	}
	if hits, err := store.Search(ctx, entry.Vector, 1, 0.9); err != nil || len(hits) != 1 {
		t.Fatalf("Search after partial clear = %v, %v, want remaining cold hit", hits, err)
	}

	cold.err = nil
	if err := store.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := *store.TieredStats(); stats != (TieredStats{}) {
		t.Errorf("successful Clear did not reset statistics: %+v", stats)
	}
	if hits, err := store.Search(ctx, entry.Vector, 1, 0.9); err != nil || len(hits) != 0 {
		t.Fatalf("Search after successful retry = %v, %v, want no hits", hits, err)
	}
}

type controlledCloseBackend struct {
	*MemoryBackend
	close func() error
}

func (b *controlledCloseBackend) Close() error { return b.close() }

func TestAudit19CloseReportsTierFailures(t *testing.T) {
	hotErr := errors.New("test hot close failure")
	coldErr := errors.New("test cold close failure")
	var calls []string
	backend := func(tier string, err error) Backend {
		return &controlledCloseBackend{close: func() error {
			calls = append(calls, tier)
			return err
		}}
	}

	store, err := NewTieredBackend(TieredConfig{
		L1: backend("L1", hotErr),
		L2: backend("L2", nil),
		L3: backend("L3", coldErr),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); !errors.Is(err, hotErr) || !errors.Is(err, coldErr) ||
		!strings.Contains(err.Error(), "close on L1") || !strings.Contains(err.Error(), "close on L3") {
		t.Fatalf("Close = %v, want both tier errors", err)
	}
	if want := []string{"L1", "L2", "L3"}; !slices.Equal(calls, want) {
		t.Fatalf("Close calls = %v, want %v", calls, want)
	}

	calls = nil
	store, err = NewTieredBackend(TieredConfig{L1: backend("L1", nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil || !slices.Equal(calls, []string{"L1"}) {
		t.Fatalf("Close with only L1 = %v, calls %v", err, calls)
	}
}

type tierQueryFixture struct {
	Backend
	entry      *Entry
	getErr     error
	count      int64
	countErr   error
	countCalls int
	context    context.Context
}

func (f *tierQueryFixture) Get(context.Context, string) (*Entry, error) { return f.entry, f.getErr }
func (f *tierQueryFixture) Count(ctx context.Context) (int64, error) {
	f.context = ctx
	f.countCalls++
	return f.count, f.countErr
}

func TestTieredGetPreservesBackendErrors(t *testing.T) {
	first, second := errors.New("L1 unavailable"), errors.New("L2 unavailable")
	l1 := &tierQueryFixture{Backend: &failingBackend{}, getErr: first}
	l2 := &tierQueryFixture{Backend: &failingBackend{}, getErr: second}
	l3 := &tierQueryFixture{Backend: &failingBackend{}, getErr: context.Canceled}
	tier, err := NewTieredBackend(TieredConfig{L1: l1, L2: l2, L3: l3})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tier.Get(t.Context(), "test"); got != nil || !errors.Is(err, first) || !errors.Is(err, second) || !errors.Is(err, context.Canceled) {
		t.Errorf("Get=%v, %v", got, err)
	}
	l3.entry, l3.getErr = &Entry{ID: "test"}, nil
	if got, err := tier.Get(t.Context(), "test"); got != l3.entry || err != nil {
		t.Errorf("L3 hit=%v, %v", got, err)
	}
}

func TestTieredCountUsesDeepestTier(t *testing.T) {
	for depth := 1; depth <= 3; depth++ {
		tiers := []*tierQueryFixture{{count: 1}, {count: 2}, {count: 3}}
		cfg := TieredConfig{L1: tiers[0]}
		if depth > 1 {
			cfg.L2 = tiers[1]
		}
		if depth > 2 {
			cfg.L3 = tiers[2]
		}
		tier, err := NewTieredBackend(cfg)
		if err != nil {
			t.Fatal(err)
		}
		ctx := t.Context()
		if got, err := tier.Count(ctx); got != int64(depth) || err != nil {
			t.Errorf("depth %d Count=%d, %v", depth, got, err)
		}
		sentinel := errors.New("count failed")
		tiers[depth-1].countErr = sentinel
		if _, err := tier.Count(ctx); !errors.Is(err, sentinel) {
			t.Errorf("depth %d count error=%v", depth, err)
		}
		for i, f := range tiers {
			if i == depth-1 {
				if f.context != ctx || f.countCalls != 2 {
					t.Errorf("depth %d selected tier calls=%d", depth, f.countCalls)
				}
			} else if f.countCalls != 0 {
				t.Errorf("unexpected Count calls in tier %d", i+1)
			}
		}
	}
}
