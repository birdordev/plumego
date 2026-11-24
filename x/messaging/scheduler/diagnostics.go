package scheduler

import (
	"context"
	"errors"
)

var (
	errScheduledTaskPanic = errors.New("scheduled task panicked")
	errStoredDependencies = errors.New("missing or cyclic dependencies")
)

func schedulerFailureCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, errInvalidStoredJob):
		return "invalid_stored_job"
	case errors.Is(err, errStoredDependencies):
		return "invalid_stored_dependencies"
	case errors.Is(err, errScheduledTaskPanic):
		return "task_panicked"
	default:
		return "scheduler_failure"
	}
}

type adminJobStatus struct {
	JobStatus
	LastError *string
}

func safeAdminStatus(status JobStatus) adminJobStatus {
	result := adminJobStatus{JobStatus: status}
	if status.LastError != nil {
		code := schedulerFailureCode(status.LastError)
		result.LastError = &code
	}
	return result
}

func safeAdminDeadLetter(entry DeadLetterEntry) DeadLetterEntry {
	if entry.Error != nil || entry.ErrorMessage != "" {
		entry.ErrorMessage = schedulerFailureCode(entry.Error)
	}
	entry.Error = nil
	return entry
}
