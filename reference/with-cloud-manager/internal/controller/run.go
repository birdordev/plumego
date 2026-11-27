package controller

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

// Publish 在同一事务写入受租约保护的结果。厂商请求在 Execute 中完成。
type Publish func(context.Context, *sql.Tx) error

type Execute func(context.Context, Task) (Publish, error)

type schedulePool uint8

const (
	inventoryPool schedulePool = iota
	workloadWatchPool
	idleAnalysisPool
)

func (s Store) Run(ctx context.Context, execute Execute) error {
	return s.run(ctx, execute, inventoryPool)
}

func (s Store) RunIdleAnalysis(ctx context.Context, execute Execute) error {
	return s.run(ctx, execute, idleAnalysisPool)
}

func (s Store) RunWorkloadWatch(parent context.Context, execute Execute) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			if err := s.run(ctx, execute, workloadWatchPool); err != nil {
				cancel(err)
			}
		})
	}
	workers.Wait()
	cause := context.Cause(ctx)
	if parent.Err() != nil && errors.Is(cause, context.Cause(parent)) {
		return nil
	}
	return cause
}

func (s Store) run(ctx context.Context, execute Execute, pool schedulePool) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		worked, err := s.step(ctx, execute, pool)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return nil
			}
			return err
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
	return nil
}

func (s Store) step(ctx context.Context, execute Execute, pool schedulePool) (bool, error) {
	lease, timeout := 11*time.Minute, 9*time.Minute
	switch pool {
	case workloadWatchPool:
		lease, timeout = 4*time.Minute, 3*time.Minute
	case idleAnalysisPool:
		lease, timeout = 2*time.Minute, 90*time.Second
	}
	j, err := s.claim(ctx, lease, pool)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	attempt, cancel := context.WithTimeout(ctx, timeout)
	publish, executionErr := execute(attempt, j.task)
	if executionErr == nil {
		executionErr = attempt.Err()
	}
	partialBucketBatch := isBucketSettingSchedule(j.task.ScheduleKind) && provider.IsFailure(executionErr, provider.Throttled) && attempt.Err() == nil
	if executionErr != nil && !partialBucketBatch {
		publish = nil
	}
	cancel()
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	outcome, delay, failures := nextAttempt(j, executionErr)
	if ctx.Err() != nil {
		return true, s.release(cleanup, j, accountCooldown(outcome, delay))
	}
	err = s.finish(cleanup, j, outcome, delay, failures, publish)
	if publish != nil && errors.Is(err, inventory.ErrStale) {
		outcome, delay, failures = nextAttempt(j, err)
		err = s.finish(cleanup, j, outcome, delay, failures, nil)
	}
	if errors.Is(err, ErrStale) {
		return true, s.release(cleanup, j, accountCooldown(outcome, delay))
	}
	return true, err
}

func nextAttempt(j job, cause error) (string, time.Duration, int) {
	if cause == nil {
		if j.task.ScheduleKind == WorkloadWatchScheduleKind {
			return "success", time.Second, 0
		}
		return "success", j.interval, 0
	}
	failures := min(j.failures+1, 16)
	delay := min(15*time.Second<<min(failures-1, 6), 15*time.Minute)
	var failure *provider.Failure
	if j.task.ScheduleKind == IdleScheduleKind {
		delay = max(delay, time.Minute)
	}
	if errors.As(cause, &failure) && failure.Kind == provider.Throttled {
		return "throttled", max(delay, min(failure.RetryAfter, 24*time.Hour)), failures
	}
	return "failure", delay, failures
}
