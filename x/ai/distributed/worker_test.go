package distributed

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	kv "github.com/spcent/plumego/store/kv"

	"github.com/spcent/plumego/x/ai/orchestration"
	"github.com/spcent/plumego/x/ai/provider"
	"github.com/spcent/plumego/x/ai/tokenizer"
	"github.com/spcent/plumego/x/messaging/mq"
	"github.com/spcent/plumego/x/messaging/pubsub"
)

// mockProvider for testing
type mockProvider struct {
	output string
	err    error
}

func (m *mockProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &provider.CompletionResponse{
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: m.output},
		},
		Usage: tokenizer.TokenUsage{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	}, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	return nil, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return nil, nil
}

func (m *mockProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return nil, nil
}

func (m *mockProvider) CountTokens(text string) (int, error) {
	return len(text), nil
}

func (m *mockProvider) Name() string {
	return "mock"
}

func TestWorker_StartStop(t *testing.T) {
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
	engine := orchestration.NewEngine()
	executor := NewStepExecutor(engine)

	t.Run("StartWorker", func(t *testing.T) {
		worker := NewWorker(queue, executor, DefaultWorkerConfig())

		err := worker.Start(ctx)
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		if !worker.IsRunning() {
			t.Error("worker should be running")
		}

		err = worker.Stop(ctx)
		if err != nil {
			t.Fatalf("Stop failed: %v", err)
		}

		if worker.IsRunning() {
			t.Error("worker should not be running")
		}
	})

	t.Run("StartAlreadyRunning", func(t *testing.T) {
		worker := NewWorker(queue, executor, DefaultWorkerConfig())

		err := worker.Start(ctx)
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer worker.Stop(ctx)

		err = worker.Start(ctx)
		if err == nil {
			t.Error("expected error when starting already running worker")
		}
	})

	t.Run("StopNotRunning", func(t *testing.T) {
		worker := NewWorker(queue, executor, DefaultWorkerConfig())

		err := worker.Stop(ctx)
		if err == nil {
			t.Error("expected error when stopping non-running worker")
		}
	})
}

func TestStepExecutor(t *testing.T) {
	ctx := t.Context()
	engine := orchestration.NewEngine()
	executor := NewStepExecutor(engine)

	t.Run("RegisterAndExecute", func(t *testing.T) {
		// Register a test step
		mockProv := &mockProvider{output: "test output"}
		testAgent := &orchestration.Agent{
			ID:       "agent-1",
			Name:     "Test Agent",
			Provider: mockProv,
			Model:    "test-model",
		}

		testStep := &orchestration.SequentialStep{
			StepName:  "test-step",
			Agent:     testAgent,
			InputFn:   func(state map[string]any) string { return "test input" },
			OutputKey: "result",
		}

		executor.RegisterStep("test-step", testStep)

		// Execute task
		task := &WorkflowTask{
			ID:            "task-1",
			ExecutionID:   "exec-1",
			WorkflowID:    "wf-1",
			StepIndex:     0,
			StepName:      "test-step",
			WorkflowState: map[string]any{},
		}

		result, state, err := executor.ExecuteTask(ctx, task)
		if err != nil {
			t.Fatalf("ExecuteTask failed: %v", err)
		}

		if result == nil {
			t.Error("result should not be nil")
		}

		if state == nil {
			t.Error("state should not be nil")
		}

		if result.Output != "test output" {
			t.Errorf("expected output 'test output', got %s", result.Output)
		}
	})

	t.Run("ExecuteNonexistentStep", func(t *testing.T) {
		task := &WorkflowTask{
			ID:            "task-2",
			ExecutionID:   "exec-1",
			WorkflowID:    "wf-1",
			StepName:      "nonexistent-step",
			WorkflowState: map[string]any{},
		}

		_, _, err := executor.ExecuteTask(ctx, task)
		if err == nil {
			t.Error("expected error for nonexistent step")
		}
	})

	t.Run("ExecuteNilTask", func(t *testing.T) {
		_, _, err := executor.ExecuteTask(ctx, nil)
		if err == nil {
			t.Error("expected error for nil task")
		}
	})
}

func TestWorkerPool(t *testing.T) {
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
	engine := orchestration.NewEngine()
	executor := NewStepExecutor(engine)

	t.Run("StartAndStop", func(t *testing.T) {
		pool := NewWorkerPool(queue, executor, DefaultPoolConfig())

		err := pool.Start(ctx)
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		if !pool.IsRunning() {
			t.Error("pool should be running")
		}

		if pool.WorkerCount() != 4 {
			t.Errorf("expected 4 workers, got %d", pool.WorkerCount())
		}

		err = pool.Stop(ctx)
		if err != nil {
			t.Fatalf("Stop failed: %v", err)
		}

		if pool.IsRunning() {
			t.Error("pool should not be running")
		}
	})

	t.Run("Scale", func(t *testing.T) {
		pool := NewWorkerPool(queue, executor, PoolConfig{
			WorkerCount: 2,
			Concurrency: 4,
		})

		err := pool.Start(ctx)
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer pool.Stop(ctx)

		if pool.WorkerCount() != 2 {
			t.Errorf("expected 2 workers, got %d", pool.WorkerCount())
		}

		// Scale up
		err = pool.Scale(ctx, 6)
		if err != nil {
			t.Fatalf("Scale up failed: %v", err)
		}

		if pool.WorkerCount() != 6 {
			t.Errorf("expected 6 workers after scale up, got %d", pool.WorkerCount())
		}

		// Scale down
		err = pool.Scale(ctx, 3)
		if err != nil {
			t.Fatalf("Scale down failed: %v", err)
		}

		if pool.WorkerCount() != 3 {
			t.Errorf("expected 3 workers after scale down, got %d", pool.WorkerCount())
		}
	})

	t.Run("ScaleInvalidCount", func(t *testing.T) {
		pool := NewWorkerPool(queue, executor, DefaultPoolConfig())

		err := pool.Start(ctx)
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer pool.Stop(ctx)

		err = pool.Scale(ctx, 0)
		if err == nil {
			t.Error("expected error for invalid scale count")
		}

		err = pool.Scale(ctx, -1)
		if err == nil {
			t.Error("expected error for negative scale count")
		}
	})

	t.Run("StartAlreadyRunning", func(t *testing.T) {
		pool := NewWorkerPool(queue, executor, DefaultPoolConfig())

		err := pool.Start(ctx)
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		defer pool.Stop(ctx)

		err = pool.Start(ctx)
		if err == nil {
			t.Error("expected error when starting already running pool")
		}
	})
}

func TestDefaultConfigs(t *testing.T) {
	t.Run("DefaultWorkerConfig", func(t *testing.T) {
		config := DefaultWorkerConfig()

		if config.ID == "" {
			t.Error("ID should not be empty")
		}
		if config.Concurrency <= 0 {
			t.Error("Concurrency should be positive")
		}
		if config.Heartbeat == 0 {
			t.Error("Heartbeat should not be zero")
		}
	})

	t.Run("DefaultPoolConfig", func(t *testing.T) {
		config := DefaultPoolConfig()

		if config.WorkerCount <= 0 {
			t.Error("WorkerCount should be positive")
		}
		if config.Concurrency <= 0 {
			t.Error("Concurrency should be positive")
		}
		if config.QueueTopic == "" {
			t.Error("QueueTopic should not be empty")
		}
	})
}

func TestWorker_HandleTask(t *testing.T) {
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
	engine := orchestration.NewEngine()
	executor := NewStepExecutor(engine)

	// Register test step
	mockProv := &mockProvider{output: "handled output"}
	testAgent := &orchestration.Agent{
		ID:       "agent-1",
		Provider: mockProv,
		Model:    "test-model",
	}

	testStep := &orchestration.SequentialStep{
		StepName:  "test-step",
		Agent:     testAgent,
		InputFn:   func(state map[string]any) string { return "input" },
		OutputKey: "result",
	}

	executor.RegisterStep("test-step", testStep)

	worker := NewWorker(queue, executor, DefaultWorkerConfig())

	t.Run("HandleValidTask", func(t *testing.T) {
		task := &WorkflowTask{
			ID:            "task-1",
			ExecutionID:   "exec-1",
			WorkflowID:    "wf-1",
			StepName:      "test-step",
			WorkflowState: map[string]any{},
			Timeout:       5 * time.Second,
		}

		result, err := worker.handleTask(ctx, task)
		if err != nil {
			t.Fatalf("handleTask failed: %v", err)
		}

		if result == nil {
			t.Fatal("result should not be nil")
		}

		if result.TaskID != "task-1" {
			t.Errorf("expected task ID task-1, got %s", result.TaskID)
		}
	})

	t.Run("HandleNilTask", func(t *testing.T) {
		_, err := worker.handleTask(ctx, nil)
		if err == nil {
			t.Error("expected error for nil task")
		}
	})
}
