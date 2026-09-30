package mq

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// AckPolicy defines the acknowledgment policy for messages.
type AckPolicy int

const (
	// AckNone - No acknowledgment required
	AckNone AckPolicy = iota
	// AckRequired - Message requires explicit acknowledgment
	AckRequired
	// AckTimeout - Message requires acknowledgment within timeout
	AckTimeout
)

// AckMessage extends Message with acknowledgment support.
type AckMessage struct {
	Message
	AckID      string
	AckPolicy  AckPolicy
	AckTimeout time.Duration
}

// ackEntry represents a pending acknowledgment for a message.
type ackEntry struct {
	messageID   string
	topic       string
	message     Message
	timestamp   time.Time
	timeout     time.Duration
	retryCount  int
	maxRetries  int
	timer       *time.Timer
	ackReceived bool
}

// ackTracker manages pending acknowledgments and timeouts.
type ackTracker struct {
	mu      sync.RWMutex
	pending map[string]*ackEntry
	broker  *InProcBroker
	closed  bool
}

// newAckTracker creates a new acknowledgment tracker.
func newAckTracker(broker *InProcBroker) *ackTracker {
	return &ackTracker{
		pending: make(map[string]*ackEntry),
		broker:  broker,
	}
}

// track starts tracking an acknowledgment for a message.
func (at *ackTracker) track(ackID, topic string, msg Message, timeout time.Duration, maxRetries int) error {
	at.mu.Lock()
	defer at.mu.Unlock()

	if at.closed {
		return ErrBrokerClosed
	}

	if _, exists := at.pending[ackID]; exists {
		return fmt.Errorf("%w: ack ID %s already exists", ErrMessageAcknowledged, ackID)
	}

	entry := &ackEntry{
		messageID:  msg.ID,
		topic:      topic,
		message:    msg,
		timestamp:  time.Now(),
		timeout:    timeout,
		retryCount: 0,
		maxRetries: maxRetries,
	}

	// Set up timeout timer
	entry.timer = time.AfterFunc(timeout, func() {
		at.handleTimeout(ackID)
	})

	at.pending[ackID] = entry
	return nil
}

// acknowledge marks a message as acknowledged.
func (at *ackTracker) acknowledge(ackID string) error {
	at.mu.Lock()
	defer at.mu.Unlock()

	if at.closed {
		return ErrBrokerClosed
	}

	entry, exists := at.pending[ackID]
	if !exists {
		return fmt.Errorf("%w: ack ID %s not found", ErrMessageNotAcked, ackID)
	}

	if entry.ackReceived {
		return fmt.Errorf("%w: ack ID %s", ErrMessageAcknowledged, ackID)
	}

	// Mark as acknowledged and stop timer
	entry.ackReceived = true
	if entry.timer != nil {
		entry.timer.Stop()
	}

	// Remove from pending
	delete(at.pending, ackID)
	return nil
}

// handleTimeout processes a timeout for an acknowledgment.
func (at *ackTracker) handleTimeout(ackID string) {
	at.mu.Lock()
	entry, exists := at.pending[ackID]
	if !exists || entry.ackReceived {
		at.mu.Unlock()
		return
	}

	entry.retryCount++
	shouldRetry := entry.retryCount <= entry.maxRetries
	at.mu.Unlock()

	if shouldRetry {
		// Retry: re-publish the message
		if err := at.broker.redeliverMessage(context.Background(), entry); err != nil {
			// If retry fails, send to dead letter queue
			if at.broker.config.EnableDeadLetterQueue {
				_ = at.broker.PublishToDeadLetter(
					context.Background(),
					entry.topic,
					entry.message,
					fmt.Sprintf("ack timeout after %d retries: %v", entry.retryCount, err),
				)
			}
		}
	} else {
		// Max retries exceeded, send to dead letter queue
		if at.broker.config.EnableDeadLetterQueue {
			_ = at.broker.PublishToDeadLetter(
				context.Background(),
				entry.topic,
				entry.message,
				fmt.Sprintf("ack timeout after %d retries", entry.retryCount),
			)
		}

		// Remove from pending
		at.mu.Lock()
		delete(at.pending, ackID)
		at.mu.Unlock()
	}
}

// close shuts down the tracker and cancels all pending timeouts.
func (at *ackTracker) close() {
	at.mu.Lock()
	defer at.mu.Unlock()

	if at.closed {
		return
	}

	at.closed = true

	// Stop all timers
	for _, entry := range at.pending {
		if entry.timer != nil {
			entry.timer.Stop()
		}
	}

	// Clear pending
	at.pending = nil
}

// stats returns statistics about pending acknowledgments.
func (at *ackTracker) stats() (pending int, oldestAge time.Duration) {
	at.mu.RLock()
	defer at.mu.RUnlock()

	pending = len(at.pending)
	if pending == 0 {
		return
	}

	now := time.Now()
	oldest := now
	for _, entry := range at.pending {
		if entry.timestamp.Before(oldest) {
			oldest = entry.timestamp
		}
	}
	oldestAge = now.Sub(oldest)

	return
}

// PublishWithAck publishes a message that requires acknowledgment.
func (b *InProcBroker) PublishWithAck(ctx context.Context, topic string, msg AckMessage) error {
	return b.executeWithObservability(ctx, OpPublish, topic, func() error {
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

		// Validate message
		if err := validateMessage(msg.Message); err != nil {
			return err
		}

		// Check memory limit
		if err := b.checkMemoryLimit(); err != nil {
			return err
		}

		// Check if acknowledgment support is enabled
		if !b.config.EnableAckSupport {
			return fmt.Errorf("%w: acknowledgment support is disabled", ErrInvalidConfig)
		}

		// Set default timeout if not specified
		if msg.AckTimeout == 0 {
			msg.AckTimeout = b.config.DefaultAckTimeout
		}

		// Generate ACK ID if not provided
		if msg.AckID == "" {
			msg.AckID = fmt.Sprintf("%s-%d", msg.ID, time.Now().UnixNano())
		}

		// Track acknowledgment if required
		if msg.AckPolicy == AckRequired || msg.AckPolicy == AckTimeout {
			maxRetries := 3 // Default max retries
			if err := b.ackTracker.track(msg.AckID, topic, msg.Message, msg.AckTimeout, maxRetries); err != nil {
				return err
			}
		}

		// Publish the message
		return b.ps.Publish(topic, msg.Message)
	})
}

// SubscribeWithAck subscribes to a topic with acknowledgment support.
func (b *InProcBroker) SubscribeWithAck(ctx context.Context, topic string, opts SubOptions) (Subscription, error) {
	var sub Subscription
	err := b.executeWithObservability(ctx, OpSubscribe, topic, func() error {
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

		// Check if acknowledgment support is enabled
		if !b.config.EnableAckSupport {
			return fmt.Errorf("%w: acknowledgment support is disabled", ErrInvalidConfig)
		}

		// Subscribe
		subscription, err := b.ps.Subscribe(context.Background(), topic, opts)
		if err != nil {
			return err
		}

		sub = subscription
		return nil
	})

	return sub, err
}

// Ack acknowledges a message by ID.
func (b *InProcBroker) Ack(ctx context.Context, topic string, messageID string) error {
	return b.executeWithObservability(ctx, OpAck, topic, func() error {
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

		// Check if acknowledgment support is enabled
		if !b.config.EnableAckSupport {
			return fmt.Errorf("%w: acknowledgment support is disabled", ErrInvalidConfig)
		}

		// Validate ackTracker
		if b.ackTracker == nil {
			return fmt.Errorf("%w: ack tracker not initialized", ErrNotInitialized)
		}

		// Acknowledge the message
		return b.ackTracker.acknowledge(messageID)
	})
}

// Nack negatively acknowledges a message by ID (request re-delivery).
func (b *InProcBroker) Nack(ctx context.Context, topic string, messageID string) error {
	return b.executeWithObservability(ctx, OpNack, topic, func() error {
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

		// Check if acknowledgment support is enabled
		if !b.config.EnableAckSupport {
			return fmt.Errorf("%w: acknowledgment support is disabled", ErrInvalidConfig)
		}

		// Validate ackTracker
		if b.ackTracker == nil {
			return fmt.Errorf("%w: ack tracker not initialized", ErrNotInitialized)
		}

		// Hold write lock for the entire check+remove so that a concurrent
		// Nack or timeout cannot also remove and redeliver the same entry.
		b.ackTracker.mu.Lock()
		entry, exists := b.ackTracker.pending[messageID]
		if !exists {
			b.ackTracker.mu.Unlock()
			return fmt.Errorf("%w: ack ID %s not found", ErrMessageNotAcked, messageID)
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(b.ackTracker.pending, messageID)
		b.ackTracker.mu.Unlock()

		// Redeliver without holding the lock (publish may block).
		if err := b.redeliverMessage(ctx, entry); err != nil {
			return fmt.Errorf("failed to redeliver message: %w", err)
		}

		return nil
	})
}
