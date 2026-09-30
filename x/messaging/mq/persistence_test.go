package mq

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spcent/plumego/x/messaging/pubsub"
)

func TestKVPersistenceBasic(t *testing.T) {
	// Create temporary directory for test
	tmpDir := filepath.Join(os.TempDir(), "mq-persistence-test")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create persistence backend
	backend, err := NewKVPersistence(tmpDir)
	if err != nil {
		t.Fatalf("failed to create persistence backend: %v", err)
	}
	defer func() { _ = backend.Close() }()

	ctx := t.Context()

	// Test save message
	msg := Message{ID: "test-1", Data: "test data"}
	err = backend.SaveMessage(ctx, "test-topic", msg)
	if err != nil {
		t.Fatalf("SaveMessage error: %v", err)
	}

	// Test get messages
	messages, err := backend.GetMessages(ctx, "test-topic", 10)
	if err != nil {
		t.Fatalf("GetMessages error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].ID != "test-1" {
		t.Fatalf("expected message ID test-1, got %s", messages[0].ID)
	}

	// Test delete message
	err = backend.DeleteMessage(ctx, "test-topic", "test-1")
	if err != nil {
		t.Fatalf("DeleteMessage error: %v", err)
	}

	// Verify deletion
	messages, err = backend.GetMessages(ctx, "test-topic", 10)
	if err != nil {
		t.Fatalf("GetMessages error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected 0 messages after delete, got %d", len(messages))
	}
}

func TestKVPersistenceMultipleMessages(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "mq-persistence-multi-test")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	backend, err := NewKVPersistence(tmpDir)
	if err != nil {
		t.Fatalf("failed to create persistence backend: %v", err)
	}
	defer func() { _ = backend.Close() }()

	ctx := t.Context()

	// Save multiple messages
	const numMessages = 10
	for i := 0; i < numMessages; i++ {
		msg := Message{
			ID:   string(rune('a' + i)),
			Data: "test data",
		}
		err = backend.SaveMessage(ctx, "test-topic", msg)
		if err != nil {
			t.Fatalf("SaveMessage error: %v", err)
		}
	}

	// Get all messages
	messages, err := backend.GetMessages(ctx, "test-topic", 0)
	if err != nil {
		t.Fatalf("GetMessages error: %v", err)
	}
	if len(messages) != numMessages {
		t.Fatalf("expected %d messages, got %d", numMessages, len(messages))
	}

	// Test limit
	messages, err = backend.GetMessages(ctx, "test-topic", 5)
	if err != nil {
		t.Fatalf("GetMessages error: %v", err)
	}
	if len(messages) != 5 {
		t.Fatalf("expected 5 messages with limit, got %d", len(messages))
	}
}

// SubOptions.Filter is tagged json:"-" — a runtime-only predicate — so a
// subscription with a Filter set must persist successfully, with the Filter
// dropped from the snapshot.
func TestKVPersistenceSubscriptionWithFilter(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "mq-persistence-sub-filter-test")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	backend, err := NewKVPersistence(tmpDir)
	if err != nil {
		t.Fatalf("failed to create persistence backend: %v", err)
	}
	defer func() { _ = backend.Close() }()

	ctx := t.Context()
	opts := SubOptions{
		BufferSize: 32,
		Policy:     pubsub.DropNewest,
		Filter:     func(msg pubsub.Message) bool { return msg.Type != "skip" },
	}
	if err := backend.SaveSubscription(ctx, "test-topic", "sub-1", opts); err != nil {
		t.Fatalf("SaveSubscription with Filter error: %v", err)
	}

	got, err := backend.GetSubscriptions(ctx, "test-topic")
	if err != nil {
		t.Fatalf("GetSubscriptions error: %v", err)
	}
	restored, ok := got["sub-1"]
	if !ok {
		t.Fatal("subscription was not restored")
	}
	if restored.BufferSize != 32 || restored.Policy != pubsub.DropNewest {
		t.Fatalf("unexpected restored options: %+v", restored)
	}
	if restored.Filter != nil {
		t.Fatal("Filter must not survive persistence")
	}
}

func TestKVPersistenceAckState(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "mq-persistence-ack-test")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	backend, err := NewKVPersistence(tmpDir)
	if err != nil {
		t.Fatalf("failed to create persistence backend: %v", err)
	}
	defer func() { _ = backend.Close() }()

	ctx := t.Context()

	// Initially, message should not be acked
	acked, err := backend.GetAckState(ctx, "test-topic", "msg-1")
	if err != nil {
		t.Fatalf("GetAckState error: %v", err)
	}
	if acked {
		t.Fatal("expected message not to be acked initially")
	}

	// Save ack state
	err = backend.SaveAckState(ctx, "test-topic", "msg-1", true)
	if err != nil {
		t.Fatalf("SaveAckState error: %v", err)
	}

	// Verify acked
	acked, err = backend.GetAckState(ctx, "test-topic", "msg-1")
	if err != nil {
		t.Fatalf("GetAckState error: %v", err)
	}
	if !acked {
		t.Fatal("expected message to be acked")
	}

	// Rollback ack
	err = backend.SaveAckState(ctx, "test-topic", "msg-1", false)
	if err != nil {
		t.Fatalf("SaveAckState error: %v", err)
	}

	// Verify not acked
	acked, err = backend.GetAckState(ctx, "test-topic", "msg-1")
	if err != nil {
		t.Fatalf("GetAckState error: %v", err)
	}
	if acked {
		t.Fatal("expected message not to be acked after rollback")
	}
}

func TestBrokerPersistence(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "mq-broker-persistence-test")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create broker with persistence
	cfg := DefaultConfig()
	cfg.EnablePersistence = true
	cfg.PersistencePath = tmpDir
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	if broker.persistenceManager == nil {
		t.Fatal("persistenceManager should be initialized when persistence is enabled")
	}

	ctx := t.Context()

	// Publish message
	msg := Message{ID: "persist-1", Data: "persistent data"}
	err = broker.Publish(ctx, "test-topic", msg)
	if err != nil {
		t.Fatalf("Publish error: %v", err)
	}

	// Recover messages
	messages, err := broker.RecoverMessages(ctx, "test-topic", 10)
	if err != nil {
		t.Fatalf("RecoverMessages error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].ID != "persist-1" {
		t.Fatalf("expected message ID persist-1, got %s", messages[0].ID)
	}
}

func TestBrokerPersistenceReplay(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "mq-broker-replay-test")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create first broker instance and publish messages
	cfg := DefaultConfig()
	cfg.EnablePersistence = true
	cfg.PersistencePath = tmpDir
	broker1, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	// Publish messages
	msg1 := Message{ID: "replay-1", Data: "data-1"}
	msg2 := Message{ID: "replay-2", Data: "data-2"}
	err = broker1.Publish(ctx, "test-topic", msg1)
	if err != nil {
		t.Fatalf("Publish error: %v", err)
	}
	err = broker1.Publish(ctx, "test-topic", msg2)
	if err != nil {
		t.Fatalf("Publish error: %v", err)
	}

	// Close first broker
	_ = broker1.Close()

	// Create second broker instance (simulating restart)
	broker2, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker2.Close() }()

	// Subscribe to topic
	sub, err := broker2.Subscribe(ctx, "test-topic", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Replay messages
	err = broker2.ReplayMessages(ctx, "test-topic", 10)
	if err != nil {
		t.Fatalf("ReplayMessages error: %v", err)
	}

	// Receive replayed messages
	received := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-sub.C():
			received++
			if received == 2 {
				return
			}
		case <-timeout:
			t.Fatalf("timeout waiting for replayed messages, received %d out of 2", received)
		}
	}
}

func TestPersistenceDisabled(t *testing.T) {
	// Create broker without persistence
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Try to recover messages when persistence is disabled
	_, err = broker.RecoverMessages(ctx, "test-topic", 10)
	if err == nil {
		t.Fatal("expected error when persistence is disabled")
	}

	// Try to replay messages when persistence is disabled
	err = broker.ReplayMessages(ctx, "test-topic", 10)
	if err == nil {
		t.Fatal("expected error when persistence is disabled")
	}
}

func TestPersistenceBatch(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "mq-persistence-batch-test")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cfg := DefaultConfig()
	cfg.EnablePersistence = true
	cfg.PersistencePath = tmpDir
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish batch of messages
	messages := []Message{
		{ID: "batch-1", Data: "data-1"},
		{ID: "batch-2", Data: "data-2"},
		{ID: "batch-3", Data: "data-3"},
	}

	err = broker.PublishBatch(ctx, "test-topic", messages)
	if err != nil {
		t.Fatalf("PublishBatch error: %v", err)
	}

	// Recover messages
	recovered, err := broker.RecoverMessages(ctx, "test-topic", 10)
	if err != nil {
		t.Fatalf("RecoverMessages error: %v", err)
	}
	if len(recovered) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(recovered))
	}
}

func TestPersistenceClose(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "mq-persistence-close-test")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cfg := DefaultConfig()
	cfg.EnablePersistence = true
	cfg.PersistencePath = tmpDir
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	// Publish message
	msg := Message{ID: "close-1", Data: "data"}
	err = broker.Publish(ctx, "test-topic", msg)
	if err != nil {
		t.Fatalf("Publish error: %v", err)
	}

	// Close broker
	_ = broker.Close()

	// Try to use persistence after close
	_, err = broker.RecoverMessages(ctx, "test-topic", 10)
	if err == nil {
		t.Fatal("expected error after broker close")
	}
}
