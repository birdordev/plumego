package pubsub

import "time"

// Message is treated as immutable once published.
// PubSub defensively copies Data/Meta on publish and per-delivery for common
// reference types (maps/slices) to avoid accidental mutation.
// Callers and subscribers should still treat Data/Meta as read-only.
//
// Example:
//
//	msg := pubsub.Message{
//		ID:   "msg-123",
//		Type: "user.created",
//		Data: map[string]any{"user_id": "123", "email": "user@example.com"},
//		Meta: map[string]string{"source": "api"},
//	}
type Message struct {
	// ID is a unique identifier for the message.
	ID string

	// Topic is the topic the message was published to.
	Topic string

	// Type is the optional event type discriminator.
	Type string

	// Time is when the message was created (set automatically on publish).
	Time time.Time

	// Data is the message payload.
	Data any

	// Meta contains arbitrary string metadata.
	Meta map[string]string
}

// BackpressurePolicy defines how a subscription handles a full buffer.
//
// Example:
//
//	opts := pubsub.SubOptions{
//		BufferSize: 16,
//		Policy:     pubsub.DropOldest,
//	}
type BackpressurePolicy int

const (
	// DropOldest drops the oldest buffered message to make room for the new one.
	// Keeps the most-recent messages; suitable for metrics and state updates.
	DropOldest BackpressurePolicy = iota

	// DropNewest discards the incoming message when the buffer is full.
	// Preserves the oldest messages; suitable for ordered event logs.
	DropNewest

	// BlockWithTimeout blocks the publisher until buffer space is available
	// or BlockTimeout elapses, then drops the message.
	BlockWithTimeout

	// CloseSubscriber closes the subscription when its buffer is full.
	// Use for fail-fast scenarios where a slow subscriber is unacceptable.
	CloseSubscriber
)

// String returns the canonical string representation of the policy.
func (p BackpressurePolicy) String() string {
	switch p {
	case DropOldest:
		return "drop_oldest"
	case DropNewest:
		return "drop_newest"
	case BlockWithTimeout:
		return "block_with_timeout"
	case CloseSubscriber:
		return "close_subscriber"
	default:
		return "unknown"
	}
}

// SubOptions configures a subscription.
//
// Example:
//
//	opts := pubsub.SubOptions{
//		BufferSize:   32,
//		Policy:       pubsub.BlockWithTimeout,
//		BlockTimeout: 100 * time.Millisecond,
//	}
type SubOptions struct {
	// BufferSize is the channel buffer size (default: 16).
	BufferSize int

	// Policy defines backpressure behaviour when the buffer is full.
	Policy BackpressurePolicy

	// BlockTimeout is used when Policy == BlockWithTimeout (default: 50ms).
	BlockTimeout time.Duration

	// ZeroCopy disables per-delivery message cloning.
	// Only set this if the subscriber guarantees read-only access to the message.
	ZeroCopy bool

	// Filter is an optional predicate applied before delivery.
	// Return false to skip the message for this subscriber.
	// Filter is called under the subscriber's delivery lock — keep it fast.
	//
	// Filter is a runtime-only predicate: it cannot be serialized, so
	// persistence snapshots silently drop it and restored subscriptions
	// deliver unfiltered until the caller re-subscribes with a Filter.
	Filter func(msg Message) bool `json:"-"`

	// UseRingBuffer enables O(1) ring-buffer delivery for DropOldest policy.
	// Only effective when Policy is DropOldest.
	UseRingBuffer bool
}

// SubscriptionStats contains live statistics for a subscription.
type SubscriptionStats struct {
	Received uint64
	Dropped  uint64
	QueueLen int
	QueueCap int
}

// Subscription represents an active subscription.
//
// Example:
//
//	sub, err := broker.Subscribe(ctx, "user.created", pubsub.DefaultSubOptions())
//	if err != nil {
//		// handle
//	}
//	defer sub.Cancel()
//
//	for msg := range sub.C() {
//		// process msg
//	}
type Subscription interface {
	// C returns the receive-only message channel.
	C() <-chan Message

	// Cancel unsubscribes and closes the channel.
	Cancel()

	// ID returns the unique subscription ID.
	ID() uint64

	// Topic returns the topic or pattern this subscription is for.
	Topic() string

	// Stats returns live subscription statistics.
	Stats() SubscriptionStats

	// Done returns a channel closed when the subscription is cancelled.
	Done() <-chan struct{}
}

// DefaultSubOptions returns production-ready default options.
func DefaultSubOptions() SubOptions {
	return SubOptions{
		BufferSize:   16,
		Policy:       DropOldest,
		BlockTimeout: 50 * time.Millisecond,
	}
}
