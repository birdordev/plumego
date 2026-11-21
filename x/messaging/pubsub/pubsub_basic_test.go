package pubsub

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPubSub_MultiSubscriberReceive(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub1, err := ps.Subscribe(t.Context(), "user.created", SubOptions{BufferSize: 8, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe sub1: %v", err)
	}
	sub2, err := ps.Subscribe(t.Context(), "user.created", SubOptions{BufferSize: 8, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe sub2: %v", err)
	}

	msg := Message{ID: "m1", Type: "user.created", Data: 1}
	if err := ps.Publish("user.created", msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-sub1.C():
		if got.ID != "m1" || got.Topic != "user.created" {
			t.Fatalf("sub1 got unexpected: %+v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("sub1 timeout")
	}

	select {
	case got := <-sub2.C():
		if got.ID != "m1" || got.Topic != "user.created" {
			t.Fatalf("sub2 got unexpected: %+v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("sub2 timeout")
	}
}

func TestPubSub_CancelStopsDelivery(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 1, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	sub.Cancel()

	_ = ps.Publish("t", Message{ID: "m1"})
	// channel should be closed
	_, ok := <-sub.C()
	if ok {
		t.Fatalf("expected channel closed")
	}
}

func TestPubSub_PatternSubscription(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.SubscribePattern("user.*", SubOptions{BufferSize: 4, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe pattern: %v", err)
	}
	defer sub.Cancel()

	if err := ps.Publish("user.created", Message{ID: "m1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-sub.C():
		if got.Topic != "user.created" {
			t.Fatalf("unexpected topic: %s", got.Topic)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("pattern subscriber timeout")
	}
}

func TestPubSub_InvalidPattern(t *testing.T) {
	ps := New()
	defer ps.Close()

	_, err := ps.SubscribePattern("[", DefaultSubOptions())
	var e *Error
	if !errors.As(err, &e) || e.Code != ErrCodeInvalidPattern {
		t.Fatalf("expected ErrCodeInvalidPattern, got %v", err)
	}
}

func TestPubSub_MessageImmutability(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub1, err := ps.Subscribe(t.Context(), "topic", SubOptions{BufferSize: 4, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe sub1: %v", err)
	}
	sub2, err := ps.Subscribe(t.Context(), "topic", SubOptions{BufferSize: 4, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe sub2: %v", err)
	}

	payload := map[string]any{"count": 1}
	meta := map[string]string{"source": "test"}
	msg := Message{ID: "m1", Data: payload, Meta: meta}
	if err := ps.Publish("topic", msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Mutate original payload/meta after publish.
	payload["count"] = 99
	meta["source"] = "mutated"

	var got1 Message
	select {
	case got1 = <-sub1.C():
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("sub1 timeout")
	}

	data1, ok := got1.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map payload, got %T", got1.Data)
	}
	if data1["count"] != 1 {
		t.Fatalf("expected original payload, got %v", data1["count"])
	}
	if got1.Meta["source"] != "test" {
		t.Fatalf("expected original meta, got %v", got1.Meta["source"])
	}

	// Mutate sub1 payload; sub2 should be unaffected.
	data1["count"] = 2

	var got2 Message
	select {
	case got2 = <-sub2.C():
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("sub2 timeout")
	}

	data2, ok := got2.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map payload, got %T", got2.Data)
	}
	if data2["count"] != 1 {
		t.Fatalf("expected isolated payload, got %v", data2["count"])
	}
	if got2.Meta["source"] != "test" {
		t.Fatalf("expected isolated meta, got %v", got2.Meta["source"])
	}
}

func TestPublishBatchDoesNotMutateInputEnvelope(t *testing.T) {
	b := New()
	defer b.Close()
	sub, err := b.Subscribe(t.Context(), "batch", SubOptions{BufferSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()
	explicit := time.Unix(123, 0).UTC()
	messages := []Message{
		{ID: "one", Data: "sample"},
		{ID: "two", Topic: "original", Time: explicit},
	}
	if err := b.PublishBatch("batch", messages); err != nil {
		t.Fatal(err)
	}
	for index, original := range messages {
		select {
		case m := <-sub.C():
			if m.ID != original.ID || m.Topic != "batch" || m.Time.IsZero() {
				t.Errorf("delivery %d envelope = %+v", index, m)
			}
			if index == 1 && !m.Time.Equal(explicit) {
				t.Errorf("explicit time changed to %v", m.Time)
			}
		case <-time.After(time.Second):
			t.Fatal("no delivery")
		}
	}
	if messages[0].Topic != "" || !messages[0].Time.IsZero() ||
		messages[1].Topic != "original" || !messages[1].Time.Equal(explicit) {
		t.Error("publish mutated caller's message envelope")
	}
}

func TestPublishAsyncDeliveryAndClosedError(t *testing.T) {
	b := New()
	t.Cleanup(func() { _ = b.Close() })
	sub, err := b.Subscribe(t.Context(), "async", SubOptions{BufferSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()
	if err := b.PublishAsync("async", Message{ID: "async-item", Data: "value"}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-sub.C():
		if got.ID != "async-item" || got.Topic != "async" || got.Data != "value" {
			t.Errorf("received=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
	drainCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := b.Drain(drainCtx); err != nil {
		t.Fatal(err)
	}
	if got := sub.Stats().Received; got != 1 {
		t.Errorf("subscription received %d messages, want 1", got)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	err = b.PublishAsync("async", Message{ID: "after-close"})
	var closed *Error
	if !errors.As(err, &closed) || closed.Code != ErrCodeClosed {
		t.Errorf("closed publish = %v", err)
	}
}

func TestPublishMultiDeliveryAndErrors(t *testing.T) {
	b := New()
	t.Cleanup(func() { _ = b.Close() })
	channels := make(map[string]<-chan Message)
	for _, topic := range []string{"first", "second"} {
		sub, err := b.Subscribe(t.Context(), topic, SubOptions{BufferSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Cancel()
		channels[topic] = sub.C()
	}
	messages := map[string][]Message{"first": {{ID: "one"}}, "second": {{ID: "two"}}}
	if err := b.PublishMulti(messages); err != nil {
		t.Fatal(err)
	}
	for topic, ch := range channels {
		select {
		case msg := <-ch:
			src := messages[topic][0]
			if msg.Topic != topic || msg.ID != src.ID || msg.Time.IsZero() ||
				src.Topic != "" || !src.Time.IsZero() {
				t.Errorf("received=%+v, input=%+v", msg, src)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing delivery on %q", topic)
		}
	}
	if err := b.PublishMulti(map[string][]Message{" ": {{ID: "invalid"}}}); err == nil {
		t.Fatal("empty topic accepted")
	} else {
		var classified *Error
		if !errors.As(err, &classified) || classified.Code != ErrCodeInvalidTopic {
			t.Errorf("invalid topic error=%v", err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	var closed *Error
	if err := b.PublishMulti(messages); !errors.As(err, &closed) || closed.Code != ErrCodeClosed {
		t.Errorf("closed broker error=%v", err)
	}
}

func TestClosedBrokerHealthErrorsIdentifyCheck(t *testing.T) {
	b := New()
	defer b.Close()
	if ready, live := b.ReadinessCheck(), b.LivenessCheck(); ready != nil || live != nil {
		t.Fatalf("healthy broker readiness=%v, liveness=%v", ready, live)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for op, err := range map[string]error{"readiness": b.ReadinessCheck(), "liveness": b.LivenessCheck()} {
		var got *Error
		if !errors.As(err, &got) || got.Code != ErrCodeClosed || got.Op != op {
			t.Errorf("%s=%v", op, err)
		}
	}
}

func TestPatternSubscriptionInspectionTracksCancellation(t *testing.T) {
	b := New()
	defer b.Close()
	add := func(pattern string) Subscription {
		t.Helper()
		s, err := b.SubscribePattern(pattern, SubOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := add("catalog.*")
	second := add("catalog.*")
	other := add("assets.*")
	list := b.ListPatterns()
	if len(list) != 2 || list[0] != "assets.*" || list[1] != "catalog.*" || b.GetPatternSubscriberCount("catalog.*") != 2 {
		t.Fatalf("patterns=%v, catalog=%d", list, b.GetPatternSubscriberCount("catalog.*"))
	}
	list[0] = "modified"
	first.Cancel()
	if got := b.GetPatternSubscriberCount("catalog.*"); got != 1 {
		t.Errorf("count after first cancel=%d", got)
	}
	second.Cancel()
	if got := b.ListPatterns(); len(got) != 1 || got[0] != "assets.*" || b.GetPatternSubscriberCount("catalog.*") != 0 {
		t.Errorf("patterns after catalog cancel=%v", got)
	}
	other.Cancel()
	if got := b.ListPatterns(); len(got) != 0 {
		t.Errorf("patterns after all cancel=%v", got)
	}
}
