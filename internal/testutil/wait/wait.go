// Package wait provides polling helpers for tests that must observe
// asynchronous state (background goroutines, timers, cleanup loops).
//
// Tests should prefer these helpers over time.Sleep: sleeping a fixed
// duration both slows the suite and flakes under load, while polling returns
// as soon as the condition holds and fails with a useful message otherwise.
package wait

import (
	"testing"
	"time"
)

// pollInterval is the delay between condition checks. One millisecond keeps
// fast-path tests snappy while leaving the goroutine scheduler plenty of
// room to run the code under test.
const pollInterval = time.Millisecond

// ForCondition polls cond until it returns true or timeout elapses, failing
// the test with desc on timeout. The timeout is a failure budget, not an
// expected duration — choose it generously (e.g. 5-10x the worst case) since
// the helper returns as soon as cond holds.
func ForCondition(t testing.TB, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, desc)
		}
		time.Sleep(pollInterval)
	}
}

// ForValue polls get until the returned value equals want or the timeout
// elapses, failing the test with the last observed value on timeout.
func ForValue[T comparable](t testing.TB, timeout time.Duration, desc string, get func() T, want T) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last T
	for {
		last = get()
		if last == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s: last value %v, want %v", timeout, desc, last, want)
		}
		time.Sleep(pollInterval)
	}
}
