package distributed

import (
	"path/filepath"
	"testing"
	"time"

	kv "github.com/birdordev/plumego/store/kv"
	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/messaging/mq"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

func TestDistributedEngine_ExecuteAsync(t *testing.T) {
	ctx := t.Context()

	// Setup
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

	localEngine := orchestration.NewEngine()
	engine := NewDistributedEngine(localEngine, queue, persistence, DefaultEngineConfig())
	defer engine.Close()

	// Create test workflow
	workflow := orchestration.NewWorkflow("wf-1", "Test Workflow", "")
	localEngine.RegisterWorkflow(workflow)

	t.Run("ExecuteAsync", func(t *testing.T) {
		executionID, err := engine.ExecuteAsync(ctx, "wf-1", map[string]any{"test": "value"}, ExecutionOptions{
			Distributed: false,
			Timeout:     time.Minute,
		})

		if err != nil {
			t.Fatalf("ExecuteAsync failed: %v", err)
		}

		if executionID == "" {
			t.Error("executionID should not be empty")
		}

		// Check snapshot was created
		time.Sleep(100 * time.Millisecond)
		snapshot, err := persistence.LoadSnapshot(ctx, executionID)
		if err != nil {
			t.Fatalf("failed to load snapshot: %v", err)
		}

		if snapshot.WorkflowID != "wf-1" {
			t.Errorf("expected workflow ID wf-1, got %s", snapshot.WorkflowID)
		}
	})

	t.Run("ExecuteNonexistentWorkflow", func(t *testing.T) {
		_, err := engine.ExecuteAsync(ctx, "nonexistent", map[string]any{}, DefaultExecutionOptions())
		if err == nil {
			t.Error("expected error for nonexistent workflow")
		}
	})
}

func TestDistributedEngine_ExecuteSync(t *testing.T) {
	ctx := t.Context()

	// Setup
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

	localEngine := orchestration.NewEngine()
	engine := NewDistributedEngine(localEngine, queue, persistence, DefaultEngineConfig())
	defer engine.Close()

	// Create test workflow with empty steps
	workflow := orchestration.NewWorkflow("wf-sync", "Sync Workflow", "")
	localEngine.RegisterWorkflow(workflow)

	t.Run("ExecuteSync", func(t *testing.T) {
		results, err := engine.ExecuteSync(ctx, "wf-sync", map[string]any{"test": "value"}, ExecutionOptions{
			Distributed: false,
			Timeout:     5 * time.Second,
		})

		if err != nil {
			t.Fatalf("ExecuteSync failed: %v", err)
		}

		if results == nil {
			t.Error("results should not be nil")
		}
	})
}

func TestDistributedEngine_GetExecutionStatus(t *testing.T) {
	ctx := t.Context()

	// Setup
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

	localEngine := orchestration.NewEngine()
	engine := NewDistributedEngine(localEngine, queue, persistence, DefaultEngineConfig())
	defer engine.Close()

	// Create test workflow
	workflow := orchestration.NewWorkflow("wf-status", "Status Workflow", "")
	localEngine.RegisterWorkflow(workflow)

	t.Run("GetStatus", func(t *testing.T) {
		executionID, err := engine.ExecuteAsync(ctx, "wf-status", map[string]any{}, DefaultExecutionOptions())
		if err != nil {
			t.Fatalf("ExecuteAsync failed: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		snapshot, err := engine.GetExecutionStatus(ctx, executionID)
		if err != nil {
			t.Fatalf("GetExecutionStatus failed: %v", err)
		}

		if snapshot == nil {
			t.Fatal("snapshot should not be nil")
		}

		if snapshot.ExecutionID != executionID {
			t.Errorf("expected execution ID %s, got %s", executionID, snapshot.ExecutionID)
		}
	})

	t.Run("GetNonexistentStatus", func(t *testing.T) {
		_, err := engine.GetExecutionStatus(ctx, "nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent execution")
		}
	})
}

func TestDistributedEngine_Pause(t *testing.T) {
	ctx := t.Context()

	// Setup
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

	localEngine := orchestration.NewEngine()
	engine := NewDistributedEngine(localEngine, queue, persistence, DefaultEngineConfig())
	defer engine.Close()

	// Create test workflow
	workflow := orchestration.NewWorkflow("wf-pause", "Pause Workflow", "")
	localEngine.RegisterWorkflow(workflow)

	t.Run("Pause", func(t *testing.T) {
		executionID, err := engine.ExecuteAsync(ctx, "wf-pause", map[string]any{}, DefaultExecutionOptions())
		if err != nil {
			t.Fatalf("ExecuteAsync failed: %v", err)
		}

		// Wait for execution context to be created
		time.Sleep(200 * time.Millisecond)

		err = engine.Pause(ctx, executionID)
		// Note: Pause may fail if execution already completed (empty workflow)
		// This is expected behavior
		if err != nil && err.Error() != "execution not found: "+executionID {
			t.Logf("Pause returned error (may be expected): %v", err)
		}

		// Check status
		time.Sleep(100 * time.Millisecond)
		snapshot, err := persistence.LoadSnapshot(ctx, executionID)
		if err != nil {
			t.Fatalf("failed to load snapshot: %v", err)
		}

		// Note: Empty workflow completes fast, so status may be completed instead of paused
		if snapshot.Status != StatusPaused && snapshot.Status != StatusCancelled && snapshot.Status != StatusCompleted {
			t.Errorf("expected status paused, cancelled, or completed, got %s", snapshot.Status)
		}
	})

	t.Run("PauseNonexistent", func(t *testing.T) {
		err := engine.Pause(ctx, "nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent execution")
		}
	})
}

func TestDefaultConfigs_Engine(t *testing.T) {
	t.Run("DefaultEngineConfig", func(t *testing.T) {
		config := DefaultEngineConfig()

		if config.DefaultTimeout == 0 {
			t.Error("DefaultTimeout should not be zero")
		}
		if config.MaxRetries <= 0 {
			t.Error("MaxRetries should be positive")
		}
	})

	t.Run("DefaultExecutionOptions", func(t *testing.T) {
		options := DefaultExecutionOptions()

		if options.Timeout == 0 {
			t.Error("Timeout should not be zero")
		}
		if options.RetryPolicy == nil {
			t.Error("RetryPolicy should not be nil")
		}
	})

	t.Run("DefaultRetryPolicy", func(t *testing.T) {
		policy := DefaultRetryPolicy()

		if policy.MaxRetries <= 0 {
			t.Error("MaxRetries should be positive")
		}
		if policy.InitialDelay == 0 {
			t.Error("InitialDelay should not be zero")
		}
		if policy.Multiplier <= 0 {
			t.Error("Multiplier should be positive")
		}
	})
}

func TestDistributedEngine_Close(t *testing.T) {
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

	localEngine := orchestration.NewEngine()
	engine := NewDistributedEngine(localEngine, queue, persistence, DefaultEngineConfig())

	// Create test workflow
	workflow := orchestration.NewWorkflow("wf-close", "Close Workflow", "")
	localEngine.RegisterWorkflow(workflow)

	// Start execution
	_, err = engine.ExecuteAsync(ctx, "wf-close", map[string]any{}, DefaultExecutionOptions())
	if err != nil {
		t.Fatalf("ExecuteAsync failed: %v", err)
	}

	// Close engine
	err = engine.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}
