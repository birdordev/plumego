package pubsub

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// AckableSubscription is a subscription with message acknowledgment support.
type AckableSubscription interface {
	Subscription

	// Receive blocks until a message is available or context is cancelled.
	Receive(ctx context.Context) (Message, error)

	// Ack acknowledges a message was successfully processed.
	Ack(msgID string) error

	// Nack negatively acknowledges a message (failed processing).
	// If requeue is true, the message will be redelivered.
	Nack(msgID string, requeue bool) error

	// PendingCount returns the number of messages pending acknowledgment.
	PendingCount() int

	// DeadLetterCount returns the number of messages in the dead letter queue.
	DeadLetterCount() int

	// DeadLetterMessages returns all messages currently in the dead letter queue.
	DeadLetterMessages() []DeadLetterMessage

	// ReprocessDeadLetters republishes dead letter messages to their original topics.
	// An optional filter selects which messages to reprocess; nil reprocesses all.
	// Returns the number of successfully reprocessed messages.
	ReprocessDeadLetters(filter func(DeadLetterMessage) bool) int

	// ClearDeadLetters removes all messages from the dead letter queue.
	ClearDeadLetters()
}

// ackableSubscriber implements AckableSubscription.
type ackableSubscriber struct {
	*subscriber

	mu         sync.Mutex
	pending    map[string]*pendingMessage // msgID -> pending message
	ackTimeout time.Duration
	maxRetries int
	deadLetter *InProcBroker
	dlTopic    string
	ps         *InProcBroker
	dlq        *deadLetterQueue
}

// pendingMessage tracks a message awaiting acknowledgment.
type pendingMessage struct {
	msg       Message
	deliverAt time.Time
	retries   int
	timer     *time.Timer
}

// AckOptions configures ackable subscription behavior.
type AckOptions struct {
	// AckTimeout is how long to wait for ack before redelivery (default: 30s)
	AckTimeout time.Duration

	// MaxRetries is max redelivery attempts before sending to dead letter (default: 3)
	MaxRetries int

	// DeadLetterTopic is the topic for messages that exceed max retries
	DeadLetterTopic string

	// DeadLetterPubSub is the pubsub for dead letter messages (nil = same pubsub)
	DeadLetterPubSub *InProcBroker

	// DLQConfig provides additional configuration for the local dead letter queue.
	// When set (non-zero), a local dead letter queue is created to store failed
	// messages for later inspection and reprocessing.
	// If DLQConfig.Topic is empty, DeadLetterTopic is used as the topic.
	DLQConfig DeadLetterConfig
}

// DefaultAckOptions returns default ack options.
func DefaultAckOptions() AckOptions {
	return AckOptions{
		AckTimeout:      30 * time.Second,
		MaxRetries:      3,
		DeadLetterTopic: "",
	}
}

// SubscribeAckable creates an ackable subscription.
// When ctx is cancelled the underlying subscription is automatically closed.
func (ps *InProcBroker) SubscribeAckable(ctx context.Context, topic string, subOpts SubOptions, ackOpts AckOptions) (AckableSubscription, error) {
	sub, err := ps.Subscribe(ctx, topic, subOpts)
	if err != nil {
		return nil, err
	}

	ackSub := &ackableSubscriber{
		subscriber: sub.(*subscriber),
		pending:    make(map[string]*pendingMessage),
		ackTimeout: ackOpts.AckTimeout,
		maxRetries: ackOpts.MaxRetries,
		dlTopic:    ackOpts.DeadLetterTopic,
		deadLetter: ackOpts.DeadLetterPubSub,
		ps:         ps,
	}

	if ackSub.ackTimeout <= 0 {
		ackSub.ackTimeout = 30 * time.Second
	}
	if ackSub.maxRetries <= 0 {
		ackSub.maxRetries = 3
	}
	if ackSub.deadLetter == nil {
		ackSub.deadLetter = ps
	}

	// Create local dead letter queue when configured
	dlqConfig := ackOpts.DLQConfig
	if dlqConfig.Topic == "" && ackSub.dlTopic != "" {
		dlqConfig.Topic = ackSub.dlTopic
	}
	if dlqConfig.Topic != "" || dlqConfig.MaxSize > 0 || dlqConfig.TTL > 0 || dlqConfig.OnDeadLetter != nil {
		ackSub.dlq = newDeadLetterQueue(dlqConfig)
	}

	return ackSub, nil
}

// Receive blocks until a message is available.
func (as *ackableSubscriber) Receive(ctx context.Context) (Message, error) {
	select {
	case msg, ok := <-as.C():
		if !ok {
			return Message{}, newErr(ErrCodeClosed, "receive", "", "subscription is closed", nil)
		}

		// Track message for ack
		as.trackMessage(msg)
		return msg, nil

	case <-ctx.Done():
		return Message{}, ctx.Err()

	case <-as.Done():
		return Message{}, newErr(ErrCodeClosed, "receive", "", "subscription is closed", nil)
	}
}

// trackMessage tracks a message for acknowledgment.
func (as *ackableSubscriber) trackMessage(msg Message) {
	msgID := msg.ID
	if msgID == "" {
		// Generate ID if not present
		msgID = generateCorrelationID()
		msg.ID = msgID
	}

	as.mu.Lock()
	defer as.mu.Unlock()

	pm := &pendingMessage{
		msg:       msg,
		deliverAt: time.Now(),
		retries:   0,
	}

	// Set ack timeout timer
	pm.timer = time.AfterFunc(as.ackTimeout, func() {
		as.handleTimeout(msgID)
	})

	as.pending[msgID] = pm
}

// handleTimeout handles ack timeout for a message.
func (as *ackableSubscriber) handleTimeout(msgID string) {
	as.mu.Lock()
	pm, ok := as.pending[msgID]
	if !ok {
		as.mu.Unlock()
		return
	}

	pm.retries++

	if pm.retries >= as.maxRetries {
		// Send to dead letter queue
		delete(as.pending, msgID)
		as.mu.Unlock()

		as.sendToDeadLetter(pm.msg, "max_retries_exceeded")
		return
	}

	// Redeliver
	pm.deliverAt = time.Now()
	pm.timer = time.AfterFunc(as.ackTimeout, func() {
		as.handleTimeout(msgID)
	})
	msg := pm.msg
	as.mu.Unlock()

	// Redeliver to subscriber channel (non-blocking).
	// Hold s.mu to synchronize with Cancel(), which closes s.ch under the same lock.
	as.subscriber.mu.Lock()
	if !as.closed.Load() {
		select {
		case as.ch <- msg:
		default:
			// Channel full, message will be redelivered on next timeout
		}
	}
	as.subscriber.mu.Unlock()
}

// Ack acknowledges a message.
func (as *ackableSubscriber) Ack(msgID string) error {
	as.mu.Lock()
	defer as.mu.Unlock()

	pm, ok := as.pending[msgID]
	if !ok {
		return newErr(ErrCodeNotFound, "ack", "", "message not found in pending", nil)
	}

	if pm.timer != nil {
		pm.timer.Stop()
	}
	delete(as.pending, msgID)

	return nil
}

// Nack negatively acknowledges a message.
func (as *ackableSubscriber) Nack(msgID string, requeue bool) error {
	as.mu.Lock()
	pm, ok := as.pending[msgID]
	if !ok {
		as.mu.Unlock()
		return newErr(ErrCodeNotFound, "ack", "", "message not found in pending", nil)
	}

	if pm.timer != nil {
		pm.timer.Stop()
	}

	if !requeue {
		// Don't requeue, send to dead letter
		delete(as.pending, msgID)
		as.mu.Unlock()

		as.sendToDeadLetter(pm.msg, "nack_no_requeue")
		return nil
	}

	// Requeue
	pm.retries++
	if pm.retries >= as.maxRetries {
		delete(as.pending, msgID)
		as.mu.Unlock()

		as.sendToDeadLetter(pm.msg, "max_retries_exceeded")
		return nil
	}

	pm.deliverAt = time.Now()
	pm.timer = time.AfterFunc(as.ackTimeout, func() {
		as.handleTimeout(msgID)
	})
	msg := pm.msg
	as.mu.Unlock()

	// Redeliver immediately.
	// Hold s.mu to synchronize with Cancel(), which closes s.ch under the same lock.
	as.subscriber.mu.Lock()
	if !as.closed.Load() {
		select {
		case as.ch <- msg:
		default:
		}
	}
	as.subscriber.mu.Unlock()

	return nil
}

// PendingCount returns the number of pending messages.
func (as *ackableSubscriber) PendingCount() int {
	as.mu.Lock()
	defer as.mu.Unlock()
	return len(as.pending)
}

// Cancel cancels the subscription and cleans up.
func (as *ackableSubscriber) Cancel() {
	as.mu.Lock()
	for _, pm := range as.pending {
		if pm.timer != nil {
			pm.timer.Stop()
		}
	}
	as.pending = make(map[string]*pendingMessage)
	as.mu.Unlock()

	as.subscriber.Cancel()
}

// sendToDeadLetter sends a message to the dead letter queue.
func (as *ackableSubscriber) sendToDeadLetter(msg Message, reason string) {
	if as.dlTopic == "" && as.dlq == nil {
		return
	}

	// Add dead letter metadata
	if msg.Meta == nil {
		msg.Meta = make(map[string]string)
	}
	msg.Meta["X-Dead-Letter-Reason"] = reason
	msg.Meta["X-Original-Topic"] = msg.Topic
	msg.Meta["X-Dead-Letter-Time"] = time.Now().UTC().Format(time.RFC3339)

	// Store in local dead letter queue
	if as.dlq != nil {
		as.dlq.Add(msg, reason)
	}

	// Publish to dead letter topic
	if as.dlTopic != "" {
		_ = as.deadLetter.Publish(as.dlTopic, msg)
	}
}

// DeadLetterConfig configures dead letter queue behavior.
type DeadLetterConfig struct {
	// Topic is the dead letter topic
	Topic string

	// MaxSize is the maximum number of messages to retain (0 = unlimited)
	MaxSize int

	// TTL is how long to retain dead letter messages (0 = forever)
	TTL time.Duration

	// OnDeadLetter is called when a message is sent to dead letter queue
	OnDeadLetter func(msg Message, reason string)
}

// deadLetterQueue manages dead letter messages.
type deadLetterQueue struct {
	mu       sync.RWMutex
	messages []DeadLetterMessage
	config   DeadLetterConfig
	sequence atomic.Uint64
}

// DeadLetterMessage represents a message in the dead letter queue.
type DeadLetterMessage struct {
	Message   Message
	Reason    string
	Timestamp time.Time
	Sequence  uint64
}

// newDeadLetterQueue creates a new dead letter queue.
func newDeadLetterQueue(config DeadLetterConfig) *deadLetterQueue {
	return &deadLetterQueue{
		messages: make([]DeadLetterMessage, 0),
		config:   config,
	}
}

// Add adds a message to the dead letter queue.
func (dlq *deadLetterQueue) Add(msg Message, reason string) {
	dlq.mu.Lock()
	defer dlq.mu.Unlock()

	seq := dlq.sequence.Add(1)

	dm := DeadLetterMessage{
		Message:   msg,
		Reason:    reason,
		Timestamp: time.Now(),
		Sequence:  seq,
	}

	dlq.messages = append(dlq.messages, dm)

	// Trim if max size exceeded
	if dlq.config.MaxSize > 0 && len(dlq.messages) > dlq.config.MaxSize {
		dlq.messages = dlq.messages[len(dlq.messages)-dlq.config.MaxSize:]
	}

	// Call callback in a goroutine; recover from user panics so a buggy
	// observer cannot crash the application.
	if fn := dlq.config.OnDeadLetter; fn != nil {
		go func(m Message, r string, f func(Message, string)) {
			defer func() { _ = recover() }()
			f(m, r)
		}(msg, reason, fn)
	}
}

// List returns all dead letter messages.
func (dlq *deadLetterQueue) List() []DeadLetterMessage {
	dlq.mu.RLock()
	defer dlq.mu.RUnlock()

	result := make([]DeadLetterMessage, len(dlq.messages))
	copy(result, dlq.messages)
	return result
}

// Count returns the number of messages.
func (dlq *deadLetterQueue) Count() int {
	dlq.mu.RLock()
	defer dlq.mu.RUnlock()
	return len(dlq.messages)
}

// Clear removes all messages.
func (dlq *deadLetterQueue) Clear() {
	dlq.mu.Lock()
	defer dlq.mu.Unlock()
	dlq.messages = dlq.messages[:0]
}

// Reprocess moves messages back to their original topics.
func (dlq *deadLetterQueue) Reprocess(ps *InProcBroker, filter func(dm DeadLetterMessage) bool) int {
	dlq.mu.Lock()
	defer dlq.mu.Unlock()

	var remaining []DeadLetterMessage
	var reprocessed int

	for _, dm := range dlq.messages {
		if filter != nil && !filter(dm) {
			remaining = append(remaining, dm)
			continue
		}

		originalTopic := dm.Message.Meta["X-Original-Topic"]
		if originalTopic == "" {
			originalTopic = dm.Message.Topic
		}

		// Clear dead letter metadata
		delete(dm.Message.Meta, "X-Dead-Letter-Reason")
		delete(dm.Message.Meta, "X-Original-Topic")
		delete(dm.Message.Meta, "X-Dead-Letter-Time")

		if err := ps.Publish(originalTopic, dm.Message); err == nil {
			reprocessed++
		} else {
			remaining = append(remaining, dm)
		}
	}

	dlq.messages = remaining
	return reprocessed
}

// DeadLetterCount returns the number of messages in the dead letter queue.
func (as *ackableSubscriber) DeadLetterCount() int {
	if as.dlq == nil {
		return 0
	}
	return as.dlq.Count()
}

// DeadLetterMessages returns all messages currently in the dead letter queue.
func (as *ackableSubscriber) DeadLetterMessages() []DeadLetterMessage {
	if as.dlq == nil {
		return nil
	}
	return as.dlq.List()
}

// ReprocessDeadLetters republishes dead letter messages to their original topics.
// An optional filter selects which messages to reprocess; nil reprocesses all.
// Returns the number of successfully reprocessed messages.
func (as *ackableSubscriber) ReprocessDeadLetters(filter func(DeadLetterMessage) bool) int {
	if as.dlq == nil {
		return 0
	}
	return as.dlq.Reprocess(as.ps, filter)
}

// ClearDeadLetters removes all messages from the dead letter queue.
func (as *ackableSubscriber) ClearDeadLetters() {
	if as.dlq != nil {
		as.dlq.Clear()
	}
}
