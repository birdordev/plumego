package distributed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
)

// Worker represents a distributed workflow task worker.
type Worker struct {
	ID          string
	queue       TaskQueue
	executor    *StepExecutor
	concurrency int
	lifecycle   sync.Mutex
	starting    bool
	running     bool
	finished    bool
	cancel      context.CancelFunc
	stopped     chan struct{}
	wg          sync.WaitGroup
	heartbeat   time.Duration
}

// WorkerConfig configures a worker.
type WorkerConfig struct {
	ID          string
	Concurrency int
	Heartbeat   time.Duration
}

// DefaultWorkerConfig returns default worker configuration.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		ID:          fmt.Sprintf("worker-%d", time.Now().UnixNano()),
		Concurrency: 4,
		Heartbeat:   30 * time.Second,
	}
}

// NewWorker creates a new workflow task worker.
func NewWorker(queue TaskQueue, executor *StepExecutor, config WorkerConfig) *Worker {
	if config.ID == "" {
		config.ID = DefaultWorkerConfig().ID
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 4
	}
	if config.Heartbeat == 0 {
		config.Heartbeat = 30 * time.Second
	}

	return &Worker{
		ID:          config.ID,
		queue:       queue,
		executor:    executor,
		concurrency: config.Concurrency,
		stopped:     make(chan struct{}),
		heartbeat:   config.Heartbeat,
	}
}

// Start starts the worker. A negative heartbeat interval is rejected before
// subscribing to the queue.
func (w *Worker) Start(ctx context.Context) error {
	if w.heartbeat < 0 {
		return fmt.Errorf("worker heartbeat interval must be nonnegative: %s", w.heartbeat)
	}
	w.lifecycle.Lock()
	if w.starting || w.running || w.finished {
		w.lifecycle.Unlock()
		return fmt.Errorf("worker already started")
	}
	w.starting = true
	w.lifecycle.Unlock()
	runCtx, cancel := context.WithCancel(ctx)

	// Subscribe to task queue
	handler := func(ctx context.Context, task *WorkflowTask) (*TaskResult, error) {
		return w.handleTask(ctx, task)
	}

	err := w.queue.Subscribe(runCtx, w.ID, handler)
	w.lifecycle.Lock()
	defer w.lifecycle.Unlock()
	w.starting = false
	if err != nil {
		cancel()
		return fmt.Errorf("failed to subscribe to queue: %w", err)
	}
	w.cancel = cancel
	w.wg.Add(1)
	go w.runHeartbeat(runCtx)
	w.running = true

	return nil
}

// Stop stops the worker gracefully.
func (w *Worker) Stop(ctx context.Context) error {
	w.lifecycle.Lock()
	if !w.running {
		w.lifecycle.Unlock()
		return fmt.Errorf("worker not running")
	}
	w.running = false
	w.finished = true
	close(w.stopped)
	w.cancel()
	w.lifecycle.Unlock()
	w.wg.Wait()

	return nil
}

// IsRunning returns whether the worker is currently running.
func (w *Worker) IsRunning() bool {
	w.lifecycle.Lock()
	defer w.lifecycle.Unlock()
	return w.running
}

// handleTask processes a single workflow task.
func (w *Worker) handleTask(ctx context.Context, task *WorkflowTask) (*TaskResult, error) {
	if task == nil {
		return nil, fmt.Errorf("task cannot be nil")
	}

	// Apply timeout if specified
	if task.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, task.Timeout)
		defer cancel()
	}

	// Execute the task
	agentResult, updatedState, err := w.executor.ExecuteTask(ctx, task)
	if err != nil {
		return &TaskResult{
			TaskID:       task.ID,
			AgentResult:  agentResult,
			UpdatedState: updatedState,
			Error:        err.Error(),
			CompletedAt:  time.Now(),
		}, err
	}

	return &TaskResult{
		TaskID:       task.ID,
		AgentResult:  agentResult,
		UpdatedState: updatedState,
		CompletedAt:  time.Now(),
	}, nil
}

// runHeartbeat sends periodic heartbeats.
func (w *Worker) runHeartbeat(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopped:
			return
		case <-ticker.C:
			// Send heartbeat (could publish to monitoring topic)
			// For now, just a no-op to maintain the heartbeat pattern
		}
	}
}

// StepExecutor executes workflow steps.
type StepExecutor struct {
	engine       *orchestration.Engine
	stepRegistry map[string]orchestration.Step
	mu           sync.RWMutex
}

// NewStepExecutor creates a new step executor.
func NewStepExecutor(engine *orchestration.Engine) *StepExecutor {
	return &StepExecutor{
		engine:       engine,
		stepRegistry: make(map[string]orchestration.Step),
	}
}

// RegisterStep registers a step for execution.
func (e *StepExecutor) RegisterStep(name string, step orchestration.Step) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stepRegistry[name] = step
}

// ExecuteTask executes a workflow task.
// A canceled context prevents a registered step from starting.
func (e *StepExecutor) ExecuteTask(ctx context.Context, task *WorkflowTask) (*orchestration.AgentResult, map[string]any, error) {
	if task == nil {
		return nil, nil, fmt.Errorf("task cannot be nil")
	}

	// Look up the step in registry
	e.mu.RLock()
	step, ok := e.stepRegistry[task.StepName]
	e.mu.RUnlock()

	if !ok {
		return nil, nil, fmt.Errorf("step not found: %s", task.StepName)
	}

	// Create a temporary workflow with the current state
	workflow := &orchestration.Workflow{
		ID:    task.WorkflowID,
		State: task.WorkflowState,
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	result, err := step.Execute(ctx, workflow)
	if err != nil {
		return result, workflow.State, fmt.Errorf("step execution failed: %w", err)
	}

	// Return result and updated state
	return result, workflow.State, nil
}

// WorkerPool manages multiple workers. Lifecycle calls serialize and can wait
// for an earlier transition until their context is canceled. Queue adapters may
// read the last committed WorkerCount and IsRunning during Subscribe. Adapters
// must return from Subscribe before calling pool lifecycle methods.
type WorkerPool struct {
	workers    []*Worker
	queue      TaskQueue
	executor   *StepExecutor
	config     PoolConfig
	running    bool
	transition chan struct{}
	mu         sync.RWMutex
}

// PoolConfig configures a worker pool.
type PoolConfig struct {
	WorkerCount int
	Concurrency int
	QueueTopic  string
}

// DefaultPoolConfig returns default pool configuration.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		WorkerCount: 4,
		Concurrency: 4,
		QueueTopic:  "distributed:tasks",
	}
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(queue TaskQueue, executor *StepExecutor, config PoolConfig) *WorkerPool {
	if config.WorkerCount <= 0 {
		config.WorkerCount = 4
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 4
	}

	return &WorkerPool{
		queue:    queue,
		executor: executor,
		config:   config,
	}
}

// Start starts all workers in the pool.
func (p *WorkerPool) Start(ctx context.Context) error {
	if _, err := p.beginTransition(ctx, false); err != nil {
		return err
	}

	count := p.config.WorkerCount
	workers, err := p.startWorkers(ctx, 0, count)
	p.finishTransition(workers, err == nil, count)
	return err
}

// Stop stops all workers in the pool.
func (p *WorkerPool) Stop(ctx context.Context) error {
	workers, err := p.beginTransition(ctx, true)
	if err != nil {
		return err
	}

	var stopErr error
	for _, worker := range workers {
		stopErr = errors.Join(stopErr, worker.Stop(ctx))
	}
	p.finishTransition(nil, false, p.config.WorkerCount)
	return stopErr
}

// Scale changes the number of workers in a running pool. If an expansion fails,
// the existing pool stays active and subscriptions added by the call are canceled.
func (p *WorkerPool) Scale(ctx context.Context, count int) error {
	if count <= 0 {
		return fmt.Errorf("worker count must be positive")
	}
	workers, err := p.beginTransition(ctx, true)
	if err != nil {
		return err
	}

	current := len(workers)
	switch {
	case count < current:
		for i := count; i < current; i++ {
			if err := workers[i].Stop(ctx); err != nil {
				p.finishTransition(workers, true, p.config.WorkerCount)
				return fmt.Errorf("failed to stop worker %d: %w", i, err)
			}
		}
		workers = workers[:count]
	case count > current:
		added, err := p.startWorkers(ctx, current, count)
		if err != nil {
			p.finishTransition(workers, true, p.config.WorkerCount)
			return err
		}
		workers = append(workers, added...)
	}
	p.finishTransition(workers, true, count)
	return nil
}

// beginTransition reserves the next lifecycle change without holding a lock
// across queue calls. Other lifecycle callers can wait without blocking readers.
func (p *WorkerPool) beginTransition(ctx context.Context, wantRunning bool) ([]*Worker, error) {
	for {
		p.mu.Lock()
		if done := p.transition; done != nil {
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		if p.running != wantRunning {
			p.mu.Unlock()
			if wantRunning {
				return nil, fmt.Errorf("pool not running")
			}
			return nil, fmt.Errorf("pool already running")
		}
		p.transition = make(chan struct{})
		workers := p.workers
		p.mu.Unlock()
		return workers, nil
	}
}

func (p *WorkerPool) finishTransition(workers []*Worker, running bool, count int) {
	p.mu.Lock()
	p.workers = workers
	p.running = running
	p.config.WorkerCount = count
	close(p.transition)
	p.transition = nil
	p.mu.Unlock()
}

func (p *WorkerPool) startWorkers(ctx context.Context, first, count int) ([]*Worker, error) {
	started := make([]*Worker, 0, count-first)
	for i := first; i < count; i++ {
		err := ctx.Err()
		if err == nil {
			worker := NewWorker(p.queue, p.executor, WorkerConfig{
				ID:          fmt.Sprintf("worker-%d", i),
				Concurrency: p.config.Concurrency,
			})
			err = worker.Start(ctx)
			if err == nil {
				started = append(started, worker)
				err = ctx.Err()
			}
		}
		if err != nil {
			err = fmt.Errorf("failed to start worker %d: %w", i, err)
			for j := len(started) - 1; j >= 0; j-- {
				err = errors.Join(err, started[j].Stop(ctx))
			}
			return nil, err
		}
	}
	return started, nil
}

// WorkerCount returns the current number of workers.
func (p *WorkerPool) WorkerCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.workers)
}

// IsRunning returns whether the pool is running.
func (p *WorkerPool) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}
