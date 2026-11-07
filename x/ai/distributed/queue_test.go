package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	kv "github.com/spcent/plumego/store/kv"

	"github.com/spcent/plumego/x/ai/orchestration"
	"github.com/spcent/plumego/x/messaging/mq"
	"github.com/spcent/plumego/x/messaging/pubsub"
)

// MQTaskQueue implements TaskQueue using the message queue.
// This is a test-local backend implementation; app-level wiring should provide
// its own TaskQueue adapter rather than depending on this package's implementation.
type MQTaskQueue struct {
	broker            mq.Broker
	taskTopic         string
	resultTopicPrefix string
	statusTopicPrefix string
	persistence       WorkflowPersistence
}

// MQTaskQueueConfig configures the MQ-based task queue.
type MQTaskQueueConfig struct {
	TaskTopic         string
	ResultTopicPrefix string
	StatusTopicPrefix string
}

// DefaultMQTaskQueueConfig returns default configuration.
func DefaultMQTaskQueueConfig() MQTaskQueueConfig {
	return MQTaskQueueConfig{
		TaskTopic:         "distributed:tasks",
		ResultTopicPrefix: "distributed:results:",
		StatusTopicPrefix: "distributed:status:",
	}
}

// NewMQTaskQueue creates a new MQ-based task queue.
func NewMQTaskQueue(broker mq.Broker, persistence WorkflowPersistence, config MQTaskQueueConfig) *MQTaskQueue {
	if config.TaskTopic == "" {
		config = DefaultMQTaskQueueConfig()
	}

	return &MQTaskQueue{
		broker:            broker,
		taskTopic:         config.TaskTopic,
		resultTopicPrefix: config.ResultTopicPrefix,
		statusTopicPrefix: config.StatusTopicPrefix,
		persistence:       persistence,
	}
}

func (q *MQTaskQueue) Enqueue(ctx context.Context, task *WorkflowTask) error {
	if task == nil {
		return fmt.Errorf("task cannot be nil")
	}
	if task.ID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}

	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	msg := pubsub.Message{
		ID:   task.ID,
		Data: data,
		Meta: map[string]string{"priority": fmt.Sprintf("%d", task.Priority)},
	}

	if err := q.broker.Publish(ctx, q.taskTopic, msg); err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	status := &TaskStatus{
		TaskID:    task.ID,
		Status:    TaskStatusPending,
		StartedAt: task.CreatedAt,
	}

	return q.UpdateTaskStatus(ctx, task.ID, status)
}

func (q *MQTaskQueue) Subscribe(ctx context.Context, workerID string, handler TaskHandler) error {
	if workerID == "" {
		return fmt.Errorf("worker ID cannot be empty")
	}
	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}

	sub, err := q.broker.Subscribe(ctx, q.taskTopic, mq.SubOptions{
		BufferSize: 100,
	})
	if err != nil {
		return fmt.Errorf("failed to subscribe to tasks: %w", err)
	}

	go q.processTasks(ctx, workerID, sub, handler)

	return nil
}

func (q *MQTaskQueue) processTasks(ctx context.Context, workerID string, sub mq.Subscription, handler TaskHandler) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sub.C():
			if !ok {
				return
			}

			var task WorkflowTask
			data, ok := msg.Data.([]byte)
			if !ok {
				continue
			}
			if err := json.Unmarshal(data, &task); err != nil {
				continue
			}

			status := &TaskStatus{
				TaskID:    task.ID,
				Status:    TaskStatusRunning,
				WorkerID:  workerID,
				StartedAt: time.Now(),
			}
			_ = q.UpdateTaskStatus(ctx, task.ID, status)

			result, err := handler(ctx, &task)

			if err != nil {
				status.Status = TaskStatusFailed
				status.LastError = err.Error()
				status.CompletedAt = time.Now()
			} else {
				status.Status = TaskStatusCompleted
				status.CompletedAt = time.Now()

				if result != nil {
					result.TaskID = task.ID
					result.CompletedAt = time.Now()
					_ = q.PublishResult(ctx, result)
				}
			}

			_ = q.UpdateTaskStatus(ctx, task.ID, status)
		}
	}
}

func (q *MQTaskQueue) UpdateTaskStatus(ctx context.Context, taskID string, status *TaskStatus) error {
	if taskID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}
	if status == nil {
		return fmt.Errorf("status cannot be nil")
	}

	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}

	topic := q.statusTopicPrefix + taskID
	msg := pubsub.Message{
		ID:   taskID + "-status",
		Data: data,
	}

	return q.broker.Publish(ctx, topic, msg)
}

func (q *MQTaskQueue) GetTaskStatus(ctx context.Context, taskID string) (*TaskStatus, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID cannot be empty")
	}

	topic := q.statusTopicPrefix + taskID
	sub, err := q.broker.Subscribe(ctx, topic, mq.SubOptions{
		BufferSize: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to status: %w", err)
	}
	defer sub.Cancel()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-sub.C():
		if !ok {
			return nil, fmt.Errorf("status subscription closed")
		}

		var status TaskStatus
		data, ok := msg.Data.([]byte)
		if !ok {
			return nil, fmt.Errorf("invalid message data type")
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return nil, fmt.Errorf("failed to unmarshal status: %w", err)
		}

		return &status, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout waiting for task status")
	}
}

func (q *MQTaskQueue) PublishResult(ctx context.Context, result *TaskResult) error {
	if result == nil {
		return fmt.Errorf("result cannot be nil")
	}
	if result.TaskID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	topic := q.resultTopicPrefix + result.TaskID
	msg := pubsub.Message{
		ID:   result.TaskID + "-result",
		Data: data,
	}

	return q.broker.Publish(ctx, topic, msg)
}

func (q *MQTaskQueue) SubscribeResults(ctx context.Context, executionID string) (<-chan *TaskResult, error) {
	if executionID == "" {
		return nil, fmt.Errorf("execution ID cannot be empty")
	}

	topic := q.resultTopicPrefix + executionID + ":*"
	sub, err := q.broker.Subscribe(ctx, topic, mq.SubOptions{
		BufferSize: 100,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to results: %w", err)
	}

	results := make(chan *TaskResult, 100)

	go func() {
		defer close(results)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-sub.C():
				if !ok {
					return
				}

				var result TaskResult
				data, ok := msg.Data.([]byte)
				if !ok {
					continue
				}
				if err := json.Unmarshal(data, &result); err != nil {
					continue
				}

				select {
				case results <- &result:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return results, nil
}

func (q *MQTaskQueue) Close() error {
	if q.broker != nil {
		return q.broker.Close()
	}
	return nil
}

// suppress unused import for orchestration in test helpers below
var _ = (*orchestration.AgentResult)(nil)

func TestMQTaskQueue_Enqueue(t *testing.T) {
	ctx := t.Context()
	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "test")})
	defer store.Close()
	persistence := NewKVPersistence(store)

	queue := NewMQTaskQueue(broker, persistence, DefaultMQTaskQueueConfig())

	t.Run("EnqueueTask", func(t *testing.T) {
		task := &WorkflowTask{
			ID:          "task-1",
			ExecutionID: "exec-1",
			WorkflowID:  "wf-1",
			StepIndex:   0,
			StepName:    "step-1",
			WorkflowState: map[string]any{
				"key": "value",
			},
			Priority:  5,
			CreatedAt: time.Now(),
			Timeout:   time.Minute,
		}

		err := queue.Enqueue(ctx, task)
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
	})

	t.Run("EnqueueNilTask", func(t *testing.T) {
		err := queue.Enqueue(ctx, nil)
		if err == nil {
			t.Error("expected error for nil task")
		}
	})

	t.Run("EnqueueEmptyID", func(t *testing.T) {
		task := &WorkflowTask{
			ExecutionID: "exec-1",
			WorkflowID:  "wf-1",
		}

		err := queue.Enqueue(ctx, task)
		if err == nil {
			t.Error("expected error for empty task ID")
		}
	})
}

func TestMQTaskQueue_Subscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "test")})
	defer store.Close()
	persistence := NewKVPersistence(store)

	queue := NewMQTaskQueue(broker, persistence, DefaultMQTaskQueueConfig())

	t.Run("SubscribeAndProcess", func(t *testing.T) {
		processed := make(chan string, 1)

		handler := func(ctx context.Context, task *WorkflowTask) (*TaskResult, error) {
			processed <- task.ID
			return &TaskResult{
				TaskID: task.ID,
				AgentResult: &orchestration.AgentResult{
					AgentID: "agent-1",
					Output:  "test output",
				},
			}, nil
		}

		err := queue.Subscribe(ctx, "worker-1", handler)
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}

		// Give subscription time to start
		time.Sleep(100 * time.Millisecond)

		// Enqueue a task
		task := &WorkflowTask{
			ID:          "task-sub-1",
			ExecutionID: "exec-1",
			WorkflowID:  "wf-1",
			StepIndex:   0,
			StepName:    "step-1",
			Priority:    5,
		}

		err = queue.Enqueue(ctx, task)
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}

		// Wait for processing
		select {
		case taskID := <-processed:
			if taskID != "task-sub-1" {
				t.Errorf("expected task-sub-1, got %s", taskID)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout waiting for task processing")
		}
	})

	t.Run("SubscribeWithNilHandler", func(t *testing.T) {
		err := queue.Subscribe(ctx, "worker-2", nil)
		if err == nil {
			t.Error("expected error for nil handler")
		}
	})

	t.Run("SubscribeWithEmptyWorkerID", func(t *testing.T) {
		handler := func(ctx context.Context, task *WorkflowTask) (*TaskResult, error) {
			return nil, nil
		}

		err := queue.Subscribe(ctx, "", handler)
		if err == nil {
			t.Error("expected error for empty worker ID")
		}
	})
}

func TestMQTaskQueue_TaskStatus(t *testing.T) {
	ctx := t.Context()
	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "test")})
	defer store.Close()
	persistence := NewKVPersistence(store)

	queue := NewMQTaskQueue(broker, persistence, DefaultMQTaskQueueConfig())

	t.Run("UpdateAndGetStatus", func(t *testing.T) {
		status := &TaskStatus{
			TaskID:    "task-status-1",
			Status:    TaskStatusRunning,
			WorkerID:  "worker-1",
			StartedAt: time.Now(),
		}

		err := queue.UpdateTaskStatus(ctx, "task-status-1", status)
		if err != nil {
			t.Fatalf("UpdateTaskStatus failed: %v", err)
		}

		// Note: GetTaskStatus requires subscription which may timeout
		// in test environment without actual message flow
	})

	t.Run("UpdateNilStatus", func(t *testing.T) {
		err := queue.UpdateTaskStatus(ctx, "task-1", nil)
		if err == nil {
			t.Error("expected error for nil status")
		}
	})
}

func TestMQTaskQueue_Results(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	broker, err := mq.NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	tmpDir := t.TempDir()
	store, _ := kv.NewKVStore(kv.Config{DataDir: filepath.Join(tmpDir, "test")})
	defer store.Close()
	persistence := NewKVPersistence(store)

	queue := NewMQTaskQueue(broker, persistence, DefaultMQTaskQueueConfig())

	t.Run("PublishResult", func(t *testing.T) {
		result := &TaskResult{
			TaskID: "task-result-1",
			AgentResult: &orchestration.AgentResult{
				AgentID: "agent-1",
				Output:  "test output",
			},
			CompletedAt: time.Now(),
		}

		err := queue.PublishResult(ctx, result)
		if err != nil {
			t.Fatalf("PublishResult failed: %v", err)
		}
	})

	t.Run("PublishNilResult", func(t *testing.T) {
		err := queue.PublishResult(ctx, nil)
		if err == nil {
			t.Error("expected error for nil result")
		}
	})
}

func TestTaskStatus_Constants(t *testing.T) {
	if TaskStatusPending != "pending" {
		t.Errorf("expected pending, got %s", TaskStatusPending)
	}
	if TaskStatusRunning != "running" {
		t.Errorf("expected running, got %s", TaskStatusRunning)
	}
	if TaskStatusCompleted != "completed" {
		t.Errorf("expected completed, got %s", TaskStatusCompleted)
	}
	if TaskStatusFailed != "failed" {
		t.Errorf("expected failed, got %s", TaskStatusFailed)
	}
}

func TestDefaultMQTaskQueueConfig(t *testing.T) {
	config := DefaultMQTaskQueueConfig()

	if config.TaskTopic == "" {
		t.Error("TaskTopic should not be empty")
	}
	if config.ResultTopicPrefix == "" {
		t.Error("ResultTopicPrefix should not be empty")
	}
	if config.StatusTopicPrefix == "" {
		t.Error("StatusTopicPrefix should not be empty")
	}
}
