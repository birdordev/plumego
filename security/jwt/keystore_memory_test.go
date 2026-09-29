package jwt

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

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
