package scheduler

import (
	"fmt"
	"time"

	"github.com/birdordev/plumego/log"
)

// AddCron registers a cron job.
// Supports standard cron syntax, @descriptors (@hourly, @daily, etc.), and @every duration.
// Examples:
//   - "0 * * * *"  - every hour
//   - "@daily"     - every day at midnight
//   - "@every 5m"  - every 5 minutes
func (s *Scheduler) AddCron(id JobID, spec string, task TaskFunc, opts ...JobOption) (JobID, error) {
	parsed, err := ParseCronSpec(spec)
	if err != nil {
		return "", err
	}
	return s.addJob(id, jobKindCron, task, parsed, time.Time{}, opts...)
}

// AddCronWithLocation registers a cron job with a specific timezone.
// This allows scheduling jobs in timezones other than UTC.
// Example: AddCronWithLocation("daily-report", "0 9 * * *", task, time.FixedZone("EST", -5*3600))
func (s *Scheduler) AddCronWithLocation(id JobID, spec string, task TaskFunc, location *time.Location, opts ...JobOption) (JobID, error) {
	if location == nil {
		location = time.UTC
	}
	parsed, err := ParseCronSpecWithLocation(spec, location)
	if err != nil {
		return "", err
	}
	return s.addJob(id, jobKindCron, task, parsed, time.Time{}, opts...)
}

// Schedule registers a job to run at a specific time.
func (s *Scheduler) Schedule(id JobID, runAt time.Time, task TaskFunc, opts ...JobOption) (JobID, error) {
	if runAt.IsZero() {
		return "", ErrRunAtRequired
	}
	return s.addJob(id, jobKindDelay, task, CronSpec{}, runAt, opts...)
}

// Delay registers a job to run after the specified delay.
func (s *Scheduler) Delay(id JobID, delay time.Duration, task TaskFunc, opts ...JobOption) (JobID, error) {
	return s.Schedule(id, s.clock.Now().Add(delay), task, opts...)
}

// TriggerNow immediately queues a job for execution regardless of its next
// scheduled time. For cron jobs the normal schedule is not affected; the job
// will also run at its next cron-calculated time. For delay jobs the
// behaviour is equivalent to re-running an already-queued job.
//
// Returns ErrJobNotFound if the job does not exist, ErrSchedulerClosed if
// the scheduler has been stopped.
func (s *Scheduler) TriggerNow(id JobID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSchedulerClosed
	}
	j, exists := s.jobs[id]
	if !exists || j.canceled.Load() {
		return ErrJobNotFound
	}
	// Delay jobs are one-shot; reject TriggerNow once they've completed.
	if j.kind == jobKindDelay && j.state == JobStateCompleted {
		return fmt.Errorf("scheduler: delay job %q has already completed", id)
	}
	if j.paused.Load() {
		return fmt.Errorf("scheduler: job %q is paused", id)
	}
	invalidateExisting := j.kind == jobKindDelay
	s.pushScheduleItemLocked(j, s.clock.Now(), invalidateExisting)
	s.wakeScheduler()
	return nil
}

// UpdateCron updates the cron expression of a registered cron job.
// The new schedule takes effect immediately; the next run time is recalculated
// from now. Returns ErrJobNotFound if the job does not exist or is not a cron
// job. Returns an error if the new expression cannot be parsed.
func (s *Scheduler) UpdateCron(id JobID, spec string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSchedulerClosed
	}
	j, exists := s.jobs[id]
	if !exists || j.canceled.Load() {
		return ErrJobNotFound
	}
	if j.kind != jobKindCron {
		return fmt.Errorf("scheduler: job %q is not a cron job", id)
	}
	parsed, err := ParseCronSpecWithLocation(spec, j.cron.location)
	if err != nil {
		return err
	}
	j.cron = parsed
	j.cronExpr = parsed.Expr()
	next := parsed.Next(s.clock.Now())
	if next.IsZero() {
		return fmt.Errorf("scheduler: new spec %q yields no future run time", spec)
	}
	j.runAt = next
	if !j.paused.Load() {
		s.pushScheduleItemLocked(j, next, true)
		s.wakeScheduler()
	}
	return nil
}

// Cancel removes a job from the scheduler.
func (s *Scheduler) Cancel(id JobID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	s.cancelJobLocked(id, j)
	return true
}

// Pause marks a job as paused.
func (s *Scheduler) Pause(id JobID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	j.paused.Store(true)
	return true
}

// Resume unpauses a job and schedules its next run.
func (s *Scheduler) Resume(id JobID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.canceled.Load() {
		return false
	}
	if !j.paused.Load() {
		return true
	}
	s.resumeJobLocked(j)
	return true
}

// PauseByGroup pauses all jobs in the specified group.
// Returns the number of jobs paused.
func (s *Scheduler) PauseByGroup(group string) int {
	if group == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(
		func(_ JobID, j *job) bool { return j.options.Group == group && !j.paused.Load() },
		func(_ JobID, j *job) { j.paused.Store(true) },
	)
}

// PauseByTags pauses all jobs that have ALL of the specified tags.
// Returns the number of jobs paused.
func (s *Scheduler) PauseByTags(tags ...string) int {
	if len(tags) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(
		func(_ JobID, j *job) bool { return hasAllTags(j.options.Tags, tags) && !j.paused.Load() },
		func(_ JobID, j *job) { j.paused.Store(true) },
	)
}

// ResumeByGroup resumes all paused jobs in the specified group.
// Returns the number of jobs resumed.
func (s *Scheduler) ResumeByGroup(group string) int {
	if group == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(
		func(_ JobID, j *job) bool { return j.options.Group == group && j.paused.Load() && !j.canceled.Load() },
		func(_ JobID, j *job) { s.resumeJobLocked(j) },
	)
}

// ResumeByTags resumes all paused jobs that have ALL of the specified tags.
// Returns the number of jobs resumed.
func (s *Scheduler) ResumeByTags(tags ...string) int {
	if len(tags) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(
		func(_ JobID, j *job) bool {
			return hasAllTags(j.options.Tags, tags) && j.paused.Load() && !j.canceled.Load()
		},
		func(_ JobID, j *job) { s.resumeJobLocked(j) },
	)
}

// CancelByGroup cancels all jobs in the specified group.
// Returns the number of jobs canceled.
func (s *Scheduler) CancelByGroup(group string) int {
	if group == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(
		func(_ JobID, j *job) bool { return j.options.Group == group },
		func(id JobID, j *job) { s.cancelJobLocked(id, j) },
	)
}

// CancelByTags cancels all jobs that have ALL of the specified tags.
// Returns the number of jobs canceled.
func (s *Scheduler) CancelByTags(tags ...string) int {
	if len(tags) == 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyBatchLocked(
		func(_ JobID, j *job) bool { return hasAllTags(j.options.Tags, tags) },
		func(id JobID, j *job) { s.cancelJobLocked(id, j) },
	)
}

// applyBatchLocked iterates all jobs and applies mutator to matched jobs.
// s.mu must be held by caller.
func (s *Scheduler) applyBatchLocked(match func(JobID, *job) bool, mutate func(JobID, *job)) int {
	if match == nil || mutate == nil {
		return 0
	}
	count := 0
	for id, j := range s.jobs {
		if !match(id, j) {
			continue
		}
		mutate(id, j)
		count++
	}
	return count
}

// hasAllTags checks if jobTags contains all of the required tags.
func hasAllTags(jobTags, required []string) bool {
	if len(required) == 0 {
		return false
	}
	tagSet := make(map[string]bool, len(jobTags))
	for _, tag := range jobTags {
		tagSet[tag] = true
	}
	for _, req := range required {
		if !tagSet[req] {
			return false
		}
	}
	return true
}

func (s *Scheduler) addJob(id JobID, kind jobKind, task TaskFunc, spec CronSpec, runAt time.Time, opts ...JobOption) (JobID, error) {
	if task == nil {
		return "", ErrTaskNil
	}
	if id == "" {
		id = JobID(fmt.Sprintf("job-%d", s.clock.Now().UnixNano()))
	}
	jobOpts := defaultJobOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&jobOpts)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrSchedulerClosed
	}
	var existing *job
	if current, exists := s.jobs[id]; exists {
		existing = current
		if !jobOpts.Replace && !existing.canceled.Load() {
			return "", ErrJobExists
		}
	}

	j := &job{
		id:          id,
		kind:        kind,
		cron:        spec,
		runAt:       runAt,
		fn:          task,
		options:     jobOpts,
		nextAttempt: 1,
	}
	if kind == jobKindCron {
		j.runAt = spec.Next(s.clock.Now())
		j.cronExpr = spec.Expr()
	}
	if kind == jobKindDelay && runAt.Before(s.clock.Now()) {
		j.runAt = s.clock.Now()
	}
	j.paused.Store(jobOpts.Paused)
	s.setJobStateLocked(j, JobStateQueued)

	// Validate dependencies
	for _, depID := range jobOpts.Dependencies {
		if depID == id {
			return "", ErrSelfDependency
		}
		if _, exists := s.jobs[depID]; !exists {
			return "", fmt.Errorf("%w: %s", ErrDependencyNotFound, depID)
		}
	}
	if s.wouldCreateDependencyCycleLocked(id, jobOpts.Dependencies) {
		return "", fmt.Errorf("%w: %s", ErrDependencyCycle, id)
	}
	if existing != nil {
		// Use cancelJobLocked to ensure full cleanup: dependency maps,
		// dependents reverse-index, and the persistence store are all
		// cleared before the replacement job is registered.
		s.cancelJobLocked(id, existing)
	}

	s.jobs[id] = j

	// Build dependents map (reverse mapping)
	for _, depID := range jobOpts.Dependencies {
		s.dependents[depID] = append(s.dependents[depID], id)
	}

	if !j.paused.Load() && len(jobOpts.Dependencies) == 0 {
		// Only schedule if no dependencies or dependencies already met
		s.pushScheduleLocked(j, j.runAt)
	} else if len(jobOpts.Dependencies) > 0 {
		// Job has dependencies, will be triggered when dependencies complete
		if s.logger != nil {
			s.logger.Info("job has dependencies, waiting for completion", log.Fields{
				"job_id":       id,
				"dependencies": jobOpts.Dependencies,
			})
		}
	}

	s.persistJobLocked(j)
	return id, nil
}

func (s *Scheduler) pushScheduleLocked(j *job, runAt time.Time) {
	if j != nil && !j.canceled.Load() {
		s.setJobStateIfIdleLocked(j, JobStateScheduled)
	}
	s.pushScheduleItemLocked(j, runAt, true)
	s.wakeScheduler()
}

func (s *Scheduler) pushScheduleItemLocked(j *job, runAt time.Time, invalidateExisting bool) {
	if j == nil {
		return
	}
	version := uint64(0)
	if invalidateExisting {
		j.scheduleVersion++
		version = j.scheduleVersion
	}
	s.queue.PushSchedule(&scheduleItem{runAt: runAt, job: j, version: version})
}

// ListDeadLetters returns all dead letter entries.
// Returns nil if DLQ is not enabled.
func (s *Scheduler) ListDeadLetters() []DeadLetterEntry {
	if s.dlq == nil {
		return nil
	}
	return s.dlq.List()
}

// GetDeadLetter retrieves a specific dead letter entry.
// Returns nil if DLQ is not enabled or entry not found.
func (s *Scheduler) GetDeadLetter(jobID JobID) (*DeadLetterEntry, bool) {
	if s.dlq == nil {
		return nil, false
	}
	return s.dlq.Get(jobID)
}

// DeleteDeadLetter removes a dead letter entry.
// Returns false if DLQ is not enabled or entry not found.
func (s *Scheduler) DeleteDeadLetter(jobID JobID) bool {
	if s.dlq == nil {
		return false
	}
	return s.dlq.Delete(jobID)
}

// ClearDeadLetters removes all dead letter entries.
// Returns the number of entries cleared.
func (s *Scheduler) ClearDeadLetters() int {
	if s.dlq == nil {
		return 0
	}
	return s.dlq.Clear()
}

// RequeueDeadLetter retries a job from the dead letter queue.
// The job must be re-registered with the same ID using AddCron or Delay.
// Returns ErrJobNotFound if the entry doesn't exist in DLQ.
func (s *Scheduler) RequeueDeadLetter(jobID JobID, task TaskFunc, opts ...JobOption) (JobID, error) {
	if s.dlq == nil {
		return "", ErrJobNotFound
	}

	entry, exists := s.dlq.Get(jobID)
	if !exists {
		return "", ErrJobNotFound
	}

	// Re-add the job as a delay task with immediate execution.
	// Copy opts to avoid mutating the caller's slice.
	allOpts := make([]JobOption, len(opts)+1)
	copy(allOpts, opts)
	allOpts[len(opts)] = ReplaceExisting()
	id, err := s.Delay(entry.JobID, 0, task, allOpts...)
	if err != nil {
		return "", err
	}
	// Remove from DLQ only after the job has been successfully re-registered.
	s.dlq.Delete(jobID)
	return id, nil
}

// DeadLetterQueueSize returns the current size of the dead letter queue.
// Returns 0 if DLQ is not enabled.
func (s *Scheduler) DeadLetterQueueSize() int {
	if s.dlq == nil {
		return 0
	}
	return s.dlq.Size()
}
