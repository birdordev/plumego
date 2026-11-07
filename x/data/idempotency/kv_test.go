package idempotency

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kvstore "github.com/spcent/plumego/store/kv"
)

func TestKVStoreIdempotency(t *testing.T) {
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open kv: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	idem := NewKVStore(store, DefaultKVConfig())

	record := Record{
		Key:         "req-1",
		RequestHash: "hash-1",
		Status:      StatusInProgress,
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}

	created, err := idem.PutIfAbsent(t.Context(), record)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !created {
		t.Fatalf("expected created")
	}

	_, found, err := idem.Get(t.Context(), "req-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found {
		t.Fatalf("expected found")
	}

	if err := idem.Complete(t.Context(), "req-1", []byte("ok")); err != nil {
		t.Fatalf("complete: %v", err)
	}

	got, found, err := idem.Get(t.Context(), "req-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found {
		t.Fatalf("expected found")
	}
	if got.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", got.Status)
	}
}

func TestKVStorePutIfAbsentConcurrent(t *testing.T) {
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open kv: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	idem := NewKVStore(store, DefaultKVConfig())
	record := Record{
		Key:       "req-concurrent",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	var created atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := idem.PutIfAbsent(t.Context(), record)
			if err != nil {
				t.Errorf("PutIfAbsent: %v", err)
				return
			}
			if ok {
				created.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := created.Load(); got != 1 {
		t.Fatalf("created count = %d, want 1", got)
	}
}

func TestKVStorePutIfAbsentConcurrentAcrossWrappers(t *testing.T) {
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open kv: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	wrappers := []*KVStore{
		NewKVStore(store, DefaultKVConfig()),
		NewKVStore(store, DefaultKVConfig()),
	}
	record := Record{
		Key:       "req-cross-wrapper",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	var created atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := wrappers[i%len(wrappers)].PutIfAbsent(t.Context(), record)
			if err != nil {
				t.Errorf("PutIfAbsent: %v", err)
				return
			}
			if ok {
				created.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := created.Load(); got != 1 {
		t.Fatalf("created count = %d, want 1", got)
	}
}

func TestKVStoreCompleteAcrossSharedWrappers(t *testing.T) {
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open kv: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	first := NewKVStore(store, DefaultKVConfig())
	second := NewKVStore(store, DefaultKVConfig())
	ctx := t.Context()
	record := Record{
		Key:       "req-shared-complete",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if created, err := first.PutIfAbsent(ctx, record); err != nil || !created {
		t.Fatalf("PutIfAbsent: created=%v err=%v", created, err)
	}
	if err := second.Complete(ctx, record.Key, []byte("ok")); err != nil {
		t.Fatalf("Complete from second wrapper: %v", err)
	}

	got, found, err := first.Get(ctx, record.Key)
	if err != nil {
		t.Fatalf("Get from first wrapper: %v", err)
	}
	if !found || got.Status != StatusCompleted || string(got.Response) != "ok" {
		t.Fatalf("shared record = %+v found=%v, want completed ok", got, found)
	}
	if err := first.Complete(ctx, record.Key, []byte("again")); err != ErrNotFound {
		t.Fatalf("second Complete = %v, want ErrNotFound", err)
	}
}

func TestKVStoreIdempotencyExpired(t *testing.T) {
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open kv: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	now := time.Now()
	idem := NewKVStore(store, KVConfig{Prefix: "idem:", Now: func() time.Time { return now }})

	record := Record{
		Key:       "req-expired",
		ExpiresAt: now.Add(-1 * time.Minute),
	}

	_, err = idem.PutIfAbsent(t.Context(), record)
	if err != ErrExpired {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func TestKVStoreCompleteOnlyInProgress(t *testing.T) {
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open kv: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	idem := NewKVStore(store, DefaultKVConfig())
	ctx := t.Context()
	record := Record{
		Key:       "req-complete-once",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	if created, err := idem.PutIfAbsent(ctx, record); err != nil || !created {
		t.Fatalf("PutIfAbsent: created=%v err=%v", created, err)
	}
	if err := idem.Complete(ctx, record.Key, []byte("ok")); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if err := idem.Complete(ctx, record.Key, []byte("again")); err != ErrNotFound {
		t.Fatalf("second Complete = %v, want ErrNotFound", err)
	}
}
