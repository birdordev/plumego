package distributed

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

	// Execute the step
	result, err := step.Execute(ctx, workflow)
	if err != nil {
		return result, workflow.State, fmt.Errorf("step execution failed: %w", err)
	}

	// Return result and updated state
	return result, workflow.State, nil
}

// WorkerPool manages multiple workers.
type WorkerPool struct {
	workers  []*Worker
	queue    TaskQueue
	executor *StepExecutor
	config   PoolConfig
	running  atomic.Bool
	mu       sync.RWMutex
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
		workers:  make([]*Worker, 0, config.WorkerCount),
	}
}

// Start starts all workers in the pool.
func (p *WorkerPool) Start(ctx context.Context) error {
	if p.running.Load() {
		return fmt.Errorf("pool already running")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running.Load() {
		return fmt.Errorf("pool already running")
	}

	p.running.Store(true)

	// Create and start workers
	for i := 0; i < p.config.WorkerCount; i++ {
		worker := NewWorker(p.queue, p.executor, WorkerConfig{
			ID:          fmt.Sprintf("worker-%d", i),
			Concurrency: p.config.Concurrency,
		})

		if err := worker.Start(ctx); err != nil {
			// Stop already started workers
			_ = p.stopWorkers(ctx)
			p.running.Store(false)
			return fmt.Errorf("failed to start worker %d: %w", i, err)
		}

		p.workers = append(p.workers, worker)
	}

	return nil
}

// Stop stops all workers in the pool.
func (p *WorkerPool) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running.Swap(false) {
		return fmt.Errorf("pool not running")
	}
	return p.stopWorkers(ctx)
}

// stopWorkers stops all workers (caller must hold lock).
func (p *WorkerPool) stopWorkers(ctx context.Context) error {
	var lastErr error
	for _, worker := range p.workers {
		if err := worker.Stop(ctx); err != nil {
			lastErr = err
		}
	}
	p.workers = p.workers[:0]
	return lastErr
}

// Scale changes the number of workers in a running pool.
func (p *WorkerPool) Scale(ctx context.Context, count int) error {
	if count <= 0 {
		return fmt.Errorf("worker count must be positive")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running.Load() {
		return fmt.Errorf("pool not running")
	}

	current := len(p.workers)

	if count == current {
		return nil
	}

	if count < current {
		// Scale down
		for i := count; i < current; i++ {
			if err := p.workers[i].Stop(ctx); err != nil {
				return fmt.Errorf("failed to stop worker %d: %w", i, err)
			}
		}
		p.workers = p.workers[:count]
	} else {
		// Scale up
		for i := current; i < count; i++ {
			worker := NewWorker(p.queue, p.executor, WorkerConfig{
				ID:          fmt.Sprintf("worker-%d", i),
				Concurrency: p.config.Concurrency,
			})

			if err := worker.Start(ctx); err != nil {
				return fmt.Errorf("failed to start worker %d: %w", i, err)
			}

			p.workers = append(p.workers, worker)
		}
	}

	p.config.WorkerCount = count
	return nil
}

// WorkerCount returns the current number of workers.
func (p *WorkerPool) WorkerCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.workers)
}

// IsRunning returns whether the pool is running.
func (p *WorkerPool) IsRunning() bool {
	return p.running.Load()
}
