package distributed

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
)

// TaskStatus represents the status of a workflow task.
type TaskStatus struct {
	TaskID      string    `json:"task_id"`
	Status      string    `json:"status"` // pending/running/completed/failed
	WorkerID    string    `json:"worker_id,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Retries     int       `json:"retries"`
	LastError   string    `json:"last_error,omitempty"`
}

// Task status constants
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
)

// WorkflowTask represents a distributed workflow step to be executed.
type WorkflowTask struct {
	ID            string            `json:"id"`
	ExecutionID   string            `json:"execution_id"`
	WorkflowID    string            `json:"workflow_id"`
	StepIndex     int               `json:"step_index"`
	StepName      string            `json:"step_name"`
	WorkflowState map[string]any    `json:"workflow_state"`
	Priority      int               `json:"priority"`
	CreatedAt     time.Time         `json:"created_at"`
	Timeout       time.Duration     `json:"timeout"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// TaskResult represents the result of task execution.
type TaskResult struct {
	TaskID       string                     `json:"task_id"`
	AgentResult  *orchestration.AgentResult `json:"agent_result"`
	UpdatedState map[string]any             `json:"updated_state"`
	Error        string                     `json:"error,omitempty"`
	CompletedAt  time.Time                  `json:"completed_at"`
}

// TaskHandler is a function that processes a workflow task.
type TaskHandler func(ctx context.Context, task *WorkflowTask) (*TaskResult, error)

// TaskQueue defines the interface for distributed task queue operations.
// Concrete implementations (e.g. MQ-backed) are provided at the app level.
type TaskQueue interface {
	// Enqueue adds a task to the queue.
	Enqueue(ctx context.Context, task *WorkflowTask) error

	// Subscribe allows a worker to receive and process tasks.
	Subscribe(ctx context.Context, workerID string, handler TaskHandler) error

	// UpdateTaskStatus updates the status of a task.
	UpdateTaskStatus(ctx context.Context, taskID string, status *TaskStatus) error

	// GetTaskStatus retrieves the current status of a task.
	GetTaskStatus(ctx context.Context, taskID string) (*TaskStatus, error)

	// PublishResult publishes a task result.
	PublishResult(ctx context.Context, result *TaskResult) error

	// SubscribeResults subscribes to task results.
	SubscribeResults(ctx context.Context, executionID string) (<-chan *TaskResult, error)

	// Close closes the task queue.
	Close() error
}
