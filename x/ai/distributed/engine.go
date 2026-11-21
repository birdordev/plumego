package distributed

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"slices"
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
	Timeout     time.Duration // Total execution timeout; zero uses the engine default
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
	// mu serializes snapshot persistence and state transitions for this execution.
	// Persistence methods must honor their context and must not reenter Pause for
	// the same execution. Steps and queue operations run outside this lock.
	mu       sync.Mutex
	snapshot *ExecutionSnapshot
	cancel   context.CancelFunc
	done     chan struct{}
	finished bool
	// result and err are written before done closes and read only afterward.
	result []*orchestration.AgentResult
	err    error
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

// ExecuteAsync executes a workflow asynchronously and returns an opaque execution ID.
func (e *DistributedEngine) ExecuteAsync(
	ctx context.Context,
	workflowID string,
	initialState map[string]any,
	options ExecutionOptions,
) (string, error) {
	id, _, _, err := e.startExecution(ctx, workflowID, initialState, options)
	return id, err
}

func (e *DistributedEngine) startExecution(
	ctx context.Context,
	workflowID string,
	initialState map[string]any,
	options ExecutionOptions,
) (string, context.Context, *executionContext, error) {
	workflow, err := e.localEngine.GetWorkflow(workflowID)
	if err != nil {
		return "", nil, nil, err
	}

	now := time.Now()
	id := fmt.Sprintf("%s-%d-%s", workflowID, now.UnixNano(), rand.Text())
	snapshot := &ExecutionSnapshot{
		ExecutionID:    id,
		WorkflowID:     workflowID,
		Distributed:    &options.Distributed,
		State:          maps.Clone(initialState),
		CompletedSteps: []int{},
		Results:        []*orchestration.AgentResult{},
		Status:         StatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
		TTL:            24 * time.Hour,
	}
	runCtx, execution, err := e.reserveExecution(ctx, id, e.executionTimeout(options))
	if err != nil {
		return "", nil, nil, err
	}
	if err := e.persistence.SaveSnapshot(runCtx, snapshot); err != nil {
		e.cleanupExecution(id, execution)
		return "", nil, nil, fmt.Errorf("save initial snapshot: %w", err)
	}
	e.launchExecution(runCtx, workflow, execution, snapshot, options)
	return id, runCtx, execution, nil
}

// ExecuteSync executes a workflow synchronously. If execution fails, it returns
// completed results and the underlying failure, including persistence errors.
func (e *DistributedEngine) ExecuteSync(
	ctx context.Context,
	workflowID string,
	initialState map[string]any,
	options ExecutionOptions,
) ([]*orchestration.AgentResult, error) {
	_, runCtx, execution, err := e.startExecution(ctx, workflowID, initialState, options)
	if err != nil {
		return nil, err
	}

	select {
	case <-execution.done:
		return execution.result, execution.err
	case <-runCtx.Done():
		// Normal completion releases the execution context after publishing results.
		select {
		case <-execution.done:
			return execution.result, execution.err
		default:
			return nil, runCtx.Err()
		}
	}
}

func (e *DistributedEngine) executionTimeout(options ExecutionOptions) time.Duration {
	if options.Timeout != 0 {
		return options.Timeout
	}
	return e.config.DefaultTimeout
}

func (e *DistributedEngine) reserveExecution(ctx context.Context, id string, timeout time.Duration) (context.Context, *executionContext, error) {
	e.executionsMu.Lock()
	defer e.executionsMu.Unlock()
	if _, exists := e.executions[id]; exists {
		return nil, nil, fmt.Errorf("execution %s is already active", id)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	execution := &executionContext{cancel: cancel, done: make(chan struct{})}
	e.executions[id] = execution
	return runCtx, execution, nil
}

func (e *DistributedEngine) launchExecution(
	ctx context.Context,
	definition *orchestration.Workflow,
	execution *executionContext,
	snapshot *ExecutionSnapshot,
	options ExecutionOptions,
) {
	workflow := &orchestration.Workflow{
		ID: definition.ID, Name: definition.Name, Description: definition.Description,
		Steps: slices.Clone(definition.Steps), State: maps.Clone(snapshot.State),
	}
	if workflow.State == nil {
		workflow.State = make(map[string]any)
	}
	execution.mu.Lock()
	execution.snapshot = snapshot
	execution.mu.Unlock()
	go func() {
		defer execution.cancel()
		defer close(execution.done)
		defer e.removeExecution(snapshot.ExecutionID)
		status, err := e.runWorkflow(ctx, workflow, execution, snapshot, options)
		e.finishExecution(ctx, execution, status, err)
	}()
}

func (e *DistributedEngine) runWorkflow(
	ctx context.Context,
	workflow *orchestration.Workflow,
	execution *executionContext,
	initial *ExecutionSnapshot,
	options ExecutionOptions,
) (ExecutionStatus, error) {
	// Older snapshots can point at the most recently completed step.
	start := initial.CurrentStep
	completed := make(map[int]struct{}, len(initial.CompletedSteps))
	for _, index := range initial.CompletedSteps {
		completed[index] = struct{}{}
	}
	for start < len(workflow.Steps) {
		if _, ok := completed[start]; !ok {
			break
		}
		start++
	}
	if err := e.markRunning(ctx, execution, start); err != nil {
		return StatusFailed, err
	}

	var results <-chan *TaskResult
	for i := start; i < len(workflow.Steps); i++ {
		if err := ctx.Err(); err != nil {
			return StatusCancelled, err
		}
		if _, ok := completed[i]; ok {
			continue
		}

		step := workflow.Steps[i]
		var result *orchestration.AgentResult
		var err error
		if options.Distributed {
			if results == nil {
				if e.taskQueue == nil {
					return StatusFailed, errors.New("distributed execution requires a task queue")
				}
				results, err = e.taskQueue.SubscribeResults(ctx, initial.ExecutionID)
				if err != nil {
					return StatusFailed, fmt.Errorf("subscribe to task results: %w", err)
				}
				if results == nil {
					return StatusFailed, errors.New("task result subscription returned a nil channel")
				}
			}
			result, err = e.executeStepDistributed(ctx, workflow, step, i, initial.ExecutionID, results)
		} else {
			result, err = step.Execute(ctx, workflow)
		}
		if ctx.Err() != nil {
			return StatusCancelled, ctx.Err()
		}
		if err != nil {
			return StatusFailed, fmt.Errorf("execute step %s: %w", step.Name(), err)
		}
		if err := e.completeStep(ctx, execution, workflow.State, i, result, options.Checkpoints); err != nil {
			return StatusFailed, err
		}
	}
	return StatusCompleted, nil
}

func (e *DistributedEngine) markRunning(ctx context.Context, execution *executionContext, start int) error {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := *execution.snapshot
	next.Status = StatusRunning
	next.CurrentStep = start
	next.Error = ""
	return e.saveExecutionSnapshot(ctx, execution, &next)
}

func (e *DistributedEngine) completeStep(
	ctx context.Context,
	execution *executionContext,
	state map[string]any,
	index int,
	result *orchestration.AgentResult,
	checkpoint bool,
) error {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := *execution.snapshot
	next.State = maps.Clone(state)
	next.CurrentStep = index + 1
	next.CompletedSteps = append(slices.Clone(next.CompletedSteps), index)
	next.Results = append(slices.Clone(next.Results), result)

	if !checkpoint {
		execution.snapshot = &next
		return nil
	}
	// The snapshot is the recovery commit point. A separately stored step result
	// can remain if saving the snapshot fails.
	if result != nil {
		if err := e.persistence.SaveStepResult(ctx, next.ExecutionID, index, result); err != nil {
			return fmt.Errorf("save checkpoint result for step %d: %w", index, err)
		}
	}
	return e.saveExecutionSnapshot(ctx, execution, &next)
}

// saveExecutionSnapshot publishes the new in-memory state only after
// persistence succeeds. The caller holds execution.mu.
func (e *DistributedEngine) saveExecutionSnapshot(ctx context.Context, execution *executionContext, next *ExecutionSnapshot) error {
	next.UpdatedAt = time.Now()
	if err := e.persistence.SaveSnapshot(ctx, next); err != nil {
		return fmt.Errorf("save %s snapshot at step %d: %w", next.Status, next.CurrentStep, err)
	}
	execution.snapshot = next
	return nil
}

func (e *DistributedEngine) finishExecution(ctx context.Context, execution *executionContext, status ExecutionStatus, cause error) {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	execution.finished = true
	execution.result = execution.snapshot.Results
	if execution.snapshot.Status == StatusPaused {
		execution.err = errors.New("execution paused")
		return
	}
	if ctx.Err() != nil && (cause == nil || errors.Is(cause, ctx.Err())) {
		status = StatusCancelled
		cause = ctx.Err()
	}

	// Terminal state remains observable after cancellation. Bound the detached
	// write using the engine's existing persistence lifetime budget.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.config.DefaultTimeout)
	defer cancel()

	next := *execution.snapshot
	next.Status = status
	if cause != nil {
		next.Error = cause.Error()
	}
	saveErr := e.saveExecutionSnapshot(saveCtx, execution, &next)
	if saveErr != nil && status == StatusCompleted {
		next.Status = StatusFailed
		next.Error = saveErr.Error()
		saveErr = errors.Join(saveErr, e.saveExecutionSnapshot(saveCtx, execution, &next))
	}
	execution.err = errors.Join(cause, saveErr)
}

func (e *DistributedEngine) executeStepDistributed(
	ctx context.Context,
	workflow *orchestration.Workflow,
	step orchestration.Step,
	stepIndex int,
	executionID string,
	results <-chan *TaskResult,
) (*orchestration.AgentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	task := &WorkflowTask{
		ID:            fmt.Sprintf("%s-%d-%s", executionID, stepIndex, rand.Text()),
		ExecutionID:   executionID,
		WorkflowID:    workflow.ID,
		StepIndex:     stepIndex,
		StepName:      step.Name(),
		WorkflowState: maps.Clone(workflow.State),
		Priority:      5,
		CreatedAt:     time.Now(),
		Timeout:       timeout,
	}
	if err := e.taskQueue.Enqueue(ctx, task); err != nil {
		return nil, fmt.Errorf("enqueue task: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result, ok := <-results:
			if !ok {
				return nil, errors.New("task result subscription closed")
			}
			if result == nil || result.TaskID != task.ID {
				continue
			}
			if result.Error != "" {
				return result.AgentResult, errors.New(result.Error)
			}
			maps.Copy(workflow.State, result.UpdatedState)
			return result.AgentResult, nil
		}
	}
}

// Resume restarts an inactive execution using its persisted snapshot. Steps
// already recorded as complete are skipped, including in older snapshots.
func (e *DistributedEngine) Resume(ctx context.Context, executionID string) error {
	runCtx, execution, err := e.reserveExecution(ctx, executionID, e.executionTimeout(DefaultExecutionOptions()))
	if err != nil {
		return err
	}
	snapshot, err := e.persistence.LoadSnapshot(runCtx, executionID)
	if err != nil {
		e.cleanupExecution(executionID, execution)
		return fmt.Errorf("load snapshot: %w", err)
	}
	if snapshot == nil || snapshot.ExecutionID != executionID {
		e.cleanupExecution(executionID, execution)
		return fmt.Errorf("persisted snapshot does not match execution %s", executionID)
	}
	if snapshot.Status == StatusCompleted || snapshot.CurrentStep < 0 {
		e.cleanupExecution(executionID, execution)
		return fmt.Errorf("execution %s cannot resume at step %d with status %s", executionID, snapshot.CurrentStep, snapshot.Status)
	}
	workflow, err := e.localEngine.GetWorkflow(snapshot.WorkflowID)
	if err != nil {
		e.cleanupExecution(executionID, execution)
		return err
	}
	if snapshot.CurrentStep > len(workflow.Steps) {
		e.cleanupExecution(executionID, execution)
		return fmt.Errorf("execution %s cannot resume past the last step", executionID)
	}
	options := DefaultExecutionOptions()
	if snapshot.Distributed != nil {
		options.Distributed = *snapshot.Distributed
	}
	e.launchExecution(runCtx, workflow, execution, snapshot, options)
	return nil
}

// Pause persists the paused state before canceling an active execution. It
// returns a persistence error if saving fails, and an error for terminal states.
func (e *DistributedEngine) Pause(ctx context.Context, executionID string) error {
	e.executionsMu.RLock()
	execution := e.executions[executionID]
	e.executionsMu.RUnlock()
	if execution == nil {
		return fmt.Errorf("execution not found: %s", executionID)
	}

	execution.mu.Lock()
	defer execution.mu.Unlock()
	snapshot := execution.snapshot
	if execution.finished || snapshot == nil || (snapshot.Status != StatusPending && snapshot.Status != StatusRunning) {
		return fmt.Errorf("execution %s cannot be paused", executionID)
	}
	next := *snapshot
	next.Status = StatusPaused
	next.Error = "paused by user"
	if err := e.saveExecutionSnapshot(ctx, execution, &next); err != nil {
		return err
	}
	execution.cancel()
	return nil
}

// GetExecutionStatus retrieves execution status from persistence.
func (e *DistributedEngine) GetExecutionStatus(ctx context.Context, executionID string) (*ExecutionSnapshot, error) {
	return e.persistence.LoadSnapshot(ctx, executionID)
}

func (e *DistributedEngine) cleanupExecution(id string, execution *executionContext) {
	execution.cancel()
	e.removeExecution(id)
}

func (e *DistributedEngine) removeExecution(id string) {
	e.executionsMu.Lock()
	delete(e.executions, id)
	e.executionsMu.Unlock()
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
