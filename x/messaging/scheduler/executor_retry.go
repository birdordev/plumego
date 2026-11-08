package scheduler

import (
	"context"
	"time"

	"github.com/birdordev/plumego/log"
)

func (s *Scheduler) handleFailure(j *job, err error) {
	s.mu.Lock()
	policy := j.options.RetryPolicy
	dlq := j.options.DeadLetter
	attempt := j.nextAttempt
	if policy.MaxAttempts > 1 && attempt < policy.MaxAttempts {
		j.nextAttempt++
	}
	s.mu.Unlock()

	retriesExhausted := policy.MaxAttempts <= 1 || attempt >= policy.MaxAttempts
	if retriesExhausted {
		finalAttempt := attempt
		if policy.MaxAttempts <= 1 {
			finalAttempt = 1
		}
		s.handleRetriesExhausted(j, err, finalAttempt, dlq)
		return
	}

	s.mu.Lock()
	s.setJobStateLocked(j, JobStateRetrying)
	s.mu.Unlock()

	backoff := time.Duration(0)
	if policy.Backoff != nil {
		backoff = policy.Backoff(attempt + 1)
		if policy.MaxBackoff > 0 && backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	} else if policy.Kind != "" {
		switch policy.Kind {
		case "fixed":
			backoff = policy.BaseDelay
		case "exponential":
			if policy.BaseDelay > 0 {
				backoff = safeExponentialBackoff(policy.BaseDelay, policy.MaxBackoff, attempt)
			}
		}
		if policy.MaxBackoff > 0 && backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	}
	s.mu.Lock()
	s.pushScheduleItemLocked(j, s.clock.Now().Add(backoff), true)
	s.mu.Unlock()
	if s.metricsSink != nil {
		s.metricsSink.ObserveRetry(j.id, attempt+1, backoff)
	}

	s.wakeScheduler()

	if s.logger != nil {
		s.logger.Warn("scheduler job failed", log.Fields{
			"job_id":  j.id,
			"error":   err,
			"attempt": attempt,
		})
	}
	s.stats.incRetry()
}

// handleRetriesExhausted handles the case when a job has no more retry attempts left.
func (s *Scheduler) handleRetriesExhausted(j *job, err error, attempts int, deadLetterCb func(context.Context, JobID, error)) {
	if j.kind == jobKindDelay {
		s.mu.Lock()
		s.setJobStateLocked(j, JobStateFailed)
		s.mu.Unlock()
	}
	if s.dlq != nil {
		s.addToDeadLetterQueue(j, err, attempts)
	}
	if deadLetterCb != nil {
		deadLetterCb(context.Background(), j.id, err)
	}
	if j.kind == jobKindCron {
		s.scheduleNext(j, s.clock.Now())
	}
	if j.kind == jobKindDelay && s.store != nil {
		_ = s.store.Delete(j.id)
	}
	// Only notify dependents of failure after all retry attempts are exhausted.
	// Moving this here (rather than on every failure) prevents dependents from
	// being marked failed when the job succeeds on a subsequent retry attempt.
	s.mu.Lock()
	s.dependencyStatus[j.id] = false
	s.mu.Unlock()
	s.handleDependentJobsOnFailure(j)
}

// addToDeadLetterQueue adds a failed job to the dead letter queue.
func (s *Scheduler) addToDeadLetterQueue(j *job, err error, attempts int) {
	now := s.clock.Now()
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	entry := DeadLetterEntry{
		JobID:        j.id,
		Error:        err,
		ErrorMessage: errMsg,
		Attempts:     attempts,
		FirstFailed:  now,
		LastFailed:   now,
		TaskName:     j.options.TaskName,
		Group:        j.options.Group,
		Tags:         append([]string(nil), j.options.Tags...), // defensive copy
	}
	s.dlq.Add(entry)
}
