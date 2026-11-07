package mq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spcent/plumego/metrics"
	"github.com/spcent/plumego/x/messaging/pubsub"
)

func TestInProcBrokerPublishSubscribe(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	sub, err := broker.Subscribe(t.Context(), "topic", SubOptions{BufferSize: 1})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	msg := Message{ID: "1", Data: "payload"}
	if err := broker.Publish(t.Context(), "topic", msg); err != nil {
		t.Fatalf("publish error: %v", err)
	}

	select {
	case got := <-sub.C():
		if got.ID != "1" {
			t.Fatalf("expected message ID 1, got %q", got.ID)
		}
		if got.Topic != "topic" {
			t.Fatalf("expected topic 'topic', got %q", got.Topic)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected message to be delivered")
	}
}

func TestInProcBrokerContextCancel(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := broker.Publish(ctx, "topic", Message{}); err == nil {
		t.Fatalf("expected publish to fail with canceled context")
	}
}

func TestInProcBrokerPanicRecovery(t *testing.T) {
	called := false
	broker, err := NewInProcBroker(panicPubSub{}, WithPanicHandler(func(ctx context.Context, op Operation, recovered any) {
		called = true
		if op != OpPublish {
			t.Fatalf("expected panic handler op publish, got %s", op)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}

	err = broker.Publish(t.Context(), "topic", Message{ID: "test-1"})
	if !errors.Is(err, ErrRecoveredPanic) {
		t.Fatalf("expected ErrRecoveredPanic, got %v", err)
	}
	if !called {
		t.Fatalf("expected panic handler to be called")
	}
}

func TestInProcBrokerMetrics(t *testing.T) {
	collector := &metricsCollector{
		NoopCollector: metrics.NewNoopCollector(),
	}
	broker, err := NewInProcBroker(pubsub.New(), WithMetricsObserver(collector))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	if err := broker.Publish(t.Context(), "topic", Message{ID: "test-1"}); err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if collector.count == 0 {
		t.Fatalf("expected metrics to be recorded")
	}
	if collector.last.Operation != OpPublish {
		t.Fatalf("expected publish operation, got %s", collector.last.Operation)
	}
	if collector.last.Topic != "topic" {
		t.Fatalf("expected topic 'topic', got %q", collector.last.Topic)
	}
	if collector.last.Panic {
		t.Fatalf("did not expect panic flag")
	}
}

func TestInProcBrokerValidation(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Test empty topic
	err = broker.Publish(t.Context(), "  ", Message{ID: "test-1"})
	if !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("expected ErrInvalidTopic for empty topic, got %v", err)
	}

	// Test invalid message (missing ID)
	err = broker.Publish(t.Context(), "topic", Message{})
	if !errors.Is(err, ErrNilMessage) {
		t.Fatalf("expected ErrNilMessage for missing ID, got %v", err)
	}

	// Test nil broker
	var nilBroker *InProcBroker
	err = nilBroker.Publish(t.Context(), "topic", Message{ID: "test-1"})
	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized for nil broker, got %v", err)
	}

	// Test empty topic for subscribe
	_, err = broker.Subscribe(t.Context(), "  ", SubOptions{})
	if !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("expected ErrInvalidTopic for empty topic in subscribe, got %v", err)
	}
}

func TestInProcBrokerConcurrent(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Subscribe with larger buffer
	sub, err := broker.Subscribe(t.Context(), "topic", SubOptions{BufferSize: 1000})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Concurrent publishers
	var wg sync.WaitGroup
	numPublishers := 5
	messagesPerPublisher := 50

	wg.Add(numPublishers)
	for i := 0; i < numPublishers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messagesPerPublisher; j++ {
				msg := Message{
					ID:   fmt.Sprintf("msg-%d-%d", id, j),
					Data: fmt.Sprintf("data-%d-%d", id, j),
				}
				if err := broker.Publish(t.Context(), "topic", msg); err != nil {
					t.Errorf("publish error: %v", err)
					return
				}
			}
		}(i)
	}

	// Wait for all publishers to finish
	wg.Wait()

	// Verify all messages were received
	expectedTotal := numPublishers * messagesPerPublisher
	received := 0
	timeout := time.After(10 * time.Second)
	for {
		select {
		case <-sub.C():
			received++
			if received == expectedTotal {
				return
			}
		case <-timeout:
			t.Fatalf("timeout waiting for messages, received %d out of %d", received, expectedTotal)
		}
	}
}

func TestInProcBrokerIdempotentClose(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}

	// First close
	if err := broker.Close(); err != nil {
		t.Fatalf("first close error: %v", err)
	}

	// Second close should be idempotent
	if err := broker.Close(); err != nil {
		t.Fatalf("second close should be idempotent, got error: %v", err)
	}

	// Third close should also be idempotent
	if err := broker.Close(); err != nil {
		t.Fatalf("third close should be idempotent, got error: %v", err)
	}
}

func TestInProcBrokerLongTopic(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Create a topic longer than 1024 characters
	longTopic := strings.Repeat("a", 1025)

	err = broker.Publish(t.Context(), longTopic, Message{ID: "test-1"})
	if !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("expected ErrInvalidTopic for long topic, got %v", err)
	}
}

func TestInProcBrokerHealthCheck(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Subscribe to a topic
	sub, err := broker.Subscribe(t.Context(), "health-test", SubOptions{BufferSize: 1})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish a message
	if err := broker.Publish(t.Context(), "health-test", Message{ID: "test-1"}); err != nil {
		t.Fatalf("publish error: %v", err)
	}

	// Check health
	status := broker.HealthCheck()
	if status.Status != "healthy" {
		t.Fatalf("expected status 'healthy', got %q", status.Status)
	}
	if status.TotalTopics != 1 {
		t.Fatalf("expected 1 topic, got %d", status.TotalTopics)
	}
	if status.TotalSubs != 1 {
		t.Fatalf("expected 1 subscriber, got %d", status.TotalSubs)
	}
	if status.Uptime == "" {
		t.Fatalf("expected non-empty uptime")
	}
	if status.Metrics.TotalPublished != 1 {
		t.Fatalf("expected 1 published message, got %d", status.Metrics.TotalPublished)
	}
}

func TestInProcBrokerConfig(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Get default config
	cfg := broker.GetConfig()
	if !cfg.EnableHealthCheck {
		t.Fatalf("expected EnableHealthCheck to be true by default")
	}
	if cfg.DefaultBufferSize != 16 {
		t.Fatalf("expected DefaultBufferSize to be 16 by default, got %d", cfg.DefaultBufferSize)
	}

	// Update config
	newCfg := DefaultConfig()
	newCfg.DefaultBufferSize = 32
	newCfg.EnableMetrics = false
	if err := broker.UpdateConfig(newCfg); err != nil {
		t.Fatalf("update config error: %v", err)
	}

	// Verify updated config
	updatedCfg := broker.GetConfig()
	if updatedCfg.DefaultBufferSize != 32 {
		t.Fatalf("expected DefaultBufferSize to be 32, got %d", updatedCfg.DefaultBufferSize)
	}
	if updatedCfg.EnableMetrics != false {
		t.Fatalf("expected EnableMetrics to be false")
	}

	// Test invalid config
	invalidCfg := DefaultConfig()
	invalidCfg.DefaultBufferSize = 0
	if err := broker.UpdateConfig(invalidCfg); err == nil {
		t.Fatalf("expected error for invalid config")
	}
}

func TestInProcBrokerPublishBatch(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Subscribe
	sub, err := broker.Subscribe(t.Context(), "batch-topic", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Create batch of messages
	messages := []Message{
		{ID: "msg-1", Data: "data-1"},
		{ID: "msg-2", Data: "data-2"},
		{ID: "msg-3", Data: "data-3"},
	}

	// Publish batch
	if err := broker.PublishBatch(t.Context(), "batch-topic", messages); err != nil {
		t.Fatalf("publish batch error: %v", err)
	}

	// Verify all messages received
	received := 0
	timeout := time.After(1 * time.Second)
	for {
		select {
		case <-sub.C():
			received++
			if received == len(messages) {
				return
			}
		case <-timeout:
			t.Fatalf("timeout waiting for messages, received %d out of %d", received, len(messages))
		}
	}
}

func TestInProcBrokerSubscribeBatch(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Subscribe to multiple topics
	topics := []string{"topic-1", "topic-2", "topic-3"}
	subs, err := broker.SubscribeBatch(t.Context(), topics, SubOptions{BufferSize: 5})
	if err != nil {
		t.Fatalf("subscribe batch error: %v", err)
	}
	if len(subs) != len(topics) {
		t.Fatalf("expected %d subscriptions, got %d", len(topics), len(subs))
	}

	// Verify each subscription works
	for i, sub := range subs {
		if err := broker.Publish(t.Context(), topics[i], Message{ID: fmt.Sprintf("test-%d", i)}); err != nil {
			t.Fatalf("publish error: %v", err)
		}
		select {
		case msg := <-sub.C():
			if msg.ID != fmt.Sprintf("test-%d", i) {
				t.Fatalf("expected message ID test-%d, got %s", i, msg.ID)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timeout waiting for message on topic %s", topics[i])
		}
	}

	// Clean up subscriptions
	for _, sub := range subs {
		sub.Cancel()
	}
}

func TestInProcBrokerInvalidConfig(t *testing.T) {
	invalidCfg := DefaultConfig()
	invalidCfg.DefaultBufferSize = 0

	_, err := NewInProcBroker(pubsub.New(), WithConfig(invalidCfg))
	if err == nil {
		t.Fatal("expected error for invalid config")
	}
}

func TestConfigValidation(t *testing.T) {
	testCases := []struct {
		name    string
		config  func() Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid default config",
			config: func() Config {
				return DefaultConfig()
			},
			wantErr: false,
		},
		{
			name: "invalid buffer size",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.DefaultBufferSize = 0
				return cfg
			},
			wantErr: true,
			errMsg:  "DefaultBufferSize must be positive",
		},
		{
			name: "negative health check interval",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.HealthCheckInterval = -1
				return cfg
			},
			wantErr: true,
			errMsg:  "HealthCheckInterval cannot be negative",
		},
		{
			name: "cluster enabled without node ID",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnableCluster = true
				cfg.ClusterNodeID = ""
				return cfg
			},
			wantErr: true,
			errMsg:  "cluster mode is not yet implemented",
		},
		{
			name: "cluster enabled with whitespace node ID",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnableCluster = true
				cfg.ClusterNodeID = "   "
				return cfg
			},
			wantErr: true,
			errMsg:  "cluster mode is not yet implemented",
		},
		{
			name: "invalid replication factor",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnableCluster = true
				cfg.ClusterNodeID = "node-1"
				cfg.ClusterReplicationFactor = 0
				return cfg
			},
			wantErr: true,
			errMsg:  "cluster mode is not yet implemented",
		},
		{
			name: "persistence enabled without path",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnablePersistence = true
				cfg.PersistencePath = ""
				return cfg
			},
			wantErr: true,
			errMsg:  "PersistencePath is required",
		},
		{
			name: "dead letter queue enabled without topic",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnableDeadLetterQueue = true
				cfg.DeadLetterTopic = ""
				return cfg
			},
			wantErr: true,
			errMsg:  "DeadLetterTopic is required",
		},
		{
			name: "negative ack timeout",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnableAckSupport = true
				cfg.DefaultAckTimeout = -1
				return cfg
			},
			wantErr: true,
			errMsg:  "DefaultAckTimeout cannot be negative",
		},
		{
			name: "negative transaction timeout",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnableTransactions = true
				cfg.TransactionTimeout = -1
				return cfg
			},
			wantErr: true,
			errMsg:  "TransactionTimeout cannot be negative",
		},
		{
			name: "cluster enabled returns not implemented",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnableCluster = true
				cfg.ClusterNodeID = "node-1"
				cfg.ClusterReplicationFactor = 2
				return cfg
			},
			wantErr: true,
			errMsg:  "cluster mode is not yet implemented",
		},
		{
			name: "valid persistence config",
			config: func() Config {
				cfg := DefaultConfig()
				cfg.EnablePersistence = true
				cfg.PersistencePath = "/tmp/mq-data"
				return cfg
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.config()
			err := cfg.Validate()

			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tc.errMsg)
					return
				}
				if !strings.Contains(err.Error(), tc.errMsg) {
					t.Errorf("expected error containing %q, got %v", tc.errMsg, err)
				}
				if !errors.Is(err, ErrInvalidConfig) {
					t.Errorf("expected error to wrap ErrInvalidConfig, got %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestInProcBrokerPriorityQueue(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Test priority message
	priorityMsg := PriorityMessage{
		Message:  Message{ID: "priority-1", Data: "high priority data"},
		Priority: PriorityHigh,
	}

	err = broker.PublishPriority(t.Context(), "priority-topic", priorityMsg)
	if err != nil {
		t.Fatalf("publish priority message error: %v", err)
	}

	// Verify priority queue is enabled by default
	cfg := broker.GetConfig()
	if !cfg.EnablePriorityQueue {
		t.Fatalf("expected EnablePriorityQueue to be true by default")
	}
}

func TestInProcBrokerAckSupport(t *testing.T) {
	// Test with acknowledgment support disabled (default)
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	cfg := broker.GetConfig()
	if cfg.EnableAckSupport {
		t.Fatalf("expected EnableAckSupport to be false by default")
	}

	// Test with acknowledgment support enabled
	ackCfg := DefaultConfig()
	ackCfg.EnableAckSupport = true
	brokerWithAck, err := NewInProcBroker(pubsub.New(), WithConfig(ackCfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brokerWithAck.Close() }()

	// Test publishing with acknowledgment
	ackMsg := AckMessage{
		Message:   Message{ID: "ack-1", Data: "ack required"},
		AckID:     "test-ack-id",
		AckPolicy: AckRequired,
	}

	err = brokerWithAck.PublishWithAck(t.Context(), "ack-topic", ackMsg)
	if err != nil {
		t.Fatalf("publish with ack error: %v", err)
	}

	// Test acknowledgment method
	err = brokerWithAck.Ack(t.Context(), "ack-topic", "test-ack-id")
	if err != nil {
		t.Fatalf("ack error: %v", err)
	}

	// Publish another message for NACK test
	ackMsg2 := AckMessage{
		Message:   Message{ID: "ack-2", Data: "ack required 2"},
		AckID:     "test-ack-id-2",
		AckPolicy: AckRequired,
	}

	err = brokerWithAck.PublishWithAck(t.Context(), "ack-topic", ackMsg2)
	if err != nil {
		t.Fatalf("publish with ack error: %v", err)
	}

	// Test NACK (note: this will trigger redelivery, which is expected)
	err = brokerWithAck.Nack(t.Context(), "ack-topic", "test-ack-id-2")
	if err != nil {
		t.Fatalf("nack error: %v", err)
	}
}

func TestInProcBrokerMemoryLimit(t *testing.T) {
	// Test with memory limit
	cfg := DefaultConfig()
	cfg.MaxMemoryUsage = 1 << 30 // 1GB limit
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Get memory usage
	memUsage := broker.GetMemoryUsage()
	if memUsage == 0 {
		t.Fatalf("expected non-zero memory usage")
	}

	// Verify config
	updatedCfg := broker.GetConfig()
	if updatedCfg.MaxMemoryUsage != 1<<30 {
		t.Fatalf("expected MaxMemoryUsage to be 1GB, got %d", updatedCfg.MaxMemoryUsage)
	}

	// Test health check includes memory info
	status := broker.HealthCheck()
	if status.MemoryUsage == 0 {
		t.Fatalf("expected non-zero memory usage in health check")
	}
	if status.MemoryLimit != 1<<30 {
		t.Fatalf("expected memory limit 1GB in health check, got %d", status.MemoryLimit)
	}
}

func TestInProcBrokerMemoryLimitExceeded(t *testing.T) {
	// Test with very low memory limit
	cfg := DefaultConfig()
	cfg.MaxMemoryUsage = 1 // 1 byte limit - will definitely exceed
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Try to publish a message - should fail due to memory limit
	err = broker.Publish(t.Context(), "test-topic", Message{ID: "test-1", Data: "test"})
	if !errors.Is(err, ErrMemoryLimitExceeded) {
		t.Fatalf("expected ErrMemoryLimitExceeded, got %v", err)
	}
}

func TestInProcBrokerMemoryLimitEnforced(t *testing.T) {
	// Test that memory limit is actually enforced on all publish methods
	cfg := DefaultConfig()
	cfg.MaxMemoryUsage = 1 // 1 byte limit
	cfg.EnablePriorityQueue = true
	cfg.EnableAckSupport = true
	cfg.EnableTransactions = true
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq" // Required when dead letter queue is enabled
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	testCases := []struct {
		name string
		fn   func() error
	}{
		{
			name: "Publish",
			fn: func() error {
				return broker.Publish(t.Context(), "topic", Message{ID: "1", Data: "test"})
			},
		},
		{
			name: "PublishBatch",
			fn: func() error {
				return broker.PublishBatch(t.Context(), "topic", []Message{{ID: "1", Data: "test"}})
			},
		},
		{
			name: "PublishPriority",
			fn: func() error {
				return broker.PublishPriority(t.Context(), "topic", PriorityMessage{
					Message:  Message{ID: "1", Data: "test"},
					Priority: PriorityHigh,
				})
			},
		},
		{
			name: "PublishWithAck",
			fn: func() error {
				return broker.PublishWithAck(t.Context(), "topic", AckMessage{
					Message:   Message{ID: "1", Data: "test"},
					AckPolicy: AckRequired,
				})
			},
		},
		{
			name: "PublishWithTransaction",
			fn: func() error {
				return broker.PublishWithTransaction(t.Context(), "topic", Message{ID: "1", Data: "test"}, "tx-1")
			},
		},
		{
			name: "PublishToDeadLetter",
			fn: func() error {
				return broker.PublishToDeadLetter(t.Context(), "topic", Message{ID: "1", Data: "test"}, "reason")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if !errors.Is(err, ErrMemoryLimitExceeded) {
				t.Errorf("%s: expected ErrMemoryLimitExceeded, got %v", tc.name, err)
			}
		})
	}
}

func TestInProcBrokerTriePattern(t *testing.T) {
	// Test Trie pattern configuration
	cfg := DefaultConfig()
	cfg.EnableTriePattern = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Verify config
	updatedCfg := broker.GetConfig()
	if !updatedCfg.EnableTriePattern {
		t.Fatalf("expected EnableTriePattern to be true")
	}

	// Note: Actual Trie implementation would be in pubsub layer
	// This test verifies the configuration is properly stored
}

func TestInProcBrokerClusterNotImplemented(t *testing.T) {
	// Cluster mode is reserved for a future release. Enabling it must return an
	// explicit error so callers get a deterministic failure rather than a silent
	// no-op. This test documents and enforces that contract.
	cfg := DefaultConfig()
	cfg.EnableCluster = true
	cfg.ClusterNodeID = "node-1"
	cfg.ClusterNodes = []string{"node-2@localhost:9000", "node-3@localhost:9001"}
	cfg.ClusterReplicationFactor = 2
	_, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err == nil {
		t.Fatal("expected error when EnableCluster is true, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestInProcBrokerTransaction(t *testing.T) {
	// Test transaction configuration
	cfg := DefaultConfig()
	cfg.EnableTransactions = true
	cfg.TransactionTimeout = 60 * time.Second
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Verify config
	updatedCfg := broker.GetConfig()
	if !updatedCfg.EnableTransactions {
		t.Fatalf("expected EnableTransactions to be true")
	}

	// Test transaction publish and commit
	txID1 := "tx-1"
	err = broker.PublishWithTransaction(t.Context(), "tx-topic", Message{ID: "tx-1", Data: "transaction data"}, txID1)
	if err != nil {
		t.Fatalf("publish with transaction error: %v", err)
	}

	// Test commit transaction
	err = broker.CommitTransaction(t.Context(), txID1)
	if err != nil {
		t.Fatalf("commit transaction error: %v", err)
	}

	// Test transaction publish and rollback (use different txID)
	txID2 := "tx-2"
	err = broker.PublishWithTransaction(t.Context(), "tx-topic", Message{ID: "tx-2", Data: "transaction data 2"}, txID2)
	if err != nil {
		t.Fatalf("publish with transaction error: %v", err)
	}

	// Test rollback transaction
	err = broker.RollbackTransaction(t.Context(), txID2)
	if err != nil {
		t.Fatalf("rollback transaction error: %v", err)
	}
}

func TestInProcBrokerDeadLetter(t *testing.T) {
	// Test dead letter queue configuration
	cfg := DefaultConfig()
	cfg.EnableDeadLetterQueue = true
	cfg.DeadLetterTopic = "dlq"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Verify config
	updatedCfg := broker.GetConfig()
	if !updatedCfg.EnableDeadLetterQueue {
		t.Fatalf("expected EnableDeadLetterQueue to be true")
	}

	// Test dead letter publish
	err = broker.PublishToDeadLetter(t.Context(), "original-topic", Message{ID: "dlq-1", Data: "dead letter data"}, "processing failed")
	if err != nil {
		t.Fatalf("publish to dead letter error: %v", err)
	}

	// Test dead letter stats
	stats := broker.GetDeadLetterStats()
	if !stats.Enabled {
		t.Fatalf("expected dead letter stats to be enabled")
	}
	if stats.Topic != "dlq" {
		t.Fatalf("expected dead letter topic 'dlq', got %s", stats.Topic)
	}
}

func TestInProcBrokerPersistence(t *testing.T) {
	// Test persistence configuration
	cfg := DefaultConfig()
	cfg.EnablePersistence = true
	cfg.PersistencePath = "/tmp/mq-data"
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Verify config
	updatedCfg := broker.GetConfig()
	if !updatedCfg.EnablePersistence {
		t.Fatalf("expected EnablePersistence to be true")
	}
	if updatedCfg.PersistencePath != "/tmp/mq-data" {
		t.Fatalf("expected PersistencePath to be '/tmp/mq-data', got %s", updatedCfg.PersistencePath)
	}
}

func TestInProcBrokerClusterDisabled(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Test cluster operations when disabled
	err = broker.PublishToCluster(t.Context(), "topic", Message{ID: "test-1", Data: "test"})
	if !errors.Is(err, ErrClusterDisabled) {
		t.Fatalf("expected ErrClusterDisabled, got %v", err)
	}

	// Test cluster status when disabled
	status := broker.GetClusterStatus()
	if status.Status != "disabled" {
		t.Fatalf("expected cluster status 'disabled', got %s", status.Status)
	}
}

func TestInProcBrokerTransactionDisabled(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Test transaction operations when disabled
	err = broker.PublishWithTransaction(t.Context(), "topic", Message{ID: "test-1", Data: "test"}, "tx-1")
	if !errors.Is(err, ErrTransactionNotSupported) {
		t.Fatalf("expected ErrTransactionNotSupported, got %v", err)
	}

	err = broker.CommitTransaction(t.Context(), "tx-1")
	if !errors.Is(err, ErrTransactionNotSupported) {
		t.Fatalf("expected ErrTransactionNotSupported, got %v", err)
	}

	err = broker.RollbackTransaction(t.Context(), "tx-1")
	if !errors.Is(err, ErrTransactionNotSupported) {
		t.Fatalf("expected ErrTransactionNotSupported, got %v", err)
	}
}

func TestInProcBrokerDeadLetterDisabled(t *testing.T) {
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	// Test dead letter operations when disabled
	err = broker.PublishToDeadLetter(t.Context(), "topic", Message{ID: "test-1", Data: "test"}, "reason")
	if !errors.Is(err, ErrDeadLetterNotSupported) {
		t.Fatalf("expected ErrDeadLetterNotSupported, got %v", err)
	}

	// Test dead letter stats when disabled
	stats := broker.GetDeadLetterStats()
	if stats.Enabled {
		t.Fatalf("expected dead letter stats to be disabled")
	}
}

type panicPubSub struct{}

func (p panicPubSub) Publish(topic string, msg pubsub.Message) error {
	panic("boom")
}

func (p panicPubSub) Subscribe(ctx context.Context, topic string, opts pubsub.SubOptions) (pubsub.Subscription, error) {
	return nil, errors.New("not implemented")
}

func (p panicPubSub) Close() error {
	return nil
}

// metricsCollector embeds NoopCollector for default implementations
// and only overrides ObserveMQ and Clear to track MQ-specific metrics.
type metricsCollector struct {
	*metrics.NoopCollector
	mu    sync.Mutex
	last  Metrics
	count int
}

func (m *metricsCollector) ObserveMQ(ctx context.Context, operation, topic string, duration time.Duration, err error, panicked bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last = Metrics{
		Operation: Operation(operation),
		Topic:     topic,
		Duration:  duration,
		Err:       err,
		Panic:     panicked,
	}
	m.count++
}

func (m *metricsCollector) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count = 0
	m.last = Metrics{}
}
