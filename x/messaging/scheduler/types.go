package scheduler

import (
	"context"
	"time"
)

// TaskFunc is the function signature for scheduled work.
type TaskFunc func(context.Context) error

// JobID identifies a scheduled job.
type JobID string

type jobIDKey struct{}
type jobAttemptKey struct{}
type jobScheduledKey struct{}
type jobLastErrorKey struct{}

// WithJobID stores the job id in context.
func WithJobID(ctx context.Context, jobID JobID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, jobIDKey{}, jobID)
}

// JobIDFromContext returns the job ID stored in the context.
func JobIDFromContext(ctx context.Context) (JobID, bool) {
	if ctx == nil {
		return "", false
	}
	val, ok := ctx.Value(jobIDKey{}).(JobID)
	return val, ok
}

// WithJobAttempt stores the attempt number in context.
func WithJobAttempt(ctx context.Context, attempt int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, jobAttemptKey{}, attempt)
}

// JobAttemptFromContext returns the attempt number stored in the context.
func JobAttemptFromContext(ctx context.Context) (int, bool) {
	if ctx == nil {
		return 0, false
	}
	val, ok := ctx.Value(jobAttemptKey{}).(int)
	return val, ok
}

// WithJobScheduledAt stores the scheduled time in context.
func WithJobScheduledAt(ctx context.Context, scheduledAt time.Time) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, jobScheduledKey{}, scheduledAt)
}

// JobScheduledAtFromContext returns the scheduled time stored in the context.
func JobScheduledAtFromContext(ctx context.Context) (time.Time, bool) {
	if ctx == nil {
		return time.Time{}, false
	}
	val, ok := ctx.Value(jobScheduledKey{}).(time.Time)
	return val, ok
}

// WithJobLastError stores the last error in context.
func WithJobLastError(ctx context.Context, err error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, jobLastErrorKey{}, err)
}

// JobLastErrorFromContext returns the last error stored in the context.
func JobLastErrorFromContext(ctx context.Context) (error, bool) {
	if ctx == nil {
		return nil, false
	}
	val, ok := ctx.Value(jobLastErrorKey{}).(error)
	return val, ok
}

// BackpressurePolicy controls the scheduler's behavior when the work queue is full.
//
// This policy determines how the scheduler handles new job executions when all
// worker threads are busy and the work queue has reached its capacity.
type BackpressurePolicy int

const (
	// BackpressureDrop immediately drops the job execution when the queue is full.
	// The dropped execution is counted in statistics and can trigger an optional callback.
	//
	// Use case: Non-critical jobs where dropping executions is acceptable:
	//   - Best-effort monitoring tasks
	//   - Optional background work
	//   - Jobs that will be retried naturally
	//
	// Behavior: The dispatch() call returns immediately. Statistics are updated,
	// and the optional OnBackpressure callback is invoked if configured.
	BackpressureDrop BackpressurePolicy = iota

	// BackpressureBlock blocks until space becomes available in the queue.
	// The caller (scheduler dispatch loop) will wait indefinitely.
	//
	// Use case: Critical jobs that must not be lost:
	//   - Payment processing
	//   - Critical data synchronization
	//   - Jobs without alternative retry mechanisms
	//
	// Behavior: The dispatch() call blocks until a worker becomes available.
	// This can cause the scheduler's dispatch loop to pause, potentially delaying
	// other jobs from being dispatched.
	//
	// Caution: Can lead to deadlock if all workers are blocked on I/O and no
	// jobs complete. Monitor queue depth and worker utilization.
	BackpressureBlock

	// BackpressureBlockTimeout blocks with a timeout when the queue is full.
	// If the timeout expires, the job is dropped.
	//
	// Use case: Important jobs with bounded wait tolerance:
	//   - API requests with SLA requirements
	//   - Jobs with time-sensitive execution windows
	//   - Graceful degradation scenarios
	//
	// Behavior: The dispatch() call blocks for up to BackpressureTimeout duration.
	// If space becomes available, the job is queued. Otherwise, it's treated as
	// dropped (statistics updated, callback invoked).
	//
	// Note: This provides a middle ground between Drop and Block, preventing
	// indefinite blocking while still attempting to queue the job.
	BackpressureBlockTimeout
)

// BackpressureConfig configures backpressure behavior.
type BackpressureConfig struct {
	// Policy determines the backpressure strategy.
	Policy BackpressurePolicy
	// Timeout is the maximum duration to wait when Policy is BackpressureBlockTimeout.
	// Ignored for other policies.
	Timeout time.Duration
	// OnBackpressure is an optional callback invoked when a job is dropped due to backpressure.
	// Called for BackpressureDrop and BackpressureBlockTimeout (on timeout).
	OnBackpressure func(jobID JobID)
}

// OverlapPolicy controls the scheduler's behavior when a scheduled job execution
// is triggered while a previous execution of the same job is still running.
//
// This policy is critical for jobs with unpredictable execution times or when
// strict execution semantics are required.
type OverlapPolicy int

const (
	// AllowConcurrent allows multiple instances of the same job to run simultaneously.
	//
	// Use case: Stateless jobs that can safely run in parallel, such as:
	//   - Health checks or monitoring tasks
	//   - Independent data fetches
	//   - Stateless notification dispatchers
	//
	// Caution: Can lead to resource exhaustion if job execution time exceeds schedule interval.
	AllowConcurrent OverlapPolicy = iota

	// SkipIfRunning skips the scheduled execution if the previous run is still active.
	// The skipped execution is dropped entirely and will not be retried.
	//
	// Use case: Jobs where missing an execution is acceptable, such as:
	//   - Periodic cache refreshes
	//   - Metrics collection (next interval will capture the data)
	//   - Status updates
	//
	// Behavior: If job started at T0 is still running at T1, the T1 execution is skipped.
	// The next opportunity to run is T2 (if the job has finished by then).
	SkipIfRunning

	// SerialQueue queues one pending execution to run immediately after the
	// current execution finishes. Only one catch-up run is retained; earlier
	// missed triggers are collapsed, not accumulated.
	//
	// Use case: Jobs that must not run concurrently but should not skip every
	// execution:
	//   - Sequential data processing pipelines
	//   - Jobs that modify shared state
	//
	// Behavior: If the job started at T0 is still running at T1, T2, and T3,
	// exactly one catch-up run is queued. Once T0 finishes, the single pending
	// run executes immediately. T2 and T3 are silently collapsed.
	//
	// For strict T0 → T1 → T2 ordering, the task must re-submit each execution.
	SerialQueue
)

// RetryPolicy controls retry behavior for failed tasks.
type RetryPolicy struct {
	// MaxAttempts includes the initial attempt (e.g. 3 = 1 try + 2 retries).
	MaxAttempts int
	// Backoff returns the delay before the next attempt.
	Backoff func(attempt int) time.Duration
	// MaxBackoff caps the backoff duration (0 = no cap).
	MaxBackoff time.Duration
	// Kind describes the retry strategy (\"fixed\" or \"exponential\") when Backoff is nil.
	Kind string
	// BaseDelay is used by fixed/exponential strategies.
	BaseDelay time.Duration
}

// JobOptions customize a job.
type JobOptions struct {
	Timeout          time.Duration
	OverlapPolicy    OverlapPolicy
	RetryPolicy      RetryPolicy
	Paused           bool
	DeadLetter       func(context.Context, JobID, error)
	Group            string
	Tags             []string
	Replace          bool
	TaskName         string
	Dependencies     []JobID
	DependencyPolicy DependencyFailurePolicy
	// Jitter adds a random delay [0, Jitter) before each execution.
	// Useful for spreading load when many jobs share the same schedule.
	Jitter time.Duration
	// MaxRuns limits the total number of successful executions.
	// When reached the job is automatically cancelled.
	// 0 means unlimited.
	MaxRuns int
}

// JobOption mutates JobOptions.
type JobOption func(*JobOptions)

// WithTimeout sets a per-run timeout.
func WithTimeout(timeout time.Duration) JobOption {
	return func(opts *JobOptions) {
		opts.Timeout = timeout
	}
}

// WithOverlapPolicy sets the overlap policy.
func WithOverlapPolicy(policy OverlapPolicy) JobOption {
	return func(opts *JobOptions) {
		opts.OverlapPolicy = policy
	}
}

// WithRetryPolicy sets the retry policy.
func WithRetryPolicy(policy RetryPolicy) JobOption {
	return func(opts *JobOptions) {
		opts.RetryPolicy = policy
	}
}

// WithDeadLetter sets a callback invoked when retries are exhausted.
func WithDeadLetter(fn func(context.Context, JobID, error)) JobOption {
	return func(opts *JobOptions) {
		opts.DeadLetter = fn
	}
}

// WithGroup sets a job group name.
func WithGroup(group string) JobOption {
	return func(opts *JobOptions) {
		opts.Group = group
	}
}

// WithTags sets job tags.
func WithTags(tags ...string) JobOption {
	return func(opts *JobOptions) {
		opts.Tags = append([]string(nil), tags...)
	}
}

// ReplaceExisting replaces an existing job with the same ID.
func ReplaceExisting() JobOption {
	return func(opts *JobOptions) {
		opts.Replace = true
	}
}

// WithTaskName associates a job with a registered task name (for persistence).
func WithTaskName(name string) JobOption {
	return func(opts *JobOptions) {
		opts.TaskName = name
	}
}

// RetryFixed returns a fixed-delay retry policy.
func RetryFixed(maxAttempts int, delay time.Duration) RetryPolicy {
	return RetryPolicy{
		MaxAttempts: maxAttempts,
		Kind:        "fixed",
		BaseDelay:   delay,
		Backoff: func(_ int) time.Duration {
			return delay
		},
	}
}

// RetryExponential returns an exponential backoff retry policy.
func RetryExponential(maxAttempts int, base time.Duration, maxBackoff time.Duration) RetryPolicy {
	return RetryPolicy{
		MaxAttempts: maxAttempts,
		MaxBackoff:  maxBackoff,
		Kind:        "exponential",
		BaseDelay:   base,
		Backoff: func(attempt int) time.Duration {
			return safeExponentialBackoff(base, maxBackoff, attempt)
		},
	}
}

// safeExponentialBackoff calculates exponential backoff without integer overflow.
// When the shift amount is too large (>= 63 bits) or the result overflows
// (becomes negative), it returns maxBackoff (or base if maxBackoff is 0).
func safeExponentialBackoff(base, maxBackoff time.Duration, attempt int) time.Duration {
	if attempt <= 1 {
		return base
	}
	shift := attempt - 1
	if shift >= 63 || base > (1<<62)/time.Duration(1<<shift) {
		// Overflow would occur; clamp to maxBackoff
		if maxBackoff > 0 {
			return maxBackoff
		}
		return base
	}
	backoff := base << shift
	if backoff <= 0 {
		// Overflow produced negative or zero; clamp
		if maxBackoff > 0 {
			return maxBackoff
		}
		return base
	}
	if maxBackoff > 0 && backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

func serializeRetry(policy RetryPolicy) RetrySpec {
	return RetrySpec{
		MaxAttempts: policy.MaxAttempts,
		MaxBackoff:  policy.MaxBackoff,
		Kind:        policy.Kind,
		BaseDelay:   policy.BaseDelay,
	}
}

func hydrateRetry(spec RetrySpec) RetryPolicy {
	policy := RetryPolicy{
		MaxAttempts: spec.MaxAttempts,
		MaxBackoff:  spec.MaxBackoff,
		Kind:        spec.Kind,
		BaseDelay:   spec.BaseDelay,
	}
	if policy.Backoff != nil || policy.Kind == "" {
		return policy
	}
	switch policy.Kind {
	case "fixed":
		if policy.BaseDelay <= 0 {
			return policy
		}
		policy.Backoff = func(_ int) time.Duration { return policy.BaseDelay }
	case "exponential":
		if policy.BaseDelay <= 0 {
			return policy
		}
		policy.Backoff = func(attempt int) time.Duration {
			return safeExponentialBackoff(policy.BaseDelay, policy.MaxBackoff, attempt)
		}
	}
	return policy
}

// Paused marks the job as paused on creation.
func Paused() JobOption {
	return func(opts *JobOptions) {
		opts.Paused = true
	}
}

// WithJitter adds a random delay in [0, d) when scheduling subsequent cron runs.
// The initial schedule uses the cron expression as given. JobStatus.NextRun
// reports the scheduled time including any jitter applied during rescheduling.
func WithJitter(d time.Duration) JobOption {
	return func(opts *JobOptions) {
		if d > 0 {
			opts.Jitter = d
		}
	}
}

// WithMaxRuns limits the total number of successful executions for a cron job.
// When the limit is reached the job cancels itself automatically.
// Has no effect on delay jobs (which run only once by design).
// n == 0 means unlimited.
func WithMaxRuns(n int) JobOption {
	return func(opts *JobOptions) {
		if n >= 0 {
			opts.MaxRuns = n
		}
	}
}

// DependencyFailurePolicy determines what happens when a dependency fails.
type DependencyFailurePolicy int

const (
	// DependencyFailureSkip skips execution of dependent jobs if a dependency fails.
	// The dependent job's next run is still scheduled (for cron jobs).
	DependencyFailureSkip DependencyFailurePolicy = iota

	// DependencyFailureCancel cancels the dependent job if a dependency fails.
	// This removes the job from the scheduler entirely.
	DependencyFailureCancel

	// DependencyFailureContinue treats a failed or canceled dependency as resolved
	// for this job. It still waits for the remaining dependencies.
	DependencyFailureContinue
)

// WithDependsOn sets job dependencies. The job will only run after all
// dependencies have completed successfully (unless policy overrides this).
func WithDependsOn(policy DependencyFailurePolicy, dependencies ...JobID) JobOption {
	return func(opts *JobOptions) {
		opts.Dependencies = append([]JobID(nil), dependencies...)
		opts.DependencyPolicy = policy
	}
}

func defaultJobOptions() JobOptions {
	return JobOptions{
		OverlapPolicy: AllowConcurrent,
	}
}

// DefaultBackpressureConfig returns the default backpressure configuration.
func DefaultBackpressureConfig() BackpressureConfig {
	return BackpressureConfig{
		Policy:  BackpressureDrop,
		Timeout: 0,
	}
}

// JobStatus exposes job runtime state for observability.
type JobState string

const (
	JobStateQueued    JobState = "queued"
	JobStateScheduled JobState = "scheduled"
	JobStateRunning   JobState = "running"
	JobStateFailed    JobState = "failed"
	JobStateRetrying  JobState = "retrying"
	JobStateCanceled  JobState = "canceled"
	JobStateCompleted JobState = "completed"
)

func (s JobState) String() string {
	return string(s)
}

type JobStatus struct {
	ID            JobID
	NextRun       time.Time
	LastRun       time.Time
	LastError     error
	Attempt       int
	Paused        bool
	Running       bool
	Kind          string
	CronExpr      string // Non-empty for cron jobs; reconstructed from spec.
	OverlapPolicy OverlapPolicy
	Group         string
	Tags          []string
	State         JobState
	StateUpdated  time.Time
}

// JobQuery defines filtering and sorting criteria for querying jobs.
type JobQuery struct {
	// Group filters jobs by group name (empty = no filter).
	Group string
	// Tags filters jobs that have ALL of the specified tags (empty = no filter).
	Tags []string
	// Kinds filters jobs by kind ("cron", "delay", or empty = no filter).
	Kinds []string
	// Running filters by running state (nil = no filter, true = only running, false = only not running).
	Running *bool
	// Paused filters by paused state (nil = no filter, true = only paused, false = only not paused).
	Paused *bool
	// States filters by job state (empty = no filter).
	States []JobState
	// OrderBy specifies sorting field: "id", "next_run", "last_run", "group" (empty = no sorting).
	OrderBy string
	// Ascending determines sort direction (default: true).
	Ascending bool
	// Limit limits the number of results (0 = no limit).
	Limit int
	// Offset skips the first N results (for pagination).
	Offset int
}

// JobQueryResult contains the query results and metadata.
type JobQueryResult struct {
	// Jobs is the filtered and sorted list of job statuses.
	Jobs []JobStatus
	// Total is the total count of matching jobs (before limit/offset).
	Total int
}
