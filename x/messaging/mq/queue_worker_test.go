package mq_test

import (
	"context"
	"errors"
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

type shutdownReleaseStore struct {
	mq.TaskStore
	release func(context.Context, string, string, mq.ReleaseOptions) error
}

func (s shutdownReleaseStore) Release(ctx context.Context, id, consumer string, opts mq.ReleaseOptions) error {
	return s.release(ctx, id, consumer, opts)
}

func TestWorkerStopBoundsForcedLeaseRelease(t *testing.T) {
	for _, block := range []bool{false, true} {
		name := "released"
		if block {
			name = "expires"
		}
		t.Run(name, func(t *testing.T) {
			backend := mqstore.NewMemory(mqstore.MemConfig{})
			attempted := make(chan struct{}, 1)
			queue := mq.NewTaskQueue(shutdownReleaseStore{
				TaskStore: backend,
				release: func(ctx context.Context, id, consumer string, opts mq.ReleaseOptions) error {
					if opts.Reason != "worker shutdown" {
						return backend.Release(ctx, id, consumer, opts)
					}
					attempted <- struct{}{}
					if block {
						<-ctx.Done()
						return ctx.Err()
					}
					return backend.Release(ctx, id, consumer, opts)
				},
			})
			const lease = time.Minute
			started := make(chan struct{})
			finish := make(chan struct{})
			defer close(finish)
			worker := mq.NewWorker(queue, mq.WorkerConfig{ConsumerID: "worker", PollInterval: time.Millisecond, LeaseDuration: lease, ShutdownTimeout: 300 * time.Millisecond})
			worker.Register("send", func(context.Context, mq.Task) error {
				close(started)
				<-finish
				return nil
			})
			if err := queue.Enqueue(t.Context(), mq.Task{ID: "stop-test", Topic: "send"}, mq.EnqueueOptions{}); err != nil {
				t.Fatal(err)
			}
			worker.Start(t.Context())
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not start")
			}

			result := make(chan error, 1)
			go func() { result <- worker.Stop(t.Context()) }()
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Stop: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("forced release outlived stop budget")
			}
			select {
			case <-attempted:
			default:
				t.Fatal("Stop did not attempt to release the lease")
			}
			opts := mq.ReserveOptions{Topics: []string{"send"}, Limit: 1, ConsumerID: "next", Now: time.Now()}
			tasks, err := queue.Reserve(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if !block {
				if len(tasks) != 1 {
					t.Fatal("released task is not reservable")
				}
				return
			}
			if len(tasks) != 0 {
				t.Fatal("lease was released before expiration")
			}
			opts.Now = opts.Now.Add(lease + time.Second)
			tasks, err = queue.Reserve(t.Context(), opts)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("after lease expiration: tasks=%v error=%v", tasks, err)
			}
		})
	}
}
