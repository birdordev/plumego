package scheduler

import "sort"

// Status returns the job status snapshot.
func (s *Scheduler) Status(id JobID) (JobStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return JobStatus{}, false
	}
	return jobStatusFrom(j), true
}

// List returns status for all jobs.
func (s *Scheduler) List() []JobStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]JobStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, jobStatusFrom(j))
	}
	return out
}

// PruneTerminalJobs removes terminal jobs from in-memory registry to bound
// memory growth in long-running processes.
//
// A job is considered terminal when its state is completed, failed, or canceled.
// Running jobs are never pruned. Jobs that still have live dependents are kept.
//
// limit controls how many jobs to remove:
//   - limit <= 0: prune all eligible jobs
//   - limit > 0: prune at most limit jobs
//
// Returns the number of pruned jobs.
func (s *Scheduler) PruneTerminalJobs(limit int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pruned := 0
	for id, j := range s.jobs {
		if j == nil || j.running.Load() || !isTerminalState(j.state) {
			continue
		}
		if s.hasLiveDependentsLocked(id) {
			continue
		}
		// Mark canceled defensively so stale queued pointers (if any) cannot run.
		j.canceled.Store(true)
		s.queue.removeJob(j)
		s.wakeScheduler()
		delete(s.jobs, id)
		s.cleanupDependencyTrackingLocked(id, j)
		pruned++
		if limit > 0 && pruned >= limit {
			break
		}
	}
	return pruned
}

// QueryJobs filters and sorts jobs based on the provided query criteria.
// Returns a JobQueryResult with matched jobs and total count.
func (s *Scheduler) QueryJobs(query JobQuery) JobQueryResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matcher := newJobQueryMatcher(query)

	// Collect and filter jobs
	var filtered []JobStatus
	for _, j := range s.jobs {
		status := jobStatusFrom(j)
		if !matcher.match(status) {
			continue
		}
		filtered = append(filtered, status)
	}

	total := len(filtered)

	// Sort results
	if query.OrderBy != "" {
		sortJobStatuses(filtered, query.OrderBy, query.Ascending)
	}

	// Apply pagination (clamp negative values to 0)
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > 0 {
		if offset >= len(filtered) {
			filtered = nil
		} else {
			filtered = filtered[offset:]
		}
	}
	limit := query.Limit
	if limit < 0 {
		limit = 0
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return JobQueryResult{
		Jobs:  filtered,
		Total: total,
	}
}

// sortJobStatuses sorts job statuses based on the specified field and direction.
func sortJobStatuses(jobs []JobStatus, orderBy string, ascending bool) {
	less := buildJobStatusLess(orderBy)
	if less == nil {
		return
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		if ascending {
			return less(jobs[i], jobs[j])
		}
		return less(jobs[j], jobs[i])
	})
}

type jobQueryMatcher struct {
	group   string
	tags    []string
	kinds   map[string]struct{}
	running *bool
	paused  *bool
	states  map[JobState]struct{}
}

func newJobQueryMatcher(query JobQuery) jobQueryMatcher {
	m := jobQueryMatcher{
		group:   query.Group,
		tags:    query.Tags,
		running: query.Running,
		paused:  query.Paused,
	}
	if len(query.Kinds) > 0 {
		m.kinds = make(map[string]struct{}, len(query.Kinds))
		for _, kind := range query.Kinds {
			m.kinds[kind] = struct{}{}
		}
	}
	if len(query.States) > 0 {
		m.states = make(map[JobState]struct{}, len(query.States))
		for _, state := range query.States {
			m.states[state] = struct{}{}
		}
	}
	return m
}

func (m jobQueryMatcher) match(status JobStatus) bool {
	if m.group != "" && status.Group != m.group {
		return false
	}
	if len(m.tags) > 0 && !hasAllTags(status.Tags, m.tags) {
		return false
	}
	if len(m.kinds) > 0 {
		if _, ok := m.kinds[status.Kind]; !ok {
			return false
		}
	}
	if m.running != nil && status.Running != *m.running {
		return false
	}
	if m.paused != nil && status.Paused != *m.paused {
		return false
	}
	if len(m.states) > 0 {
		if _, ok := m.states[status.State]; !ok {
			return false
		}
	}
	return true
}

func buildJobStatusLess(orderBy string) func(a, b JobStatus) bool {
	switch orderBy {
	case "id":
		return func(a, b JobStatus) bool {
			return a.ID < b.ID
		}
	case "next_run":
		return func(a, b JobStatus) bool {
			if a.NextRun.Equal(b.NextRun) {
				return a.ID < b.ID
			}
			return a.NextRun.Before(b.NextRun)
		}
	case "last_run":
		return func(a, b JobStatus) bool {
			if a.LastRun.Equal(b.LastRun) {
				return a.ID < b.ID
			}
			return a.LastRun.Before(b.LastRun)
		}
	case "group":
		return func(a, b JobStatus) bool {
			if a.Group == b.Group {
				return a.ID < b.ID
			}
			return a.Group < b.Group
		}
	default:
		return nil
	}
}

func jobStatusFrom(j *job) JobStatus {
	kind := "delay"
	if j.kind == jobKindCron {
		kind = "cron"
	}
	return JobStatus{
		ID:            j.id,
		NextRun:       j.runAt,
		LastRun:       j.lastRun,
		LastError:     j.lastError,
		Attempt:       j.nextAttempt,
		Paused:        j.paused.Load(),
		Running:       j.running.Load(),
		Kind:          kind,
		CronExpr:      j.cronExpr,
		OverlapPolicy: j.options.OverlapPolicy,
		Group:         j.options.Group,
		Tags:          append([]string(nil), j.options.Tags...),
		State:         j.state,
		StateUpdated:  j.stateAt,
	}
}
