// Package mq provides an in-process message broker with advanced features.
//
// Features:
//   - Basic pub/sub messaging with topic-based routing
//   - Priority queue support for message ordering
//   - Message TTL (time-to-live) with automatic expiration
//   - Message acknowledgment (ACK/NACK) with timeout and retry
//   - Transaction support for atomic message publishing
//   - Dead letter queue for failed message handling
//   - Persistence storage with automatic recovery
//   - Memory management with configurable limits
//   - Metrics collection and observability
//
// Status:
//   - Core features: Production-ready ✓
//   - Advanced features (transactions, persistence, DLQ): Stable ✓
//   - Cluster mode: Interface defined, implementation pending
//
// Example usage:
//
//	cfg := mq.DefaultConfig()
//	cfg.EnableTransactions = true
//	cfg.EnablePersistence = true
//	cfg.PersistencePath = "/data/mq"
//	cfg.EnableDeadLetterQueue = true
//	cfg.DeadLetterTopic = "dlq"
//
//	broker := mq.NewInProcBroker(pubsub.New(), mq.WithConfig(cfg))
//	defer broker.Close()
//
//	// Publish with transaction
//	txID := "tx-1"
//	broker.PublishWithTransaction(ctx, "orders", msg1, txID)
//	broker.PublishWithTransaction(ctx, "orders", msg2, txID)
//	broker.CommitTransaction(ctx, txID)
//
//	// Subscribe and process
//	sub, _ := broker.Subscribe(ctx, "orders", mq.SubOptions{BufferSize: 100})
//	for msg := range sub.C() {
//	    // Process message
//	}
package mq

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

// InProcBroker adapts pubsub.Broker to the Broker interface.
type InProcBroker struct {
	ps            pubsub.Broker
	metrics       MetricsObserver
	panicHandler  PanicHandler
	config        Config
	startTime     time.Time
	lastError     error
	lastPanic     error
	lastPanicTime time.Time

	priorityMu     sync.Mutex
	priorityQueues map[string]*priorityDispatcher
	prioritySeq    uint64
	priorityClosed atomic.Bool

	ackTracker         *ackTracker
	ttlTracker         *ttlTracker
	txManager          *transactionManager
	deadLetterManager  *deadLetterManager
	persistenceManager *persistenceManager

	// cachedMemUsage stores the most recent memory usage reading.
	// Updated by a background goroutine to avoid STW on every Publish.
	cachedMemUsage atomic.Uint64
	memSamplerDone chan struct{}
	memSamplerStop sync.Once // guarantees memSamplerDone is closed exactly once

	// consumerGroupMgr is lazily initialized for InProcPubSub backends.
	consumerGroupMgr *pubsub.ConsumerGroupManager
}

// Option configures the broker.
type Option func(*InProcBroker)

// WithMetricsObserver registers a metrics observer.
func WithMetricsObserver(observer MetricsObserver) Option {
	return func(b *InProcBroker) {
		b.metrics = observer
	}
}

// WithPanicHandler registers a panic handler.
func WithPanicHandler(handler PanicHandler) Option {
	return func(b *InProcBroker) {
		b.panicHandler = handler
	}
}

// WithConfig sets the broker configuration.
// Validation happens in the constructor (NewInProcBroker).
func WithConfig(cfg Config) Option {
	return func(b *InProcBroker) {
		b.config = cfg
	}
}

// validateTopic checks if a topic is valid.
func validateTopic(topic string) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return fmt.Errorf("%w: cannot be empty", ErrInvalidTopic)
	}
	if len(topic) > MaxTopicLength {
		return fmt.Errorf("%w: topic too long (max %d characters)", ErrInvalidTopic, MaxTopicLength)
	}
	return nil
}

// validateMessage checks if a message is valid.
func validateMessage(msg Message) error {
	if msg.ID == "" {
		return fmt.Errorf("%w: ID is required", ErrNilMessage)
	}
	return nil
}

// validatePublishOperation performs common validation for all publish operations.
// It checks context, broker initialization, topic, and optionally message.
func (b *InProcBroker) validatePublishOperation(ctx context.Context, topic string, msg *Message) error {
	// Validate context
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// Validate broker initialization
	if b == nil || b.ps == nil {
		return ErrNotInitialized
	}

	// Validate topic
	if err := validateTopic(topic); err != nil {
		return err
	}

	// Validate message if provided
	if msg != nil {
		if err := validateMessage(*msg); err != nil {
			return err
		}
	}

	return nil
}

// validateSubscribeOperation performs common validation for all subscribe operations.
// It checks context, broker initialization, and topic.
func (b *InProcBroker) validateSubscribeOperation(ctx context.Context, topic string) error {
	// Validate context
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// Validate broker initialization
	if b == nil || b.ps == nil {
		return ErrNotInitialized
	}

	// Validate topic
	if err := validateTopic(topic); err != nil {
		return err
	}

	return nil
}

// validateTTL checks if a TTL message is valid and not expired.
func (b *InProcBroker) validateTTL(expiresAt time.Time) error {
	if expiresAt.IsZero() {
		return nil // No TTL set, message doesn't expire
	}

	if time.Now().After(expiresAt) {
		return fmt.Errorf("%w: message expired at %v", ErrMessageExpired, expiresAt)
	}

	return nil
}

// NewInProcBroker wraps the in-process pubsub implementation and returns an
// error if configuration is invalid or persistence initialization fails.
func NewInProcBroker(ps pubsub.Broker, opts ...Option) (*InProcBroker, error) {
	return newInProcBroker(ps, opts...)
}

// newInProcBroker is the shared constructor implementation.
func newInProcBroker(ps pubsub.Broker, opts ...Option) (_ *InProcBroker, retErr error) {
	if ps == nil {
		ps = pubsub.New()
	}
	broker := &InProcBroker{
		ps:             ps,
		config:         DefaultConfig(),
		startTime:      time.Now(),
		memSamplerDone: make(chan struct{}),
	}

	// Apply options; capture any panic as an error.
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					retErr = fmt.Errorf("broker option failed: %v", r)
				}
			}()
			opt(broker)
		}()
		if retErr != nil {
			return nil, retErr
		}
	}

	// Validate the final config (after all options have been applied).
	if err := broker.config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid broker config: %w", err)
	}

	// Initialize ackTracker if ACK support is enabled
	if broker.config.EnableAckSupport {
		broker.ackTracker = newAckTracker(broker)
	}

	// Initialize ttlTracker if TTL is enabled (MessageTTL > 0)
	if broker.config.MessageTTL > 0 {
		broker.ttlTracker = newTTLTracker(broker)
	}

	// Initialize txManager if transactions are enabled
	if broker.config.EnableTransactions {
		broker.txManager = newTransactionManager(broker)
	}

	// Initialize deadLetterManager if dead letter queue is enabled
	if broker.config.EnableDeadLetterQueue {
		broker.deadLetterManager = newDeadLetterManager(broker)
	}

	// Initialize persistenceManager if persistence is enabled
	if broker.config.EnablePersistence {
		if broker.config.PersistencePath == "" {
			return nil, fmt.Errorf("invalid broker config: PersistencePath is required when persistence is enabled")
		}
		backend, err := NewKVPersistence(broker.config.PersistencePath)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize persistence: %w", err)
		}
		broker.persistenceManager = newPersistenceManager(broker, backend)
	}

	// Initialize consumer group manager for InProcPubSub backends.
	if inproc, ok := ps.(*pubsub.InProcBroker); ok {
		broker.consumerGroupMgr = pubsub.NewConsumerGroupManager(inproc)
	}

	// Start background memory sampler only when a memory limit is configured.
	// This avoids STW pauses from runtime.ReadMemStats on every Publish call.
	// Perform an initial synchronous sample so callers see a non-zero value
	// immediately after construction without waiting for the first tick.
	if broker.config.MaxMemoryUsage > 0 {
		broker.sampleMemory()
		broker.startMemSampler()
	}

	return broker, nil
}

// Publish sends a message to a topic.
func (b *InProcBroker) Publish(ctx context.Context, topic string, msg Message) error {
	return b.executeWithObservability(ctx, OpPublish, topic, func() error {
		// Validate operation
		if err := b.validatePublishOperation(ctx, topic, &msg); err != nil {
			return err
		}

		// Check memory limit
		if err := b.checkMemoryLimit(); err != nil {
			return err
		}

		// Persist message if persistence is enabled
		if b.config.EnablePersistence && b.persistenceManager != nil {
			if err := b.persistenceManager.saveMessage(ctx, topic, msg); err != nil {
				// Log error but don't fail the publish
				// This ensures availability over consistency
				b.lastError = fmt.Errorf("failed to persist message: %w", err)
			}
		}

		return b.ps.Publish(topic, msg)
	})
}

// PublishBatch sends multiple messages to a topic in a single operation.
func (b *InProcBroker) PublishBatch(ctx context.Context, topic string, msgs []Message) error {
	return b.executeWithObservability(ctx, OpPublish, topic, func() error {
		if err := b.validatePublishOperation(ctx, topic, nil); err != nil {
			return err
		}

		// Validate each message up-front before touching persistence or pubsub.
		for i := range msgs {
			if err := validateMessage(msgs[i]); err != nil {
				return err
			}
		}

		if err := b.checkMemoryLimit(); err != nil {
			return err
		}

		// Persist messages if persistence is enabled.
		if b.config.EnablePersistence && b.persistenceManager != nil {
			for _, msg := range msgs {
				if err := b.persistenceManager.saveMessage(ctx, topic, msg); err != nil {
					b.lastError = fmt.Errorf("failed to persist message: %w", err)
				}
			}
		}

		for _, msg := range msgs {
			if err := b.ps.Publish(topic, msg); err != nil {
				return err
			}
		}

		return nil
	})
}

// SubscribeBatch subscribes to multiple topics at once.
func (b *InProcBroker) SubscribeBatch(ctx context.Context, topics []string, opts SubOptions) ([]Subscription, error) {
	var subs []Subscription

	err := b.executeWithObservability(ctx, OpSubscribe, "", func() error {
		for _, topic := range topics {
			if err := b.validateSubscribeOperation(ctx, topic); err != nil {
				return err
			}
			sub, err := b.ps.Subscribe(context.Background(), topic, opts)
			if err != nil {
				return err
			}
			subs = append(subs, sub)
		}
		return nil
	})

	if err != nil {
		// Clean up any subscriptions that were created
		for _, sub := range subs {
			sub.Cancel()
		}
		return nil, err
	}

	return subs, nil
}

// Subscribe registers a subscription for a topic.
func (b *InProcBroker) Subscribe(ctx context.Context, topic string, opts SubOptions) (sub Subscription, err error) {
	err = b.executeWithObservability(ctx, OpSubscribe, topic, func() error {
		// Validate context
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}

		// Validate broker initialization
		if b == nil || b.ps == nil {
			return ErrNotInitialized
		}

		// Validate topic
		if err := validateTopic(topic); err != nil {
			return err
		}

		// Subscribe
		var subscription Subscription
		subscription, err = b.ps.Subscribe(context.Background(), topic, opts)
		if err != nil {
			return err
		}

		// Store subscription for return
		sub = subscription
		return nil
	})

	return sub, err
}

// GetMemoryUsage returns the most recently sampled memory usage in bytes.
// When no memory limit is configured the value is always 0; call
// runtime.ReadMemStats directly for an exact on-demand reading.
func (b *InProcBroker) GetMemoryUsage() uint64 {
	return b.cachedMemUsage.Load()
}

// redeliverMessage re-publishes a message that failed acknowledgment.
func (b *InProcBroker) redeliverMessage(ctx context.Context, entry *ackEntry) error {
	if b == nil || b.ps == nil {
		return ErrNotInitialized
	}
	return b.Publish(ctx, entry.topic, entry.message)
}
