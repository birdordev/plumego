package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kvstore "github.com/birdordev/plumego/store/kv"
)

func TestAudit08ExpiredLookupPreservesNewClaim(t *testing.T) {
	base, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	now := time.Now()
	old, err := json.Marshal(Record{Key: "test", ExpiresAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := base.Set("idem:test", old, time.Hour); err != nil {
		t.Fatal(err)
	}

	read, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	reader := NewKVStore(base, KVConfig{Now: func() time.Time {
		close(read)
		<-release
		return now
	}})
	writer := NewKVStore(base, KVConfig{Now: func() time.Time { return now }})
	go func() {
		defer close(done)
		if _, found, err := reader.Get(t.Context(), "test"); found || err != nil {
			t.Errorf("expired lookup: found=%v, err=%v", found, err)
		}
	}()
	<-read
	claim := Record{Key: "test", RequestHash: "new", ExpiresAt: now.Add(time.Hour)}
	created, err := writer.PutIfAbsent(t.Context(), claim)
	close(release)
	<-done
	if err != nil || !created {
		t.Fatalf("new claim: created=%v, err=%v", created, err)
	}
	got, found, err := writer.Get(t.Context(), claim.Key)
	if err != nil || !found || got.RequestHash != claim.RequestHash {
		t.Fatalf("lookup new claim: found=%v, hash=%q, err=%v", found, got.RequestHash, err)
	}
	if created, err := writer.PutIfAbsent(t.Context(), claim); err != nil || created {
		t.Fatalf("claim retry: created=%v, err=%v", created, err)
	}
}

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

func TestKVStorePutIfAbsentDoesNotReportSuccessOnSetFailure(t *testing.T) {
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	wrapper := NewKVStore(store, KVConfig{Now: func() time.Time {
		// With no expiry and no existing key, the clock first runs after Get.
		if err := store.Close(); err != nil {
			t.Error(err)
		}
		return time.Unix(100, 0)
	}})
	created, err := wrapper.PutIfAbsent(t.Context(), Record{Key: "fixture"})
	if created || !errors.Is(err, kvstore.ErrStoreClosed) {
		t.Errorf("created=%v err=%v", created, err)
	}
}

func TestKVStoreHonorsCanceledOperations(t *testing.T) {
	base, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	s := NewKVStore(base, DefaultKVConfig())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	seed := func(key string) {
		t.Helper()
		if _, err := s.PutIfAbsent(t.Context(), Record{Key: key}); err != nil {
			t.Fatal(err)
		}
	}
	seed("complete")
	seed("delete")
	if ok, err := s.PutIfAbsent(ctx, Record{Key: "new"}); ok || !errors.Is(err, context.Canceled) {
		t.Errorf("put=%v/%v", ok, err)
	}
	if _, ok, err := s.Get(ctx, "complete"); ok || !errors.Is(err, context.Canceled) {
		t.Errorf("get=%v/%v", ok, err)
	}
	if err := s.Complete(ctx, "complete", []byte("response")); !errors.Is(err, context.Canceled) {
		t.Errorf("complete=%v", err)
	}
	if err := s.Delete(ctx, "delete"); !errors.Is(err, context.Canceled) {
		t.Errorf("delete=%v", err)
	}
	if _, found, err := s.Get(t.Context(), "new"); err != nil || found {
		t.Errorf("canceled claim created a record: %t, %v", found, err)
	}
	if got, found, err := s.Get(t.Context(), "complete"); err != nil || !found || got.Status != StatusInProgress || len(got.Response) != 0 {
		t.Errorf("canceled complete changed state: %+v, %t, %v", got, found, err)
	}
	if _, found, err := s.Get(t.Context(), "delete"); err != nil || !found {
		t.Errorf("canceled delete removed the record: %t, %v", found, err)
	}

	s.mu.Lock()
	enter := make(chan struct{})
	done := make(chan error, 1)
	waitCtx, stop := context.WithCancel(t.Context())
	defer stop()
	go func() {
		close(enter)
		done <- s.Delete(waitCtx, "delete")
	}()
	<-enter
	stop()
	s.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("canceled under lock: %v", err)
	}
	if _, found, err := s.Get(t.Context(), "delete"); err != nil || !found {
		t.Errorf("canceled waiter removed record: %t, %v", found, err)
	}
}
