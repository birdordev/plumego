package ratelimit_test

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/x/resilience/ratelimit"
)

func TestTokenBucket_Allow(t *testing.T) {
	b := ratelimit.New(10, 3) // 10/s, burst 3

	// Burst should be immediately available.
	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("expected token %d to be available", i+1)
		}
	}
	// Bucket is now empty.
	if b.Allow() {
		t.Fatal("expected bucket to be empty after burst")
	}
}

func TestTokenBucket_AllowN_Zero(t *testing.T) {
	b := ratelimit.New(0, 0) // disabled
	if !b.AllowN(0) {
		t.Fatal("AllowN(0) should always return true")
	}
}

func TestTokenBucket_Wait(t *testing.T) {
	b := ratelimit.New(1000, 1) // fast refill
	b.Allow()                   // drain

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	if err := b.Wait(ctx); err != nil {
		t.Fatalf("Wait timed out unexpectedly: %v", err)
	}
}

func TestTokenBucket_WaitCancelled(t *testing.T) {
	b := ratelimit.New(0.001, 1) // very slow refill
	b.Allow()                    // drain

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	if err := b.Wait(ctx); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestTokenBucket_UpdateRate(t *testing.T) {
	b := ratelimit.New(1, 1)
	b.Allow() // drain

	// Crank rate up so refill is fast.
	b.UpdateRate(10000, 10)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	if err := b.Wait(ctx); err != nil {
		t.Fatalf("Wait after UpdateRate failed: %v", err)
	}
}

func TestTokenBucket_Concurrent(t *testing.T) {
	// rate=0 disables replenishment so only the initial burst tokens are available.
	b := ratelimit.New(0, 100)
	var wg sync.WaitGroup
	allowed := make(chan bool, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed <- b.Allow()
		}()
	}
	wg.Wait()
	close(allowed)

	count := 0
	for ok := range allowed {
		if ok {
			count++
		}
	}
	if count > 100 {
		t.Errorf("allowed %d tokens but burst is 100", count)
	}
}

func TestKeyedBuckets_Allow(t *testing.T) {
	k := ratelimit.NewKeyed(100, 2)

	if !k.Allow("a") {
		t.Fatal("first Allow for key a should succeed")
	}
	if !k.Allow("a") {
		t.Fatal("second Allow for key a should succeed (burst=2)")
	}
	if k.Allow("a") {
		t.Fatal("third Allow for key a should fail (burst exceeded)")
	}

	// Different key gets its own bucket.
	if !k.Allow("b") {
		t.Fatal("key b should have its own fresh bucket")
	}
}

func TestKeyedBuckets_Delete(t *testing.T) {
	k := ratelimit.NewKeyed(100, 1)
	k.Allow("x") // drain

	k.Delete("x")
	if k.Len() != 0 {
		t.Errorf("expected 0 buckets after Delete, got %d", k.Len())
	}

	// Fresh bucket after delete.
	if !k.Allow("x") {
		t.Fatal("expect fresh bucket for x after delete")
	}
}

func TestKeyedBuckets_UpdateRate(t *testing.T) {
	k := ratelimit.NewKeyed(1, 1)
	k.Allow("z") // drain

	k.UpdateRate(10000, 10)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	if err := k.Wait(ctx, "z"); err != nil {
		t.Fatalf("Wait after UpdateRate: %v", err)
	}
}

func TestWaitOnCanceledContextDoesNotConsumeToken(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	bucket := ratelimit.New(0, 1)
	if err := bucket.Wait(ctx); err != context.Canceled {
		t.Fatalf("Wait = %v, want context.Canceled", err)
	}
	if got := bucket.Remaining(); got != 1 {
		t.Fatalf("Remaining = %d, want 1", got)
	}
	if err := bucket.WaitN(ctx, 0); err != nil {
		t.Fatalf("WaitN(0) = %v", err)
	}
}

func TestWaitNObservesLiveRateUpdate(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "updated"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bucket := ratelimit.New(0, 1)
				if !bucket.Allow() {
					t.Fatal("failed to exhaust the initial token")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				result := make(chan error, 1)
				go func() { result <- bucket.WaitN(ctx, 2) }()
				synctest.Wait()
				select {
				case err := <-result:
					t.Fatalf("WaitN returned before update: %v", err)
				default:
				}
				if canceled {
					cancel()
					if err := <-result; !errors.Is(err, context.Canceled) {
						t.Fatalf("WaitN = %v", err)
					}
					bucket.UpdateRate(2000, 2)
					time.Sleep(time.Millisecond)
					if bucket.Remaining() != 2 {
						t.Fatal("canceled waiter consumed newly available tokens")
					}
					return
				}
				bucket.UpdateRate(2000, 2)
				if err := <-result; err != nil {
					t.Fatalf("WaitN = %v", err)
				}
				if bucket.Remaining() != 0 {
					t.Fatal("wait did not consume exactly two tokens")
				}
			})
		})
	}
}

func TestNonFiniteRateDoesNotReplenishTokens(t *testing.T) {
	for _, tc := range []struct {
		name string
		rate float64
	}{
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				constructed := ratelimit.New(tc.rate, 1)
				updated := ratelimit.New(1, 1)
				updated.UpdateRate(tc.rate, 0)
				keyed := ratelimit.NewKeyed(tc.rate, 1)
				if !constructed.Allow() || !updated.Allow() || !keyed.Allow("existing") {
					t.Fatal("initial tokens were unavailable")
				}
				keyed.UpdateRate(tc.rate, 0)
				if !keyed.Allow("new") {
					t.Fatal("new key lost initial capacity")
				}
				time.Sleep(time.Second)
				if constructed.Allow() || updated.Allow() || keyed.Allow("existing") || keyed.Allow("new") {
					t.Fatal("a non-finite rate replenished an exhausted bucket")
				}
				constructed.UpdateRate(1, 0)
				updated.UpdateRate(1, 0)
				keyed.UpdateRate(1, 0)
				time.Sleep(time.Second)
				if !constructed.Allow() || !updated.Allow() || !keyed.Allow("existing") || !keyed.Allow("new") {
					t.Fatal("finite rate did not resume replenishment")
				}
			})
		})
	}
}

func TestTokenBucketRejectsAboveIntegerCapacity(t *testing.T) {
	const burst = 1 << 53
	b := ratelimit.New(0, burst)
	if b.AllowN(burst + 1) {
		t.Fatal("allowed request above capacity")
	}
	if got := b.Remaining(); got != burst {
		t.Fatalf("remaining = %d, want %d", got, burst)
	}
	if !b.AllowN(burst) {
		t.Fatal("exact capacity was not available")
	}
}

func TestKeyedWaitDoesNotAllocateCanceledBucket(t *testing.T) {
	buckets := ratelimit.NewKeyed(0, 2)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := buckets.Wait(ctx, "new"); !errors.Is(err, context.Canceled) || buckets.Len() != 0 {
		t.Fatalf("pre-canceled Wait = %v, buckets = %d", err, buckets.Len())
	}
	if err := buckets.Wait(t.Context(), "active"); err != nil || buckets.Len() != 1 {
		t.Fatalf("active Wait = %v, buckets = %d", err, buckets.Len())
	}
	if err := buckets.Wait(ctx, "active"); !errors.Is(err, context.Canceled) || buckets.Remaining("active") != 1 {
		t.Fatalf("canceled existing Wait = %v, remaining = %d", err, buckets.Remaining("active"))
	}
}

func TestTokenBucketPreservesWholeTokensAtLargeBurst(t *testing.T) {
	b := ratelimit.New(0, math.MaxInt64)
	if !b.AllowN(math.MaxInt64-1) || b.Remaining() != 1 || !b.Allow() || b.Allow() {
		t.Fatal("large debit did not leave exactly one token")
	}

	b = ratelimit.New(0, math.MaxInt64)
	b.UpdateRate(0, math.MaxInt64-1)
	if b.AllowN(math.MaxInt64) || !b.AllowN(math.MaxInt64-2) || b.Remaining() != 1 {
		t.Fatal("burst update did not preserve exact whole-token accounting")
	}

	synctest.Test(t, func(t *testing.T) {
		b := ratelimit.New(0.5, 1)
		if !b.Allow() {
			t.Fatal("initial token unavailable")
		}
		time.Sleep(time.Second)
		if b.Allow() {
			t.Fatal("admitted a partial token")
		}
		time.Sleep(time.Second)
		if !b.Allow() || b.Allow() {
			t.Fatal("two half-token refill intervals did not yield exactly one token")
		}
	})
}
