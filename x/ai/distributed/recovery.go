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

// RecoverFailedExecution attempts to recover a failed execution.
func (rm *RecoveryManager) RecoverFailedExecution(ctx context.Context, executionID string) error {
	// Load snapshot
	snapshot, err := rm.persistence.LoadSnapshot(ctx, executionID)
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}

	if snapshot.Status != StatusFailed {
		return fmt.Errorf("execution is not in failed state: %s", snapshot.Status)
	}

	// Resume from last checkpoint
	return rm.engine.Resume(ctx, executionID)
}

// MonitorTimeouts waits for cancellation. It does not currently scan for or\n// handle timed-out executions.
func (rm *RecoveryManager) MonitorTimeouts(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Check for timed-out executions
			// This requires scanning snapshots
			// For production, implement proper timeout tracking
		}
	}
}

// RetryWithPolicy retries an operation with exponential backoff.
// Cancellation prevents the next attempt from starting.
func RetryWithPolicy(
	ctx context.Context,
	policy *RetryPolicy,
	operation func() error,
) error {
	var lastErr error
	delay := policy.InitialDelay
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
