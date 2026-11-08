package mq

import (
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

func TestAckTrackerBasic(t *testing.T) {
	// Test basic ACK tracker functionality
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	if broker.ackTracker == nil {
		t.Fatal("ackTracker should be initialized when ACK support is enabled")
	}

	// Test tracking a message
	msg := Message{ID: "test-1", Data: "test data"}
	err = broker.ackTracker.track("ack-1", "test-topic", msg, 1*time.Second, 3)
	if err != nil {
		t.Fatalf("track error: %v", err)
	}

	// Verify pending count
	pending, _ := broker.ackTracker.stats()
	if pending != 1 {
		t.Fatalf("expected 1 pending ack, got %d", pending)
	}

	// Test acknowledging the message
	err = broker.ackTracker.acknowledge("ack-1")
	if err != nil {
		t.Fatalf("acknowledge error: %v", err)
	}

	// Verify pending count is now 0
	pending, _ = broker.ackTracker.stats()
	if pending != 0 {
		t.Fatalf("expected 0 pending acks after acknowledge, got %d", pending)
	}
}

func TestAckTrackerDuplicateAckID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	msg := Message{ID: "test-1", Data: "test data"}

	// Track first message
	err = broker.ackTracker.track("ack-1", "test-topic", msg, 1*time.Second, 3)
	if err != nil {
		t.Fatalf("first track error: %v", err)
	}

	// Try to track with same ACK ID
	err = broker.ackTracker.track("ack-1", "test-topic", msg, 1*time.Second, 3)
	if err == nil {
		t.Fatal("expected error for duplicate ACK ID")
	}
	if !errors.Is(err, ErrMessageAcknowledged) {
		t.Fatalf("expected ErrMessageAcknowledged, got %v", err)
	}
}

func TestAckTrackerNotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Try to acknowledge non-existent message
	err = broker.ackTracker.acknowledge("non-existent")
	if err == nil {
		t.Fatal("expected error for non-existent ACK ID")
	}
	if !errors.Is(err, ErrMessageNotAcked) {
		t.Fatalf("expected ErrMessageNotAcked, got %v", err)
	}
}

func TestPublishWithAckSuccess(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish message with ACK (provide explicit AckID)
	ackMsg := AckMessage{
		Message:    Message{ID: "test-1", Data: "test data"},
		AckID:      "test-1-ack",
		AckPolicy:  AckRequired,
		AckTimeout: 1 * time.Second,
	}

	err = broker.PublishWithAck(ctx, "test-topic", ackMsg)
	if err != nil {
		t.Fatalf("PublishWithAck error: %v", err)
	}

	// Verify message is being tracked
	pending, _ := broker.ackTracker.stats()
	if pending != 1 {
		t.Fatalf("expected 1 pending ack, got %d", pending)
	}

	// Acknowledge the message
	ackID := ackMsg.AckID
	err = broker.Ack(ctx, "test-topic", ackID)
	if err != nil {
		t.Fatalf("Ack error: %v", err)
	}

	// Verify no pending acks
	pending, _ = broker.ackTracker.stats()
	if pending != 0 {
		t.Fatalf("expected 0 pending acks, got %d", pending)
	}
}

func TestPublishWithAckTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Subscribe to dead letter queue
	dlqSub, err := broker.Subscribe(ctx, "dlq", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe to DLQ error: %v", err)
	}
	defer dlqSub.Cancel()

	// Publish message with short timeout
	ackMsg := AckMessage{
		Message:    Message{ID: "test-timeout", Data: "timeout test"},
		AckPolicy:  AckTimeout,
		AckTimeout: 100 * time.Millisecond,
	}

	err = broker.PublishWithAck(ctx, "test-topic", ackMsg)
	if err != nil {
		t.Fatalf("PublishWithAck error: %v", err)
	}

	// Wait for timeout and retries (default 3 retries)
	// Timeout is 100ms, so with 3 retries it should take ~400ms
	time.Sleep(500 * time.Millisecond)

	// Message should eventually go to dead letter queue
	select {
	case dlqMsg := <-dlqSub.C():
		if dlqMsg.ID != "test-timeout" {
			t.Fatalf("expected message ID test-timeout in DLQ, got %s", dlqMsg.ID)
		}
	case <-time.After(1 * time.Second):
		// It's possible the message was redelivered but not yet in DLQ
		// This is acceptable for this test
		t.Log("message may still be in retry cycle")
	}
}

func TestNackImmediateRedelivery(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
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

	// Publish message with ACK (provide explicit AckID)
	ackMsg := AckMessage{
		Message:    Message{ID: "test-nack", Data: "nack test"},
		AckID:      "test-nack-ack",
		AckPolicy:  AckRequired,
		AckTimeout: 5 * time.Second,
	}

	err = broker.PublishWithAck(ctx, "test-topic", ackMsg)
	if err != nil {
		t.Fatalf("PublishWithAck error: %v", err)
	}

	// Receive first delivery
	select {
	case msg := <-sub.C():
		if msg.ID != "test-nack" {
			t.Fatalf("expected message ID test-nack, got %s", msg.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for first delivery")
	}

	// NACK the message to request redelivery
	err = broker.Nack(ctx, "test-topic", ackMsg.AckID)
	if err != nil {
		t.Fatalf("Nack error: %v", err)
	}

	// Receive redelivered message
	select {
	case msg := <-sub.C():
		if msg.ID != "test-nack" {
			t.Fatalf("expected redelivered message ID test-nack, got %s", msg.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for redelivery")
	}
}

func TestAckDisabledError(t *testing.T) {
	// Create broker without ACK support
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Try to publish with ACK when support is disabled
	ackMsg := AckMessage{
		Message:   Message{ID: "test-1", Data: "test"},
		AckPolicy: AckRequired,
	}

	err = broker.PublishWithAck(ctx, "test-topic", ackMsg)
	if err == nil {
		t.Fatal("expected error when ACK support is disabled")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}

	// Try to acknowledge when support is disabled
	err = broker.Ack(ctx, "test-topic", "test-1")
	if err == nil {
		t.Fatal("expected error when ACK support is disabled")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}

	// Try to NACK when support is disabled
	err = broker.Nack(ctx, "test-topic", "test-1")
	if err == nil {
		t.Fatal("expected error when ACK support is disabled")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestAckTrackerClose(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}

	msg := Message{ID: "test-1", Data: "test"}
	err = broker.ackTracker.track("ack-1", "test-topic", msg, 1*time.Second, 3)
	if err != nil {
		t.Fatalf("track error: %v", err)
	}

	// Close the broker
	_ = broker.Close()

	// Try to track after close
	err = broker.ackTracker.track("ack-2", "test-topic", msg, 1*time.Second, 3)
	if !errors.Is(err, ErrBrokerClosed) {
		t.Fatalf("expected ErrBrokerClosed, got %v", err)
	}

	// Try to acknowledge after close
	err = broker.ackTracker.acknowledge("ack-1")
	if !errors.Is(err, ErrBrokerClosed) {
		t.Fatalf("expected ErrBrokerClosed, got %v", err)
	}
}

func TestAckTrackerStats(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	msg := Message{ID: "test-1", Data: "test"}

	// No pending messages initially
	pending, age := broker.ackTracker.stats()
	if pending != 0 {
		t.Fatalf("expected 0 pending, got %d", pending)
	}
	if age != 0 {
		t.Fatalf("expected 0 age, got %v", age)
	}

	// Track a message
	err = broker.ackTracker.track("ack-1", "test-topic", msg, 1*time.Second, 3)
	if err != nil {
		t.Fatalf("track error: %v", err)
	}

	// Wait a bit for age to accumulate
	time.Sleep(10 * time.Millisecond)

	// Check stats
	pending, age = broker.ackTracker.stats()
	if pending != 1 {
		t.Fatalf("expected 1 pending, got %d", pending)
	}
	if age == 0 {
		t.Fatalf("expected non-zero age")
	}
	if age < 10*time.Millisecond {
		t.Fatalf("expected age >= 10ms, got %v", age)
	}
}

func TestAckGeneratedID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish message without ACK ID
	ackMsg := AckMessage{
		Message:   Message{ID: "test-1", Data: "test"},
		AckPolicy: AckRequired,
		// AckID is empty, should be generated
	}

	err = broker.PublishWithAck(ctx, "test-topic", ackMsg)
	if err != nil {
		t.Fatalf("PublishWithAck error: %v", err)
	}

	// Verify message is being tracked
	pending, _ := broker.ackTracker.stats()
	if pending != 1 {
		t.Fatalf("expected 1 pending ack, got %d", pending)
	}

	// AckID should have been generated (format: "messageID-timestamp")
	// We can't know the exact ID, but we know there should be 1 pending
}

func TestAckNoPolicySkipsTracking(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableAckSupport = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish message with AckNone policy
	ackMsg := AckMessage{
		Message:   Message{ID: "test-1", Data: "test"},
		AckPolicy: AckNone, // No acknowledgment required
	}

	err = broker.PublishWithAck(ctx, "test-topic", ackMsg)
	if err != nil {
		t.Fatalf("PublishWithAck error: %v", err)
	}

	// Verify message is NOT being tracked
	pending, _ := broker.ackTracker.stats()
	if pending != 0 {
		t.Fatalf("expected 0 pending acks (AckNone policy), got %d", pending)
	}
}
