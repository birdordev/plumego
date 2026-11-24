package mq

import "time"

const (
	DefaultTaskMaxAttempts = 5
	DefaultLeaseDuration   = 30 * time.Second
	DefaultPollInterval    = 200 * time.Millisecond
)

const (
	TaskStatusQueued  = "queued"
	TaskStatusLeased  = "leased"
	TaskStatusDone    = "done"
	TaskStatusDead    = "dead"
	TaskStatusExpired = "expired"
)

type Task struct {
	ID       string
	Topic    string
	TenantID string
	Payload  []byte
	Meta     map[string]string

	Priority  MessagePriority
	ExpiresAt time.Time

	DedupeKey   string
	AvailableAt time.Time

	Attempts    int
	MaxAttempts int
	// ExecutionAttempts 是已登记的处理器执行序号，由启用 BeginExecution 的 Worker 设置。
	ExecutionAttempts int

	LeaseOwner string
	LeaseUntil time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

type EnqueueOptions struct {
	DedupeKey   string
	AvailableAt time.Time
	ExpiresAt   time.Time
	Priority    *MessagePriority
	MaxAttempts int
}

type ReserveOptions struct {
	Topics     []string
	Limit      int
	Lease      time.Duration
	ConsumerID string
	Now        time.Time
}

type ReleaseOptions struct {
	RetryAt time.Time
	Reason  string
	Now     time.Time
}

type Stats struct {
	Queued  int64
	Leased  int64
	Dead    int64
	Expired int64
}
