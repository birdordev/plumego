package jwt

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/internal/testutil/wait"
)

func TestInMemoryKeyStoreCanceledWhileWaitingToSet(t *testing.T) {
	store := NewInMemoryKeyStore()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	func() {
		store.mu.RLock()
		defer store.mu.RUnlock()
		go func() { result <- store.SetContext(ctx, "fixture", []byte("sample"), 0) }()
		waitForKeyStoreWriter(t, store)
		cancel()
	}()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("SetContext = %v, want context.Canceled", err)
	}
	if _, err := store.GetContext(t.Context(), "fixture"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("GetContext = %v, want ErrUnknownKey", err)
	}
}

func TestInMemoryKeyStoreCanceledWhileWaitingToRead(t *testing.T) {
	for _, method := range []string{"GetContext", "KeysContext"} {
		t.Run(method, func(t *testing.T) {
			store := NewInMemoryKeyStore()
			if err := store.SetContext(t.Context(), "fixture", []byte("value"), 0); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type readResult struct {
				hasValue bool
				err      error
			}
			result := make(chan readResult, 1)

			func() {
				store.mu.Lock()
				defer store.mu.Unlock()
				go func() {
					if method == "GetContext" {
						value, err := store.GetContext(ctx, "fixture")
						result <- readResult{value != nil, err}
						return
					}
					keys, err := store.KeysContext(ctx)
					result <- readResult{keys != nil, err}
				}()

				waitForKeyStoreReader(t, method)
				cancel()
			}()

			select {
			case got := <-result:
				if got.hasValue || !errors.Is(got.err, context.Canceled) {
					t.Fatalf("%s returned a result: %t, error: %v, want nil result and context.Canceled", method, got.hasValue, got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s did not return after the write lock was released", method)
			}
			if !store.mu.TryLock() {
				t.Fatalf("%s did not release the read lock", method)
			}
			store.mu.Unlock()
		})
	}
}

func waitForKeyStoreWriter(t *testing.T, store *InMemoryKeyStore) {
	t.Helper()
	// The caller holds a read lock. A queued writer prevents additional readers.
	wait.ForCondition(t, 5*time.Second, "SetContext waiting for the write lock", func() bool {
		if store.mu.TryRLock() {
			store.mu.RUnlock()
			return false
		}
		return true
	})
}

func waitForKeyStoreReader(t *testing.T, method string) {
	t.Helper()
	var stack [64 << 10]byte
	wait.ForCondition(t, 5*time.Second, method+" waiting for the read lock", func() bool {
		n := runtime.Stack(stack[:], true)
		for trace := range strings.SplitSeq(string(stack[:n]), "\n\n") {
			if strings.Contains(trace, "sync.(*RWMutex).RLock(") &&
				strings.Contains(trace, "(*InMemoryKeyStore)."+method+"(") {
				return true
			}
		}
		return false
	})
}

func TestInMemoryKeyStoreTTLStartsAfterWaitingToSet(t *testing.T) {
	store := NewInMemoryKeyStore()
	const ttl = 20 * time.Millisecond
	result := make(chan error, 1)
	releasingAt := func() time.Time {
		store.mu.RLock()
		defer store.mu.RUnlock()
		go func() { result <- store.SetContext(t.Context(), "fixture", []byte("value"), ttl) }()
		waitForKeyStoreWriter(t, store)
		// Keep the writer queued beyond the entire TTL.
		time.Sleep(2 * ttl)
		return time.Now()
	}()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if expiry := store.entries["fixture"].expiry; expiry.Before(releasingAt.Add(ttl)) {
		t.Fatalf("expiry minus lock release time = %v, want at least %v", expiry.Sub(releasingAt), ttl)
	}
}

func TestInMemoryKeyStoreKeysUsesClockAfterWaitingToRead(t *testing.T) {
	store := NewInMemoryKeyStore()
	if err := store.SetContext(t.Context(), "permanent", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	type result struct {
		keys []string
		err  error
	}
	done := make(chan result, 1)
	func() {
		store.mu.Lock()
		defer store.mu.Unlock()
		go func() {
			keys, err := store.KeysContext(t.Context())
			done <- result{keys, err}
		}()
		waitForKeyStoreReader(t, "KeysContext")
		// This entry expires before the reader can acquire the lock.
		store.entries["expired"] = memEntry{expiry: time.Now()}
	}()
	got := <-done
	if got.err != nil || !slices.Equal(got.keys, []string{"permanent"}) {
		t.Fatalf("KeysContext = %v, %v; want [permanent], nil", got.keys, got.err)
	}
}

func TestInMemoryKeyStore_SetAndGet(t *testing.T) {
	store := NewInMemoryKeyStore()
	ctx := context.Background()

	if err := store.SetContext(ctx, "kid-1", []byte("secret-1"), 0); err != nil {
		t.Fatalf("SetContext() error = %v", err)
	}

	got, err := store.GetContext(ctx, "kid-1")
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}
	if string(got) != "secret-1" {
		t.Errorf("GetContext() = %q, want %q", got, "secret-1")
	}
}

func TestInMemoryKeyStore_GetMissingKey(t *testing.T) {
	store := NewInMemoryKeyStore()

	_, err := store.GetContext(context.Background(), "absent")
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("GetContext() error = %v, want ErrUnknownKey", err)
	}
}

// waitForKeyExpiry polls until GetContext reports ErrKeyExpired for key.
// Polling with a generous deadline avoids both fixed sleeps and clock
// granularity races.
func waitForKeyExpiry(t *testing.T, store *InMemoryKeyStore, key string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := store.GetContext(context.Background(), key)
		if errors.Is(err, ErrKeyExpired) {
			return
		}
		if err != nil {
			t.Fatalf("GetContext() error = %v, want ErrKeyExpired eventually", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("key %q did not expire within 2s", key)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestInMemoryKeyStore_ExpiredKey(t *testing.T) {
	store := NewInMemoryKeyStore()
	ctx := context.Background()

	// Only a positive TTL produces an expiring entry; zero or negative means
	// "never expires".
	if err := store.SetContext(ctx, "kid-exp", []byte("secret"), time.Millisecond); err != nil {
		t.Fatalf("SetContext() error = %v", err)
	}

	waitForKeyExpiry(t, store, "kid-exp")
}

func TestInMemoryKeyStore_NonPositiveTTLNeverExpires(t *testing.T) {
	store := NewInMemoryKeyStore()
	ctx := context.Background()

	for _, ttl := range []time.Duration{0, -time.Second} {
		key := fmt.Sprintf("k-%v", ttl)
		if err := store.SetContext(ctx, key, []byte("v"), ttl); err != nil {
			t.Fatalf("SetContext(%v) error = %v", ttl, err)
		}
		if _, err := store.GetContext(ctx, key); err != nil {
			t.Errorf("GetContext() ttl=%v error = %v, want never-expiring entry", ttl, err)
		}
	}
}

func TestInMemoryKeyStore_KeysExcludesExpired(t *testing.T) {
	store := NewInMemoryKeyStore()
	ctx := context.Background()

	if err := store.SetContext(ctx, "live", []byte("v"), time.Minute); err != nil {
		t.Fatalf("SetContext(live) error = %v", err)
	}
	if err := store.SetContext(ctx, "forever", []byte("v"), 0); err != nil {
		t.Fatalf("SetContext(forever) error = %v", err)
	}
	if err := store.SetContext(ctx, "dead", []byte("v"), time.Millisecond); err != nil {
		t.Fatalf("SetContext(dead) error = %v", err)
	}
	waitForKeyExpiry(t, store, "dead")

	keys, err := store.KeysContext(ctx)
	if err != nil {
		t.Fatalf("KeysContext() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("KeysContext() = %v, want 2 live keys", keys)
	}
	if !slices.Contains(keys, "live") || !slices.Contains(keys, "forever") {
		t.Errorf("KeysContext() = %v, want live and forever", keys)
	}
}

func TestInMemoryKeyStore_ContextCancelled(t *testing.T) {
	store := NewInMemoryKeyStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.GetContext(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Errorf("GetContext() error = %v, want context.Canceled", err)
	}
	if err := store.SetContext(ctx, "k", []byte("v"), 0); !errors.Is(err, context.Canceled) {
		t.Errorf("SetContext() error = %v, want context.Canceled", err)
	}
	if _, err := store.KeysContext(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("KeysContext() error = %v, want context.Canceled", err)
	}
}

func TestInMemoryKeyStore_ValueIsolation(t *testing.T) {
	store := NewInMemoryKeyStore()
	ctx := context.Background()

	original := []byte("secret")
	if err := store.SetContext(ctx, "k", original, 0); err != nil {
		t.Fatalf("SetContext() error = %v", err)
	}
	// Mutating the caller's slice must not corrupt the stored copy.
	original[0] = 'X'

	got, err := store.GetContext(ctx, "k")
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}
	if string(got) != "secret" {
		t.Errorf("stored value = %q, want %q (SetContext must copy)", got, "secret")
	}

	// Mutating the returned slice must not corrupt the stored copy either.
	got[0] = 'Y'
	again, err := store.GetContext(ctx, "k")
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}
	if string(again) != "secret" {
		t.Errorf("stored value = %q, want %q (GetContext must copy)", again, "secret")
	}
}

func TestInMemoryKeyStore_ConcurrentAccess(t *testing.T) {
	store := NewInMemoryKeyStore()
	ctx := context.Background()

	const workers = 8
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer wg.Done()
			key := string(rune('a' + id%4))
			for i := 0; i < iterations; i++ {
				if err := store.SetContext(ctx, key, []byte("v"), time.Minute); err != nil {
					t.Errorf("SetContext() error = %v", err)
					return
				}
				if _, err := store.GetContext(ctx, key); err != nil {
					t.Errorf("GetContext() error = %v", err)
					return
				}
				if _, err := store.KeysContext(ctx); err != nil {
					t.Errorf("KeysContext() error = %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestInMemoryKeyStoreZeroValue(t *testing.T) {
	var store InMemoryKeyStore
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.SetContext(ctx, "key", []byte("discard"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled SetContext = %v", err)
	}

	for _, key := range []string{"first", "second"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			value := []byte("example")
			if err := store.SetContext(t.Context(), key, value, 0); err != nil {
				t.Fatal(err)
			}
			value[0] = 'X'
			if got, err := store.GetContext(t.Context(), key); err != nil || string(got) != "example" {
				t.Fatalf("stored value = %q, %v", got, err)
			}
		})
	}
}

func TestInMemoryKeyStoreExpiryBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewInMemoryKeyStore()
		if err := store.SetContext(t.Context(), "key", []byte("test value"), time.Second); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			advance time.Duration
			expired bool
		}{
			{time.Second - time.Nanosecond, false},
			{time.Nanosecond, true},
			{time.Nanosecond, true},
		} {
			time.Sleep(tc.advance)
			keys, err := store.KeysContext(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			value, err := store.GetContext(t.Context(), "key")
			if tc.expired {
				if len(keys) != 0 || value != nil || !errors.Is(err, ErrKeyExpired) {
					t.Fatalf("expired key: keys = %v, error = %v", keys, err)
				}
			} else if len(keys) != 1 || keys[0] != "key" || err != nil || len(value) == 0 {
				t.Fatalf("active key: keys = %v, error = %v", keys, err)
			}
		}
	})
}

func TestInMemoryKeyStoreCopiesAndRejectsCanceledOverwrite(t *testing.T) {
	store := NewInMemoryKeyStore()
	input := []byte("original")
	if err := store.SetContext(t.Context(), "fixture", input, 0); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	first, err := store.GetContext(t.Context(), "fixture")
	if err != nil || string(first) != "original" {
		t.Fatalf("first read=%q err=%v", first, err)
	}
	first[0] = 'Y'
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.SetContext(ctx, "fixture", input, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled overwrite=%v", err)
	}
	again, err := store.GetContext(t.Context(), "fixture")
	if err != nil || string(again) != "original" {
		t.Fatalf("second read=%q err=%v", again, err)
	}
}

func BenchmarkKeyStoreKeysExpiryRatio(b *testing.B) {
	const entries = 4096
	for _, live := range []int{0, 32, entries} {
		b.Run(fmt.Sprint(live), func(b *testing.B) {
			store := NewInMemoryKeyStore()
			past := time.Now().Add(-time.Hour)
			for i := range entries {
				var expiry time.Time
				if i >= live {
					expiry = past
				}
				store.entries[fmt.Sprint(i)] = memEntry{expiry: expiry}
			}
			b.ReportAllocs()
			for b.Loop() {
				keys, err := store.KeysContext(b.Context())
				if err != nil || len(keys) != live {
					b.Fatalf("keys=%d err=%v", len(keys), err)
				}
			}
		})
	}
}

func TestExpiredKeySnapshotAndOverwriteUseNewValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewInMemoryKeyStore()
		if err := store.SetContext(t.Context(), "fixture", []byte("first"), time.Second); err != nil {
			t.Fatal(err)
		}
		old, err := store.GetContext(t.Context(), "fixture")
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		if keys, err := store.KeysContext(t.Context()); err != nil || keys == nil || len(keys) != 0 {
			t.Fatalf("expired enumeration=%v err=%v", keys, err)
		}
		if _, err := store.GetContext(t.Context(), "fixture"); !errors.Is(err, ErrKeyExpired) {
			t.Fatalf("expired read=%v", err)
		}
		if err := store.SetContext(t.Context(), "fixture", []byte("second"), 0); err != nil {
			t.Fatal(err)
		}
		old[0] = 'X'
		if got, err := store.GetContext(t.Context(), "fixture"); err != nil || string(got) != "second" {
			t.Fatalf("overwritten value=%q err=%v", got, err)
		}
		if keys, err := store.KeysContext(t.Context()); err != nil || !slices.Equal(keys, []string{"fixture"}) {
			t.Fatalf("live enumeration=%v err=%v", keys, err)
		}
	})
}
