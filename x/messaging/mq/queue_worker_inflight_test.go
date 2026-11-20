package mq

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

type workerInflightTestStore struct {
	TaskStore
	reserved bool
	acked    []string
	released []string
}

func (s *workerInflightTestStore) Reserve(ctx context.Context, _ ReserveOptions) ([]Task, error) {
	if s.reserved {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.reserved = true
	return []Task{{ID: "first", Topic: "test"}, {ID: "second", Topic: "test"}}, nil
}

func (s *workerInflightTestStore) Ack(_ context.Context, id, _ string, _ time.Time) error {
	s.acked = append(s.acked, id)
	return nil
}

func (s *workerInflightTestStore) Release(_ context.Context, id, _ string, _ ReleaseOptions) error {
	s.released = append(s.released, id)
	return nil
}

func TestWorkerTracksLeaseBeforeDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &workerInflightTestStore{}
		w := NewWorker(NewTaskQueue(store), WorkerConfig{
			ConsumerID: "worker", Concurrency: 2, MaxInflight: 2,
		})
		w.config = normalizeWorkerConfig(w.config)
		w.config.LeaseExtendInterval = 0
		w.runCtx, w.runCancel = context.WithCancel(t.Context())
		w.pollCtx, w.pollCancel = context.WithCancel(w.runCtx)
		w.pollDone = make(chan struct{})
		w.tasksCh = make(chan Task)
		w.inflightTasks = make(map[string]Task)
		defer w.runCancel()
		w.Register("test", func(context.Context, Task) error { return nil })
		w.wg.Add(1)
		go w.pollLoop()

		synctest.Wait() // First send is waiting for a worker.
		if got := w.inflight.Load(); got != 2 || len(w.inflightTasks) != 2 {
			t.Fatalf("before delivery count = %d, leases = %v", got, w.inflightTasks)
		}
		w.handleTask(<-w.tasksCh)
		synctest.Wait() // Second delivery is still blocked.
		if got := w.inflight.Load(); got != 1 || w.inflightTasks["second"].ID != "second" {
			t.Fatalf("after first task count = %d, leases = %v", got, w.inflightTasks)
		}

		if err := w.forceStop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := w.inflight.Load(); got != 0 || len(w.inflightTasks) != 0 {
			t.Fatalf("after cancel count = %d, leases = %v", got, w.inflightTasks)
		}
		if !slices.Equal(store.acked, []string{"first"}) || !slices.Equal(store.released, []string{"second"}) {
			t.Fatalf("acknowledged = %v, released = %v", store.acked, store.released)
		}
	})
}
