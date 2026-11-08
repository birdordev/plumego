package distributed

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
)

// DistributedEngine orchestrates distributed workflow execution.
type DistributedEngine struct {
	localEngine  *orchestration.Engine
	taskQueue    TaskQueue
	persistence  WorkflowPersistence
	config       EngineConfig
	executions   map[string]*executionContext
	executionsMu sync.RWMutex
}

// EngineConfig configures the distributed engine.
type EngineConfig struct {
	DefaultTimeout    time.Duration
	EnableCheckpoints bool
	MaxRetries        int
	RetryDelay        time.Duration
}

// DefaultEngineConfig returns default engine configuration.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		DefaultTimeout:    30 * time.Minute,
		EnableCheckpoints: true,
		MaxRetries:        3,
		RetryDelay:        time.Second,
	}
}

// ExecutionOptions configures workflow execution.
type ExecutionOptions struct {
	Distributed bool          // Use distributed execution
	Timeout     time.Duration // Execution timeout
	RetryPolicy *RetryPolicy  // Retry policy for failed steps
	Checkpoints bool          // Enable checkpoint snapshots
}

// DefaultExecutionOptions returns default execution options.
func DefaultExecutionOptions() ExecutionOptions {
	return ExecutionOptions{
		Distributed: true,
		Timeout:     30 * time.Minute,
		RetryPolicy: DefaultRetryPolicy(),
		Checkpoints: true,
	}
}

// RetryPolicy defines retry behavior for failed steps.
type RetryPolicy struct {
	MaxRetries   int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64 // Exponential backoff multiplier
}

// DefaultRetryPolicy returns default retry policy.
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxRetries:   3,
		InitialDelay: time.Second,
		MaxDelay:     time.Minute,
		Multiplier:   2.0,
	}
}

// executionContext tracks execution state.
type executionContext struct {
	executionID string
	workflowID  string
	snapshot    *ExecutionSnapshot
	cancel      context.CancelFunc
	results     chan *TaskResult
	mu          sync.Mutex
}

// NewDistributedEngine creates a new distributed execution engine.
func NewDistributedEngine(
	localEngine *orchestration.Engine,
	taskQueue TaskQueue,
	persistence WorkflowPersistence,
	config EngineConfig,
) *DistributedEngine {
	if config.DefaultTimeout == 0 {
		config = DefaultEngineConfig()
	}

	return &DistributedEngine{
		localEngine: localEngine,
		taskQueue:   taskQueue,
		persistence: persistence,
		config:      config,
		executions:  make(map[string]*executionContext),
	}
}

// ExecuteAsync executes a workflow asynchronously.
func (e *DistributedEngine) ExecuteAsync(
	ctx context.Context,
	workflowID string,
	initialState map[string]any,
	options ExecutionOptions,
) (string, error) {
	// Generate execution ID
	executionID := fmt.Sprintf("%s-%d", workflowID, time.Now().UnixNano())

	// Get workflow
	workflow, err := e.localEngine.GetWorkflow(workflowID)
	if err != nil {
		return "", fmt.Errorf("workflow not found: %w", err)
	}

	// Create initial snapshot
	snapshot := &ExecutionSnapshot{
		ExecutionID:    executionID,
		WorkflowID:     workflowID,
		State:          initialState,
		CompletedSteps: []int{},
		CurrentStep:    0,
		Results:        []*orchestration.AgentResult{},
		Status:         StatusPending,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		TTL:            24 * time.Hour,
	}

	// Save initial snapshot
	if err := e.persistence.SaveSnapshot(ctx, snapshot); err != nil {
		return "", fmt.Errorf("failed to save initial snapshot: %w", err)
	}

	// Create execution context
	execCtx, cancel := context.WithCancel(ctx)
	e.executionsMu.Lock()
	e.executions[executionID] = &executionContext{
		executionID: executionID,
		workflowID:  workflowID,
		snapshot:    snapshot,
		cancel:      cancel,
		results:     make(chan *TaskResult, len(workflow.Steps)),
	}
	e.executionsMu.Unlock()

	// Execute in background
	go e.executeWorkflow(execCtx, workflow, executionID, initialState, options)

	return executionID, nil
}

// ExecuteSync executes a workflow synchronously.
func (e *DistributedEngine) ExecuteSync(
	ctx context.Context,
	workflowID string,
	initialState map[string]any,
	options ExecutionOptions,
) ([]*orchestration.AgentResult, error) {
	executionID, err := e.ExecuteAsync(ctx, workflowID, initialState, options)
	if err != nil {
		return nil, err
	}

	// Wait for completion
	return e.waitForCompletion(ctx, executionID, options.Timeout)
}

// executeWorkflow executes a workflow (internal).
func (e *DistributedEngine) executeWorkflow(
	ctx context.Context,
	workflow *orchestration.Workflow,
	executionID string,
	initialState map[string]any,
	options ExecutionOptions,
) {
	defer e.cleanupExecution(executionID)

	// Get execution context
	e.executionsMu.RLock()
	execCtx, ok := e.executions[executionID]
	e.executionsMu.RUnlock()
	if !ok {
		return
	}

	// Update status to running
	execCtx.mu.Lock()
	execCtx.snapshot.Status = StatusRunning
	execCtx.snapshot.State = initialState
	execCtx.mu.Unlock()
	_ = e.persistence.SaveSnapshot(ctx, execCtx.snapshot)

	// Execute steps
	for i, step := range workflow.Steps {
		select {
		case <-ctx.Done():
			e.updateSnapshotStatus(ctx, execCtx, StatusCancelled, "execution cancelled")
			return
		default:
		}

		// Update current step
		execCtx.mu.Lock()
		execCtx.snapshot.CurrentStep = i
		execCtx.mu.Unlock()

		// Execute step
		var result *orchestration.AgentResult
		var err error

		if options.Distributed {
			// Distributed execution via task queue
			result, err = e.executeStepDistributed(ctx, workflow, step, i, execCtx, options)
		} else {
			// Local execution
			result, err = step.Execute(ctx, workflow)
		}

		if err != nil {
			e.updateSnapshotStatus(ctx, execCtx, StatusFailed, err.Error())
			return
		}

		// Save result
		execCtx.mu.Lock()
		execCtx.snapshot.Results = append(execCtx.snapshot.Results, result)
		execCtx.snapshot.CompletedSteps = append(execCtx.snapshot.CompletedSteps, i)
		execCtx.snapshot.State = workflow.State
		execCtx.mu.Unlock()

		// Save checkpoint if enabled
		if options.Checkpoints {
			_ = e.persistence.SaveSnapshot(ctx, execCtx.snapshot)
			_ = e.persistence.SaveStepResult(ctx, executionID, i, result)
		}
	}

	// Mark as completed
	e.updateSnapshotStatus(ctx, execCtx, StatusCompleted, "")
}

// executeStepDistributed executes a step via task queue.
func (e *DistributedEngine) executeStepDistributed(
	ctx context.Context,
	workflow *orchestration.Workflow,
	step orchestration.Step,
	stepIndex int,
	execCtx *executionContext,
	options ExecutionOptions,
) (*orchestration.AgentResult, error) {
	// Create task
	task := &WorkflowTask{
		ID:            fmt.Sprintf("%s-%d", execCtx.executionID, stepIndex),
		ExecutionID:   execCtx.executionID,
		WorkflowID:    execCtx.workflowID,
		StepIndex:     stepIndex,
		StepName:      step.Name(),
		WorkflowState: execCtx.snapshot.State,
		Priority:      5,
		CreatedAt:     time.Now(),
		Timeout:       options.Timeout,
	}

	// Enqueue task
	if err := e.taskQueue.Enqueue(ctx, task); err != nil {
		return nil, fmt.Errorf("failed to enqueue task: %w", err)
	}

	// Wait for result with timeout
	timeout := options.Timeout
	if timeout == 0 {
		timeout = e.config.DefaultTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-execCtx.results:
		if result.Error != "" {
			return result.AgentResult, fmt.Errorf("%s", result.Error)
		}
		// Update workflow state with result state
		if result.UpdatedState != nil {
			for k, v := range result.UpdatedState {
				workflow.State[k] = v
			}
		}
		return result.AgentResult, nil
	case <-timer.C:
		return nil, fmt.Errorf("task execution timeout after %v", timeout)
	}
}

// Resume resumes a paused or failed execution.
func (e *DistributedEngine) Resume(ctx context.Context, executionID string) error {
	// Load snapshot
	snapshot, err := e.persistence.LoadSnapshot(ctx, executionID)
	if err != nil {
		return fmt.Errorf("failed to load snapshot: %w", err)
	}

	if snapshot.Status == StatusCompleted {
		return fmt.Errorf("execution already completed")
	}

	// Get workflow
	workflow, err := e.localEngine.GetWorkflow(snapshot.WorkflowID)
	if err != nil {
		return fmt.Errorf("workflow not found: %w", err)
	}

	// Continue from current step
	options := DefaultExecutionOptions()
	go e.resumeWorkflow(ctx, workflow, snapshot, options)

	return nil
}

// resumeWorkflow resumes workflow execution from snapshot.
func (e *DistributedEngine) resumeWorkflow(
	ctx context.Context,
	workflow *orchestration.Workflow,
	snapshot *ExecutionSnapshot,
	options ExecutionOptions,
) {
	// Restore workflow state
	workflow.State = snapshot.State

	// Continue from current step
	for i := snapshot.CurrentStep; i < len(workflow.Steps); i++ {
		step := workflow.Steps[i]

		var result *orchestration.AgentResult
		var err error

		if options.Distributed {
			execCtx := &executionContext{
				executionID: snapshot.ExecutionID,
				workflowID:  snapshot.WorkflowID,
				snapshot:    snapshot,
				results:     make(chan *TaskResult, 1),
			}
			result, err = e.executeStepDistributed(ctx, workflow, step, i, execCtx, options)
		} else {
			result, err = step.Execute(ctx, workflow)
		}

		if err != nil {
			snapshot.Status = StatusFailed
			snapshot.Error = err.Error()
			_ = e.persistence.SaveSnapshot(ctx, snapshot)
			return
		}

		snapshot.Results = append(snapshot.Results, result)
		snapshot.CompletedSteps = append(snapshot.CompletedSteps, i)
		snapshot.CurrentStep = i + 1
		_ = e.persistence.SaveSnapshot(ctx, snapshot)
	}

	snapshot.Status = StatusCompleted
	_ = e.persistence.SaveSnapshot(ctx, snapshot)
}

// Pause pauses an execution.
func (e *DistributedEngine) Pause(ctx context.Context, executionID string) error {
	e.executionsMu.RLock()
	execCtx, ok := e.executions[executionID]
	e.executionsMu.RUnlock()

	if !ok {
		return fmt.Errorf("execution not found: %s", executionID)
	}

	execCtx.cancel()
	e.updateSnapshotStatus(ctx, execCtx, StatusPaused, "paused by user")

	return nil
}

// GetExecutionStatus retrieves execution status.
func (e *DistributedEngine) GetExecutionStatus(
	ctx context.Context,
	executionID string,
) (*ExecutionSnapshot, error) {
	return e.persistence.LoadSnapshot(ctx, executionID)
}

// waitForCompletion waits for execution to complete.
func (e *DistributedEngine) waitForCompletion(
	ctx context.Context,
	executionID string,
	timeout time.Duration,
) ([]*orchestration.AgentResult, error) {
	if timeout == 0 {
		timeout = e.config.DefaultTimeout
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("execution timeout after %v", timeout)
			}

			snapshot, err := e.persistence.LoadSnapshot(ctx, executionID)
			if err != nil {
				return nil, err
			}

			switch snapshot.Status {
			case StatusCompleted:
				return snapshot.Results, nil
			case StatusFailed:
				return snapshot.Results, fmt.Errorf("execution failed: %s", snapshot.Error)
			case StatusCancelled:
				return snapshot.Results, fmt.Errorf("execution cancelled")
			}
		}
	}
}

// updateSnapshotStatus updates snapshot status.
func (e *DistributedEngine) updateSnapshotStatus(
	ctx context.Context,
	execCtx *executionContext,
	status ExecutionStatus,
	errorMsg string,
) {
	execCtx.mu.Lock()
	execCtx.snapshot.Status = status
	execCtx.snapshot.Error = errorMsg
	execCtx.snapshot.UpdatedAt = time.Now()
	execCtx.mu.Unlock()

	_ = e.persistence.SaveSnapshot(ctx, execCtx.snapshot)
}

// cleanupExecution removes execution context.
func (e *DistributedEngine) cleanupExecution(executionID string) {
	e.executionsMu.Lock()
	defer e.executionsMu.Unlock()

	if execCtx, ok := e.executions[executionID]; ok {
		close(execCtx.results)
		delete(e.executions, executionID)
	}
}

// Close closes the distributed engine.
func (e *DistributedEngine) Close() error {
	e.executionsMu.Lock()
	defer e.executionsMu.Unlock()

	// Cancel all running executions
	for _, execCtx := range e.executions {
		execCtx.cancel()
	}

	return nil
}
