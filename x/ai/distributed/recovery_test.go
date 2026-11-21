package distributed

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
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
	synctest.Test(t, func(t *testing.T) {
		sentinel := errors.New("test retry failure")
		start := time.Now()
		var attempts []time.Duration
		err := RetryWithPolicy(t.Context(), &RetryPolicy{
			MaxRetries: 4, InitialDelay: time.Second, MaxDelay: 3 * time.Second, Multiplier: 2,
		}, func() error {
			attempts = append(attempts, time.Since(start))
			return sentinel
		})
		want := []time.Duration{0, time.Second, 3 * time.Second, 6 * time.Second, 9 * time.Second}
		if !errors.Is(err, sentinel) || !slices.Equal(attempts, want) {
			t.Errorf("attempts = %v, error = %v; want %v and %v", attempts, err, want, sentinel)
		}
	})
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

type recoverySnapshotBackend struct {
	WorkflowPersistence
	snapshot *ExecutionSnapshot
	err      error
}

func (p recoverySnapshotBackend) LoadSnapshot(context.Context, string) (*ExecutionSnapshot, error) {
	return p.snapshot, p.err
}

func TestRecoveryManagerRejectsInvalidSnapshots(t *testing.T) {
	readErr := errors.New("read failed")
	for _, tc := range []struct {
		name     string
		snapshot *ExecutionSnapshot
		err      error
	}{
		{"missing", nil, nil},
		{"wrong execution", &ExecutionSnapshot{ExecutionID: "other", Status: StatusFailed}, nil},
		{"non-failed", &ExecutionSnapshot{ExecutionID: "exec", Status: StatusCompleted}, nil},
		{"adapter error", nil, readErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rm := NewRecoveryManager(recoverySnapshotBackend{snapshot: tc.snapshot, err: tc.err}, nil, nil)
			err := rm.RecoverFailedExecution(t.Context(), "exec")
			if err == nil || (tc.err != nil && !errors.Is(err, tc.err)) {
				t.Fatalf("RecoverFailedExecution = %v", err)
			}
		})
	}
	synctest.Test(t, func(t *testing.T) {
		p := &engineTestPersistence{snapshot: ExecutionSnapshot{WorkflowID: "workflow", ExecutionID: "exec", Status: StatusFailed}}
		results := make(chan *TaskResult, 1)
		q := engineTestQueue{
			subscribe: func(context.Context, string) (<-chan *TaskResult, error) { return results, nil },
			enqueue: func(_ context.Context, task *WorkflowTask) error {
				results <- &TaskResult{TaskID: task.ID, AgentResult: &orchestration.AgentResult{Output: "done"}}
				return nil
			},
		}
		engine := newEngineForTest(q, p, engineTestStep(nil))
		if err := NewRecoveryManager(p, q, engine).RecoverFailedExecution(t.Context(), "exec"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		snapshot, err := p.LoadSnapshot(t.Context(), "exec")
		if err != nil || snapshot.Status != StatusCompleted {
			t.Fatalf("recovered = %+v, %v", snapshot, err)
		}
	})
}

func TestMonitorTimeoutsWaitsOnlyForCancellation(t *testing.T) {
	for _, interval := range []time.Duration{-time.Second, 0, time.Second} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			// No persistence or queue is required for the cancellation-only wait.
			go func() { result <- new(RecoveryManager).MonitorTimeouts(ctx, interval) }()
			synctest.Wait()
			select {
			case err := <-result:
				t.Fatalf("interval %v returned early: %v", interval, err)
			default:
			}
			cancel()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("interval %v: %v", interval, err)
			}
		})
	}
}

func TestRetryWithPolicyRejectsInvalidInputs(t *testing.T) {
	calls := 0
	operation := func() error { calls++; return nil }
	for _, tc := range []struct {
		name      string
		policy    *RetryPolicy
		operation func() error
		want      string
	}{
		{"nil policy", nil, operation, "retry policy is required"},
		{"nil operation", DefaultRetryPolicy(), nil, "retry operation is required"},
		{"negative count", &RetryPolicy{MaxRetries: -1}, operation, "retry policy MaxRetries must be non-negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := RetryWithPolicy(t.Context(), tc.policy, tc.operation); err == nil || err.Error() != tc.want {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
	if calls != 0 {
		t.Errorf("operation called %d times for invalid inputs", calls)
	}
}

func TestRetryWithPolicyCapsFirstDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		n := 0
		err := RetryWithPolicy(t.Context(), &RetryPolicy{MaxRetries: 1, InitialDelay: time.Minute, MaxDelay: time.Second, Multiplier: 2}, func() error {
			n++
			if n == 1 {
				return errors.New("retry")
			}
			return nil
		})
		if elapsed := time.Since(start); err != nil || elapsed != time.Second {
			t.Errorf("err=%v elapsed=%v", err, elapsed)
		}
	})
}
