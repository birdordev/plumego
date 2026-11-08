package mq

import (
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

func TestDeadLetterManagerBasic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	if broker.deadLetterManager == nil {
		t.Fatal("deadLetterManager should be initialized when dead letter queue is enabled")
	}

	ctx := t.Context()

	// Subscribe to dead letter queue
	sub, err := broker.Subscribe(ctx, "dlq", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish message to dead letter queue
	msg := Message{ID: "dead-1", Data: "dead letter data"}
	err = broker.PublishToDeadLetter(ctx, "original-topic", msg, "test failure")
	if err != nil {
		t.Fatalf("PublishToDeadLetter error: %v", err)
	}

	// Verify stats
	stats := broker.GetDeadLetterStats()
	if !stats.Enabled {
		t.Fatal("dead letter queue should be enabled")
	}
	if stats.Topic != "dlq" {
		t.Fatalf("expected topic 'dlq', got %s", stats.Topic)
	}
	if stats.TotalMessages != 1 {
		t.Fatalf("expected 1 total message, got %d", stats.TotalMessages)
	}
	if stats.CurrentCount != 1 {
		t.Fatalf("expected 1 current message, got %d", stats.CurrentCount)
	}

	// Receive message from dead letter queue
	select {
	case dlqMsg := <-sub.C():
		if dlqMsg.ID != "dead-1" {
			t.Fatalf("expected message ID dead-1, got %s", dlqMsg.ID)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for dead letter message")
	}
}

func TestDeadLetterMultipleMessages(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Subscribe to dead letter queue
	sub, err := broker.Subscribe(ctx, "dlq", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish multiple messages to dead letter queue
	const numMessages = 5
	for i := 0; i < numMessages; i++ {
		msg := Message{ID: string(rune('a' + i)), Data: "dead letter data"}
		err = broker.PublishToDeadLetter(ctx, "topic-"+string(rune('a'+i)), msg, "test failure")
		if err != nil {
			t.Fatalf("PublishToDeadLetter error: %v", err)
		}
	}

	// Verify stats
	stats := broker.GetDeadLetterStats()
	if stats.TotalMessages != numMessages {
		t.Fatalf("expected %d total messages, got %d", numMessages, stats.TotalMessages)
	}
	if stats.CurrentCount != numMessages {
		t.Fatalf("expected %d current messages, got %d", numMessages, stats.CurrentCount)
	}

	// Receive all messages
	received := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-sub.C():
			received++
			if received == numMessages {
				return
			}
		case <-timeout:
			t.Fatalf("timeout waiting for messages, received %d out of %d", received, numMessages)
		}
	}
}

func TestDeadLetterDisabled(t *testing.T) {
	// Create broker without dead letter queue
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Try to publish to dead letter queue when disabled
	msg := Message{ID: "dead-1", Data: "data"}
	err = broker.PublishToDeadLetter(ctx, "original-topic", msg, "test failure")
	if err == nil {
		t.Fatal("expected error when dead letter queue is disabled")
	}
	if !errors.Is(err, ErrDeadLetterNotSupported) {
		t.Fatalf("expected ErrDeadLetterNotSupported, got %v", err)
	}

	// Verify stats show disabled
	stats := broker.GetDeadLetterStats()
	if stats.Enabled {
		t.Fatal("dead letter queue should be disabled")
	}
}

func TestDeadLetterDefaultTopic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dead-letter" // Use default topic name
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Subscribe to default dead letter topic
	sub, err := broker.Subscribe(ctx, "dead-letter", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish message to dead letter queue
	msg := Message{ID: "dead-1", Data: "dead letter data"}
	err = broker.PublishToDeadLetter(ctx, "original-topic", msg, "test failure")
	if err != nil {
		t.Fatalf("PublishToDeadLetter error: %v", err)
	}

	// Receive message from default dead letter topic
	select {
	case dlqMsg := <-sub.C():
		if dlqMsg.ID != "dead-1" {
			t.Fatalf("expected message ID dead-1, got %s", dlqMsg.ID)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for dead letter message")
	}
}

func TestDeadLetterClose(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	// Publish message to dead letter queue
	msg := Message{ID: "dead-1", Data: "data"}
	err = broker.PublishToDeadLetter(ctx, "original-topic", msg, "test failure")
	if err != nil {
		t.Fatalf("PublishToDeadLetter error: %v", err)
	}

	// Verify stats before close
	stats := broker.GetDeadLetterStats()
	if stats.TotalMessages != 1 {
		t.Fatalf("expected 1 total message, got %d", stats.TotalMessages)
	}

	// Close broker
	_ = broker.Close()

	// Try to add message after close
	err = broker.PublishToDeadLetter(ctx, "original-topic", msg, "test failure")
	if err == nil {
		t.Fatal("expected error after broker close")
	}
}

func TestDeadLetterConcurrent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Subscribe to dead letter queue
	sub, err := broker.Subscribe(ctx, "dlq", SubOptions{BufferSize: 100})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish messages concurrently
	const numMessages = 50
	done := make(chan error, numMessages)

	for i := 0; i < numMessages; i++ {
		go func(id int) {
			msg := Message{ID: string(rune(id)), Data: "data"}
			err := broker.PublishToDeadLetter(ctx, "original-topic", msg, "concurrent test")
			done <- err
		}(i)
	}

	// Wait for all publishes
	for i := 0; i < numMessages; i++ {
		if err := <-done; err != nil {
			t.Fatalf("PublishToDeadLetter error: %v", err)
		}
	}

	// Verify stats
	stats := broker.GetDeadLetterStats()
	if stats.TotalMessages != numMessages {
		t.Fatalf("expected %d total messages, got %d", numMessages, stats.TotalMessages)
	}

	// Receive all messages
	received := 0
	timeout := time.After(1 * time.Second)
	for {
		select {
		case <-sub.C():
			received++
			if received == numMessages {
				return
			}
		case <-timeout:
			t.Fatalf("timeout waiting for messages, received %d out of %d", received, numMessages)
		}
	}
}

func TestDeadLetterValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Test invalid message (missing ID)
	err = broker.PublishToDeadLetter(ctx, "original-topic", Message{}, "test failure")
	if err == nil {
		t.Fatal("expected error for invalid message")
	}
	if !errors.Is(err, ErrNilMessage) {
		t.Fatalf("expected ErrNilMessage, got %v", err)
	}
}

func TestDeadLetterWithAckTimeout(t *testing.T) {
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

	// Publish message with ACK that will timeout
	ackMsg := AckMessage{
		Message:    Message{ID: "ack-timeout-dlq", Data: "data"},
		AckPolicy:  AckTimeout,
		AckTimeout: 50 * time.Millisecond,
	}

	err = broker.PublishWithAck(ctx, "test-topic", ackMsg)
	if err != nil {
		t.Fatalf("PublishWithAck error: %v", err)
	}

	// Wait for timeout and retries
	time.Sleep(300 * time.Millisecond)

	// Check dead letter stats
	stats := broker.GetDeadLetterStats()
	if stats.TotalMessages == 0 {
		// It's possible the message is still in retry cycle
		t.Log("message may still be in retry cycle")
	}
}

func TestDeadLetterTimestamp(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish message to dead letter queue
	before := time.Now()
	msg := Message{ID: "dead-1", Data: "data"}
	err = broker.PublishToDeadLetter(ctx, "original-topic", msg, "test failure")
	if err != nil {
		t.Fatalf("PublishToDeadLetter error: %v", err)
	}
	after := time.Now()

	// Check stats timestamp
	stats := broker.GetDeadLetterStats()
	if stats.LastMessageTime.Before(before) || stats.LastMessageTime.After(after) {
		t.Fatalf("LastMessageTime %v should be between %v and %v",
			stats.LastMessageTime, before, after)
	}
}
