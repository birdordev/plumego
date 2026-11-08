package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	xscheduler "github.com/birdordev/plumego/x/messaging/scheduler"
	"with-events/internal/order"
)

func TestScheduleEnqueuesDelayedJob(t *testing.T) {
	s := &fakeScheduler{}
	job := NewRetryJob(s, &fakePublisher{}, nil)

	if err := job.Schedule(t.Context(), order.OrderCreated{ID: "evt-1", OrderID: "ord-1"}, 5*time.Second); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	if s.delay != 5*time.Second {
		t.Fatalf("delay = %s, want 5s", s.delay)
	}
	if s.id != "order.retry.evt-1" {
		t.Fatalf("id = %q, want order.retry.evt-1", s.id)
	}
	if s.task == nil {
		t.Fatal("expected scheduled task")
	}
}

func TestHandleRepublishesOnFirstAttempt(t *testing.T) {
	publisher := &fakePublisher{}
	job := NewRetryJob(&fakeScheduler{}, publisher, nil)

	if err := job.Handle(t.Context(), order.OrderCreated{ID: "evt-1"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if publisher.count != 1 {
		t.Fatalf("publish count = %d, want 1", publisher.count)
	}
}

func TestHandleStopsAfterMaxRetries(t *testing.T) {
	publisher := &fakePublisher{err: errors.New("publish failed")}
	s := &fakeScheduler{}
	job := NewRetryJob(s, publisher, nil)
	event := order.OrderCreated{ID: "evt-1"}

	for i := 0; i < MaxRetries; i++ {
		if err := job.Handle(t.Context(), event); err != nil {
			t.Fatalf("handle attempt %d: %v", i+1, err)
		}
	}
	if err := job.Handle(t.Context(), event); err == nil {
		t.Fatal("expected final retry error")
	}
	if s.scheduled != MaxRetries {
		t.Fatalf("scheduled retries = %d, want %d", s.scheduled, MaxRetries)
	}
}

func TestHandleDoesNotRetryWhenAlreadyProcessed(t *testing.T) {
	store := order.NewMemoryIdempotencyStore()
	store.Mark("evt-1")
	publisher := &fakePublisher{err: errors.New("publish failed")}
	s := &fakeScheduler{}
	job := NewRetryJob(s, publisher, store)

	if err := job.Handle(t.Context(), order.OrderCreated{ID: "evt-1"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if publisher.count != 0 {
		t.Fatalf("publish count = %d, want 0", publisher.count)
	}
	if s.scheduled != 0 {
		t.Fatalf("scheduled retries = %d, want 0", s.scheduled)
	}
}

type fakeScheduler struct {
	id         xscheduler.JobID
	delay      time.Duration
	task       xscheduler.TaskFunc
	scheduled  int
	registered string
}

func (s *fakeScheduler) RegisterTask(name string, task xscheduler.TaskFunc) error {
	s.registered = name
	s.task = task
	return nil
}

func (s *fakeScheduler) Delay(id xscheduler.JobID, delay time.Duration, task xscheduler.TaskFunc, _ ...xscheduler.JobOption) (xscheduler.JobID, error) {
	s.id = id
	s.delay = delay
	s.task = task
	s.scheduled++
	return id, nil
}

type fakePublisher struct {
	count int
	err   error
}

func (p *fakePublisher) Publish(context.Context, order.OrderCreated) error {
	p.count++
	return p.err
}
