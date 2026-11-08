package mq_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

type memoryDeduper struct {
	seen sync.Map
}

func (d *memoryDeduper) IsCompleted(ctx context.Context, key string) (bool, error) {
	_, ok := d.seen.Load(key)
	return ok, nil
}

func (d *memoryDeduper) MarkCompleted(ctx context.Context, key string, ttl time.Duration) error {
	d.seen.Store(key, struct{}{})
	return nil
}

func TestWorkerDeduperSkipsCompleted(t *testing.T) {
	store := mqstore.NewMemory(mqstore.MemConfig{})
	queue := mq.NewTaskQueue(store)

	deduper := &memoryDeduper{}
	deduper.seen.Store("tenant-1:task-1", struct{}{})

	var handled atomic.Int64
	worker := mq.NewWorker(queue, mq.WorkerConfig{
		ConsumerID:      "worker-1",
		Concurrency:     1,
		PollInterval:    10 * time.Millisecond,
		LeaseDuration:   50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
		Deduper:         deduper,
	})
	worker.Register("send", func(ctx context.Context, task mq.Task) error {
		handled.Add(1)
		return nil
	})

	worker.Start(t.Context())
	defer func() {
		_ = worker.Stop(t.Context())
	}()

	err := queue.Enqueue(t.Context(), mq.Task{
		ID:       "task-1",
		Topic:    "send",
		TenantID: "tenant-1",
		Payload:  []byte(`{}`),
	}, mq.EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if handled.Load() != 0 {
		t.Fatalf("expected handler to be skipped, got %d", handled.Load())
	}

	reserved, err := queue.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"send"},
		Limit:      1,
		ConsumerID: "probe",
		Now:        time.Now(),
	})
	if err != nil {
		t.Fatalf("reserve after: %v", err)
	}
	if len(reserved) != 0 {
		t.Fatalf("expected no tasks after dedupe, got %d", len(reserved))
	}
}

func TestWorkerStopDrainsInflight(t *testing.T) {
	store := mqstore.NewMemory(mqstore.MemConfig{})
	queue := mq.NewTaskQueue(store)

	started := make(chan struct{})
	finish := make(chan struct{})

	worker := mq.NewWorker(queue, mq.WorkerConfig{
		ConsumerID:      "worker-1",
		Concurrency:     1,
		PollInterval:    10 * time.Millisecond,
		LeaseDuration:   50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	})
	worker.Register("send", func(ctx context.Context, task mq.Task) error {
		close(started)
		select {
		case <-finish:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	worker.Start(t.Context())

	err := queue.Enqueue(t.Context(), mq.Task{
		ID:       "task-2",
		Topic:    "send",
		TenantID: "tenant-1",
		Payload:  []byte(`{}`),
	}, mq.EnqueueOptions{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("handler did not start")
	}

	stopErr := make(chan error, 1)
	go func() {
		stopErr <- worker.Stop(t.Context())
	}()

	time.Sleep(50 * time.Millisecond)
	close(finish)

	select {
	case err := <-stopErr:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("stop timed out")
	}

	reserved, err := queue.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"send"},
		Limit:      1,
		ConsumerID: "probe",
		Now:        time.Now(),
	})
	if err != nil {
		t.Fatalf("reserve after stop: %v", err)
	}
	if len(reserved) != 0 {
		t.Fatalf("expected no tasks after stop, got %d", len(reserved))
	}
}
