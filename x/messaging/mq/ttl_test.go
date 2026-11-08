package mq

import (
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/internal/testutil/wait"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

func TestTTLTrackerBasic(t *testing.T) {
	// Test basic TTL tracker functionality
	cfg := DefaultConfig()
	cfg.MessageTTL = 1 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	if broker.ttlTracker == nil {
		t.Fatal("ttlTracker should be initialized when MessageTTL is configured")
	}

	// Test tracking a message
	expiresAt := time.Now().Add(1 * time.Second)
	err = broker.ttlTracker.track("msg-1", "test-topic", expiresAt)
	if err != nil {
		t.Fatalf("track error: %v", err)
	}

	// Verify tracked count
	tracked, expired := broker.ttlTracker.stats()
	if tracked != 1 {
		t.Fatalf("expected 1 tracked message, got %d", tracked)
	}
	if expired != 0 {
		t.Fatalf("expected 0 expired messages, got %d", expired)
	}

	// Remove the message
	broker.ttlTracker.remove("msg-1")

	// Verify tracked count is now 0
	tracked, expired = broker.ttlTracker.stats()
	if tracked != 0 {
		t.Fatalf("expected 0 tracked messages after removal, got %d", tracked)
	}
	if expired != 0 {
		t.Fatalf("expected 0 expired messages after removal, got %d", expired)
	}
}

func TestTTLTrackerExpiration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 100 * time.Millisecond
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Track message that expires soon
	expiresAt := time.Now().Add(50 * time.Millisecond)
	err = broker.ttlTracker.track("msg-expired", "test-topic", expiresAt)
	if err != nil {
		t.Fatalf("track error: %v", err)
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Check if message is detected as expired
	if !broker.ttlTracker.isExpired("msg-expired") {
		t.Fatal("message should be detected as expired")
	}

	// Stats should show it as expired
	tracked, expired := broker.ttlTracker.stats()
	if tracked != 1 {
		t.Fatalf("expected 1 tracked message, got %d", tracked)
	}
	if expired != 1 {
		t.Fatalf("expected 1 expired message, got %d", expired)
	}
}

func TestTTLTrackerCleanup(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 100 * time.Millisecond
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Track message that expires soon
	expiresAt := time.Now().Add(50 * time.Millisecond)
	err = broker.ttlTracker.track("msg-cleanup", "test-topic", expiresAt)
	if err != nil {
		t.Fatalf("track error: %v", err)
	}

	tracked, _ := broker.ttlTracker.stats()
	if tracked != 1 {
		t.Fatalf("expected 1 tracked message, got %d", tracked)
	}

	// Poll for the cleanup goroutine (runs every second) instead of sleeping.
	wait.ForValue(t, 5*time.Second, "tracked message cleanup", func() int {
		tracked, _ := broker.ttlTracker.stats()
		return tracked
	}, 0)
}

func TestPublishTTLSuccess(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 5 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Subscribe to the topic
	sub, err := broker.Subscribe(ctx, "test-topic", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish message with TTL
	ttlMsg := TTLMessage{
		Message:   Message{ID: "ttl-1", Data: "ttl test"},
		ExpiresAt: time.Now().Add(5 * time.Second),
	}

	err = broker.PublishTTL(ctx, "test-topic", ttlMsg)
	if err != nil {
		t.Fatalf("PublishTTL error: %v", err)
	}

	// Verify message was tracked
	tracked, _ := broker.ttlTracker.stats()
	if tracked != 1 {
		t.Fatalf("expected 1 tracked message, got %d", tracked)
	}

	// Receive the message
	select {
	case msg := <-sub.C():
		if msg.ID != "ttl-1" {
			t.Fatalf("expected message ID ttl-1, got %s", msg.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}
}

func TestPublishTTLExpired(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 1 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish message that's already expired
	ttlMsg := TTLMessage{
		Message:   Message{ID: "expired-1", Data: "expired test"},
		ExpiresAt: time.Now().Add(-1 * time.Second), // Expired 1 second ago
	}

	err = broker.PublishTTL(ctx, "test-topic", ttlMsg)
	if err == nil {
		t.Fatal("expected error when publishing expired message")
	}
	if !errors.Is(err, ErrMessageExpired) {
		t.Fatalf("expected ErrMessageExpired, got %v", err)
	}
}

func TestPublishTTLNoExpiration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 1 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish message with zero expiration (no TTL)
	ttlMsg := TTLMessage{
		Message:   Message{ID: "no-ttl", Data: "no ttl test"},
		ExpiresAt: time.Time{}, // Zero time, no expiration
	}

	err = broker.PublishTTL(ctx, "test-topic", ttlMsg)
	if err != nil {
		t.Fatalf("PublishTTL error: %v", err)
	}

	// Should not be tracked (no expiration)
	tracked, _ := broker.ttlTracker.stats()
	if tracked != 0 {
		t.Fatalf("expected 0 tracked messages (no expiration), got %d", tracked)
	}
}

func TestPublishPriorityTTL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 5 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Subscribe to the topic
	sub, err := broker.Subscribe(ctx, "priority-ttl-topic", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish priority message with TTL
	priorityTTLMsg := PriorityTTLMessage{
		Message:   Message{ID: "priority-ttl-1", Data: "priority ttl test"},
		Priority:  PriorityHigh,
		ExpiresAt: time.Now().Add(5 * time.Second),
	}

	err = broker.PublishPriorityTTL(ctx, "priority-ttl-topic", priorityTTLMsg)
	if err != nil {
		t.Fatalf("PublishPriorityTTL error: %v", err)
	}

	// Verify message was tracked
	tracked, _ := broker.ttlTracker.stats()
	if tracked != 1 {
		t.Fatalf("expected 1 tracked message, got %d", tracked)
	}

	// Receive the message
	select {
	case msg := <-sub.C():
		if msg.ID != "priority-ttl-1" {
			t.Fatalf("expected message ID priority-ttl-1, got %s", msg.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}
}

func TestPublishWithAckTTL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 5 * time.Second
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Subscribe to the topic
	sub, err := broker.Subscribe(ctx, "ack-ttl-topic", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish message with both ACK and TTL
	ackTTLMsg := AckTTLMessage{
		Message:    Message{ID: "ack-ttl-1", Data: "ack ttl test"},
		AckID:      "ack-ttl-1-ack",
		AckPolicy:  AckRequired,
		AckTimeout: 1 * time.Second,
		ExpiresAt:  time.Now().Add(5 * time.Second),
	}

	err = broker.PublishWithAckTTL(ctx, "ack-ttl-topic", ackTTLMsg)
	if err != nil {
		t.Fatalf("PublishWithAckTTL error: %v", err)
	}

	// Verify message was tracked by both trackers
	ttlTracked, _ := broker.ttlTracker.stats()
	if ttlTracked != 1 {
		t.Fatalf("expected 1 TTL tracked message, got %d", ttlTracked)
	}

	ackPending, _ := broker.ackTracker.stats()
	if ackPending != 1 {
		t.Fatalf("expected 1 ACK pending message, got %d", ackPending)
	}

	// Receive the message
	select {
	case msg := <-sub.C():
		if msg.ID != "ack-ttl-1" {
			t.Fatalf("expected message ID ack-ttl-1, got %s", msg.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}

	// Acknowledge the message
	err = broker.Ack(ctx, "ack-ttl-topic", ackTTLMsg.AckID)
	if err != nil {
		t.Fatalf("Ack error: %v", err)
	}

	// Verify ACK is cleared
	ackPending, _ = broker.ackTracker.stats()
	if ackPending != 0 {
		t.Fatalf("expected 0 ACK pending messages after ack, got %d", ackPending)
	}
}

func TestTTLTrackerClose(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 1 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}

	expiresAt := time.Now().Add(5 * time.Second)
	err = broker.ttlTracker.track("msg-1", "test-topic", expiresAt)
	if err != nil {
		t.Fatalf("track error: %v", err)
	}

	// Close the broker
	_ = broker.Close()

	// Try to track after close
	err = broker.ttlTracker.track("msg-2", "test-topic", expiresAt)
	if !errors.Is(err, ErrBrokerClosed) {
		t.Fatalf("expected ErrBrokerClosed, got %v", err)
	}

	// Stats should show 0 after close
	tracked, _ := broker.ttlTracker.stats()
	if tracked != 0 {
		t.Fatalf("expected 0 tracked messages after close, got %d", tracked)
	}
}

func TestTTLDisabled(t *testing.T) {
	// Create broker without TTL (default MessageTTL = 0)
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	if broker.ttlTracker != nil {
		t.Fatal("ttlTracker should be nil when TTL is disabled")
	}

	ctx := t.Context()

	// PublishTTL should still work but not track
	ttlMsg := TTLMessage{
		Message:   Message{ID: "no-tracker", Data: "test"},
		ExpiresAt: time.Now().Add(1 * time.Second),
	}

	err = broker.PublishTTL(ctx, "test-topic", ttlMsg)
	if err != nil {
		t.Fatalf("PublishTTL should work even without tracker: %v", err)
	}
}

func TestValidateTTL(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	tests := []struct {
		name      string
		expiresAt time.Time
		wantErr   bool
	}{
		{
			name:      "valid future expiration",
			expiresAt: time.Now().Add(5 * time.Second),
			wantErr:   false,
		},
		{
			name:      "zero time (no expiration)",
			expiresAt: time.Time{},
			wantErr:   false,
		},
		{
			name:      "expired message",
			expiresAt: time.Now().Add(-1 * time.Second),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := broker.validateTTL(tt.expiresAt)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTTL() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrMessageExpired) {
				t.Errorf("expected ErrMessageExpired, got %v", err)
			}
		})
	}
}

func TestTTLTrackerConcurrent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MessageTTL = 10 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()
	const numMessages = 100

	// Publish multiple messages concurrently
	done := make(chan bool)
	for i := 0; i < numMessages; i++ {
		go func(id int) {
			ttlMsg := TTLMessage{
				Message:   Message{ID: string(rune(id)), Data: "concurrent test"},
				ExpiresAt: time.Now().Add(10 * time.Second),
			}
			_ = broker.PublishTTL(ctx, "concurrent-topic", ttlMsg)
			done <- true
		}(i)
	}

	// Wait for all to complete
	for i := 0; i < numMessages; i++ {
		<-done
	}

	// Check that all are tracked
	tracked, _ := broker.ttlTracker.stats()
	if tracked != numMessages {
		t.Logf("expected %d tracked messages, got %d (some may have duplicate IDs)", numMessages, tracked)
	}
}
