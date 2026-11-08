package cache

import (
	"errors"
	"strconv"
	"sync"
	"testing"
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
