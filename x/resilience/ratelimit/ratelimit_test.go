package ratelimit_test

import (
	"context"
	"sync"
	"testing"
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
