package scheduler

import "github.com/birdordev/plumego/log"

func (s *Scheduler) logError(msg string, err error) {
	if s.logger == nil || err == nil {
		return
	}
	s.logger.Error(msg, log.Fields{"error": err})
}

func (s *Scheduler) persistJobLocked(j *job) {
	if s.store == nil || j == nil {
		return
	}
	if j.kind != jobKindDelay {
		return
	}
	if j.options.TaskName == "" {
		return
	}
	job := StoredJob{
		ID:               j.id,
		Kind:             "delay",
		RunAt:            j.runAt,
		TaskName:         j.options.TaskName,
		Group:            j.options.Group,
		Tags:             append([]string(nil), j.options.Tags...),
		Timeout:          j.options.Timeout,
		Overlap:          j.options.OverlapPolicy,
		Retry:            serializeRetry(j.options.RetryPolicy),
		CreatedAt:        s.clock.Now(),
		Dependencies:     append([]JobID(nil), j.options.Dependencies...),
		DependencyPolicy: j.options.DependencyPolicy,
	}
	_ = s.store.Save(job)
}

func (s *Scheduler) loadPersisted() {
	if s.store == nil {
		return
	}
	stored, err := s.store.List()
	if err != nil {
		s.logError("scheduler load persisted jobs failed", err)
		return
	}

	// Two-pass loading: first restore jobs without dependencies so that
	// dependency targets exist when the second pass runs.
	var withDeps []StoredJob
	for _, item := range stored {
		if item.TaskName == "" {
			continue
		}
		if s.lookupTask(item.TaskName) == nil {
			continue
		}
		if len(item.Dependencies) > 0 {
			withDeps = append(withDeps, item)
			continue
		}
		if _, err := s.Schedule(item.ID, item.RunAt, s.lookupTask(item.TaskName), s.buildLoadOpts(item)...); err != nil {
			if s.logger != nil {
				s.logger.Warn("scheduler: failed to restore job", log.Fields{
					"job_id": item.ID,
					"error":  err.Error(),
				})
			}
		}
	}

	// Second pass: jobs with dependencies (their targets are now registered).
	for _, item := range withDeps {
		task := s.lookupTask(item.TaskName)
		if task == nil {
			continue
		}
		opts := append(s.buildLoadOpts(item), WithDependsOn(item.DependencyPolicy, item.Dependencies...))
		if _, err := s.Schedule(item.ID, item.RunAt, task, opts...); err != nil {
			if s.logger != nil {
				s.logger.Warn("scheduler: failed to restore job with dependencies", log.Fields{
					"job_id":       item.ID,
					"dependencies": item.Dependencies,
					"error":        err.Error(),
				})
			}
		}
	}
}

// buildLoadOpts constructs the JobOption slice for a persisted job (without dependencies).
func (s *Scheduler) buildLoadOpts(item StoredJob) []JobOption {
	return []JobOption{
		WithTimeout(item.Timeout),
		WithOverlapPolicy(item.Overlap),
		WithRetryPolicy(hydrateRetry(item.Retry)),
		WithGroup(item.Group),
		WithTags(item.Tags...),
		WithTaskName(item.TaskName),
		ReplaceExisting(),
	}
}

func (s *Scheduler) lookupTask(name string) TaskFunc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.registry[name]
}

// triggerDependentJobs checks and triggers jobs that depend on the completed job.
func (s *Scheduler) triggerDependentJobs(completedJobID JobID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dependentIDs := s.dependents[completedJobID]
	if len(dependentIDs) == 0 {
		return
	}

	for _, depJobID := range dependentIDs {
		depJob, exists := s.jobs[depJobID]
		if !exists || depJob.canceled.Load() || depJob.paused.Load() {
			continue
		}

		if s.allDependenciesMetLocked(depJob) {
			s.setJobStateIfIdleLocked(depJob, JobStateScheduled)
			s.pushScheduleItemLocked(depJob, depJob.runAt, true)
			s.wakeScheduler()
			if s.logger != nil {
				s.logger.Info("triggering dependent job", log.Fields{
					"job_id":     depJobID,
					"dependency": completedJobID,
				})
			}
		}
	}
}

// handleDependentJobsOnFailure handles dependent jobs when a dependency fails.
func (s *Scheduler) handleDependentJobsOnFailure(failedJob *job) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dependentIDs := s.dependents[failedJob.id]
	if len(dependentIDs) == 0 {
		return
	}

	for _, depJobID := range dependentIDs {
		depJob, exists := s.jobs[depJobID]
		if !exists || depJob.canceled.Load() {
			continue
		}
		s.applyDependentPolicyLocked(depJobID, depJob, failedJob.id, dependencyEventFailure)
	}
}

func (s *Scheduler) setJobStateLocked(j *job, state JobState) {
	if j == nil {
		return
	}
	if j.state == state {
		return
	}
	j.state = state
	j.stateAt = s.clock.Now()
}

func (s *Scheduler) setJobStateIfIdleLocked(j *job, state JobState) {
	if j == nil {
		return
	}
	if j.running.Load() {
		return
	}
	if j.state == JobStateRetrying {
		return
	}
	s.setJobStateLocked(j, state)
}

// wakeScheduler sends a non-blocking signal to the scheduler run loop.
func (s *Scheduler) wakeScheduler() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// cancelJobLocked cancels a single job. Must be called with s.mu held.
func (s *Scheduler) cancelJobLocked(id JobID, j *job) {
	j.canceled.Store(true)
	s.setJobStateLocked(j, JobStateCanceled)
	if s.store != nil {
		_ = s.store.Delete(id)
	}
	// Notify dependent jobs that were waiting for this job before cleaning
	// up the dependents map entry, so we still have the list of waiters.
	if deps := s.dependents[id]; len(deps) > 0 {
		s.notifyDependentsCanceledLocked(id, deps)
	}
	// Clean up dependency tracking to prevent memory leaks.
	s.cleanupDependencyTrackingLocked(id, j)
}

// retireJobLocked is called when a job has exhausted its MaxRuns quota after a
// SUCCESSFUL execution. Unlike cancelJobLocked it:
//   - Preserves the JobStateCompleted state (does not overwrite with Canceled).
//   - Does not notify dependents via DependencyFailurePolicy; the caller has
//     already set dependencyStatus[id]=true and triggerDependentJobs will handle
//     them as a normal successful completion.
//   - Still cleans up the schedule queue slot, store entry, and dependency maps
//     so the job does not run again.
//
// Must be called with s.mu held.
func (s *Scheduler) retireJobLocked(id JobID, j *job) {
	j.canceled.Store(true)
	// State is already JobStateCompleted — do not override.
	if s.store != nil {
		_ = s.store.Delete(id)
	}
	// Clean up dependency tracking (same as cancelJobLocked, minus dependent notification).
	s.cleanupDependencyTrackingLocked(id, j)
}

// notifyDependentsCanceledLocked applies DependencyFailurePolicy to each job
// that was waiting for canceledID to complete. Called from cancelJobLocked;
// s.mu must be held.
func (s *Scheduler) notifyDependentsCanceledLocked(canceledID JobID, dependentIDs []JobID) {
	for _, depJobID := range dependentIDs {
		depJob, exists := s.jobs[depJobID]
		if !exists || depJob.canceled.Load() {
			continue
		}
		s.applyDependentPolicyLocked(depJobID, depJob, canceledID, dependencyEventCancellation)
	}
}

type dependencyEvent struct {
	noun  string
	field string
}

var (
	dependencyEventFailure      = dependencyEvent{noun: "failure", field: "failed_dependency"}
	dependencyEventCancellation = dependencyEvent{noun: "cancellation", field: "canceled_dependency"}
)

func (s *Scheduler) applyDependentPolicyLocked(depJobID JobID, depJob *job, triggerID JobID, event dependencyEvent) {
	switch depJob.options.DependencyPolicy {
	case DependencyFailureSkip:
		s.logDependentPolicy("skipping dependent job due to dependency "+event.noun, depJobID, triggerID, event.field, true)
		s.skipDependentJobLocked(depJobID, depJob)
	case DependencyFailureCancel:
		s.logDependentPolicy("canceling dependent job due to dependency "+event.noun, depJobID, triggerID, event.field, true)
		s.cancelJobLocked(depJobID, depJob)
	case DependencyFailureContinue:
		s.logDependentPolicy("continuing dependent job despite dependency "+event.noun, depJobID, triggerID, event.field, false)
		// DependencyFailureContinue ignores the triggering dependency outcome for
		// this scheduling decision only.
		if s.allDependenciesMetExceptLocked(depJob, triggerID) {
			s.setJobStateIfIdleLocked(depJob, JobStateScheduled)
			s.pushScheduleItemLocked(depJob, depJob.runAt, true)
			s.wakeScheduler()
		}
	}
}

func (s *Scheduler) skipDependentJobLocked(depJobID JobID, depJob *job) {
	switch depJob.kind {
	case jobKindCron:
		// Reschedule cron job at its next natural trigger time.
		next := depJob.cron.Next(s.clock.Now())
		depJob.runAt = next
		s.setJobStateIfIdleLocked(depJob, JobStateScheduled)
		s.pushScheduleItemLocked(depJob, next, true)
		s.wakeScheduler()
	case jobKindDelay:
		// A delay job runs once; if its dependency is skipped/canceled with Skip
		// policy, mark it failed and remove persisted entry.
		s.setJobStateLocked(depJob, JobStateFailed)
		if s.store != nil {
			_ = s.store.Delete(depJobID)
		}
	}
}

func (s *Scheduler) logDependentPolicy(msg string, depJobID JobID, triggerID JobID, triggerField string, warn bool) {
	if s.logger == nil {
		return
	}
	fields := log.Fields{"job_id": depJobID}
	fields[triggerField] = triggerID
	if warn {
		s.logger.Warn(msg, fields)
		return
	}
	s.logger.Info(msg, fields)
}

func (s *Scheduler) cleanupDependencyTrackingLocked(id JobID, j *job) {
	s.cleanupDependencyLinksLocked(id, j)
	delete(s.dependencyStatus, id)
}

func (s *Scheduler) cleanupDependencyLinksLocked(id JobID, j *job) {
	delete(s.dependents, id)
	if j == nil {
		return
	}
	for _, depID := range j.options.Dependencies {
		deps := s.dependents[depID]
		filtered := deps[:0]
		for _, d := range deps {
			if d != id {
				filtered = append(filtered, d)
			}
		}
		if len(filtered) == 0 {
			delete(s.dependents, depID)
		} else {
			s.dependents[depID] = filtered
		}
	}
}

func (s *Scheduler) hasLiveDependentsLocked(id JobID) bool {
	deps := s.dependents[id]
	if len(deps) == 0 {
		return false
	}
	live := deps[:0]
	for _, depID := range deps {
		depJob, exists := s.jobs[depID]
		if !exists || depJob == nil {
			continue
		}
		if depJob.canceled.Load() || isTerminalState(depJob.state) {
			continue
		}
		live = append(live, depID)
	}
	if len(live) == 0 {
		delete(s.dependents, id)
		return false
	}
	s.dependents[id] = live
	return true
}

func isTerminalState(state JobState) bool {
	switch state {
	case JobStateCompleted, JobStateFailed, JobStateCanceled:
		return true
	default:
		return false
	}
}

// resumeJobLocked resumes a paused job and reschedules it. Must be called with s.mu held.
func (s *Scheduler) resumeJobLocked(j *job) {
	j.paused.Store(false)

	switch j.kind {
	case jobKindCron:
		j.runAt = j.cron.Next(s.clock.Now())
		s.pushScheduleLocked(j, j.runAt)
	case jobKindDelay:
		s.pushScheduleLocked(j, j.runAt)
	}
}

// allDependenciesMetLocked checks if all dependencies for a job have completed successfully.
// Must be called with s.mu held.
func (s *Scheduler) allDependenciesMetLocked(j *job) bool {
	for _, requiredDepID := range j.options.Dependencies {
		if status, ok := s.dependencyStatus[requiredDepID]; !ok || !status {
			return false
		}
	}
	return true
}

// allDependenciesMetExceptLocked is like allDependenciesMetLocked but skips
// the dependency identified by exceptID. Used for DependencyFailureContinue:
// the failed dependency is ignored so the dependent job can still proceed if
// all other dependencies are satisfied.
// Must be called with s.mu held.
func (s *Scheduler) allDependenciesMetExceptLocked(j *job, exceptID JobID) bool {
	for _, requiredDepID := range j.options.Dependencies {
		if requiredDepID == exceptID {
			continue // intentionally ignore this dependency's outcome
		}
		if status, ok := s.dependencyStatus[requiredDepID]; !ok || !status {
			return false
		}
	}
	return true
}
