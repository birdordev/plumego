package distributed

import (
	"context"
	"fmt"
	"time"
)

// RecoveryManager handles fault tolerance and task recovery.
type RecoveryManager struct {
	persistence WorkflowPersistence
	taskQueue   TaskQueue
	engine      *DistributedEngine
}

// NewRecoveryManager creates a new recovery manager.
func NewRecoveryManager(
	persistence WorkflowPersistence,
	taskQueue TaskQueue,
	engine *DistributedEngine,
) *RecoveryManager {
	return &RecoveryManager{
		persistence: persistence,
		taskQueue:   taskQueue,
		engine:      engine,
	}
}

// RecoverStaleTasks is reserved for stale-task scanning. It currently returns\n// zero without scanning or retrying tasks.
func (rm *RecoveryManager) RecoverStaleTasks(ctx context.Context, staleThreshold time.Duration) (int, error) {
	// This would require scanning the KV store for tasks that haven't been updated
	// within the threshold. For now, return 0 as this requires key scanning support.
	return 0, nil
}

// RecoverFailedExecution resumes a failed execution with a matching snapshot.
// Missing snapshots and executions in other states return an error.
func (rm *RecoveryManager) RecoverFailedExecution(ctx context.Context, executionID string) error {
	// Load snapshot
	snapshot, err := rm.persistence.LoadSnapshot(ctx, executionID)
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}

	if snapshot == nil || snapshot.ExecutionID != executionID {
		return fmt.Errorf("persisted snapshot does not match execution %s", executionID)
	}
	if snapshot.Status != StatusFailed {
		return fmt.Errorf("execution is not in failed state: %s", snapshot.Status)
	}

	// Resume from last checkpoint
	return rm.engine.Resume(ctx, executionID)
}

// MonitorTimeouts waits for cancellation. The interval argument is reserved
// for future timeout checks.
func (rm *RecoveryManager) MonitorTimeouts(ctx context.Context, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

// RetryWithPolicy retries an operation with exponential backoff.
// Cancellation prevents the next attempt from starting. A nil policy, nil
// operation, or negative MaxRetries returns an argument error. A positive
// MaxDelay caps every retry delay, including the first.
func RetryWithPolicy(
	ctx context.Context,
	policy *RetryPolicy,
	operation func() error,
) error {
	if policy == nil {
		return fmt.Errorf("retry policy is required")
	}
	if operation == nil {
		return fmt.Errorf("retry operation is required")
	}
	if policy.MaxRetries < 0 {
		return fmt.Errorf("retry policy MaxRetries must be non-negative")
	}
	var lastErr error
	delay := policy.InitialDelay
	if policy.MaxDelay > 0 {
		delay = min(delay, policy.MaxDelay)
	}
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for i := 0; i <= policy.MaxRetries; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = operation()
		if lastErr == nil {
			return nil
		}

		if i < policy.MaxRetries {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				// Calculate next delay with exponential backoff
				delay = time.Duration(float64(delay) * policy.Multiplier)
				if delay > policy.MaxDelay {
					delay = policy.MaxDelay
				}
			}
		}
	}

	return fmt.Errorf("operation failed after %d retries: %w", policy.MaxRetries, lastErr)
}
