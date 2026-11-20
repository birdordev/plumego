package distributed

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	kv "github.com/birdordev/plumego/store/kv"

	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
	"github.com/birdordev/plumego/x/messaging/mq"
	"github.com/birdordev/plumego/x/messaging/pubsub"
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

func TestWorkerPoolScaleRequiresRunning(t *testing.T) {
	var subscriptions []context.Context
	entered := make(chan struct{})
	release := make(chan struct{})
	q := lifecycleQueue{subscribe: func(ctx context.Context) error {
		subscriptions = append(subscriptions, ctx)
		if len(subscriptions) == 2 {
			close(entered)
			<-release
		}
		return nil
	}}
	p := NewWorkerPool(q, nil, PoolConfig{WorkerCount: 1})
	if err := p.Scale(t.Context(), 2); err == nil || len(subscriptions) != 0 {
		t.Fatalf("cold scale: subscriptions=%d err=%v", len(subscriptions), err)
	}
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	scaled := make(chan error, 1)
	go func() { scaled <- p.Scale(t.Context(), 2) }()
	<-entered
	stopped := make(chan error, 1)
	stopping := make(chan struct{})
	go func() {
		close(stopping)
		stopped <- p.Stop(t.Context())
	}()
	<-stopping
	close(release)
	if err := <-scaled; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	for _, ctx := range subscriptions {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("subscription not canceled")
		}
	}
	if err := p.Scale(t.Context(), 3); err == nil || p.WorkerCount() != 0 || p.IsRunning() || len(subscriptions) != 2 {
		t.Fatalf("stopped scale: workers=%d subscriptions=%d err=%v", p.WorkerCount(), len(subscriptions), err)
	}
}

func TestWorkerStartRejectsNegativeHeartbeat(t *testing.T) {
	for _, interval := range []time.Duration{-time.Second, 0, time.Second} {
		calls := 0
		worker := NewWorker(lifecycleQueue{subscribe: func(context.Context) error { calls++; return nil }}, nil, WorkerConfig{Heartbeat: interval})
		err := worker.Start(t.Context())
		if interval < 0 {
			if err == nil || calls != 0 {
				t.Fatalf("invalid heartbeat: calls=%d err=%v", calls, err)
			}
			continue
		}
		if err != nil || calls != 1 {
			t.Fatalf("heartbeat %v: calls=%d err=%v", interval, calls, err)
		}
		if err := worker.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkerPoolSubscribeCanReadPoolState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var pool *WorkerPool
		var subscriptions []context.Context
		entered := make(chan struct{})
		release := make(chan struct{})
		q := lifecycleQueue{subscribe: func(ctx context.Context) error {
			count := pool.WorkerCount()
			running := pool.IsRunning()
			if count != len(subscriptions) || running != (count > 0) {
				t.Errorf("committed state during Subscribe = %d, %t", count, running)
			}
			subscriptions = append(subscriptions, ctx)
			if len(subscriptions) == 2 {
				close(entered)
				<-release
			}
			return nil
		}}
		pool = NewWorkerPool(q, nil, PoolConfig{WorkerCount: 1})
		if err := pool.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		scaled := make(chan error, 1)
		go func() { scaled <- pool.Scale(t.Context(), 2) }()
		<-entered

		waitCtx, cancel := context.WithCancel(t.Context())
		waiting := make(chan error, 1)
		go func() { waiting <- pool.Scale(waitCtx, 3) }()
		synctest.Wait()
		cancel()
		if err := <-waiting; !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting scale = %v, want cancellation", err)
		}
		stopped := make(chan error, 1)
		go func() { stopped <- pool.Stop(t.Context()) }()
		synctest.Wait()
		if count := pool.WorkerCount(); count != 1 {
			t.Fatalf("uncommitted worker count = %d", count)
		}
		close(release)
		if err := <-scaled; err != nil {
			t.Fatal(err)
		}
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		if pool.WorkerCount() != 0 || pool.IsRunning() {
			t.Fatal("pool retained workers after Stop")
		}
		for _, ctx := range subscriptions {
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("subscription not canceled")
			}
		}
	})
}

func TestWorkerPoolScaleRollsBackNewWorkers(t *testing.T) {
	injected := errors.New("subscription failed")
	var subscriptions []context.Context
	q := lifecycleQueue{subscribe: func(ctx context.Context) error {
		subscriptions = append(subscriptions, ctx)
		if len(subscriptions) == 3 {
			return injected
		}
		return nil
	}}
	pool := NewWorkerPool(q, nil, PoolConfig{WorkerCount: 1})
	if err := pool.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool.IsRunning() {
			if err := pool.Stop(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	if err := pool.Scale(t.Context(), 3); !errors.Is(err, injected) {
		t.Fatalf("scale error = %v", err)
	}
	if pool.WorkerCount() != 1 || pool.config.WorkerCount != 1 || !pool.IsRunning() ||
		subscriptions[0].Err() != nil || !errors.Is(subscriptions[1].Err(), context.Canceled) ||
		!errors.Is(subscriptions[2].Err(), context.Canceled) {
		t.Fatal("expansion did not roll back only the new workers")
	}
	if err := pool.Scale(t.Context(), 3); err != nil || pool.WorkerCount() != 3 {
		t.Fatalf("retry scale = %v, workers = %d", err, pool.WorkerCount())
	}
	if err := pool.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range subscriptions {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("subscription not canceled")
		}
	}
}
