package controller

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type Scan func(context.Context, inventory.Key, string, string) error

func (s Store) Run(ctx context.Context, scan Scan) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		worked, err := s.step(ctx, scan)
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

func (s Store) step(ctx context.Context, scan Scan) (bool, error) {
	j, err := s.claim(ctx, 11*time.Minute)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	attempt, cancel := context.WithTimeout(ctx, 9*time.Minute)
	scanErr := scan(attempt, j.key, j.provider, j.credentialRef)
	cancel()
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return true, s.release(cleanup, j)
	}
	outcome, delay, failures := nextAttempt(j, scanErr)
	err = s.finish(cleanup, j, outcome, delay, failures)
	if errors.Is(err, ErrStale) {
		return true, nil
	}
	return true, err
}

func nextAttempt(j job, cause error) (string, time.Duration, int) {
	if cause == nil {
		return "success", j.interval, 0
	}
	failures := min(j.failures+1, 16)
	delay := min(15*time.Second<<min(failures-1, 6), 15*time.Minute)
	var failure *provider.Failure
	if errors.As(cause, &failure) && failure.Kind == provider.Throttled {
		return "throttled", max(delay, min(failure.RetryAfter, 24*time.Hour)), failures
	}
	return "failure", delay, failures
}
