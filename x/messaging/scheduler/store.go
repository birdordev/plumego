package scheduler

import "time"

// Store persists scheduled jobs on a best-effort basis. A save failure leaves
// the job scheduled in memory and is reported through the configured logger.
// The job may be unavailable for restoration after a restart.
type Store interface {
	Save(job StoredJob) error
	// Delete removes a persisted job. Cancel may retry deletion after an error.
	Delete(id JobID) error
	// List may return readable jobs together with an error for other records.
	List() ([]StoredJob, error)
}

// StoredJob captures the serialized form of a scheduled job.
type StoredJob struct {
	ID               JobID
	Kind             string
	RunAt            time.Time
	TaskName         string
	Group            string
	Tags             []string
	Timeout          time.Duration
	Overlap          OverlapPolicy
	Retry            RetrySpec
	CreatedAt        time.Time
	Dependencies     []JobID
	DependencyPolicy DependencyFailurePolicy
}

// RetrySpec is a serializable retry policy.
type RetrySpec struct {
	MaxAttempts int
	MaxBackoff  time.Duration
	Kind        string
	BaseDelay   time.Duration
}
