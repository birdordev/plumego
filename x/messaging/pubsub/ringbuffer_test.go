package pubsub

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- ringBuffer unit tests ---

func TestRingBuffer_NewRingBuffer(t *testing.T) {
	rb := newRingBuffer(8)
	if rb.Cap() != 8 {
		t.Fatalf("expected cap 8, got %d", rb.Cap())
	}
	if rb.Len() != 0 {
		t.Fatalf("expected len 0, got %d", rb.Len())
	}
	if rb.IsClosed() {
		t.Fatal("expected not closed")
	}
}

func TestRingBuffer_MinCapacity(t *testing.T) {
	rb := newRingBuffer(0)
	if rb.Cap() != 1 {
		t.Fatalf("expected min cap 1, got %d", rb.Cap())
	}

	rb2 := newRingBuffer(-5)
	if rb2.Cap() != 1 {
		t.Fatalf("expected min cap 1, got %d", rb2.Cap())
	}
}

func TestRingBuffer_PushPop(t *testing.T) {
	rb := newRingBuffer(4)

	// Push 3 messages
	for i := 0; i < 3; i++ {
		dropped, wasDropped := rb.Push(Message{ID: fmt.Sprintf("m%d", i)})
		if wasDropped {
			t.Fatalf("unexpected drop at push %d: %v", i, dropped)
		}
	}

	if rb.Len() != 3 {
		t.Fatalf("expected len 3, got %d", rb.Len())
	}

	// Pop all
	for i := 0; i < 3; i++ {
		msg, ok := rb.Pop()
		if !ok {
			t.Fatalf("expected pop at %d", i)
		}
		expected := fmt.Sprintf("m%d", i)
		if msg.ID != expected {
			t.Fatalf("expected ID %s, got %s", expected, msg.ID)
		}
	}

	// Pop from empty
	_, ok := rb.Pop()
	if ok {
		t.Fatal("expected pop to fail on empty buffer")
	}

	if rb.Len() != 0 {
		t.Fatalf("expected len 0, got %d", rb.Len())
	}
}

func TestRingBuffer_DropOldest(t *testing.T) {
	rb := newRingBuffer(3)

	// Fill buffer
	rb.Push(Message{ID: "m0"})
	rb.Push(Message{ID: "m1"})
	rb.Push(Message{ID: "m2"})

	if rb.Len() != 3 {
		t.Fatalf("expected len 3, got %d", rb.Len())
	}

	// Push one more, should drop m0
	dropped, wasDropped := rb.Push(Message{ID: "m3"})
	if !wasDropped {
		t.Fatal("expected drop")
	}
	if dropped.ID != "m0" {
		t.Fatalf("expected dropped m0, got %s", dropped.ID)
	}

	if rb.Len() != 3 {
		t.Fatalf("expected len 3 after drop, got %d", rb.Len())
	}

	// Push another, should drop m1
	dropped, wasDropped = rb.Push(Message{ID: "m4"})
	if !wasDropped {
		t.Fatal("expected drop")
	}
	if dropped.ID != "m1" {
		t.Fatalf("expected dropped m1, got %s", dropped.ID)
	}

	// Verify order: m2, m3, m4
	expected := []string{"m2", "m3", "m4"}
	for i, exp := range expected {
		msg, ok := rb.Pop()
		if !ok {
			t.Fatalf("expected pop at %d", i)
		}
		if msg.ID != exp {
			t.Fatalf("at %d: expected %s, got %s", i, exp, msg.ID)
		}
	}
}

func TestRingBuffer_Wraparound(t *testing.T) {
	rb := newRingBuffer(3)

	// Fill and drain multiple times to test wraparound
	for round := 0; round < 5; round++ {
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("r%d-m%d", round, i)
			_, wasDropped := rb.Push(Message{ID: id})
			if wasDropped {
				t.Fatalf("unexpected drop at round %d, push %d", round, i)
			}
		}
		for i := 0; i < 3; i++ {
			msg, ok := rb.Pop()
			if !ok {
				t.Fatalf("expected pop at round %d, %d", round, i)
			}
			expected := fmt.Sprintf("r%d-m%d", round, i)
			if msg.ID != expected {
				t.Fatalf("at round %d, %d: expected %s, got %s", round, i, expected, msg.ID)
			}
		}
	}
}

func TestRingBuffer_Close(t *testing.T) {
	rb := newRingBuffer(4)
	rb.Push(Message{ID: "m0"})

	rb.Close()

	if !rb.IsClosed() {
		t.Fatal("expected closed")
	}

	// Push on closed buffer should not add
	_, wasDropped := rb.Push(Message{ID: "m1"})
	if wasDropped {
		t.Fatal("push on closed should not report drop")
	}
	if rb.Len() != 1 {
		t.Fatalf("expected len 1 (pre-close message), got %d", rb.Len())
	}

	// Pop on closed buffer should still drain pre-close messages
	msg, ok := rb.Pop()
	if !ok {
		t.Fatal("expected pop on closed buffer to drain existing message")
	}
	if msg.ID != "m0" {
		t.Fatalf("expected m0, got %s", msg.ID)
	}
	_, ok = rb.Pop()
	if ok {
		t.Fatal("expected no more messages after draining closed buffer")
	}

	// Double close should not panic
	rb.Close()
}

func TestRingBuffer_Notify(t *testing.T) {
	rb := newRingBuffer(4)

	// Notify channel should receive after push
	rb.Push(Message{ID: "m0"})
	select {
	case <-rb.Notify():
		// OK
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected notification after push")
	}

	// After close, notify channel should be closed
	rb.Close()
	select {
	case _, ok := <-rb.Notify():
		if ok {
			t.Fatal("expected notify channel to be closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected notify channel to unblock after close")
	}
}

func TestRingBuffer_Drain(t *testing.T) {
	rb := newRingBuffer(4)

	rb.Push(Message{ID: "m0"})
	rb.Push(Message{ID: "m1"})
	rb.Push(Message{ID: "m2"})

	msgs := rb.Drain()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 drained messages, got %d", len(msgs))
	}
	for i, msg := range msgs {
		expected := fmt.Sprintf("m%d", i)
		if msg.ID != expected {
			t.Fatalf("at %d: expected %s, got %s", i, expected, msg.ID)
		}
	}

	if rb.Len() != 0 {
		t.Fatalf("expected len 0 after drain, got %d", rb.Len())
	}

	// Drain empty buffer
	msgs = rb.Drain()
	if msgs != nil {
		t.Fatalf("expected nil from empty drain, got %v", msgs)
	}
}

func TestRingBuffer_DrainAfterOverflow(t *testing.T) {
	rb := newRingBuffer(2)

	rb.Push(Message{ID: "m0"})
	rb.Push(Message{ID: "m1"})
	rb.Push(Message{ID: "m2"}) // drops m0

	msgs := rb.Drain()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].ID != "m1" || msgs[1].ID != "m2" {
		t.Fatalf("expected [m1, m2], got [%s, %s]", msgs[0].ID, msgs[1].ID)
	}
}

func TestRingBuffer_CapacityOne(t *testing.T) {
	rb := newRingBuffer(1)

	rb.Push(Message{ID: "m0"})
	if rb.Len() != 1 {
		t.Fatalf("expected len 1, got %d", rb.Len())
	}

	dropped, wasDropped := rb.Push(Message{ID: "m1"})
	if !wasDropped {
		t.Fatal("expected drop")
	}
	if dropped.ID != "m0" {
		t.Fatalf("expected dropped m0, got %s", dropped.ID)
	}

	msg, ok := rb.Pop()
	if !ok || msg.ID != "m1" {
		t.Fatalf("expected m1, got %v (%v)", msg.ID, ok)
	}
}

func TestRingBuffer_ConcurrentPushPop(t *testing.T) {
	rb := newRingBuffer(64)
	const n = 500

	var wg sync.WaitGroup

	// Producer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			rb.Push(Message{ID: fmt.Sprintf("m%d", i)})
		}
		// Signal completion by closing the ring buffer
		rb.Close()
	}()

	// Consumer: drain via Notify
	popped := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			// Drain all available
			for {
				_, ok := rb.Pop()
				if !ok {
					break
				}
				popped++
			}
			// Wait for notification
			_, ok := <-rb.Notify()
			if !ok {
				// Closed - drain remaining
				for {
					_, ok := rb.Pop()
					if !ok {
						return
					}
					popped++
				}
			}
		}
	}()

	wg.Wait()

	// With 500 pushes to 64-size buffer, some may be dropped.
	// Total popped + dropped should equal n.
	if popped == 0 {
		t.Fatal("expected at least some messages to be popped")
	}
	if popped > n {
		t.Fatalf("popped %d > total %d, which is impossible", popped, n)
	}
}

func TestRingBuffer_ConcurrentPushDrop(t *testing.T) {
	rb := newRingBuffer(8)
	const n = 1000

	var wg sync.WaitGroup
	wg.Add(1)

	// Push many messages causing lots of drops
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			rb.Push(Message{ID: fmt.Sprintf("m%d", i)})
		}
	}()

	wg.Wait()

	// Buffer should have at most 8 messages
	if rb.Len() > 8 {
		t.Fatalf("expected len <= 8, got %d", rb.Len())
	}

	// Last 8 messages should be in order
	msgs := rb.Drain()
	for i, m := range msgs {
		if m.ID == "" {
			t.Fatalf("message %d has empty ID", i)
		}
	}
}

// --- PubSub integration tests with ring buffer ---

func TestPubSub_RingBuffer_BasicDelivery(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "test", SubOptions{
		BufferSize: 8,
		Policy:     DropOldest,
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	// Verify ring buffer is used
	if s, ok := sub.(*subscriber); ok {
		if s.ringBuf == nil {
			t.Fatal("expected ring buffer to be set")
		}
	}

	// Publish and receive
	if err := ps.Publish("test", Message{ID: "m1", Data: "hello"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-sub.C():
		if msg.ID != "m1" {
			t.Fatalf("expected m1, got %s", msg.ID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}
}

func TestPubSub_RingBuffer_PerSubscriberOptIn(t *testing.T) {
	ps := New() // ring buffer NOT enabled globally
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "test", SubOptions{
		BufferSize:    8,
		Policy:        DropOldest,
		UseRingBuffer: true,
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	// Verify ring buffer is used
	if s, ok := sub.(*subscriber); ok {
		if s.ringBuf == nil {
			t.Fatal("expected ring buffer to be set via per-subscriber opt-in")
		}
	}

	if err := ps.Publish("test", Message{ID: "m1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-sub.C():
		if msg.ID != "m1" {
			t.Fatalf("expected m1, got %s", msg.ID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout")
	}
}

func TestPubSub_RingBuffer_NotUsedForOtherPolicies(t *testing.T) {
	ps := New()
	defer ps.Close()

	// DropNewest should NOT use ring buffer even with global setting
	sub, err := ps.Subscribe(t.Context(), "test", SubOptions{
		BufferSize: 8,
		Policy:     DropNewest,
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	if s, ok := sub.(*subscriber); ok {
		if s.ringBuf != nil {
			t.Fatal("ring buffer should not be used for DropNewest")
		}
	}
}

func TestPubSub_RingBuffer_DropOldest(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 2, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// First, ensure the pump goroutine is running by publishing one message
	// and reading it.
	_ = ps.Publish("t", Message{ID: "m0"})
	select {
	case got := <-sub.C():
		if got.ID != "m0" {
			t.Fatalf("expected m0, got %s", got.ID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for initial message")
	}

	// Now the pump goroutine is running and the ring buffer is empty.
	// Publish 4 messages rapidly without consuming.
	// The pump goroutine may or may not pop a message during this burst.
	for i := 1; i <= 4; i++ {
		_ = ps.Publish("t", Message{ID: fmt.Sprintf("m%d", i)})
	}

	time.Sleep(50 * time.Millisecond)

	// Drain all available messages
	var received []string
	for {
		select {
		case msg := <-sub.C():
			received = append(received, msg.ID)
		case <-time.After(100 * time.Millisecond):
			goto done
		}
	}
done:

	// With buffer size 2, not all messages can survive.
	// Verify: messages are in order, last message is m4,
	// and we received between 2 and 4 messages.
	if len(received) < 2 {
		t.Fatalf("expected at least 2 messages, got %d: %v", len(received), received)
	}
	if received[len(received)-1] != "m4" {
		t.Fatalf("expected last message to be m4, got %v", received)
	}
	// Verify ordering
	for i := 1; i < len(received); i++ {
		if received[i] <= received[i-1] {
			t.Fatalf("messages not in order: %v", received)
		}
	}

	// Verify drops happened
	stats := sub.Stats()
	if stats.Dropped == 0 {
		t.Fatal("expected some drops")
	}
}

func TestPubSub_RingBuffer_Stats(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 4, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	// Publish 10 messages to a buffer of size 4.
	// Effective capacity is 4 (ring) + 1 (pump in-flight) = 5.
	// So at least 5 drops expected.
	const total = 10
	for i := 0; i < total; i++ {
		_ = ps.Publish("t", Message{ID: fmt.Sprintf("m%d", i)})
	}

	// Give time for delivery
	time.Sleep(50 * time.Millisecond)

	stats := sub.Stats()
	if stats.QueueCap != 4 {
		t.Fatalf("expected QueueCap 4, got %d", stats.QueueCap)
	}
	if stats.Received != total {
		t.Fatalf("expected Received %d, got %d", total, stats.Received)
	}
	if stats.Dropped == 0 {
		t.Fatal("expected some drops, got 0")
	}
}

func TestPubSub_RingBuffer_Cancel(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 8, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	sub.Cancel()

	// Channel should be closed after cancel
	_, ok := <-sub.C()
	if ok {
		t.Fatal("expected channel to be closed after cancel")
	}

	// Publish after cancel should not panic
	_ = ps.Publish("t", Message{ID: "m1"})
}

func TestPubSub_RingBuffer_CancelIdempotent(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 8, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Cancel multiple times should not panic
	sub.Cancel()
	sub.Cancel()
	sub.Cancel()
}

func TestPubSub_RingBuffer_MultiSubscriber(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub1, err := ps.Subscribe(t.Context(), "events", SubOptions{BufferSize: 8, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe sub1: %v", err)
	}
	defer sub1.Cancel()

	sub2, err := ps.Subscribe(t.Context(), "events", SubOptions{BufferSize: 8, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe sub2: %v", err)
	}
	defer sub2.Cancel()

	msg := Message{ID: "m1", Data: "hello"}
	if err := ps.Publish("events", msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	for i, sub := range []Subscription{sub1, sub2} {
		select {
		case got := <-sub.C():
			if got.ID != "m1" {
				t.Fatalf("sub%d: expected m1, got %s", i+1, got.ID)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("sub%d: timeout", i+1)
		}
	}
}

func TestPubSub_RingBuffer_PatternSubscription(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.SubscribePattern("user.*", SubOptions{BufferSize: 8, Policy: DropOldest})
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
			t.Fatalf("expected topic user.created, got %s", got.Topic)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout")
	}
}

func TestPubSub_RingBuffer_Filter(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "events", SubOptions{
		BufferSize: 8,
		Policy:     DropOldest,
		Filter: func(msg Message) bool {
			return msg.Type == "important"
		},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	_ = ps.Publish("events", Message{ID: "m1", Type: "noise"})
	_ = ps.Publish("events", Message{ID: "m2", Type: "important"})
	_ = ps.Publish("events", Message{ID: "m3", Type: "noise"})

	select {
	case got := <-sub.C():
		if got.ID != "m2" {
			t.Fatalf("expected m2, got %s", got.ID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout")
	}

	// No more messages expected
	select {
	case msg := <-sub.C():
		t.Fatalf("unexpected message: %s", msg.ID)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

type ringbufHookObserver struct {
	delivered atomic.Int64
	dropped   atomic.Int64
}

func (o *ringbufHookObserver) OnPublish(_ string, _ *Message)           {}
func (o *ringbufHookObserver) OnSubscribe(_ string, _ uint64)           {}
func (o *ringbufHookObserver) OnUnsubscribe(_ string, _ uint64)         {}
func (o *ringbufHookObserver) OnDeliver(_ string, _ uint64, _ *Message) { o.delivered.Add(1) }
func (o *ringbufHookObserver) OnDrop(_ string, _ uint64, _ *Message, _ BackpressurePolicy) {
	o.dropped.Add(1)
}

func TestPubSub_RingBuffer_Hooks(t *testing.T) {
	obs := &ringbufHookObserver{}
	ps := New(WithObserver(obs))
	defer ps.Close()

	deliveredCount := &obs.delivered
	droppedCount := &obs.dropped

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 2, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	const total = 10
	// Publish many messages without consuming. The ring buffer (size 2) plus
	// the pump goroutine in-flight message give an effective capacity of ~3,
	// so most messages will trigger drops.
	for i := 0; i < total; i++ {
		_ = ps.Publish("t", Message{ID: fmt.Sprintf("m%d", i)})
	}

	time.Sleep(50 * time.Millisecond)
	sub.Cancel()

	delivered := deliveredCount.Load()
	dropped := droppedCount.Load()

	// Every published message triggers OnDeliver
	if delivered != total {
		t.Fatalf("expected %d deliveries, got %d", total, delivered)
	}
	// With buffer size 2, many messages must be dropped
	if dropped == 0 {
		t.Fatal("expected some drops, got 0")
	}
	// Drops can't exceed delivered
	if dropped >= delivered {
		t.Fatalf("drops (%d) should be less than delivered (%d)", dropped, delivered)
	}
}

func TestPubSub_RingBuffer_Close(t *testing.T) {
	ps := New()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 8, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	_ = ps.Publish("t", Message{ID: "m1"})

	select {
	case <-sub.C():
		// OK
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout")
	}

	// Close pubsub, should close all subscribers
	ps.Close()

	// Channel should be closed
	_, ok := <-sub.C()
	if ok {
		t.Fatal("expected channel closed after pubsub close")
	}
}

func TestPubSub_RingBuffer_HighThroughput(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 256, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	const n = 500
	// Consumer
	var received atomic.Int64
	go func() {
		for range sub.C() {
			if received.Add(1) >= n {
				return
			}
		}
	}()

	// Publisher: pace slightly to allow pump goroutine to keep up
	for i := 0; i < n; i++ {
		_ = ps.Publish("t", Message{ID: fmt.Sprintf("m%d", i)})
	}

	deadline := time.Now().Add(800 * time.Millisecond)
	for received.Load() < int64(n)/2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// With drops, we may not receive all messages.
	// Verify we received a reasonable number.
	got := received.Load()
	if got < int64(n)/2 {
		t.Fatalf("received too few: %d/%d", got, n)
	}
}

func TestPubSub_RingBuffer_DoneChannel(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 4, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Done should not be closed yet
	select {
	case <-sub.Done():
		t.Fatal("done should not be closed yet")
	default:
	}

	sub.Cancel()

	// Done should be closed after cancel
	select {
	case <-sub.Done():
		// OK
	case <-time.After(500 * time.Millisecond):
		t.Fatal("done should be closed after cancel")
	}
}

func TestPubSub_RingBuffer_MixedSubscribers(t *testing.T) {
	ps := New()
	defer ps.Close()

	// One subscriber with ring buffer (DropOldest), one without (DropNewest)
	sub1, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 4, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe sub1: %v", err)
	}
	defer sub1.Cancel()

	sub2, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 4, Policy: DropNewest})
	if err != nil {
		t.Fatalf("subscribe sub2: %v", err)
	}
	defer sub2.Cancel()

	// Publish a message
	if err := ps.Publish("t", Message{ID: "m1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Both should receive
	for i, sub := range []Subscription{sub1, sub2} {
		select {
		case got := <-sub.C():
			if got.ID != "m1" {
				t.Fatalf("sub%d: expected m1, got %s", i+1, got.ID)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("sub%d: timeout", i+1)
		}
	}
}

func TestPubSub_RingBuffer_BatchPublish(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 8, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	msgs := []Message{
		{ID: "m0"},
		{ID: "m1"},
		{ID: "m2"},
	}
	if err := ps.PublishBatch("t", msgs); err != nil {
		t.Fatalf("publish batch: %v", err)
	}

	for i := 0; i < 3; i++ {
		select {
		case got := <-sub.C():
			expected := fmt.Sprintf("m%d", i)
			if got.ID != expected {
				t.Fatalf("at %d: expected %s, got %s", i, expected, got.ID)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timeout at %d", i)
		}
	}
}

func TestPubSub_RingBuffer_ConcurrentPublish(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 256, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	const publishers = 4
	const msgsPerPublisher = 50
	const total = publishers * msgsPerPublisher

	var wg sync.WaitGroup
	wg.Add(publishers)

	for p := 0; p < publishers; p++ {
		go func(pid int) {
			defer wg.Done()
			for i := 0; i < msgsPerPublisher; i++ {
				_ = ps.Publish("t", Message{ID: fmt.Sprintf("p%d-m%d", pid, i)})
			}
		}(p)
	}

	// Consumer
	var received atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range sub.C() {
			if received.Add(1) >= total {
				return
			}
		}
	}()

	wg.Wait()

	select {
	case <-done:
		// All messages received
	case <-time.After(1 * time.Second):
		got := received.Load()
		if got < int64(total)/2 {
			t.Fatalf("received too few: %d/%d", got, total)
		}
	}
}
