package mq

import (
	"container/heap"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

type recordingPubSub struct {
	publishCh chan Message
}

func (r *recordingPubSub) Publish(topic string, msg pubsub.Message) error {
	if r.publishCh != nil {
		r.publishCh <- msg
	}
	return nil
}

func (r *recordingPubSub) Subscribe(ctx context.Context, topic string, opts pubsub.SubOptions) (pubsub.Subscription, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingPubSub) Close() error {
	if r.publishCh != nil {
		close(r.publishCh)
	}
	return nil
}

func TestPriorityQueueOrdering(t *testing.T) {
	var pq priorityQueue
	heap.Init(&pq)

	heap.Push(&pq, &priorityEnvelope{priority: PriorityLow, seq: 2})
	heap.Push(&pq, &priorityEnvelope{priority: PriorityHigh, seq: 1})
	heap.Push(&pq, &priorityEnvelope{priority: PriorityHigh, seq: 3})
	heap.Push(&pq, &priorityEnvelope{priority: PriorityNormal, seq: 4})

	want := []struct {
		priority MessagePriority
		seq      uint64
	}{
		{PriorityHigh, 1},
		{PriorityHigh, 3},
		{PriorityNormal, 4},
		{PriorityLow, 2},
	}

	for i := 0; i < len(want); i++ {
		item := heap.Pop(&pq).(*priorityEnvelope)
		if item.priority != want[i].priority || item.seq != want[i].seq {
			t.Fatalf("pop %d: expected priority %d seq %d, got priority %d seq %d",
				i, want[i].priority, want[i].seq, item.priority, item.seq)
		}
	}
}

func TestPublishPriorityRespectsContextCancel(t *testing.T) {
	ps := &recordingPubSub{publishCh: make(chan Message, 1)}
	cfg := DefaultConfig()
	cfg.EnablePriorityQueue = true

	broker, err := NewInProcBroker(ps, WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = broker.PublishPriority(ctx, "topic", PriorityMessage{
		Message:  Message{ID: "msg-1"},
		Priority: PriorityHigh,
	})
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}

	select {
	case <-ps.publishCh:
		t.Fatalf("unexpected publish after context cancel")
	case <-time.After(50 * time.Millisecond):
	}
}
