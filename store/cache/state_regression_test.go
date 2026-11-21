package cache

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestFailedMutationPreservesValueAndAccounting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial string
		limit   uint64
		mutate  func(*MemoryCache) error
		want    error
	}{
		{"set capacity", "old", 3, func(c *MemoryCache) error { return c.Set(nil, "key", []byte("larger"), 0) }, ErrCacheFull},
		{"append capacity", "old", 3, func(c *MemoryCache) error { return c.Append(nil, "key", []byte("more")) }, ErrCacheFull},
		{"counter capacity", "9", 1, func(c *MemoryCache) error { _, err := c.Incr(nil, "key", 1); return err }, ErrCacheFull},
		{"counter overflow", "9223372036854775807", 0, func(c *MemoryCache) error { _, err := c.Incr(nil, "key", 1); return err }, ErrNotInteger},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache, err := NewMemoryCacheWithConfig(Config{MaxMemoryUsage: tc.limit})
			if err != nil {
				t.Fatal(err)
			}
			defer cache.Close()
			if err := cache.Set(t.Context(), "key", []byte(tc.initial), 0); err != nil {
				t.Fatal(err)
			}
			before := cache.Stats()
			if err := tc.mutate(cache); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if got, err := cache.Get(t.Context(), "key"); err != nil || string(got) != tc.initial {
				t.Fatalf("value after failed mutation = %q, %v", got, err)
			}
			if got := cache.Stats(); got != before {
				t.Fatalf("stats after failed mutation = %+v, want %+v", got, before)
			}
			if err := cache.Delete(t.Context(), "key"); err != nil {
				t.Fatal(err)
			}
			if err := cache.Set(t.Context(), "retry", []byte("1"), 0); err != nil {
				t.Fatalf("retry after releasing capacity: %v", err)
			}
			if got := cache.Stats(); got.Entries != 1 || got.MemoryUsage != 1 {
				t.Fatalf("stats after retry = %+v", got)
			}
		})
	}
}

func TestDecrFullSignedRangePreservesTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache, err := NewMemoryCacheWithConfig(Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer cache.Close()
		legacy, err := encodeGobInt64(-1)
		if err != nil {
			t.Fatal(err)
		}
		if err := cache.Set(t.Context(), "key", legacy, 2*time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		if got, err := cache.Decr(t.Context(), "key", minInt64); err != nil || got != maxInt64 {
			t.Fatalf("Decr = %d, %v; want %d", got, err, maxInt64)
		}
		if _, err := cache.Decr(t.Context(), "key", -1); !errors.Is(err, ErrNotInteger) {
			t.Fatalf("overflow error = %v", err)
		}
		if value, err := cache.Get(t.Context(), "key"); err != nil || string(value) != strconv.FormatInt(maxInt64, 10) {
			t.Fatalf("value after overflow = %q, %v", value, err)
		}
		time.Sleep(time.Second)
		if _, err := cache.Get(t.Context(), "key"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("at original deadline: %v", err)
		}
	})
}

func TestConcurrentCountersAndAppendsDoNotLoseUpdates(t *testing.T) {
	cache, err := NewMemoryCacheWithConfig(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	const workers, iterations = 16, 25
	start := make(chan struct{})
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range iterations {
				if _, err := cache.Incr(nil, "counter", 1); err != nil {
					failures <- err
					return
				}
				if err := cache.Append(nil, "append", []byte("x")); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	counter, err := cache.Get(t.Context(), "counter")
	if err != nil {
		t.Fatal(err)
	}
	if string(counter) != strconv.Itoa(workers*iterations) {
		t.Fatalf("counter = %s, want %d", counter, workers*iterations)
	}
	appended, err := cache.Get(t.Context(), "append")
	if err != nil {
		t.Fatal(err)
	}
	if len(appended) != workers*iterations {
		t.Fatalf("appended bytes = %d, want %d", len(appended), workers*iterations)
	}
	if got := cache.Stats(); got.Entries != 2 || got.MemoryUsage != uint64(len(counter)+len(appended)) {
		t.Fatalf("stats = %+v", got)
	}
}

func TestRejectedAppendPreservesValueAndExpiration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := NewMemoryCacheWithConfig(Config{MaxMemoryUsage: 4})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Set(t.Context(), "key", []byte("old"), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second / 2)
		if err := c.Append(t.Context(), "key", []byte("too large")); !errors.Is(err, ErrCacheFull) {
			t.Fatalf("Append = %v, want ErrCacheFull", err)
		}
		if got, err := c.Get(t.Context(), "key"); err != nil || string(got) != "old" {
			t.Fatalf("Get = %q, %v", got, err)
		}
		time.Sleep(time.Second / 2)
		if _, err := c.Get(t.Context(), "key"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get at original expiry = %v", err)
		}
	})
}

func BenchmarkRejectedAppend(b *testing.B) {
	for _, size := range []int{1024, 1 << 20} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			c, err := NewMemoryCacheWithConfig(Config{MaxMemoryUsage: 32})
			if err != nil {
				b.Fatal(err)
			}
			defer c.Close()
			data := make([]byte, size)
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				if err := c.Append(ctx, "key", data); !errors.Is(err, ErrCacheFull) {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestAppendCapacityFailurePreservesExpiration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache, err := NewMemoryCacheWithConfig(Config{MaxMemoryUsage: 4, DefaultTTL: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer cache.Close()
		if err := cache.Set(t.Context(), "key", []byte("old"), 2*time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		before := cache.Stats()
		if err := cache.Append(t.Context(), "key", []byte("too large")); !errors.Is(err, ErrCacheFull) {
			t.Fatalf("append error=%v, want capacity error", err)
		}
		if value, err := cache.Get(t.Context(), "key"); err != nil || string(value) != "old" || cache.Stats() != before {
			t.Fatalf("value=%q error=%v stats=%+v", value, err, cache.Stats())
		}
		if err := cache.Append(t.Context(), "key", []byte("!")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		if _, err := cache.Get(t.Context(), "key"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("value still available at original deadline: %v", err)
		}
	})
}

func TestCloseTracksFinalPendingWriteInStats(t *testing.T) {
	cache := NewMemoryCache()
	if err := cache.Set(t.Context(), "existing", []byte("one"), 0); err != nil {
		t.Fatal(err)
	}
	if err := cache.lockWriteOperation(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cache.Close() }()
	<-cache.stopChan
	before := cache.Stats()
	cache.storeValueLocked("in-flight", []byte("two"), time.Time{}, 0, false)
	cache.writeMu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	after := cache.Stats()
	cache.store.Range(func(_, _ any) bool {
		t.Error("closed cache retained a payload")
		return false
	})
	if err := cache.Close(); err != nil || cache.Stats() != after {
		t.Fatalf("repeated Close modified stats: %v", err)
	}
	if _, err := cache.Get(t.Context(), "existing"); !errors.Is(err, ErrCacheClosed) {
		t.Fatalf("Get after Close: %v", err)
	}
	if !before.Closed || before.Entries != 1 || before.MemoryUsage != 3 ||
		!after.Closed || after.Entries != 2 || after.MemoryUsage != 6 {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}

func TestExpiredAppendFailureDropsExpiredItemAndAllowsRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := NewMemoryCacheWithConfig(Config{MaxMemoryUsage: 2, CleanupInterval: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Set(t.Context(), "key", []byte("ab"), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if err := c.Append(t.Context(), "key", []byte("too long")); !errors.Is(err, ErrCacheFull) {
			t.Fatalf("Append=%v", err)
		}
		if stats := c.Stats(); stats.Entries != 0 || stats.MemoryUsage != 0 {
			t.Fatalf("expired value still tracked: %+v", stats)
		}
		if err := c.Append(t.Context(), "key", []byte("x")); err != nil {
			t.Fatal(err)
		}
		if got, err := c.Get(t.Context(), "key"); err != nil || string(got) != "x" {
			t.Fatalf("Get=%q, %v", got, err)
		}
	})
}

func TestClearDoesNotInvalidateReaderCopy(t *testing.T) {
	c := NewMemoryCache()
	defer c.Close()
	if err := c.Set(t.Context(), "key", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	copied := make(chan []byte, 1)
	resume := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		got, err := c.Get(t.Context(), "key")
		if err != nil {
			t.Error(err)
		}
		copied <- got
		<-resume
	}()
	got := <-copied
	if err := c.Clear(t.Context()); err != nil {
		t.Error(err)
	}
	close(resume)
	<-finished
	if string(got) != "value" || c.Stats().Entries != 0 {
		t.Fatalf("copy=%q stats=%+v", got, c.Stats())
	}
	if _, err := c.Get(t.Context(), "key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Clear: %v", err)
	}
}

func TestStaleCleanupScanPreservesReplacementAndAccounting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := mustNewMemoryCacheWithConfig(t, Config{})
		defer cache.Close()
		const key = "key"
		if err := cache.Set(t.Context(), key, []byte("old"), time.Second); err != nil {
			t.Fatal(err)
		}
		var captured, replaceDone, cleaned = make(chan struct{}), make(chan struct{}), make(chan struct{})
		time.Sleep(2 * time.Second)
		go func() {
			item, _ := cache.store.Load(key)
			close(captured)
			<-replaceDone
			cache.removeExpiredItem(key, item.(cacheItem))
			close(cleaned)
		}()
		<-captured
		err := cache.Set(t.Context(), key, []byte("updated"), time.Second)
		close(replaceDone)
		<-cleaned
		if value, getErr := cache.Get(t.Context(), key); err != nil || getErr != nil || string(value) != "updated" {
			t.Fatalf("Set error=%v replacement=%q get error=%v", err, value, getErr)
		}
		if stats := cache.Stats(); stats.Entries != 1 || stats.MemoryUsage != uint64(len("updated")) {
			t.Fatalf("replacement accounting = %+v", stats)
		}
		time.Sleep(2 * time.Second)
		cache.cleanupExpired()
		if stats := cache.Stats(); stats.Entries != 0 || stats.MemoryUsage != 0 {
			t.Fatalf("cleanup accounting = %+v", stats)
		}
	})
}

func TestLegacyIntegerMigrationPreservesBytesAndTTLOnFailure(t *testing.T) {
	cache := mustNewMemoryCacheWithConfig(t, Config{})
	defer cache.Close()
	for _, tc := range []struct {
		name    string
		initial []byte
		want    string
	}{
		{name: "migrate", initial: mustLegacyCounter(t, 7), want: "8"},
		{name: "overflow", initial: mustLegacyCounter(t, maxInt64)},
		{name: "not an integer", initial: []byte("not an integer")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if err := cache.Set(ctx, tc.name, tc.initial, time.Minute); err != nil {
				t.Fatal(err)
			}
			expiration := cachedExpiration(t, cache, tc.name)
			_, updateErr := cache.Incr(ctx, tc.name, 1)
			got, err := cache.Get(ctx, tc.name)
			if err != nil || !cachedExpiration(t, cache, tc.name).Equal(expiration) {
				t.Fatalf("read error=%v or TTL changed", err)
			}
			if tc.want == "" {
				if updateErr == nil || string(got) != string(tc.initial) {
					t.Fatalf("rejected update changed bytes or succeeded: %v", updateErr)
				}
			} else if updateErr != nil || string(got) != tc.want {
				t.Fatalf("migration = %q, %v", got, updateErr)
			}
		})
	}
}

func mustLegacyCounter(t *testing.T, count int64) []byte {
	t.Helper()
	raw, err := encodeGobInt64(count)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type deleteCheckpointContext struct {
	context.Context
	once    sync.Once
	checked chan struct{}
}

func (c *deleteCheckpointContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestDeleteCanceledWhileCloseWaitsRetainsFinalCounts(t *testing.T) {
	cache := mustNewMemoryCacheWithConfig(t, Config{})
	if err := cache.Set(t.Context(), "fixture", []byte("before"), 0); err != nil {
		t.Fatal(err)
	}
	cache.writeMu.Lock()
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &deleteCheckpointContext{Context: parent, checked: make(chan struct{})}
	deleted, closed := make(chan error, 1), make(chan error, 1)
	go func() { deleted <- cache.Delete(ctx, "fixture") }()
	<-ctx.checked
	go func() { closed <- cache.Close() }()
	<-cache.stopChan
	cancel()
	cache.writeMu.Unlock()
	if err := <-deleted; !errors.Is(err, context.Canceled) {
		t.Fatalf("delete = %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if got := cache.Stats(); !got.Closed || got.Entries != 1 || got.MemoryUsage != 6 {
		t.Fatalf("close snapshot = %+v", got)
	}
}

func TestExpiredReadNearClosePreservesFinalAccounting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := NewMemoryCacheWithConfig(Config{CleanupInterval: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Set(t.Context(), "expired", []byte("value"), time.Second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		c.writeMu.Lock()
		_, expiredErr := c.Get(t.Context(), "expired")
		done := make(chan error, 1)
		go func() { done <- c.Close() }()
		<-c.stopChan
		_, closedErr := c.Get(t.Context(), "expired")
		exists, existsErr := c.Exists(t.Context(), "expired")
		c.writeMu.Unlock()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if !errors.Is(expiredErr, ErrNotFound) || !errors.Is(closedErr, ErrCacheClosed) ||
			exists || !errors.Is(existsErr, ErrCacheClosed) {
			t.Fatalf("expired=%v closed=%v exists=%t err=%v", expiredErr, closedErr, exists, existsErr)
		}
		if stats := c.Stats(); !stats.Closed || stats.Entries != 1 || stats.MemoryUsage != 5 {
			t.Fatalf("final stats=%+v", stats)
		}
		if _, ok := c.store.Load("expired"); ok {
			t.Fatal("closed cache retained the expired payload")
		}
	})
}

type cacheEntryContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *cacheEntryContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestCanceledSetWaitingForWriteLockPreservesCache(t *testing.T) {
	c := NewMemoryCache()
	defer c.Close()
	if err := c.Set(t.Context(), "fixture", []byte("old"), 0); err != nil {
		t.Fatal(err)
	}
	before := c.Stats()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	checkpoint := &cacheEntryContext{Context: ctx, checked: make(chan struct{})}
	done := make(chan error, 1)
	c.writeMu.Lock()
	go func() { done <- c.Set(checkpoint, "fixture", []byte("new value"), 0) }()
	<-checkpoint.checked
	cancel()
	c.writeMu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("set error=%v", err)
	}
	if after := c.Stats(); after != before {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
	if val, err := c.Get(t.Context(), "fixture"); string(val) != "old" || err != nil {
		t.Fatalf("value=%q err=%v", val, err)
	}
	if err := c.Set(t.Context(), "fixture", []byte("next"), 0); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredUncollectedKeyCountsUntilReadCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := NewMemoryCacheWithConfig(Config{MaxMemoryUsage: 5, CleanupInterval: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Set(t.Context(), "expire", []byte("old"), time.Second); err != nil {
			t.Fatal(err)
		}
		if err := c.Set(t.Context(), "keep", []byte("ok"), 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if err := c.Set(t.Context(), "next", []byte("new"), 0); !errors.Is(err, ErrCacheFull) {
			t.Fatalf("before read=%v", err)
		}
		if stats := c.Stats(); stats.Entries != 2 || stats.MemoryUsage != 5 {
			t.Fatalf("before=%+v", stats)
		}
		if _, err := c.Get(t.Context(), "expire"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired=%v", err)
		}
		if err := c.Set(t.Context(), "next", []byte("new"), 0); err != nil {
			t.Fatal(err)
		}
		if got, err := c.Get(t.Context(), "keep"); string(got) != "ok" || err != nil {
			t.Fatalf("retained=%q err=%v", got, err)
		}
		if stats := c.Stats(); stats.Entries != 2 || stats.MemoryUsage != 5 {
			t.Fatalf("after=%+v", stats)
		}
	})
}

func TestCacheAppliesByteKeyLimitAcrossOperations(t *testing.T) {
	c, err := NewMemoryCacheWithConfig(Config{MaxKeyLength: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Set(t.Context(), "éé", []byte("sample"), 0); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Get(t.Context(), "éé"); string(got) != "sample" || err != nil {
		t.Fatalf("read=%q err=%v", got, err)
	}
	before := c.Stats()
	for _, key := range []string{"ééa", "éé\t"} {
		if err := c.Set(t.Context(), key, []byte("sample"), 0); !errors.Is(err, ErrKeyTooLong) {
			t.Errorf("set=%v", err)
		}
		if _, err := c.Get(t.Context(), key); !errors.Is(err, ErrKeyTooLong) {
			t.Errorf("get=%v", err)
		}
		if ok, err := c.Exists(t.Context(), key); ok || !errors.Is(err, ErrKeyTooLong) {
			t.Errorf("exists=%t err=%v", ok, err)
		}
	}
	if after := c.Stats(); after != before {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}
