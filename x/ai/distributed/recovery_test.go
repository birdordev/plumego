package distributed

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// --- RetryWithPolicy ---

func TestRetryWithPolicy_SuccessFirstTry(t *testing.T) {
	calls := 0
	err := RetryWithPolicy(t.Context(), &RetryPolicy{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, Multiplier: 1}, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryWithPolicy_SuccessOnRetry(t *testing.T) {
	calls := 0
	err := RetryWithPolicy(t.Context(), &RetryPolicy{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, Multiplier: 1}, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryWithPolicy_ExhaustsRetries(t *testing.T) {
	sentinel := errors.New("persistent")
	calls := 0
	err := RetryWithPolicy(t.Context(), &RetryPolicy{MaxRetries: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, Multiplier: 1}, func() error {
		calls++
		return sentinel
	})
	if err == nil {
		t.Error("expected error after exhausting retries")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected error to wrap sentinel, got: %v", err)
	}
	if calls != 3 { // initial + 2 retries
		t.Errorf("expected 3 calls (1 + 2 retries), got %d", calls)
	}
}

func TestRetryWithPolicy_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already cancelled

	err := RetryWithPolicy(ctx, &RetryPolicy{MaxRetries: 5, InitialDelay: time.Second, MaxDelay: time.Second, Multiplier: 1}, func() error {
		return errors.New("fail")
	})
	// Either context.Canceled or wrapped operation error — must not hang
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestRetryWithPolicy_ZeroRetries(t *testing.T) {
	sentinel := errors.New("fail once")
	calls := 0
	err := RetryWithPolicy(t.Context(), &RetryPolicy{MaxRetries: 0, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, Multiplier: 1}, func() error {
		calls++
		return sentinel
	})
	if err == nil {
		t.Error("expected error with zero retries")
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 call with MaxRetries=0, got %d", calls)
	}
}

func TestRetryWithPolicy_MaxDelayClamp(t *testing.T) {
	// Multiplier would grow delay past MaxDelay; verify it clamps and does not block.
	calls := 0
	err := RetryWithPolicy(t.Context(), &RetryPolicy{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, Multiplier: 100}, func() error {
		calls++
		return errors.New("fail")
	})
	if err == nil {
		t.Error("expected error")
	}
	if calls != 4 { // 1 + 3 retries
		t.Errorf("expected 4 calls, got %d", calls)
	}
}

// --- RecoverStaleTasks (stub — always returns 0, nil) ---

func TestRecoveryManager_RecoverStaleTasks_Stub(t *testing.T) {
	rm := &RecoveryManager{}
	n, err := rm.RecoverStaleTasks(t.Context(), time.Minute)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

// --- MonitorTimeouts context cancellation ---

func TestRecoveryManager_MonitorTimeouts_ContextCancel(t *testing.T) {
	rm := &RecoveryManager{}
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- rm.MonitorTimeouts(ctx, 10*time.Millisecond)
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("MonitorTimeouts did not return after context cancellation")
	}
}

func TestRetryWithPolicySkipsCanceledOperation(t *testing.T) {
	t.Run("before first attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := 0
		err := RetryWithPolicy(ctx, &RetryPolicy{}, func() error { calls++; return nil })
		if calls != 0 || !errors.Is(err, context.Canceled) {
			t.Errorf("calls = %d, error = %v", calls, err)
		}
	})
	t.Run("during retry wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan struct{})
			done := make(chan error, 1)
			calls := 0
			go func() {
				done <- RetryWithPolicy(ctx, &RetryPolicy{MaxRetries: 3, InitialDelay: time.Hour}, func() error {
					calls++
					if calls == 1 {
						close(started)
					}
					return errors.New("test operation failure")
				})
			}()
			<-started
			synctest.Wait()
			cancel()
			if err := <-done; calls != 1 || !errors.Is(err, context.Canceled) {
				t.Errorf("calls = %d, error = %v", calls, err)
			}
		})
	})
}
