package mq

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// TTLMessage extends Message with TTL support.
type TTLMessage struct {
	Message
	// ExpiresAt is the timestamp when the message expires
	ExpiresAt time.Time
}

// PriorityTTLMessage combines priority and TTL features.
type PriorityTTLMessage struct {
	Message
	Priority  MessagePriority
	ExpiresAt time.Time
}

// AckTTLMessage combines acknowledgment and TTL features.
type AckTTLMessage struct {
	Message
	AckID      string
	AckPolicy  AckPolicy
	AckTimeout time.Duration
	ExpiresAt  time.Time
}

// ttlEntry tracks a message with TTL.
type ttlEntry struct {
	messageID string
	topic     string
	expiresAt time.Time
}

// ttlTracker manages messages with TTL and handles expiration cleanup.
type ttlTracker struct {
	mu       sync.RWMutex
	messages map[string]*ttlEntry // messageID -> entry
	broker   *InProcBroker
	stopCh   chan struct{}
	done     chan struct{} // closed when cleanupLoop exits
	closed   bool
}

// newTTLTracker creates a new TTL tracker and starts the cleanup goroutine.
func newTTLTracker(broker *InProcBroker) *ttlTracker {
	tt := &ttlTracker{
		messages: make(map[string]*ttlEntry),
		broker:   broker,
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}

	// Start background cleanup goroutine
	go tt.cleanupLoop()

	return tt
}

// track registers a message with TTL for tracking.
func (tt *ttlTracker) track(messageID, topic string, expiresAt time.Time) error {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	if tt.closed {
		return ErrBrokerClosed
	}

	// Don't track if already expired
	if time.Now().After(expiresAt) {
		return ErrMessageExpired
	}

	tt.messages[messageID] = &ttlEntry{
		messageID: messageID,
		topic:     topic,
		expiresAt: expiresAt,
	}

	return nil
}

// isExpired checks if a message has expired.
func (tt *ttlTracker) isExpired(messageID string) bool {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	entry, exists := tt.messages[messageID]
	if !exists {
		return false
	}

	return time.Now().After(entry.expiresAt)
}

// remove removes a message from tracking (e.g., when consumed).
func (tt *ttlTracker) remove(messageID string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	delete(tt.messages, messageID)
}

// cleanupLoop runs in the background and removes expired messages.
func (tt *ttlTracker) cleanupLoop() {
	defer close(tt.done)
	ticker := time.NewTicker(1 * time.Second) // Check every second
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tt.cleanupExpiredMessages()
		case <-tt.stopCh:
			return
		}
	}
}

// cleanupExpiredMessages removes all expired messages from tracking.
func (tt *ttlTracker) cleanupExpiredMessages() {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	now := time.Now()
	expiredIDs := make([]string, 0)

	for id, entry := range tt.messages {
		if now.After(entry.expiresAt) {
			expiredIDs = append(expiredIDs, id)
		}
	}

	// Remove expired messages
	for _, id := range expiredIDs {
		delete(tt.messages, id)
	}

	// Note: The actual message removal from pubsub would require
	// additional support in the pubsub package to remove specific messages
	// from subscriber buffers. For now, we just track expiration.
}

// close stops the cleanup goroutine and clears all tracked messages.
func (tt *ttlTracker) close() {
	tt.mu.Lock()
	if tt.closed {
		tt.mu.Unlock()
		return
	}
	tt.closed = true
	close(tt.stopCh)
	tt.messages = make(map[string]*ttlEntry)
	tt.mu.Unlock()

	// Wait for cleanupLoop to exit before returning, so the caller knows
	// the goroutine is fully stopped. Must not hold tt.mu here because
	// cleanupLoop acquires it inside cleanupExpiredMessages.
	<-tt.done
}

// stats returns the number of tracked messages and count of expired messages.
func (tt *ttlTracker) stats() (tracked int, expired int) {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	tracked = len(tt.messages)
	now := time.Now()

	for _, entry := range tt.messages {
		if now.After(entry.expiresAt) {
			expired++
		}
	}

	return
}

// PublishTTL publishes a message with time-to-live expiration.
func (b *InProcBroker) PublishTTL(ctx context.Context, topic string, msg TTLMessage) error {
	return b.executeWithObservability(ctx, OpPublish, topic, func() error {
		// Validate operation
		if err := b.validatePublishOperation(ctx, topic, &msg.Message); err != nil {
			return err
		}

		// Validate TTL
		if err := b.validateTTL(msg.ExpiresAt); err != nil {
			return err
		}

		// Check memory limit
		if err := b.checkMemoryLimit(); err != nil {
			return err
		}

		// Track message with TTL if tracker is enabled
		if b.ttlTracker != nil && !msg.ExpiresAt.IsZero() {
			if err := b.ttlTracker.track(msg.ID, topic, msg.ExpiresAt); err != nil {
				return err
			}
		}

		return b.ps.Publish(topic, msg.Message)
	})
}

// PublishPriorityTTL publishes a message with both priority and TTL.
func (b *InProcBroker) PublishPriorityTTL(ctx context.Context, topic string, msg PriorityTTLMessage) error {
	return b.executeWithObservability(ctx, OpPublish, topic, func() error {
		// Validate operation
		if err := b.validatePublishOperation(ctx, topic, &msg.Message); err != nil {
			return err
		}

		// Validate TTL
		if err := b.validateTTL(msg.ExpiresAt); err != nil {
			return err
		}

		// Check memory limit
		if err := b.checkMemoryLimit(); err != nil {
			return err
		}

		// Track message with TTL if tracker is enabled
		if b.ttlTracker != nil && !msg.ExpiresAt.IsZero() {
			if err := b.ttlTracker.track(msg.ID, topic, msg.ExpiresAt); err != nil {
				return err
			}
		}

		// Create priority envelope and dispatch
		env := &priorityEnvelope{
			msg:      msg.Message,
			priority: msg.Priority,
			seq:      atomic.AddUint64(&b.prioritySeq, 1),
			ctx:      ctx,
			done:     make(chan error, 1),
		}

		dispatcher, err := b.ensurePriorityDispatcher(topic)
		if err != nil {
			return err
		}

		// Enqueue message with priority
		if err := dispatcher.enqueue(env); err != nil {
			return err
		}

		if ctx == nil {
			return <-env.done
		}

		select {
		case err := <-env.done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

// PublishWithAckTTL publishes a message with both acknowledgment and TTL.
func (b *InProcBroker) PublishWithAckTTL(ctx context.Context, topic string, msg AckTTLMessage) error {
	return b.executeWithObservability(ctx, OpPublish, topic, func() error {
		// Validate operation
		if err := b.validatePublishOperation(ctx, topic, &msg.Message); err != nil {
			return err
		}

		// Validate TTL
		if err := b.validateTTL(msg.ExpiresAt); err != nil {
			return err
		}

		// Check memory limit
		if err := b.checkMemoryLimit(); err != nil {
			return err
		}

		// Check if acknowledgment support is enabled
		if !b.config.EnableAckSupport {
			return ErrInvalidConfig
		}

		// Set default timeout if not specified
		ackTimeout := msg.AckTimeout
		if ackTimeout == 0 {
			ackTimeout = b.config.DefaultAckTimeout
		}

		// Generate ACK ID if not provided
		ackID := msg.AckID
		if ackID == "" {
			ackID = msg.ID
		}

		// Track acknowledgment if required
		if msg.AckPolicy == AckRequired || msg.AckPolicy == AckTimeout {
			maxRetries := 3 // Default retry count
			if err := b.ackTracker.track(ackID, topic, msg.Message, ackTimeout, maxRetries); err != nil {
				return err
			}
		}

		// Track message with TTL if tracker is enabled
		if b.ttlTracker != nil && !msg.ExpiresAt.IsZero() {
			if err := b.ttlTracker.track(msg.ID, topic, msg.ExpiresAt); err != nil {
				return err
			}
		}

		return b.ps.Publish(topic, msg.Message)
	})
}
