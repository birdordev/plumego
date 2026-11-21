package cache

import (
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
	if !before.Closed || before.Entries != 1 || before.MemoryUsage != 3 ||
		!after.Closed || after.Entries != 2 || after.MemoryUsage != 6 {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}
