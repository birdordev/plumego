package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/birdordev/plumego/x/messaging/scheduler"
)

// registerScheduledJobs sets up periodic maintenance tasks on the scheduler.
func (s *Service) registerScheduledJobs() error {
	if s.scheduler == nil {
		return nil
	}
	var regErrs []error

	// Retry dead-letter tasks every hour.
	if _, err := s.scheduler.AddCron("messaging.dlq-retry", "0 * * * *", s.retryDeadLetters,
		scheduler.WithTimeout(5*time.Minute),
		scheduler.WithOverlapPolicy(scheduler.SkipIfRunning),
		scheduler.WithGroup("messaging"),
		scheduler.WithTags("dlq", "retry"),
	); err != nil {
		wrapped := fmt.Errorf("messaging: register scheduled job messaging.dlq-retry: %w", err)
		regErrs = append(regErrs, wrapped)
		if s.logger != nil {
			s.logger.Error("failed to register scheduled job", log.Fields{
				"job_id": "messaging.dlq-retry",
				"error":  err.Error(),
			})
		}
	}

	// Log queue stats every 5 minutes.
	if _, err := s.scheduler.AddCron("messaging.stats-log", "*/5 * * * *", s.logStats,
		scheduler.WithTimeout(30*time.Second),
		scheduler.WithOverlapPolicy(scheduler.SkipIfRunning),
		scheduler.WithGroup("messaging"),
		scheduler.WithTags("stats"),
	); err != nil {
		wrapped := fmt.Errorf("messaging: register scheduled job messaging.stats-log: %w", err)
		regErrs = append(regErrs, wrapped)
		if s.logger != nil {
			s.logger.Error("failed to register scheduled job", log.Fields{
				"job_id": "messaging.stats-log",
				"error":  err.Error(),
			})
		}
	}
	return errors.Join(regErrs...)
}

// retryDeadLetters replays dead-letter tasks back into the queue.
func (s *Service) retryDeadLetters(ctx context.Context) error {
	replayer, ok := s.store.(store.DLQReplayer)
	if !ok {
		return nil
	}
	result, err := replayer.ReplayDLQ(ctx, store.ReplayOptions{
		Max:           100,
		Now:           time.Now(),
		AvailableAt:   time.Now(),
		ResetAttempts: true,
	})
	if err != nil {
		return err
	}
	if s.logger != nil && result.Replayed > 0 {
		s.logger.Info("dlq replay completed", log.Fields{
			"replayed":  result.Replayed,
			"remaining": result.Remaining,
		})
	}
	return nil
}

// logStats writes queue depth to the logger.
func (s *Service) logStats(ctx context.Context) error {
	stats, err := s.queue.Stats(ctx)
	if err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.Info("messaging queue stats", log.Fields{
			"queued":       stats.Queued,
			"leased":       stats.Leased,
			"dead":         stats.Dead,
			"expired":      stats.Expired,
			"total_sent":   s.totalSent.Load(),
			"total_failed": s.totalFailed.Load(),
		})
	}
	return nil
}
