package mq

import (
	"context"
	"errors"
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

type finalizationTestStore struct {
	TaskStore
	task     *Task
	finalize func(context.Context, string) error
	cleanup  chan struct{}
}

func (s *finalizationTestStore) Reserve(ctx context.Context, _ ReserveOptions) ([]Task, error) {
	if s.task != nil {
		task := *s.task
		s.task = nil
		return []Task{task}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *finalizationTestStore) Ack(ctx context.Context, _, _ string, _ time.Time) error {
	return s.finalize(ctx, "ack")
}

func (s *finalizationTestStore) MoveToDLQ(ctx context.Context, _, _, _ string, _ time.Time) error {
	return s.finalize(ctx, "dlq")
}

func (s *finalizationTestStore) Release(ctx context.Context, _, _ string, opts ReleaseOptions) error {
	if opts.Reason == "worker shutdown" {
		if err := ctx.Err(); err != nil {
			return err
		}
		close(s.cleanup)
		return nil
	}
	return s.finalize(ctx, "release")
}

type completedTestDeduper struct{}

func (completedTestDeduper) IsCompleted(context.Context, string) (bool, error)          { return true, nil }
func (completedTestDeduper) MarkCompleted(context.Context, string, time.Duration) error { return nil }

func TestWorkerCancelsBlockedFinalization(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		operation string
		attempt   int
		dedupe    bool
		graceful  bool
	}{
		{"graceful ack", "ack", 1, false, true},
		{"forced ack", "ack", 1, false, false},
		{"already completed ack", "ack", 1, true, false},
		{"retry release", "release", 1, false, false},
		{"failed last attempt", "dlq", 2, false, false},
		{"exceeded attempts", "dlq", 3, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started := make(chan context.Context, 1)
				finished := make(chan error, 1)
				allow := make(chan struct{})
				store := &finalizationTestStore{
					task:    &Task{ID: "task", Topic: "topic", Attempts: scenario.attempt, MaxAttempts: 2},
					cleanup: make(chan struct{}),
					finalize: func(ctx context.Context, operation string) error {
						if operation != scenario.operation {
							t.Errorf("operation = %s, want %s", operation, scenario.operation)
						}
						started <- ctx
						var err error
						select {
						case <-allow:
						case <-ctx.Done():
							err = ctx.Err()
						}
						finished <- err
						return err
					},
				}
				config := WorkerConfig{ShutdownTimeout: time.Minute}
				if scenario.dedupe {
					config.Deduper = completedTestDeduper{}
					config.DedupeKeyFunc = func(Task) string { return "dedupe" }
				}
				worker := NewWorker(NewTaskQueue(store), config)
				worker.Register("topic", func(context.Context, Task) error {
					if scenario.operation != "ack" {
						return errors.New("task failed")
					}
					return nil
				})
				worker.Start(t.Context())
				req := <-started
				if _, ok := req.Deadline(); !ok || req.Err() != nil {
					t.Errorf("finalization context = %v", req)
				}
				stopCtx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- worker.Stop(stopCtx) }()
				synctest.Wait()
				if scenario.graceful {
					if req.Err() != nil {
						t.Errorf("graceful Stop canceled finalization: %v", req.Err())
					}
					close(allow)
					if err := <-done; err != nil {
						t.Fatal(err)
					}
					if err := <-finished; err != nil {
						t.Fatal(err)
					}
					return
				}
				if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("Stop = %v", err)
				}
				if err := <-finished; !errors.Is(err, context.Canceled) {
					t.Errorf("finalization = %v", err)
				}
				select {
				case <-store.cleanup:
				default:
					t.Error("forced Stop did not release the retained lease")
				}
			})
		})
	}
}
