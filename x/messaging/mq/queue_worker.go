package mq

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type TaskHandler func(ctx context.Context, task Task) error

type WorkerConfig struct {
	ConsumerID          string
	Concurrency         int
	LeaseDuration       time.Duration
	PollInterval        time.Duration
	MaxInflight         int
	RetryPolicy         RetryPolicy
	LeaseExtendInterval time.Duration
	// ShutdownTimeout bounds Stop and individual task finalization calls.
	ShutdownTimeout time.Duration
	MetricsObserver MetricsObserver
	Deduper         TaskDeduper
	DedupeKeyFunc   func(Task) string
	DedupeTTL       time.Duration
	DedupeErrorHook func(ctx context.Context, task Task, err error)
}

type Worker struct {
	queue    *TaskQueue
	config   WorkerConfig
	mu       sync.RWMutex
	handlers map[string]TaskHandler

	tasksCh    chan Task
	runCtx     context.Context
	runCancel  context.CancelFunc
	pollCtx    context.Context
	pollCancel context.CancelFunc
	wg         sync.WaitGroup
	startMu    sync.Mutex
	started    bool
	inflight   atomic.Int64

	inflightMu    sync.Mutex
	inflightTasks map[string]Task
	pollDone      chan struct{}
	closeOnce     sync.Once
}

func NewWorker(q *TaskQueue, cfg WorkerConfig) *Worker {
	return &Worker{
		queue:    q,
		config:   cfg,
		handlers: make(map[string]TaskHandler),
	}
}

func (w *Worker) Register(topic string, handler TaskHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.handlers == nil {
		w.handlers = make(map[string]TaskHandler)
	}
	w.handlers[topic] = handler
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.queue == nil {
		return
	}
	w.startMu.Lock()
	defer w.startMu.Unlock()
	if w.started {
		return
	}
	w.started = true
	if ctx == nil {
		ctx = context.Background()
	}

	w.config = normalizeWorkerConfig(w.config)
	w.runCtx, w.runCancel = context.WithCancel(ctx)
	w.pollCtx, w.pollCancel = context.WithCancel(ctx)
	w.tasksCh = make(chan Task, w.config.MaxInflight)
	if w.inflightTasks == nil {
		w.inflightTasks = make(map[string]Task)
	}
	if w.pollDone == nil {
		w.pollDone = make(chan struct{})
	}

	w.wg.Add(1)
	go w.pollLoop()

	for i := 0; i < w.config.Concurrency; i++ {
		w.wg.Add(1)
		go w.workerLoop()
	}
}

// Stop drains tasks within the earlier of the caller's deadline and
// ShutdownTimeout. A portion of that time is reserved for releasing in-flight
// leases after cancellation. Leases that cannot be released expire normally.
func (w *Worker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.startMu.Lock()
	started := w.started
	w.startMu.Unlock()
	if !started {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if w.config.ShutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.config.ShutdownTimeout)
		defer cancel()
	}

	drainCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		cleanupBudget := min(time.Second, time.Until(deadline)/10)
		if cleanupBudget > 0 {
			var cancel context.CancelFunc
			drainCtx, cancel = context.WithDeadline(ctx, deadline.Add(-cleanupBudget))
			defer cancel()
		}
	}

	if w.pollCancel != nil {
		w.pollCancel()
	}

	if err := waitForPoll(drainCtx, w.pollDone); err != nil {
		return errors.Join(err, w.forceStop(ctx))
	}

	w.closeTasks()

	if err := waitWithContext(drainCtx, &w.wg); err != nil {
		return errors.Join(err, w.forceStop(ctx))
	}
	w.runCancel()
	return nil
}

func (w *Worker) pollLoop() {
	defer w.wg.Done()
	defer func() {
		if w.pollDone != nil {
			close(w.pollDone)
		}
	}()

	for {
		select {
		case <-w.pollCtx.Done():
			return
		default:
		}

		topics := w.registeredTopics()
		if len(topics) == 0 {
			if !sleepWithContext(w.pollCtx, w.config.PollInterval) {
				return
			}
			continue
		}

		available := w.config.MaxInflight - int(w.inflight.Load())
		if available <= 0 {
			if !sleepWithContext(w.pollCtx, w.config.PollInterval) {
				return
			}
			continue
		}

		limit := w.config.Concurrency
		if available < limit {
			limit = available
		}

		tasks, err := w.queue.Reserve(w.pollCtx, ReserveOptions{
			Topics:     topics,
			Limit:      limit,
			Lease:      w.config.LeaseDuration,
			ConsumerID: w.config.ConsumerID,
		})
		if err != nil {
			if !sleepWithContext(w.pollCtx, w.config.PollInterval) {
				return
			}
			continue
		}
		if len(tasks) == 0 {
			if !sleepWithContext(w.pollCtx, w.config.PollInterval) {
				return
			}
			continue
		}

		w.trackInflight(tasks)
		for i, task := range tasks {
			select {
			case <-w.runCtx.Done():
				// Forced shutdown owns the remaining leases. They were
				// reserved but will never reach a handler.
				w.inflight.Add(-int64(len(tasks) - i))
				return
			case w.tasksCh <- task:
			}
		}
	}
}

func (w *Worker) workerLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.runCtx.Done():
			return
		case task, ok := <-w.tasksCh:
			if !ok {
				return
			}
			w.handleTask(task)
		}
	}
}

func (w *Worker) handleTask(task Task) {
	defer w.inflight.Add(-1)
	defer func() {
		// Leave shutdown cleanup ownership intact when the operation was
		// canceled by the worker lifecycle.
		if w.runCtx.Err() == nil {
			w.untrackInflight(task.ID)
		}
	}()

	if task.MaxAttempts > 0 && task.Attempts > task.MaxAttempts {
		ctx, cancel := context.WithTimeout(w.runCtx, w.config.ShutdownTimeout)
		defer cancel()
		_ = w.queue.MoveToDLQ(ctx, task.ID, w.config.ConsumerID, "max attempts exceeded", time.Now())
		w.observeLatency(task)
		return
	}

	if w.config.Deduper != nil {
		key := w.dedupeKey(task)
		if key != "" {
			ctx, cancel := context.WithTimeout(w.runCtx, w.config.ShutdownTimeout)
			completed, err := w.config.Deduper.IsCompleted(ctx, key)
			cancel()
			if err != nil {
				if w.config.DedupeErrorHook != nil {
					w.config.DedupeErrorHook(context.Background(), task, err)
				}
			} else if completed {
				w.ackTask(task, false)
				return
			}
		}
	}

	handler := w.handlerFor(task.Topic)
	if handler == nil {
		err := fmt.Errorf("no handler registered for topic %s", task.Topic)
		w.failTask(task, err)
		return
	}

	taskCtx, cancel := context.WithCancel(w.runCtx)
	var leaseLost atomic.Bool
	stopLease := make(chan struct{})

	if w.config.LeaseExtendInterval > 0 {
		go func() {
			w.extendLeaseLoop(taskCtx, task, &leaseLost, stopLease, cancel)
		}()
	}

	err := handler(taskCtx, task)
	close(stopLease)
	cancel()

	if leaseLost.Load() {
		return
	}

	if err == nil {
		w.ackTask(task, true)
		return
	}

	w.failTask(task, err)
}

func (w *Worker) ackTask(task Task, markCompleted bool) {
	ctx, cancel := context.WithTimeout(w.runCtx, w.config.ShutdownTimeout)
	defer cancel()
	if markCompleted {
		w.markDeduped(ctx, task)
	}
	_ = w.queue.Ack(ctx, task.ID, w.config.ConsumerID, time.Now())
	w.observeLatency(task)
}

func (w *Worker) failTask(task Task, err error) {
	ctx, cancel := context.WithTimeout(w.runCtx, w.config.ShutdownTimeout)
	defer cancel()
	maxAttempts := task.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultTaskMaxAttempts
	}

	if task.Attempts >= maxAttempts {
		_ = w.queue.MoveToDLQ(ctx, task.ID, w.config.ConsumerID, err.Error(), time.Now())
		w.observeLatency(task)
		return
	}

	retryDelay := w.config.RetryPolicy.NextDelay(task, err)
	retryAt := time.Now().Add(retryDelay)
	releaseErr := w.queue.Release(ctx, task.ID, w.config.ConsumerID, ReleaseOptions{
		RetryAt: retryAt,
		Reason:  err.Error(),
		Now:     time.Now(),
	})
	if releaseErr != nil && !errors.Is(releaseErr, ErrLeaseLost) {
		return
	}
	w.observeLatency(task)
}

func (w *Worker) extendLeaseLoop(ctx context.Context, task Task, leaseLost *atomic.Bool, stop <-chan struct{}, cancel context.CancelFunc) {
	ticker := time.NewTicker(w.config.LeaseExtendInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := w.queue.ExtendLease(context.Background(), task.ID, w.config.ConsumerID, w.config.LeaseDuration, time.Now())
			if err != nil {
				if errors.Is(err, ErrLeaseLost) || errors.Is(err, ErrTaskNotFound) {
					leaseLost.Store(true)
					cancel()
					return
				}
			}
		}
	}
}

func (w *Worker) registeredTopics() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	topics := make([]string, 0, len(w.handlers))
	for topic := range w.handlers {
		topics = append(topics, topic)
	}
	return topics
}

func (w *Worker) handlerFor(topic string) TaskHandler {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.handlers[topic]
}

func normalizeWorkerConfig(cfg WorkerConfig) WorkerConfig {
	if cfg.ConsumerID == "" {
		cfg.ConsumerID = defaultConsumerID()
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.MaxInflight <= 0 {
		cfg.MaxInflight = cfg.Concurrency
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = DefaultLeaseDuration
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.LeaseExtendInterval <= 0 {
		cfg.LeaseExtendInterval = cfg.LeaseDuration / 2
	}
	if cfg.RetryPolicy == nil {
		cfg.RetryPolicy = DefaultRetryPolicy()
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	return cfg
}

func (w *Worker) observeLatency(task Task) {
	if task.CreatedAt.IsZero() {
		return
	}
	collector := w.config.MetricsObserver
	if collector == nil && w.queue != nil {
		collector = w.queue.collector()
	}
	if collector == nil {
		return
	}
	collector.ObserveMQ(context.Background(), "queue_latency", task.Topic, time.Since(task.CreatedAt), nil, false)
}

func (w *Worker) trackInflight(tasks []Task) {
	w.inflightMu.Lock()
	for _, task := range tasks {
		w.inflightTasks[task.ID] = task
	}
	w.inflight.Add(int64(len(tasks)))
	w.inflightMu.Unlock()
}

func (w *Worker) untrackInflight(taskID string) {
	w.inflightMu.Lock()
	delete(w.inflightTasks, taskID)
	w.inflightMu.Unlock()
}

func (w *Worker) releaseInflight(ctx context.Context) error {
	if w.queue == nil {
		return nil
	}
	w.inflightMu.Lock()
	tasks := make([]Task, 0, len(w.inflightTasks))
	for _, task := range w.inflightTasks {
		tasks = append(tasks, task)
	}
	w.inflightMu.Unlock()

	var releaseErr error
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return errors.Join(releaseErr, err)
		}
		now := time.Now()
		err := w.queue.Release(ctx, task.ID, w.config.ConsumerID, ReleaseOptions{
			RetryAt: now,
			Reason:  "worker shutdown",
			Now:     now,
		})
		if err != nil && !errors.Is(err, ErrLeaseLost) && !errors.Is(err, ErrTaskNotFound) {
			releaseErr = errors.Join(releaseErr, err)
			continue
		}
		w.untrackInflight(task.ID)
	}
	return releaseErr
}

func (w *Worker) closeTasks() {
	if w.tasksCh == nil {
		return
	}
	w.closeOnce.Do(func() {
		close(w.tasksCh)
	})
}

func (w *Worker) forceStop(ctx context.Context) error {
	if w.pollCancel != nil {
		w.pollCancel()
	}
	if w.runCancel != nil {
		w.runCancel()
	}
	// Let a Reserve already returning hand off its leases before taking the
	// cleanup snapshot. If it outlives the stop budget, leases expire normally.
	return errors.Join(waitForPoll(ctx, w.pollDone), w.releaseInflight(ctx))
}

func (w *Worker) markDeduped(ctx context.Context, task Task) {
	if w.config.Deduper == nil {
		return
	}
	key := w.dedupeKey(task)
	if key == "" {
		return
	}
	if err := w.config.Deduper.MarkCompleted(ctx, key, w.config.DedupeTTL); err != nil {
		if w.config.DedupeErrorHook != nil {
			w.config.DedupeErrorHook(ctx, task, err)
		}
	}
}

func (w *Worker) dedupeKey(task Task) string {
	if w.config.DedupeKeyFunc != nil {
		return w.config.DedupeKeyFunc(task)
	}
	key := task.DedupeKey
	if key == "" {
		key = task.ID
	}
	if key == "" {
		return ""
	}
	if task.TenantID != "" {
		return task.TenantID + ":" + key
	}
	return key
}

func defaultConsumerID() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("worker-%s-%d", host, os.Getpid())
}

func waitWithContext(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func waitForPoll(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = DefaultPollInterval
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
