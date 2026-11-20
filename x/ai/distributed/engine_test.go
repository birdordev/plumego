package distributed

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
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

type engineTestStep func(context.Context, *orchestration.Workflow) (*orchestration.AgentResult, error)

func (engineTestStep) Name() string { return "test-step" }

func (s engineTestStep) Execute(ctx context.Context, wf *orchestration.Workflow) (*orchestration.AgentResult, error) {
	return s(ctx, wf)
}

type engineTestQueue struct {
	TaskQueue
	subscribe func(context.Context, string) (<-chan *TaskResult, error)
	enqueue   func(context.Context, *WorkflowTask) error
}

func (q engineTestQueue) SubscribeResults(ctx context.Context, id string) (<-chan *TaskResult, error) {
	return q.subscribe(ctx, id)
}

func (q engineTestQueue) Enqueue(ctx context.Context, task *WorkflowTask) error {
	return q.enqueue(ctx, task)
}

type engineTestPersistence struct {
	WorkflowPersistence
	mu       sync.Mutex
	snapshot ExecutionSnapshot
	save     func(*ExecutionSnapshot) error
	saveStep func(int) error
}

func (p *engineTestPersistence) SaveSnapshot(_ context.Context, snapshot *ExecutionSnapshot) error {
	if p.save != nil {
		if err := p.save(snapshot); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot = *snapshot
	return nil
}

func (p *engineTestPersistence) LoadSnapshot(context.Context, string) (*ExecutionSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := p.snapshot
	return &snapshot, nil
}

func (p *engineTestPersistence) SaveStepResult(_ context.Context, _ string, index int, _ *orchestration.AgentResult) error {
	if p.saveStep != nil {
		return p.saveStep(index)
	}
	return nil
}

func newEngineForTest(queue TaskQueue, persistence WorkflowPersistence, steps ...orchestration.Step) *DistributedEngine {
	local := orchestration.NewEngine()
	workflow := orchestration.NewWorkflow("workflow", "Workflow", "")
	for _, step := range steps {
		workflow.AddStep(step)
	}
	local.RegisterWorkflow(workflow)
	return NewDistributedEngine(local, queue, persistence, DefaultEngineConfig())
}

func TestEngineReceivesSubscribedTaskResults(t *testing.T) {
	injected := errors.New("subscribe failed")
	for _, tc := range []struct {
		name      string
		wantError string
	}{
		{name: "success"},
		{name: "closed", wantError: "subscription closed"},
		{name: "remote error", wantError: "worker failed"},
		{name: "subscribe error", wantError: "subscribe failed"},
		{name: "cancellation", wantError: "context canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			resultCh := make(chan *TaskResult, 4)
			subscribed := make(chan context.Context, 1)
			var subscriptions, steps int
			queue := engineTestQueue{
				subscribe: func(ctx context.Context, id string) (<-chan *TaskResult, error) {
					subscriptions++
					if tc.name == "subscribe error" {
						return nil, injected
					}
					subscribed <- ctx
					return resultCh, nil
				},
				enqueue: func(_ context.Context, task *WorkflowTask) error {
					steps++
					if subscriptions != 1 {
						return errors.New("expected subscription before enqueue")
					}
					if tc.name == "cancellation" {
						cancel()
						return nil
					}
					if tc.name == "closed" {
						close(resultCh)
						return nil
					}
					if task.StepIndex == 1 && task.WorkflowState["first"] != "value" {
						return errors.New("missing previous step state")
					}
					resultCh <- nil
					resultCh <- &TaskResult{TaskID: "unrelated", Error: "ignore"}
					if tc.name == "remote error" {
						resultCh <- &TaskResult{TaskID: task.ID, Error: "worker failed"}
					} else {
						resultCh <- &TaskResult{TaskID: task.ID, AgentResult: &orchestration.AgentResult{Output: "done"}, UpdatedState: map[string]any{"first": "value"}}
					}
					return nil
				},
			}
			step := engineTestStep(nil)
			engine := newEngineForTest(queue, &engineTestPersistence{}, step, step)
			got, err := engine.ExecuteSync(ctx, "workflow", nil, ExecutionOptions{Distributed: true, Timeout: time.Second, Checkpoints: true})
			if tc.wantError == "" {
				if err != nil || len(got) != 2 || got[0].Output != "done" || steps != 2 || subscriptions != 1 {
					t.Fatalf("ExecuteSync = %v, %v; steps = %d; subscriptions = %d", got, err, steps, subscriptions)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("ExecuteSync error=%v, want %q", err, tc.wantError)
			}
			if tc.name == "subscribe error" {
				if !errors.Is(err, injected) || steps != 0 {
					t.Fatalf("subscription error=%v, steps=%d", err, steps)
				}
				return
			}
			select {
			case subscriptionCtx := <-subscribed:
				select {
				case <-subscriptionCtx.Done():
				case <-time.After(time.Second):
					t.Fatal("result subscription still active")
				}
			case <-time.After(time.Second):
				t.Fatal("subscription never started")
			}
		})
	}
}

func TestResumeSkipsCommittedWorkflowSteps(t *testing.T) {
	for _, cursor := range []int{0, 1} {
		t.Run(fmt.Sprint(cursor), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := &engineTestPersistence{snapshot: ExecutionSnapshot{
					ExecutionID: "resume", WorkflowID: "workflow", Status: StatusFailed, CurrentStep: cursor,
					CompletedSteps: []int{0}, Results: []*orchestration.AgentResult{{Output: "old"}},
					State: map[string]any{"input": "saved"},
				}}
				resultCh := make(chan *TaskResult, 2)
				var dispatched []int
				q := engineTestQueue{
					subscribe: func(context.Context, string) (<-chan *TaskResult, error) { return resultCh, nil },
					enqueue: func(_ context.Context, task *WorkflowTask) error {
						dispatched = append(dispatched, task.StepIndex)
						if task.WorkflowState["input"] != "saved" {
							return errors.New("snapshot state not restored")
						}
						resultCh <- &TaskResult{TaskID: task.ID, AgentResult: &orchestration.AgentResult{Output: "new"}}
						return nil
					},
				}
				step := engineTestStep(nil)
				engine := newEngineForTest(q, p, step, step)
				if err := engine.Resume(t.Context(), "resume"); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				got, err := engine.GetExecutionStatus(t.Context(), "resume")
				if err != nil || !slices.Equal(dispatched, []int{1}) || got.Status != StatusCompleted || got.CurrentStep != 2 ||
					!slices.Equal(got.CompletedSteps, []int{0, 1}) || len(got.Results) != 2 || got.Results[0].Output != "old" || got.Results[1].Output != "new" {
					t.Fatalf("snapshot=%+v dispatched=%v err=%v", got, dispatched, err)
				}
			})
		})
	}
}

func TestPausePreservesWinningExecutionState(t *testing.T) {
	saveFailure := errors.New("pause write failed")
	for _, mode := range []string{"pause wins", "completion wins", "pause write fails"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started := make(chan struct{})
				proceed := make(chan struct{})
				p := &engineTestPersistence{save: func(s *ExecutionSnapshot) error {
					if mode == "pause write fails" && s.Status == StatusPaused {
						return saveFailure
					}
					return nil
				}}
				engine := newEngineForTest(nil, p, engineTestStep(func(ctx context.Context, _ *orchestration.Workflow) (*orchestration.AgentResult, error) {
					close(started)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-proceed:
						return &orchestration.AgentResult{Output: "finished"}, nil
					}
				}))
				id, err := engine.ExecuteAsync(t.Context(), "workflow", nil, ExecutionOptions{})
				if err != nil {
					t.Fatal(err)
				}
				<-started
				if mode == "completion wins" {
					close(proceed)
					synctest.Wait()
				}
				err = engine.Pause(t.Context(), id)
				want := StatusPaused
				switch mode {
				case "pause wins":
					if err != nil {
						t.Fatal(err)
					}
				case "completion wins":
					if err == nil {
						t.Fatal("Pause accepted a completed execution")
					}
					want = StatusCompleted
				case "pause write fails":
					close(proceed)
					if !errors.Is(err, saveFailure) {
						t.Fatalf("Pause error=%v", err)
					}
					want = StatusCompleted
				}
				synctest.Wait()
				got, err := engine.GetExecutionStatus(t.Context(), id)
				if err != nil || got.Status != want {
					t.Fatalf("status=%+v err=%v; want %s", got, err, want)
				}
			})
		})
	}
}

func TestEngineStopsOnCheckpointPersistenceFailure(t *testing.T) {
	checkpointErr := errors.New("checkpoint failure")
	finishErr := errors.New("terminal save failure")
	for _, mode := range []string{"result", "snapshot", "snapshot and terminal"} {
		t.Run(mode, func(t *testing.T) {
			var calls, resultWrites int
			p := &engineTestPersistence{
				save: func(s *ExecutionSnapshot) error {
					if s.Status == StatusRunning && s.CurrentStep == 1 && mode != "result" {
						return checkpointErr
					}
					if s.Status == StatusFailed && mode == "snapshot and terminal" {
						return finishErr
					}
					return nil
				},
				saveStep: func(int) error {
					resultWrites++
					if mode == "result" {
						return checkpointErr
					}
					return nil
				},
			}
			step := engineTestStep(func(_ context.Context, wf *orchestration.Workflow) (*orchestration.AgentResult, error) {
				calls++
				wf.SetState("local", "step output")
				return &orchestration.AgentResult{Output: "done"}, nil
			})
			engine := newEngineForTest(nil, p, step, step)
			got, err := engine.ExecuteSync(t.Context(), "workflow", nil, ExecutionOptions{Checkpoints: true, Timeout: time.Second})
			if calls != 1 || resultWrites != 1 || len(got) != 0 || !errors.Is(err, checkpointErr) {
				t.Fatalf("calls=%d writes=%d results=%v err=%v", calls, resultWrites, got, err)
			}
			status := StatusFailed
			if mode == "snapshot and terminal" {
				status = StatusRunning
				if !errors.Is(err, finishErr) {
					t.Fatalf("terminal write error lost: %v", err)
				}
			}
			snapshot, _ := p.LoadSnapshot(t.Context(), "")
			if snapshot.Status != status || snapshot.CurrentStep != 0 || len(snapshot.CompletedSteps) != 0 || len(snapshot.State) != 0 {
				t.Fatalf("incorrect recovery snapshot %+v", snapshot)
			}
		})
	}
}

func TestResumeIgnoresPriorDispatchResult(t *testing.T) {
	for _, lateError := range []string{"", "prior dispatch failed"} {
		synctest.Test(t, func(t *testing.T) {
			persistence := &engineTestPersistence{}
			var results chan *TaskResult
			var firstID, resumedID string
			initialFailure := "initial failure"
			queue := engineTestQueue{
				subscribe: func(context.Context, string) (<-chan *TaskResult, error) {
					results = make(chan *TaskResult, 2)
					return results, nil
				},
				enqueue: func(_ context.Context, task *WorkflowTask) error {
					if firstID == "" {
						firstID = task.ID
						results <- &TaskResult{TaskID: firstID, Error: initialFailure}
						return nil
					}
					resumedID = task.ID
					results <- &TaskResult{TaskID: firstID, Error: lateError, AgentResult: &orchestration.AgentResult{Output: "old"}}
					results <- &TaskResult{TaskID: resumedID, AgentResult: &orchestration.AgentResult{Output: "current"}}
					return nil
				},
			}
			engine := newEngineForTest(queue, persistence, engineTestStep(nil))
			executionID, err := engine.ExecuteAsync(t.Context(), "workflow", nil, DefaultExecutionOptions())
			if err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			before, err := engine.GetExecutionStatus(t.Context(), executionID)
			if err != nil || before.Status != StatusFailed || !strings.Contains(before.Error, initialFailure) {
				t.Fatalf("initial run = %+v, %v", before, err)
			}
			if err := engine.Resume(t.Context(), executionID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			got, err := engine.GetExecutionStatus(t.Context(), executionID)
			if err != nil || firstID == resumedID || got.Status != StatusCompleted ||
				len(got.Results) != 1 || got.Results[0].Output != "current" {
				t.Fatalf("resumed run = %+v, %v; dispatch IDs: %q, %q", got, err, firstID, resumedID)
			}
		})
	}
}
