package mq

import (
	"context"
	"testing"
	"time"
)

type availabilityTestStore struct {
	TaskStore
	at time.Time
}

func (s availabilityTestStore) NextAvailable(context.Context, []string, time.Time) (time.Time, error) {
	return s.at, nil
}

func TestIdlePollingBoundsAndFutureHint(t *testing.T) {
	const base, maximum = 20 * time.Millisecond, time.Second
	for _, check := range []struct{ current, want time.Duration }{
		{base, 2 * base},
		{maximum * 3 / 4, maximum},
		{maximum, maximum},
		{time.Duration(1<<63 - 2), time.Duration(1<<63 - 1)},
	} {
		cap := maximum
		if check.current > maximum {
			cap = time.Duration(1<<63 - 1)
		}
		if got := nextIdlePoll(check.current, cap); got != check.want {
			t.Fatalf("idle interval from %s = %s, want %s", check.current, got, check.want)
		}
	}
	for range 50 {
		if got := jitterIdlePoll(maximum, base, maximum); got < 750*time.Millisecond || got > maximum {
			t.Fatalf("jitter escaped clamp: %s", got)
		}
	}
	now := time.Unix(100, 0)
	store := availabilityTestStore{at: now.Add(42 * time.Millisecond)}
	queue := NewTaskQueue(store, WithQueueNowFunc(func() time.Time { return now }))
	w := NewWorker(queue, WorkerConfig{PollInterval: base, MaxIdlePollInterval: maximum})
	w.pollCtx = t.Context()
	if got := w.idlePollDelay([]string{"topic"}, maximum); got != 42*time.Millisecond {
		t.Fatalf("future availability did not cap wait: %s", got)
	}
	w.config.MaxIdlePollInterval = 0
	if got := w.idlePollDelay([]string{"topic"}, maximum); got != base {
		t.Fatalf("fixed polling default changed: %s", got)
	}
}

func TestClosedWakeChannelRetainsPeriodicWait(t *testing.T) {
	w := NewWorker(NewTaskQueue(nil), WorkerConfig{})
	w.pollCtx = t.Context()
	closed := make(chan struct{})
	close(closed)
	var wake <-chan struct{} = closed
	notified, running := w.waitIdle(10*time.Millisecond, &wake)
	if notified || !running || wake != nil {
		t.Fatalf("closed wake channel: notified=%t running=%t", notified, running)
	}
}
