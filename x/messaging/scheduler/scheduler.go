package scheduler

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/birdordev/plumego/log"
)

const (
	// DefaultWorkChannelBuffer is the default buffer size for the work channel.
	DefaultWorkChannelBuffer = 256
)

// Error definitions moved to errors.go

// Scheduler coordinates cron, delayed, and retryable jobs.
type Scheduler struct {
	mu      sync.RWMutex
	jobs    map[JobID]*job
	queue   scheduleHeap
	wakeCh  chan struct{}
	stopCh  chan struct{}
	closed  bool
	started bool // guards against multiple Start() calls

	// stopCtx is cancelled when the scheduler is stopped, allowing
	// running tasks to detect shutdown when no per-job timeout is set.
	stopCtx    context.Context
	stopCancel context.CancelFunc

	workerCount  int
	workCh       chan *runRequest
	logger       log.StructuredLogger
	panicHandler func(context.Context, JobID, any)
	clock        Clock
	store        Store
	registry     map[string]TaskFunc
	metricsSink  MetricsSink
	backpressure BackpressureConfig
	rngMu        sync.Mutex
	rng          *rand.Rand

	// dependents tracks which jobs depend on each job (reverse mapping)
	// Key: dependency JobID, Value: slice of dependent JobIDs
	dependents map[JobID][]JobID

	// dependencyStatus tracks completion status of dependencies
	// Key: JobID, Value: true if completed successfully, false if failed
	dependencyStatus map[JobID]bool

	// dlq manages failed jobs for inspection and requeuing
	dlq *DeadLetterQueue

	wg sync.WaitGroup

	stats schedulerStats
}

// Option configures the scheduler.
type Option func(*Scheduler)

// WithWorkers sets the worker pool size.
func WithWorkers(n int) Option {
	return func(s *Scheduler) {
		if n > 0 {
			s.workerCount = n
		}
	}
}

// WithQueueSize sets the internal work queue size.
func WithQueueSize(n int) Option {
	return func(s *Scheduler) {
		if n > 0 {
			s.workCh = make(chan *runRequest, n)
		}
	}
}

// WithLogger sets the structured logger.
func WithLogger(logger log.StructuredLogger) Option {
	return func(s *Scheduler) {
		s.logger = logger
	}
}

// WithPanicHandler registers a panic handler for task execution.
func WithPanicHandler(handler func(context.Context, JobID, any)) Option {
	return func(s *Scheduler) {
		s.panicHandler = handler
	}
}

// WithMetricsSink registers a metrics sink for scheduler events.
func WithMetricsSink(sink MetricsSink) Option {
	return func(s *Scheduler) {
		s.metricsSink = sink
	}
}

// WithClock injects a custom clock for testing.
func WithClock(clock Clock) Option {
	return func(s *Scheduler) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithStore sets a persistence store for delay jobs.
func WithStore(store Store) Option {
	return func(s *Scheduler) {
		s.store = store
	}
}

// WithBackpressure configures the backpressure behavior when the work queue is full.
func WithBackpressure(config BackpressureConfig) Option {
	return func(s *Scheduler) {
		s.backpressure = config
	}
}

// WithRandomSeed sets the scheduler-local random seed used for jitter.
func WithRandomSeed(seed int64) Option {
	return func(s *Scheduler) {
		s.rng = rand.New(rand.NewSource(seed))
	}
}

// WithDeadLetterQueue enables dead letter queue for failed jobs.
// maxSize limits the number of entries (0 = unlimited).
func WithDeadLetterQueue(maxSize int) Option {
	return func(s *Scheduler) {
		s.dlq = NewDeadLetterQueue(maxSize)
	}
}

// New constructs a Scheduler.
func New(opts ...Option) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{
		jobs:             make(map[JobID]*job),
		queue:            scheduleHeap{},
		wakeCh:           make(chan struct{}, 1),
		stopCh:           make(chan struct{}),
		workerCount:      4,
		workCh:           make(chan *runRequest, DefaultWorkChannelBuffer),
		logger:           log.NewLogger(),
		clock:            realClock{},
		registry:         make(map[string]TaskFunc),
		backpressure:     DefaultBackpressureConfig(),
		rng:              rand.New(rand.NewSource(time.Now().UnixNano())),
		dependents:       make(map[JobID][]JobID),
		dependencyStatus: make(map[JobID]bool),
		stopCtx:          ctx,
		stopCancel:       cancel,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Start launches scheduler goroutines.
// Calling Start more than once on the same Scheduler is a no-op.
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	s.loadPersisted()
	s.wg.Add(1)
	go s.runLoop()
	for i := 0; i < s.workerCount; i++ {
		s.wg.Add(1)
		go s.worker()
	}
}

// RegisterTask registers a named task for persistence recovery.
// Returns an error if the name is empty or the task is nil.
func (s *Scheduler) RegisterTask(name string, task TaskFunc) error {
	if name == "" {
		return ErrTaskNameEmpty
	}
	if task == nil {
		return ErrTaskNil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry[name] = task
	return nil
}

// Stop stops the scheduler and waits for shutdown.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.stopCh)
	s.stopCancel() // signal running tasks via stopCtx
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
