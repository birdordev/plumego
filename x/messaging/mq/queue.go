package mq

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"
)

type TaskQueue struct {
	store   TaskStore
	now     func() time.Time
	metrics MetricsObserver
	ready   chan struct{}
}

type TaskQueueOption func(*TaskQueue)

func WithQueueNowFunc(now func() time.Time) TaskQueueOption {
	return func(q *TaskQueue) {
		q.now = now
	}
}

func WithQueueMetricsObserver(observer MetricsObserver) TaskQueueOption {
	return func(q *TaskQueue) {
		q.metrics = observer
	}
}

func NewTaskQueue(store TaskStore, opts ...TaskQueueOption) *TaskQueue {
	q := &TaskQueue{store: store, now: time.Now, ready: make(chan struct{}, 1)}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

func (q *TaskQueue) Enqueue(ctx context.Context, task Task, opts EnqueueOptions) error {
	if q == nil || q.store == nil {
		return ErrNotInitialized
	}

	if strings.TrimSpace(task.ID) == "" {
		return fmt.Errorf("%w: task id is required", ErrInvalidConfig)
	}
	if err := validateTopic(task.Topic); err != nil {
		return err
	}

	now := q.now()

	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now

	if opts.DedupeKey != "" {
		task.DedupeKey = opts.DedupeKey
	}
	if opts.AvailableAt.IsZero() {
		if task.AvailableAt.IsZero() {
			task.AvailableAt = now
		}
	} else {
		task.AvailableAt = opts.AvailableAt
	}
	if !opts.ExpiresAt.IsZero() {
		task.ExpiresAt = opts.ExpiresAt
	}
	if !task.ExpiresAt.IsZero() && !task.ExpiresAt.After(now) {
		return ErrTaskExpired
	}
	if opts.Priority != nil {
		task.Priority = *opts.Priority
	} else if task.Priority == 0 {
		task.Priority = PriorityNormal
	}

	maxAttempts := task.MaxAttempts
	if opts.MaxAttempts > 0 {
		maxAttempts = opts.MaxAttempts
	}
	if maxAttempts <= 0 {
		maxAttempts = DefaultTaskMaxAttempts
	}
	task.MaxAttempts = maxAttempts

	start := time.Now()
	err := q.store.Insert(ctx, task)
	if err == nil && !task.AvailableAt.After(now) {
		select {
		case q.ready <- struct{}{}:
		default:
		}
	}
	q.observe(ctx, "queue_enqueue", task.Topic, start, err)
	return err
}

func (q *TaskQueue) Reserve(ctx context.Context, opts ReserveOptions) ([]Task, error) {
	if q == nil || q.store == nil {
		return nil, ErrNotInitialized
	}
	if strings.TrimSpace(opts.ConsumerID) == "" {
		return nil, fmt.Errorf("%w: consumer id is required", ErrInvalidConfig)
	}
	if opts.Limit <= 0 {
		return nil, nil
	}
	if opts.Lease <= 0 {
		opts.Lease = DefaultLeaseDuration
	}
	if opts.Now.IsZero() {
		opts.Now = q.now()
	}

	start := time.Now()
	tasks, err := q.store.Reserve(ctx, opts)
	topic := ""
	if len(opts.Topics) > 0 {
		topic = opts.Topics[0]
	}
	q.observe(ctx, "queue_reserve", topic, start, err)
	return tasks, err
}

// ReserveFenced assigns a fresh lease owner to this reservation. Finalization
// and renewal must pass the returned task's LeaseOwner, rather than the
// configured consumer ID. Together, task ID and lease owner identify a claim.
func (q *TaskQueue) ReserveFenced(ctx context.Context, opts ReserveOptions) ([]Task, error) {
	if strings.TrimSpace(opts.ConsumerID) == "" {
		return q.Reserve(ctx, opts)
	}
	opts.ConsumerID += "/" + rand.Text()
	return q.Reserve(ctx, opts)
}

func (q *TaskQueue) Ack(ctx context.Context, taskID, consumerID string, now time.Time) error {
	if q == nil || q.store == nil {
		return ErrNotInitialized
	}
	if now.IsZero() {
		now = q.now()
	}
	start := time.Now()
	err := q.store.Ack(ctx, taskID, consumerID, now)
	q.observe(ctx, "queue_ack", "", start, err)
	return err
}

func (q *TaskQueue) Release(ctx context.Context, taskID, consumerID string, opts ReleaseOptions) error {
	if q == nil || q.store == nil {
		return ErrNotInitialized
	}
	if opts.Now.IsZero() {
		opts.Now = q.now()
	}
	if opts.RetryAt.IsZero() {
		opts.RetryAt = opts.Now
	}
	start := time.Now()
	err := q.store.Release(ctx, taskID, consumerID, opts)
	q.observe(ctx, "queue_release", "", start, err)
	return err
}

func (q *TaskQueue) MoveToDLQ(ctx context.Context, taskID, consumerID, reason string, now time.Time) error {
	if q == nil || q.store == nil {
		return ErrNotInitialized
	}
	if now.IsZero() {
		now = q.now()
	}
	start := time.Now()
	err := q.store.MoveToDLQ(ctx, taskID, consumerID, reason, now)
	q.observe(ctx, "queue_dlq", "", start, err)
	return err
}

func (q *TaskQueue) ExtendLease(ctx context.Context, taskID, consumerID string, lease time.Duration, now time.Time) error {
	if q == nil || q.store == nil {
		return ErrNotInitialized
	}
	if lease <= 0 {
		lease = DefaultLeaseDuration
	}
	if now.IsZero() {
		now = q.now()
	}
	start := time.Now()
	err := q.store.ExtendLease(ctx, taskID, consumerID, lease, now)
	q.observe(ctx, "queue_extend", "", start, err)
	return err
}

func (q *TaskQueue) Stats(ctx context.Context) (Stats, error) {
	if q == nil || q.store == nil {
		return Stats{}, ErrNotInitialized
	}
	start := time.Now()
	stats, err := q.store.Stats(ctx)
	q.observe(ctx, "queue_stats", "", start, err)
	return stats, err
}

func (q *TaskQueue) observe(ctx context.Context, op, topic string, start time.Time, err error) {
	if q.metrics == nil {
		return
	}
	q.metrics.ObserveMQ(ctx, op, topic, time.Since(start), err, false)
}

func (q *TaskQueue) collector() MetricsObserver {
	return q.metrics
}
