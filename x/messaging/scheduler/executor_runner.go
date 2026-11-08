package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/birdordev/plumego/log"
)

func (s *Scheduler) runLoop() {
	defer s.wg.Done()
	for {
		item := s.nextDue()
		if item == nil {
			select {
			case <-s.stopCh:
				return
			case <-s.wakeCh:
				continue
			}
		}

		wait := item.runAt.Sub(s.clock.Now())
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-s.wakeCh:
				timer.Stop()
				continue
			case <-s.stopCh:
				timer.Stop()
				return
			}
		}

		// Sample clock after the wait so popDue sees an accurate "now".
		now := s.clock.Now()

		// Dispatch due items
		for {
			due := s.popDue(now)
			if due == nil {
				break
			}
			s.dispatch(due.job)
		}
	}
}

func (s *Scheduler) nextDue() *scheduleItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil
	}
	return s.queue.Peek()
}

func (s *Scheduler) popDue(now time.Time) *scheduleItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	for {
		item := s.queue.PopDue(now)
		if item == nil {
			return nil
		}
		if item.job == nil {
			continue
		}
		if item.version > 0 && item.job.scheduleVersion != item.version {
			continue
		}
		return item
	}
}

func (s *Scheduler) dispatch(j *job) {
	if j == nil || j.canceled.Load() || j.paused.Load() {
		return
	}

	if j.options.OverlapPolicy == SkipIfRunning && !j.running.CompareAndSwap(false, true) {
		s.scheduleNext(j, s.clock.Now())
		return
	}
	if j.options.OverlapPolicy == SerialQueue {
		if !j.running.CompareAndSwap(false, true) {
			// Use atomic operation to set pending flag to avoid race condition
			j.pending.Store(true)
			return
		}
	}

	req := &runRequest{job: j, enqueuedAt: s.clock.Now()}

	// Handle backpressure based on configured policy
	switch s.backpressure.Policy {
	case BackpressureDrop:
		// Default behavior: try to send, drop if full
		select {
		case s.workCh <- req:
			s.stats.incQueued()
		case <-s.stopCh:
		default:
			s.handleBackpressure(j)
		}

	case BackpressureBlock:
		// Block indefinitely until space is available
		select {
		case s.workCh <- req:
			s.stats.incQueued()
		case <-s.stopCh:
		}

	case BackpressureBlockTimeout:
		if s.backpressure.Timeout <= 0 {
			// No timeout configured — fall back to non-blocking drop.
			select {
			case s.workCh <- req:
				s.stats.incQueued()
			case <-s.stopCh:
			default:
				s.handleBackpressure(j)
			}
		} else {
			timer := time.NewTimer(s.backpressure.Timeout)
			defer timer.Stop()
			select {
			case s.workCh <- req:
				s.stats.incQueued()
			case <-timer.C:
				s.handleBackpressure(j)
			case <-s.stopCh:
			}
		}
	}
}

func (s *Scheduler) handleBackpressure(j *job) {
	s.stats.incDropped()
	if s.metricsSink != nil {
		s.metricsSink.ObserveDrop(j.id)
	}
	if s.logger != nil {
		s.logger.Warn("scheduler queue full, job dropped", log.Fields{"job_id": j.id})
	}
	if s.backpressure.OnBackpressure != nil {
		s.backpressure.OnBackpressure(j.id)
	}
	// dispatch() set running=true via CAS before calling us, but the job never
	// actually executed. Clear the flag so the job can be dispatched again.
	if j.options.OverlapPolicy == SkipIfRunning || j.options.OverlapPolicy == SerialQueue {
		j.running.Store(false)
	}
	// Reschedule cron jobs for their next occurrence so they are not silently
	// lost when the worker pool is saturated.
	if j.kind == jobKindCron {
		s.scheduleNext(j, s.clock.Now())
	}
}

func (s *Scheduler) scheduleNext(j *job, base time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || j.canceled.Load() {
		return
	}
	var next time.Time
	switch j.kind {
	case jobKindCron:
		next = j.cron.Next(base)
	case jobKindDelay:
		// delay jobs run only once unless retried
		return
	}
	if next.IsZero() {
		return
	}
	// Apply jitter: randomise execution start within [0, Jitter).
	if j.options.Jitter > 0 {
		next = next.Add(s.nextJitter(j.options.Jitter))
	}
	j.runAt = next
	s.setJobStateIfIdleLocked(j, JobStateScheduled)
	s.pushScheduleItemLocked(j, next, true)
	s.wakeScheduler()
}

func (s *Scheduler) nextJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	s.rngMu.Lock()
	defer s.rngMu.Unlock()
	if s.rng == nil {
		s.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return time.Duration(s.rng.Int63n(int64(max)))
}

func (s *Scheduler) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case req := <-s.workCh:
			if req == nil || req.job == nil {
				continue
			}
			s.execute(req.job, req.enqueuedAt)
		}
	}
}

func (s *Scheduler) execute(j *job, enqueuedAt time.Time) {
	// Derive execution context from the scheduler's stop context so that
	// running tasks are notified when the scheduler shuts down.
	ctx := s.stopCtx
	cancel := func() {}
	if j.options.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, j.options.Timeout)
	}
	queueDelay := s.clock.Now().Sub(enqueuedAt)
	s.stats.addQueueDelay(queueDelay)
	start := s.clock.Now()
	s.mu.Lock()
	attempt := j.nextAttempt
	scheduledAt := j.runAt
	lastErr := j.lastError
	s.setJobStateLocked(j, JobStateRunning)
	s.mu.Unlock()
	ctx = WithJobID(ctx, j.id)
	ctx = WithJobAttempt(ctx, attempt)
	ctx = WithJobScheduledAt(ctx, scheduledAt)
	if lastErr != nil {
		ctx = WithJobLastError(ctx, lastErr)
	}
	err := func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if s.panicHandler != nil {
					s.panicHandler(ctx, j.id, recovered)
				}
				err = fmt.Errorf("panic: %v", recovered)
			}
		}()
		return j.fn(ctx)
	}()
	cancel()

	if j.options.OverlapPolicy == SkipIfRunning || j.options.OverlapPolicy == SerialQueue {
		j.running.Store(false)
	}

	s.mu.Lock()
	j.lastRun = start
	j.lastError = err
	s.mu.Unlock()

	elapsed := s.clock.Now().Sub(start)

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			// Per-job timeout fired.
			s.stats.incTimeout()
		case errors.Is(err, context.Canceled):
			// Scheduler shutting down; do not count as a task timeout.
			s.stats.incCanceled()
		}
		s.stats.incFailure()
		if s.metricsSink != nil {
			s.metricsSink.ObserveRun(j.id, elapsed, err, queueDelay)
		}
		s.handleFailure(j, err)
		return
	}

	s.stats.incSuccess()
	if s.metricsSink != nil {
		s.metricsSink.ObserveRun(j.id, elapsed, nil, queueDelay)
	}
	s.mu.Lock()
	j.nextAttempt = 1
	j.runCount++
	maxRunsReached := j.options.MaxRuns > 0 && j.runCount >= int64(j.options.MaxRuns)
	if j.kind == jobKindDelay || maxRunsReached {
		s.setJobStateLocked(j, JobStateCompleted)
	}
	// Mark dependency as succeeded so dependent-job checks see the correct status.
	s.dependencyStatus[j.id] = true
	// Use atomic operation to read and clear pending flag.
	// Guard with !j.running.Load(): if dispatch() stole the running slot between
	// j.running.Store(false) (line above) and here, the pending execution is
	// already handled by that dispatch — don't re-queue again.
	pending := j.options.OverlapPolicy == SerialQueue && j.pending.Swap(false)
	if pending && !maxRunsReached && !j.running.Load() {
		s.setJobStateIfIdleLocked(j, JobStateScheduled)
		s.pushScheduleItemLocked(j, s.clock.Now(), true)
		s.wakeScheduler()
	}
	s.mu.Unlock()

	if j.kind == jobKindCron && !pending && !maxRunsReached {
		s.scheduleNext(j, start)
	}
	if j.kind == jobKindDelay && s.store != nil {
		_ = s.store.Delete(j.id)
	}

	// Trigger dependent jobs on success. Must happen BEFORE retireJobLocked so
	// that the dependents map entry for this job is still intact.
	s.triggerDependentJobs(j.id)

	// Retire the job after dependents have been triggered. retireJobLocked only
	// stops future scheduling; it preserves the Completed state and does not
	// re-notify dependents (they were already handled above as a success).
	if maxRunsReached {
		s.mu.Lock()
		s.retireJobLocked(j.id, j)
		s.mu.Unlock()
	}
}
