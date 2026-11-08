package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	xscheduler "github.com/birdordev/plumego/x/messaging/scheduler"
	"with-events/internal/order"
)

const (
	RetryTaskName = "order.retry"
	MaxRetries    = 3
)

// Scheduler is the delayed-job surface used by RetryJob.
type Scheduler interface {
	RegisterTask(name string, task xscheduler.TaskFunc) error
	Delay(id xscheduler.JobID, delay time.Duration, task xscheduler.TaskFunc, opts ...xscheduler.JobOption) (xscheduler.JobID, error)
}

// Publisher republishes order events.
type Publisher interface {
	Publish(ctx context.Context, event order.OrderCreated) error
}

// RetryJob schedules and executes delayed order-event retries.
type RetryJob struct {
	scheduler Scheduler
	publisher Publisher
	processed order.IdempotencyStore

	mu       sync.Mutex
	attempts map[string]int
}

// NewRetryJob creates a delayed retry job.
func NewRetryJob(s Scheduler, publisher Publisher, processed order.IdempotencyStore) *RetryJob {
	return &RetryJob{
		scheduler: s,
		publisher: publisher,
		processed: processed,
		attempts:  make(map[string]int),
	}
}

// Register registers the named retry task for schedulers with persistent task recovery.
func (r *RetryJob) Register(context.Context) error {
	if r == nil || r.scheduler == nil {
		return fmt.Errorf("retry scheduler is not configured")
	}
	return r.scheduler.RegisterTask(RetryTaskName, func(context.Context) error { return nil })
}

// Schedule enqueues a delayed retry for event.
func (r *RetryJob) Schedule(ctx context.Context, event order.OrderCreated, delay time.Duration) error {
	if r == nil || r.scheduler == nil {
		return fmt.Errorf("retry scheduler is not configured")
	}
	if event.ID == "" {
		event.ID = event.OrderID
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	jobID := xscheduler.JobID("order.retry." + event.ID)
	_, err = r.scheduler.Delay(jobID, delay, func(runCtx context.Context) error {
		var retryEvent order.OrderCreated
		if err := json.Unmarshal(payload, &retryEvent); err != nil {
			return err
		}
		return r.Handle(runCtx, retryEvent)
	}, xscheduler.ReplaceExisting(), xscheduler.WithTaskName(RetryTaskName))
	if err != nil {
		return err
	}
	_ = ctx
	return nil
}

// Handle republishes an event or schedules another retry when publishing fails.
func (r *RetryJob) Handle(ctx context.Context, event order.OrderCreated) error {
	if r == nil || r.publisher == nil {
		return fmt.Errorf("retry publisher is not configured")
	}
	if event.ID == "" {
		event.ID = event.OrderID
	}
	if r.processed != nil && r.processed.Seen(event.ID) {
		return nil
	}
	if err := r.publisher.Publish(ctx, event); err != nil {
		attempt := r.nextAttempt(event.ID)
		if attempt > MaxRetries {
			return err
		}
		return r.Schedule(ctx, event, backoff(attempt))
	}
	return nil
}

func (r *RetryJob) nextAttempt(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts[id]++
	return r.attempts[id]
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
