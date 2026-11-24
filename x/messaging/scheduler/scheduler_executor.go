package scheduler

import (
	"errors"

	"github.com/birdordev/plumego/log"
)

func (s *Scheduler) logError(msg string, err error) {
	if s.logger == nil || err == nil {
		return
	}
	s.logger.Error(msg, log.Fields{"failure_code": schedulerFailureCode(err)})
}

func (s *Scheduler) persistJobLocked(j *job) error {
	if s.store == nil || j == nil || j.kind != jobKindDelay || j.options.TaskName == "" {
		return nil
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
	return s.store.Save(job)
}

// deleteTerminalJobLocked serializes deletion with replacement and persistence
// of a job with the same ID. Call with s.mu held.
func (s *Scheduler) deleteTerminalJobLocked(j *job) error {
	if s.store == nil || j.kind != jobKindDelay || j.options.TaskName == "" || s.jobs[j.id] != j {
		return nil
	}
	if err := s.store.Delete(j.id); err != nil {
		if retryErr := s.store.Delete(j.id); retryErr != nil {
			return errors.Join(err, retryErr)
		}
	}
	return nil
}

func (s *Scheduler) reportTerminalCleanupFailure(id JobID, err error) {
	if err != nil && s.logger != nil {
		s.logger.Error("scheduler terminal job cleanup failed", log.Fields{
			"job_id": id, "operation": "delete", "failure_code": schedulerFailureCode(err),
		})
	}
}

func (s *Scheduler) loadPersisted() {
	if s.store == nil {
		return
	}
	stored, err := s.store.List()
	if err != nil {
		s.logError("scheduler load persisted jobs failed", err)
	}

	// Queue records whose dependencies are already registered. Each restored
	// job then releases the records that were waiting for it.
	type pendingJob struct {
		item    StoredJob
		task    TaskFunc
		missing int
	}
	var pending []*pendingJob
	var ready []*pendingJob
	waiting := make(map[JobID][]*pendingJob)
	for _, item := range stored {
		if item.TaskName == "" {
			continue
		}
		task := s.lookupTask(item.TaskName)
		if task == nil {
			continue
		}
		job := &pendingJob{item: item, task: task}
		for _, dependency := range item.Dependencies {
			if _, exists := s.Status(dependency); !exists {
				waiting[dependency] = append(waiting[dependency], job)
				job.missing++
			}
		}
		if job.missing == 0 {
			ready = append(ready, job)
		} else {
			pending = append(pending, job)
		}
	}
	for i := 0; i < len(ready); i++ {
		job := ready[i]
		item := job.item
		opts := append(s.buildLoadOpts(item), WithDependsOn(item.DependencyPolicy, item.Dependencies...))
		if _, err := s.Schedule(item.ID, item.RunAt, job.task, opts...); err != nil {
			s.reportRestoreFailure(item, err)
			continue
		}
		for _, dependent := range waiting[item.ID] {
			dependent.missing--
			if dependent.missing == 0 {
				ready = append(ready, dependent)
			}
		}
		delete(waiting, item.ID)
	}
	for _, job := range pending {
		if job.missing > 0 {
			s.reportRestoreFailure(job.item, errStoredDependencies)
		}
	}
}

func (s *Scheduler) reportRestoreFailure(item StoredJob, err error) {
	if s.logger != nil {
		s.logger.Warn("scheduler: failed to restore job", log.Fields{
			"job_id": item.ID, "dependencies": item.Dependencies, "failure_code": schedulerFailureCode(err),
		})
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
func (s *Scheduler) triggerDependentJobs(completedJob *job) {
	s.mu.Lock()
	defer s.mu.Unlock()

	completedJobID := completedJob.id
	if s.jobs[completedJobID] != completedJob || completedJob.canceled.Load() {
		return
	}
	dependentIDs := s.dependents[completedJobID]
	if len(dependentIDs) == 0 {
		return
	}

	for _, depJobID := range dependentIDs {
		depJob, exists := s.jobs[depJobID]
		if !exists || depJob.canceled.Load() {
			continue
		}
		if depJob.options.DependencyPolicy == DependencyFailureContinue {
			depJob.resolveDependencyLocked(completedJobID)
		}
		if s.scheduleDependentIfReadyLocked(depJob) {
			if s.logger != nil {
				s.logger.Info("triggering dependent job", log.Fields{
					"job_id":     depJobID,
					"dependency": completedJobID,
				})
			}
		}
	}
}

type terminalCleanupFailure struct {
	id  JobID
	err error
}

func (s *Scheduler) unlockAndReportCleanup(failures *[]terminalCleanupFailure) {
	s.mu.Unlock()
	for _, failure := range *failures {
		s.reportTerminalCleanupFailure(failure.id, failure.err)
	}
}

// handleDependentJobsOnFailure handles dependent jobs when a dependency fails.
func (s *Scheduler) handleDependentJobsOnFailure(failedJob *job) {
	var failures []terminalCleanupFailure
	s.mu.Lock()
	defer s.unlockAndReportCleanup(&failures)

	if s.jobs[failedJob.id] != failedJob || failedJob.canceled.Load() {
		return
	}
	s.dependencyStatus[failedJob.id] = false
	dependentIDs := s.dependents[failedJob.id]
	if len(dependentIDs) == 0 {
		return
	}

	for _, depJobID := range dependentIDs {
		depJob, exists := s.jobs[depJobID]
		if !exists || depJob.canceled.Load() {
			continue
		}
		s.applyDependentPolicyLocked(depJobID, depJob, failedJob.id, dependencyEventFailure, &failures)
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
func (s *Scheduler) cancelJobLocked(id JobID, j *job, failures *[]terminalCleanupFailure) {
	s.setJobCanceledLocked(j)
	if s.store != nil {
		_ = s.store.Delete(id)
	}
	s.finishJobCancellationLocked(id, j, failures)
}

func (s *Scheduler) setJobCanceledLocked(j *job) {
	j.canceled.Store(true)
	s.setJobStateLocked(j, JobStateCanceled)
	s.queue.removeJob(j)
	s.wakeScheduler()
}

func (s *Scheduler) finishJobCancellationLocked(id JobID, j *job, failures *[]terminalCleanupFailure) {
	// Notify dependent jobs that were waiting for this job before cleaning
	// up the dependents map entry, so we still have the list of waiters.
	if deps := s.dependents[id]; len(deps) > 0 {
		s.notifyDependentsCanceledLocked(id, deps, failures)
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
//   - Still cleans up the schedule queue slot and dependency maps
//     so the job does not run again.
//
// Must be called with s.mu held.
func (s *Scheduler) retireJobLocked(j *job) {
	if s.jobs[j.id] != j || j.canceled.Load() {
		return
	}
	j.canceled.Store(true)
	s.queue.removeJob(j)
	s.wakeScheduler()
	// State is already JobStateCompleted — do not override.
	// Clean up dependency tracking (same as cancelJobLocked, minus dependent notification).
	s.cleanupDependencyTrackingLocked(j.id, j)
}

// notifyDependentsCanceledLocked applies DependencyFailurePolicy to each job
// that was waiting for canceledID to complete. Called from cancelJobLocked;
// s.mu must be held.
func (s *Scheduler) notifyDependentsCanceledLocked(canceledID JobID, dependentIDs []JobID, failures *[]terminalCleanupFailure) {
	for _, depJobID := range dependentIDs {
		depJob, exists := s.jobs[depJobID]
		if !exists || depJob.canceled.Load() {
			continue
		}
		s.applyDependentPolicyLocked(depJobID, depJob, canceledID, dependencyEventCancellation, failures)
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

func (s *Scheduler) applyDependentPolicyLocked(depJobID JobID, depJob *job, triggerID JobID, event dependencyEvent, failures *[]terminalCleanupFailure) {
	switch depJob.options.DependencyPolicy {
	case DependencyFailureSkip:
		s.logDependentPolicy("skipping dependent job due to dependency "+event.noun, depJobID, triggerID, event.field, true)
		if err := s.skipDependentJobLocked(depJob); err != nil {
			*failures = append(*failures, terminalCleanupFailure{id: depJobID, err: err})
		}
	case DependencyFailureCancel:
		s.logDependentPolicy("canceling dependent job due to dependency "+event.noun, depJobID, triggerID, event.field, true)
		s.cancelJobLocked(depJobID, depJob, failures)
	case DependencyFailureContinue:
		s.logDependentPolicy("continuing dependent job despite dependency "+event.noun, depJobID, triggerID, event.field, false)
		depJob.resolveDependencyLocked(triggerID)
		s.scheduleDependentIfReadyLocked(depJob)
	}
}

func (s *Scheduler) skipDependentJobLocked(depJob *job) error {
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
		return s.deleteTerminalJobLocked(depJob)
	}
	return nil
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

// resumeJobLocked unpauses a job and reschedules it when its dependencies
// are ready. Must be called with s.mu held.
func (s *Scheduler) resumeJobLocked(j *job) {
	j.paused.Store(false)
	if !s.dependenciesReadyLocked(j) {
		return
	}
	if j.kind == jobKindCron {
		j.runAt = j.cron.Next(s.clock.Now())
	}
	s.pushScheduleLocked(j, j.runAt)
}

// resolveDependencyLocked records that this job no longer waits on id.
// It leaves the shared success or failure status of the dependency intact.
func (j *job) resolveDependencyLocked(id JobID) {
	if j.resolvedDependencies == nil {
		j.resolvedDependencies = make(map[JobID]struct{})
	}
	j.resolvedDependencies[id] = struct{}{}
}

func (s *Scheduler) scheduleDependentIfReadyLocked(j *job) bool {
	if j.paused.Load() || (j.kind == jobKindDelay && j.state != JobStateQueued) || !s.dependenciesReadyLocked(j) {
		return false
	}
	s.pushScheduleLocked(j, j.runAt)
	return true
}

func (s *Scheduler) dependenciesReadyLocked(j *job) bool {
	for _, dependency := range j.options.Dependencies {
		if _, resolved := j.resolvedDependencies[dependency]; !resolved && !s.dependencyStatus[dependency] {
			return false
		}
	}
	return true
}
