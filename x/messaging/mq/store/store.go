package store

import (
	"context"
	"database/sql"
	"time"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectMySQL    Dialect = "mysql"
)

type SQLConfig struct {
	Dialect          Dialect
	Table            string
	DLQTable         string
	AttemptsTable    string
	EnableAttemptLog bool
	// DeferredExpiration 让 PostgreSQL 领取只排除超期任务，由调用方运行 ExpireBatch。
	DeferredExpiration bool
	// BlockedClaimsTable 是 PostgreSQL 可选的可信表或视图，按 task_id 排除暂停领取的任务。
	BlockedClaimsTable string
	// PriorityAging 提升等待达到阈值的 PostgreSQL 任务，按实际可领取时间优先领取。零值保留严格优先级。
	PriorityAging  time.Duration
	AttemptLogHook AttemptLogHook
	Now            func() time.Time
}

// ExpirationOptions 指定 PostgreSQL 清理的状态分区、批次上限和时间。
type ExpirationOptions struct {
	Leased bool
	Limit  int
	Now    time.Time
}

type AttemptLogError struct {
	Op      string
	TaskID  string
	Attempt int
	Err     error
}

type AttemptLogHook func(ctx context.Context, info AttemptLogError)

func DefaultSQLConfig() SQLConfig {
	return SQLConfig{
		Dialect:          DialectPostgres,
		Table:            "mq_tasks",
		DLQTable:         "mq_task_dlq",
		AttemptsTable:    "mq_task_attempts",
		EnableAttemptLog: false,
		Now:              time.Now,
	}
}

type SQLStore struct {
	db      *sql.DB
	cfg     SQLConfig
	nowFunc func() time.Time
}

// ReplayOptions configures DLQ replay behavior.
type ReplayOptions struct {
	Max           int
	Now           time.Time
	AvailableAt   time.Time
	ResetAttempts bool
}

// ReplayResult reports DLQ replay results.
type ReplayResult struct {
	Replayed  int
	Remaining int
}

// DLQReplayer exposes DLQ replay operations when supported.
type DLQReplayer interface {
	ReplayDLQ(ctx context.Context, opts ReplayOptions) (ReplayResult, error)
}

type MemConfig struct {
	Now func() time.Time
}

func DefaultMemConfig() MemConfig {
	return MemConfig{Now: time.Now}
}
