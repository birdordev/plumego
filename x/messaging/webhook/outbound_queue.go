package webhook

import (
	"context"
	"sync"
	"time"
)

type Task struct {
	DeliveryID string
}

type Queue struct {
	ch         chan Task
	dropPolicy DropPolicy
	blockWait  time.Duration
	closeOnce  sync.Once
	mu         sync.Mutex
	done       chan struct{}
	senders    sync.WaitGroup
}

func NewQueue(size int, policy DropPolicy, blockWait time.Duration) *Queue {
	return &Queue{
		ch:         make(chan Task, size),
		done:       make(chan struct{}),
		dropPolicy: policy,
		blockWait:  blockWait,
	}
}

// Enqueue admits a task according to the queue policy. Once closing starts,
// new calls fail and pending calls may complete only while the task channel is open.
func (q *Queue) Enqueue(ctx context.Context, t Task) error {
	q.mu.Lock()
	select {
	case <-q.done:
		q.mu.Unlock()
		return errQueueClosed
	default:
		q.senders.Add(1)
		q.mu.Unlock()
	}
	defer q.senders.Done()

	if q.dropPolicy != BlockWithLimit {
		select {
		case q.ch <- t:
			return nil
		default:
			return ErrQueueFull
		}
	}
	if q.blockWait <= 0 {
		select {
		case q.ch <- t:
			return nil
		case <-q.done:
			return errQueueClosed
		case <-ctx.Done():
			return ctx.Err()
		default:
			return ErrQueueFull
		}
	}

	timer := time.NewTimer(q.blockWait)
	defer timer.Stop()
	select {
	case q.ch <- t:
		return nil
	case <-q.done:
		return errQueueClosed
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrQueueFull
	}
}

func (q *Queue) Chan() <-chan Task { return q.ch }

// Close wakes waiting senders, waits for them to finish, and closes the task
// channel. Receivers can drain tasks that were already queued.
func (q *Queue) Close() {
	q.closeOnce.Do(func() {
		q.mu.Lock()
		close(q.done)
		q.mu.Unlock()
		q.senders.Wait()
		close(q.ch)
	})
}
