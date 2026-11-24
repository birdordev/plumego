package mq

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type TaskHandler func(ctx context.Context, task Task) error

type WorkerConfig struct {
	ConsumerID    string
	Pool          string
	Version       string
	Concurrency   int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	// MaxIdlePollInterval 启用空闲指数退避。零值保留固定轮询间隔。
	MaxIdlePollInterval time.Duration
	// Wake 接收外部队列变更提示。每个 Worker 应使用独立的通知订阅。
	Wake                <-chan struct{}
	MaxInflight         int
	RetryPolicy         RetryPolicy
	LeaseExtendInterval time.Duration
	// MaxQueueWait 从本次可领取时间计时。MaxTaskAge 从原始入队时间计时。
	MaxQueueWait time.Duration
	MaxTaskAge   time.Duration
	TaskTimeout  time.Duration
	// BeginExecution 在调用处理器前登记持久化执行次数。正数是本次执行序号，
	// 零表示预算用尽。ErrExecutionHandled 表示回调已提交状态转换。
	// ErrClaimPaused 会归还领取，其他错误让领取按原租约恢复。
	BeginExecution func(context.Context, Task) (int, error)
	// ResolveOutcome 返回 true 表示已提交，false 表示已确认可安全重试。
	// 返回错误表示结果仍未知，Worker 将任务隔离到死信。
	ResolveOutcome func(context.Context, Task) (bool, error)
	// ShutdownTimeout bounds Stop and individual task finalization calls.
	ShutdownTimeout time.Duration
	MetricsObserver MetricsObserver
	Deduper         TaskDeduper
	DedupeKeyFunc   func(Task) string
	DedupeTTL       time.Duration
	DedupeErrorHook func(ctx context.Context, task Task, err error)
	// FinalizationErrorHook 接收当前领取的最后一次终态写入错误。
	FinalizationErrorHook func(ctx context.Context, taskID, operation string, err error)
}

type Worker struct {
	identity WorkerIdentity
	queue    *TaskQueue
	config   WorkerConfig
	mu       sync.RWMutex
	handlers map[string]TaskHandler

	tasksCh           chan Task
	capacityAvailable chan struct{}
	runCtx            context.Context
	runCancel         context.CancelFunc
	pollCtx           context.Context
	pollCancel        context.CancelFunc
	wg                sync.WaitGroup
	startMu           sync.Mutex
	started           bool
	inflight          atomic.Int64

	inflightMu    sync.Mutex
	inflightTasks map[taskClaim]inflightTask
	pollDone      chan struct{}
	workersDone   chan struct{}
	closeOnce     sync.Once
}

// WorkerIdentity 记录本次启动代次、执行池和版本。
type WorkerIdentity struct {
	ConsumerID string
	InstanceID string
	Pool       string
	Version    string
}

func (w *Worker) Identity() WorkerIdentity { return w.identity }

type taskClaim struct {
	id, owner string
}

type inflightTask struct {
	task      Task
	executing bool
}

func NewWorker(q *TaskQueue, cfg WorkerConfig) *Worker {
	if cfg.ConsumerID == "" {
		cfg.ConsumerID = defaultConsumerID()
	}
	return &Worker{
		identity:          WorkerIdentity{ConsumerID: cfg.ConsumerID, InstanceID: rand.Text(), Pool: cfg.Pool, Version: cfg.Version},
		queue:             q,
		config:            cfg,
		handlers:          make(map[string]TaskHandler),
		capacityAvailable: make(chan struct{}, 1),
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
		w.inflightTasks = make(map[taskClaim]inflightTask)
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
	w.workersDone = make(chan struct{})
	go func() {
		w.wg.Wait()
		close(w.workersDone)
	}()
}

// Stop 在调用方期限和 ShutdownTimeout 中较早的时间内排空任务。
// 强停时先请求取消，再仅释放当前未执行处理器或结果核对的领取。
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

	if err := waitForWorkerSignal(drainCtx, w.pollDone); err != nil {
		return errors.Join(err, w.forceStop(ctx))
	}

	w.closeTasks()

	if err := waitForWorkerSignal(drainCtx, w.workersDone); err != nil {
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

	wake := w.config.Wake
	idle := w.config.PollInterval
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
			select {
			case <-w.pollCtx.Done():
				return
			case <-w.capacityAvailable:
			}
			continue
		}

		limit := w.config.Concurrency
		if available < limit {
			limit = available
		}

		tasks, err := w.queue.ReserveFenced(w.pollCtx, ReserveOptions{
			Topics:     topics,
			Limit:      limit,
			Lease:      w.config.LeaseDuration,
			ConsumerID: w.identity.ConsumerID + "/" + w.identity.InstanceID,
		})
		if err != nil {
			delay := jitterIdlePoll(idle, w.config.PollInterval, w.config.MaxIdlePollInterval)
			if !sleepWithContext(w.pollCtx, delay) {
				return
			}
			idle = nextIdlePoll(idle, w.config.MaxIdlePollInterval)
			continue
		}
		if len(tasks) == 0 {
			delay := w.idlePollDelay(topics, idle)
			notified, running := w.waitIdle(delay, &wake)
			if !running {
				return
			}
			if notified {
				idle = w.config.PollInterval
			} else {
				idle = nextIdlePoll(idle, w.config.MaxIdlePollInterval)
			}
			continue
		}

		idle = w.config.PollInterval
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
	defer func() {
		w.inflight.Add(-1)
		select {
		case w.capacityAvailable <- struct{}{}:
		default:
		}
	}()
	defer func() {
		// Leave shutdown cleanup ownership intact when the operation was
		// canceled by the worker lifecycle.
		if w.runCtx.Err() == nil {
			w.untrackInflight(task)
		}
	}()

	if w.runCtx.Err() != nil || (!task.LeaseUntil.IsZero() && !time.Now().Before(task.LeaseUntil)) {
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

	now := time.Now()
	totalDeadline := workerDeadline(task.CreatedAt, w.config.MaxTaskAge)
	if !totalDeadline.IsZero() && !now.Before(totalDeadline) {
		w.deadLetter(task, "task age limit exceeded")
		return
	}
	queueDeadline := workerDeadline(task.AvailableAt, w.config.MaxQueueWait)
	if !queueDeadline.IsZero() && !now.Before(queueDeadline) {
		w.deadLetter(task, "task queue wait limit exceeded")
		return
	}

	if w.config.BeginExecution == nil && task.MaxAttempts > 0 && task.Attempts > task.MaxAttempts {
		w.deadLetter(task, "max attempts exceeded")
		return
	}

	handler := w.handlerFor(task.Topic)
	if handler == nil {
		if w.config.BeginExecution != nil {
			w.deadLetter(task, "permanent task failure")
			return
		}
		err := fmt.Errorf("no handler registered for topic %s", task.Topic)
		w.failTask(task, err)
		return
	}

	if !w.confirmClaim(&task) {
		return
	}
	now = time.Now()
	if !totalDeadline.IsZero() && !now.Before(totalDeadline) {
		w.deadLetter(task, "task age limit exceeded")
		return
	}
	if !queueDeadline.IsZero() && !now.Before(queueDeadline) {
		w.deadLetter(task, "task queue wait limit exceeded")
		return
	}
	deadline := totalDeadline
	if w.config.TaskTimeout > 0 {
		candidate := time.Now().Add(w.config.TaskTimeout)
		if deadline.IsZero() || candidate.Before(deadline) {
			deadline = candidate
		}
	}
	var taskCtx context.Context
	var cancel context.CancelFunc
	if deadline.IsZero() {
		taskCtx, cancel = context.WithCancel(w.runCtx)
	} else {
		taskCtx, cancel = context.WithDeadline(w.runCtx, deadline)
	}
	if w.config.BeginExecution != nil {
		startDeadline := time.Now().Add(w.config.ShutdownTimeout)
		if !task.LeaseUntil.IsZero() {
			startDeadline = minTime(task.LeaseUntil, startDeadline)
		}
		startCtx, finish := context.WithDeadline(taskCtx, startDeadline)
		n, err := w.config.BeginExecution(startCtx, task)
		startErr := startCtx.Err()
		finish()
		if errors.Is(err, ErrExecutionHandled) {
			cancel()
			return
		}
		if errors.Is(err, ErrClaimPaused) && startErr == nil {
			releaseCtx, endRelease := context.WithTimeout(taskCtx, w.config.ShutdownTimeout)
			w.finalizeTask(releaseCtx, task, "release", func() error {
				return w.queue.Release(releaseCtx, task.ID, task.LeaseOwner, ReleaseOptions{
					RetryAt: task.AvailableAt, Reason: "schedule claim paused", Now: time.Now(),
				})
			})
			endRelease()
			cancel()
			return
		}
		if err != nil || startErr != nil {
			if hook := w.config.FinalizationErrorHook; hook != nil && !errors.Is(err, ErrLeaseLost) {
				hook(taskCtx, task.ID, "execution_start", errors.Join(err, startErr))
			}
			cancel()
			return
		}
		if n <= 0 {
			cancel()
			w.deadLetter(task, "max attempts exceeded")
			return
		}
		task.ExecutionAttempts = n
	}
	var leaseLost atomic.Bool
	leaseDone := make(chan struct{})

	if w.config.LeaseExtendInterval > 0 {
		go func() {
			defer close(leaseDone)
			w.extendLeaseLoop(taskCtx, task, &leaseLost, cancel)
		}()
	} else {
		close(leaseDone)
	}

	var err error
	if taskCtx.Err() == nil && w.beginHandler(task) {
		err = callTaskHandler(taskCtx, handler, task)
		if err == nil && errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
			err = &TaskFailure{Kind: TaskFailureOutcomeUnknown}
		}
		if taskFailure(err).Kind == TaskFailureOutcomeUnknown && w.config.ResolveOutcome != nil && taskCtx.Err() == nil {
			done, resolveErr := callOutcomeResolver(taskCtx, w.config.ResolveOutcome, task)
			if resolveErr == nil && taskCtx.Err() == nil {
				if done {
					err = nil
				} else {
					err = &TaskFailure{Kind: TaskFailureTransient, Cause: err}
				}
			}
		}
		w.endHandler(task)
	}
	timedOut := errors.Is(taskCtx.Err(), context.DeadlineExceeded)
	cancel()
	<-leaseDone

	if leaseLost.Load() || w.runCtx.Err() != nil {
		return
	}

	if timedOut && !totalDeadline.IsZero() && !time.Now().Before(totalDeadline) {
		w.deadLetter(task, "task age limit exceeded")
		return
	}
	if timedOut && taskFailure(err).Kind == TaskFailureTransient {
		err = context.DeadlineExceeded
	}
	if err == nil {
		w.ackTask(task, true)
		return
	}

	w.failTask(task, err)
}

func workerDeadline(origin time.Time, interval time.Duration) time.Time {
	if origin.IsZero() || interval <= 0 {
		return time.Time{}
	}
	return origin.Add(interval)
}

func (w *Worker) confirmClaim(task *Task) bool {
	if w.runCtx.Err() != nil {
		return false
	}
	if task.LeaseUntil.IsZero() {
		return true
	}
	now := time.Now()
	if !now.Before(task.LeaseUntil) {
		return false
	}
	ctx, cancel := context.WithDeadline(w.runCtx, minTime(task.LeaseUntil, now.Add(w.config.LeaseExtendInterval)))
	defer cancel()
	if err := w.queue.ExtendLease(ctx, task.ID, task.LeaseOwner, w.config.LeaseDuration, now); err != nil || ctx.Err() != nil {
		return false
	}
	task.LeaseUntil = now.Add(w.config.LeaseDuration)
	return true
}

func (w *Worker) deadLetter(task Task, reason string) {
	ctx, cancel := context.WithTimeout(w.runCtx, w.config.ShutdownTimeout)
	defer cancel()
	w.finalizeTask(ctx, task, "dlq", func() error {
		return w.queue.MoveToDLQ(ctx, task.ID, task.LeaseOwner, reason, time.Now())
	})
}

func (w *Worker) ackTask(task Task, markCompleted bool) {
	ctx, cancel := context.WithTimeout(w.runCtx, w.config.ShutdownTimeout)
	defer cancel()
	if markCompleted {
		w.markDeduped(ctx, task)
	}
	w.finalizeTask(ctx, task, "ack", func() error {
		return w.queue.Ack(ctx, task.ID, task.LeaseOwner, time.Now())
	})
}

func (w *Worker) failTask(task Task, err error) {
	failure := taskFailure(err)
	switch failure.Kind {
	case TaskFailurePermanent:
		w.deadLetter(task, "permanent task failure")
		return
	case TaskFailureOutcomeUnknown:
		w.deadLetter(task, "task outcome unknown")
		return
	}
	maxAttempts := task.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultTaskMaxAttempts
	}
	attempt := task
	if task.ExecutionAttempts > 0 {
		attempt.Attempts = task.ExecutionAttempts
	}
	if attempt.Attempts >= maxAttempts {
		w.deadLetter(task, "task failed")
		return
	}

	delay := w.config.RetryPolicy.NextDelay(attempt, err)
	reason := "task failed"
	if failure.Kind == TaskFailureRateLimited {
		delay = max(delay, failure.RetryAfter)
		reason = "task rate limited"
	} else if errors.Is(err, context.DeadlineExceeded) {
		reason = "task execution timed out"
	}
	retryAt := time.Now().Add(max(delay, 0))
	if deadline := workerDeadline(task.CreatedAt, w.config.MaxTaskAge); !deadline.IsZero() && !retryAt.Before(deadline) {
		w.deadLetter(task, "task retry would exceed age limit")
		return
	}
	ctx, cancel := context.WithTimeout(w.runCtx, w.config.ShutdownTimeout)
	defer cancel()
	w.finalizeTask(ctx, task, "release", func() error {
		return w.queue.Release(ctx, task.ID, task.LeaseOwner, ReleaseOptions{
			RetryAt: retryAt,
			Reason:  reason,
			Now:     time.Now(),
		})
	})
}

func (w *Worker) finalizeTask(ctx context.Context, task Task, operation string, write func() error) {
	if err := write(); err != nil {
		if ctx.Err() == nil && !errors.Is(err, ErrLeaseLost) && !errors.Is(err, ErrTaskNotFound) {
			if retryErr := write(); retryErr == nil {
				w.observeLatency(task)
				return
			} else {
				err = errors.Join(err, retryErr)
			}
		}
		if hook := w.config.FinalizationErrorHook; hook != nil {
			hook(ctx, task.ID, operation, err)
		}
		return
	}
	w.observeLatency(task)
}

func callTaskHandler(ctx context.Context, handler TaskHandler, task Task) (err error) {
	defer func() {
		if recover() != nil {
			err = errTaskHandlerPanic
		}
	}()
	return handler(ctx, task)
}

func callOutcomeResolver(ctx context.Context, resolver func(context.Context, Task) (bool, error), task Task) (done bool, err error) {
	defer func() {
		if recover() != nil {
			err = errTaskHandlerPanic
		}
	}()
	return resolver(ctx, task)
}

var errTaskHandlerPanic = errors.New("task handler panicked")

func (w *Worker) extendLeaseLoop(ctx context.Context, task Task, leaseLost *atomic.Bool, cancel context.CancelFunc) {
	expiry := task.LeaseUntil
	if expiry.IsZero() {
		expiry = time.Now().Add(w.config.LeaseDuration)
	}
	timer := time.NewTimer(min(w.config.LeaseExtendInterval, time.Until(expiry)))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now()
		if !now.Before(expiry) {
			break
		}
		renewCtx, stopRenew := context.WithDeadline(ctx, minTime(expiry, now.Add(w.config.LeaseExtendInterval)))
		err := w.queue.ExtendLease(renewCtx, task.ID, task.LeaseOwner, w.config.LeaseDuration, now)
		renewErr := renewCtx.Err()
		stopRenew()
		if errors.Is(err, ErrLeaseLost) || errors.Is(err, ErrTaskNotFound) || !time.Now().Before(expiry) {
			break
		}
		if err == nil && renewErr == nil {
			expiry = now.Add(w.config.LeaseDuration)
		}
		timer.Reset(min(w.config.LeaseExtendInterval, time.Until(expiry)))
	}
	leaseLost.Store(true)
	cancel()
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
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
	if cfg.LeaseExtendInterval <= 0 || cfg.LeaseExtendInterval >= cfg.LeaseDuration {
		cfg.LeaseExtendInterval = cfg.LeaseDuration / 2
	}
	cfg.LeaseExtendInterval = max(cfg.LeaseExtendInterval, time.Nanosecond)
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
		w.inflightTasks[taskClaim{task.ID, task.LeaseOwner}] = inflightTask{task: task}
	}
	w.inflight.Add(int64(len(tasks)))
	w.inflightMu.Unlock()
}

func (w *Worker) beginHandler(task Task) bool {
	w.inflightMu.Lock()
	defer w.inflightMu.Unlock()
	if w.runCtx.Err() != nil {
		return false
	}
	key := taskClaim{task.ID, task.LeaseOwner}
	if entry, ok := w.inflightTasks[key]; ok {
		entry.executing = true
		w.inflightTasks[key] = entry
	}
	return true
}

func (w *Worker) endHandler(task Task) {
	w.inflightMu.Lock()
	defer w.inflightMu.Unlock()
	key := taskClaim{task.ID, task.LeaseOwner}
	if entry, ok := w.inflightTasks[key]; ok {
		entry.executing = false
		w.inflightTasks[key] = entry
	}
}

func (w *Worker) untrackInflight(task Task) {
	w.inflightMu.Lock()
	delete(w.inflightTasks, taskClaim{task.ID, task.LeaseOwner})
	w.inflightMu.Unlock()
}

func (w *Worker) releaseInflight(ctx context.Context) error {
	if w.queue == nil {
		return nil
	}
	w.inflightMu.Lock()
	tasks := make([]Task, 0, len(w.inflightTasks))
	for _, entry := range w.inflightTasks {
		if !entry.executing {
			tasks = append(tasks, entry.task)
		}
	}
	w.inflightMu.Unlock()

	var releaseErr error
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return errors.Join(releaseErr, err)
		}
		now := time.Now()
		err := w.queue.Release(ctx, task.ID, task.LeaseOwner, ReleaseOptions{
			RetryAt: now,
			Reason:  "worker shutdown",
			Now:     now,
		})
		if err != nil && !errors.Is(err, ErrLeaseLost) && !errors.Is(err, ErrTaskNotFound) {
			releaseErr = errors.Join(releaseErr, err)
			continue
		}
		w.untrackInflight(task)
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
	// 先等已发出的领取交接给本地清理流程，再给处理器一段时间响应取消。
	if err := waitForWorkerSignal(ctx, w.pollDone); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		cooperate, cancel := context.WithTimeout(ctx, time.Until(deadline)/2)
		_ = waitForWorkerSignal(cooperate, w.workersDone)
		cancel()
	}
	return w.releaseInflight(ctx)
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

func waitForWorkerSignal(ctx context.Context, done <-chan struct{}) error {
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
