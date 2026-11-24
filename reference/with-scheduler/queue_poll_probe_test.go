package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

type measuredQueueStore struct {
	*mqstore.SQLStore
	reserves atomic.Int64
	hints    atomic.Int64
}

func (s *measuredQueueStore) Reserve(ctx context.Context, opts mq.ReserveOptions) ([]mq.Task, error) {
	s.reserves.Add(1)
	return s.SQLStore.Reserve(ctx, opts)
}

func (s *measuredQueueStore) NextAvailable(ctx context.Context, topics []string, now time.Time) (time.Time, error) {
	s.hints.Add(1)
	return s.SQLStore.NextAvailable(ctx, topics, now)
}

func TestPostgresQueuePollingProbe(t *testing.T) {
	if os.Getenv("PLUMEGO_TEST_QUEUE_CAPACITY") != "1" {
		t.Skip("set PLUMEGO_TEST_QUEUE_CAPACITY=1 to run the capacity probe")
	}
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	cfg := mqstore.DefaultSQLConfig()
	cfg.DeferredExpiration = true
	queue := mqstore.NewSQL(db, cfg)
	repo := repository{db, queue}
	pause := func(delay time.Duration) {
		t.Helper()
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	stop := func(w *mq.Worker) {
		t.Helper()
		if err := w.Stop(ctx); err != nil {
			t.Error(err)
		}
	}
	const interval = 20 * time.Millisecond
	var baseline int64
	for _, maximum := range []time.Duration{0, 400 * time.Millisecond} {
		mode := "fixed"
		if maximum != 0 {
			mode = "adaptive"
		}
		t.Run(mode, func(t *testing.T) {
			measured := &measuredQueueStore{SQLStore: queue}
			var workers []*mq.Worker
			for i := range 4 {
				w := mq.NewWorker(mq.NewTaskQueue(measured), mq.WorkerConfig{
					ConsumerID: fmt.Sprintf("idle-%d", i), Concurrency: 1, PollInterval: interval, MaxIdlePollInterval: maximum,
				})
				w.Register("demo.record", func(context.Context, mq.Task) error { return nil })
				w.Start(ctx)
				workers = append(workers, w)
			}
			t.Cleanup(func() {
				for _, w := range workers {
					stop(w)
				}
			})
			pause(700 * time.Millisecond)
			startReserves, startHints := measured.reserves.Load(), measured.hints.Load()
			start := time.Now()
			pause(2 * time.Second)
			reserves, hints := measured.reserves.Load()-startReserves, measured.hints.Load()-startHints
			qps := float64(reserves+hints) / time.Since(start).Seconds()
			t.Logf("empty 4-worker queue: reserves=%d hints=%d queries/second=%.1f", reserves, hints, qps)
			if maximum == 0 {
				baseline = reserves + hints
			} else if (reserves+hints)*2 >= baseline {
				t.Errorf("idle backoff reduced query count by less than half: fixed=%d adaptive=%d", baseline, reserves+hints)
			}
		})
		t.Run(mode+" intake latency", func(t *testing.T) {
			var wake <-chan struct{}
			if maximum != 0 {
				wake = startQueueTestListener(t, queueListenerConfig(t, db))
			}
			started := make(chan mq.Task, 1)
			w := mq.NewWorker(mq.NewTaskQueue(queue), mq.WorkerConfig{
				Concurrency: 1, MaxInflight: 1, PollInterval: interval, MaxIdlePollInterval: maximum, Wake: wake,
			})
			w.Register("demo.record", func(_ context.Context, task mq.Task) error {
				started <- task
				return nil
			})
			w.Start(ctx)
			defer stop(w)
			const samples = 60
			latencies := make([]time.Duration, 0, samples)
			for i := range samples {
				pause(65 * time.Millisecond)
				start := time.Now()
				id, err := repo.submit(ctx, "tenant", fmt.Sprintf("poll-probe-%s-%d", mode, i),
					submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
				if err != nil {
					t.Fatal(err)
				}
				select {
				case task := <-started:
					if task.ID != id {
						t.Fatalf("unexpected task ID: %s", task.ID)
					}
					latencies = append(latencies, time.Since(start))
				case <-time.After(2 * time.Second):
					t.Fatal("new task was not claimed")
				}
			}
			slices.Sort(latencies)
			p95 := latencies[samples*95/100-1]
			t.Logf("submit to handler start (%d samples): p50=%s p95=%s max=%s", samples, latencies[samples/2-1], p95, latencies[samples-1])
			if maximum > 0 && p95 >= maximum/2 {
				t.Errorf("notification path did not keep p95 below half the fallback interval: %s", p95)
			}
		})
	}
}
