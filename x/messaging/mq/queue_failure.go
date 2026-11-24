package mq

import (
	"errors"
	"time"
)

// TaskFailureKind 描述处理器返回的失败类型。
type TaskFailureKind string

const (
	TaskFailureTransient      TaskFailureKind = "transient"
	TaskFailurePermanent      TaskFailureKind = "permanent"
	TaskFailureRateLimited    TaskFailureKind = "rate_limited"
	TaskFailureOutcomeUnknown TaskFailureKind = "outcome_unknown"
)

// TaskFailure 可携带供重试策略使用的原因。Worker 持久化固定错误摘要。
type TaskFailure struct {
	Kind       TaskFailureKind
	RetryAfter time.Duration
	Cause      error
}

func (e *TaskFailure) Error() string {
	if e != nil && e.Cause != nil {
		return e.Cause.Error()
	}
	return "task failed"
}

func (e *TaskFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func taskFailure(err error) TaskFailure {
	var failure *TaskFailure
	if errors.As(err, &failure) && failure != nil {
		return *failure
	}
	return TaskFailure{Kind: TaskFailureTransient, Cause: err}
}
