package wait

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestForConditionReturnsImmediatelyWhenTrue(t *testing.T) {
	start := time.Now()
	ForCondition(t, time.Second, "already-true condition", func() bool { return true })
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("expected immediate return, took %s", elapsed)
	}
}

func TestForConditionPollsUntilTrue(t *testing.T) {
	var flag atomic.Bool
	go func() {
		time.Sleep(20 * time.Millisecond)
		flag.Store(true)
	}()
	ForCondition(t, 5*time.Second, "async flag", flag.Load)
}

func TestForValuePollsUntilEqual(t *testing.T) {
	var counter atomic.Int64
	go func() {
		time.Sleep(20 * time.Millisecond)
		counter.Store(42)
	}()
	ForValue(t, 5*time.Second, "counter to reach 42", counter.Load, int64(42))
}

func TestForConditionTimesOut(t *testing.T) {
	fatal := make(chan string, 1)
	mock := &recordingTB{fatal: fatal}
	start := time.Now()
	func() {
		defer func() {
			if r := recover(); r != nil && r != errFatalfCalled {
				panic(r)
			}
		}()
		ForCondition(mock, 20*time.Millisecond, "never-true condition", func() bool { return false })
	}()
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("expected to wait for the timeout, returned after %s", elapsed)
	}
	select {
	case msg := <-fatal:
		if msg == "" {
			t.Fatal("expected a failure message")
		}
	default:
		t.Fatal("expected Fatalf to be called on timeout")
	}
}

// errFatalfCalled is the sentinel panic recordingTB.Fatalf raises to mimic
// how testing.T aborts the test goroutine on failure.
var errFatalfCalled = errors.New("fatalf called")

// recordingTB is a minimal testing.TB double that captures Fatalf instead of
// aborting the test, so timeout behavior can be asserted.
type recordingTB struct {
	testing.TB
	fatal chan<- string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.fatal <- fmt.Sprintf(format, args...)
	panic(errFatalfCalled)
}
