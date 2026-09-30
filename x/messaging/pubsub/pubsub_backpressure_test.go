package pubsub

import (
	"testing"
	"time"
)

func TestBackpressure_DropOldest(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 2, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Warm the pump goroutine so DropOldest semantics are exercised through the
	// ring buffer path rather than assuming a plain buffered channel.
	_ = ps.Publish("t", Message{ID: "m0"})
	select {
	case got := <-sub.C():
		if got.ID != "m0" {
			t.Fatalf("expected m0, got %s", got.ID)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for initial message")
	}

	for i := 1; i <= 4; i++ {
		_ = ps.Publish("t", Message{ID: "m" + string(rune('0'+i))})
	}

	time.Sleep(50 * time.Millisecond)

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
	if len(received) < 2 {
		t.Fatalf("expected at least 2 messages, got %d: %v", len(received), received)
	}
	if received[len(received)-1] != "m4" {
		t.Fatalf("expected last message to be m4, got %v", received)
	}
	for i := 1; i < len(received); i++ {
		if received[i] <= received[i-1] {
			t.Fatalf("messages not in order: %v", received)
		}
	}
	if stats := sub.Stats(); stats.Dropped == 0 {
		t.Fatalf("expected drops for drop-oldest policy, got stats=%+v", stats)
	}
}

func TestBackpressure_DropNewest(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 1, Policy: DropNewest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	_ = ps.Publish("t", Message{ID: "m1"})
	_ = ps.Publish("t", Message{ID: "m2"}) // should be dropped

	got := <-sub.C()
	if got.ID != "m1" {
		t.Fatalf("expected m1, got %s", got.ID)
	}
}

func TestBackpressure_BlockWithTimeout(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 1, Policy: BlockWithTimeout, BlockTimeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	_ = ps.Publish("t", Message{ID: "m1"})
	// buffer full, publish should not block too long; it may drop
	start := time.Now()
	_ = ps.Publish("t", Message{ID: "m2"})
	if time.Since(start) > 120*time.Millisecond {
		t.Fatalf("publish blocked too long")
	}

	<-sub.C() // drain m1
}

func TestBackpressure_CloseSubscriber(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 1, Policy: CloseSubscriber})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	_ = ps.Publish("t", Message{ID: "m1"})
	_ = ps.Publish("t", Message{ID: "m2"}) // should close subscriber if full

	// Drain first
	<-sub.C()

	// Channel should eventually be closed
	select {
	case _, ok := <-sub.C():
		if ok {
			// could still have a buffered value; try next read
			_, ok2 := <-sub.C()
			if ok2 {
				t.Fatalf("expected closed")
			}
		}
	case <-time.After(200 * time.Millisecond):
		// some timings might not close immediately; this should be rare.
		t.Fatalf("expected subscriber to close")
	}
}
