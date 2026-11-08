package ratelimit_test

import (
	"context"
	"fmt"

	"github.com/birdordev/plumego/x/resilience/ratelimit"
)

func ExampleNew() {
	// Allow 100 requests per second with a burst capacity of 200.
	bucket := ratelimit.New(100, 200)

	// The bucket starts full, so the first allow call succeeds.
	fmt.Println(bucket.Allow())

	// Output:
	// true
}

func ExampleTokenBucket_Allow() {
	// Burst of 2 means only the first 2 tokens are available immediately.
	bucket := ratelimit.New(10, 2)

	fmt.Println(bucket.Allow()) // token 1
	fmt.Println(bucket.Allow()) // token 2
	fmt.Println(bucket.Allow()) // bucket empty → denied

	// Output:
	// true
	// true
	// false
}

func ExampleTokenBucket_Wait() {
	bucket := ratelimit.New(1000, 1)

	ctx := context.Background()
	// Consume the single burst token.
	_ = bucket.Allow()

	// Wait blocks until a token is available; with rate=1000/s the wait is
	// sub-millisecond, so this completes instantly in tests.
	err := bucket.Wait(ctx)
	fmt.Println(err)

	// Output:
	// <nil>
}

func ExampleNewKeyed() {
	// Per-key limiter: each unique key gets its own 10 req/s bucket, burst 10.
	keyed := ratelimit.NewKeyed(10, 10)

	// Different keys are independent.
	fmt.Println(keyed.Allow("user-1"))
	fmt.Println(keyed.Allow("user-2"))

	// Output:
	// true
	// true
}
