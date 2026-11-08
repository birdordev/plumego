package mq

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

// Configuration constants
const (
	// DefaultBufferSize is the default buffer size for new subscriptions.
	DefaultBufferSize = 16

	// MaxTopicLength is the maximum length of a topic name in characters.
	MaxTopicLength = 1024

	// DefaultHealthCheckInterval is the default interval for health checks.
	DefaultHealthCheckInterval = 30 * time.Second

	// DefaultAckTimeoutDuration is the default timeout for message acknowledgment.
	DefaultAckTimeoutDuration = 30 * time.Second

	// DefaultClusterSyncInterval is the default interval for cluster state synchronization.
	DefaultClusterSyncInterval = 5 * time.Second

	// DefaultTransactionTimeoutDuration is the default timeout for transactions.
	DefaultTransactionTimeoutDuration = 30 * time.Second
)

// Message re-exports pubsub.Message for broker compatibility.
type Message = pubsub.Message

// SubOptions re-exports pubsub.SubOptions for broker compatibility.
type SubOptions = pubsub.SubOptions

// Subscription re-exports pubsub.Subscription for broker compatibility.
type Subscription = pubsub.Subscription

// Operation labels broker actions for observability.
type Operation string

const (
	OpPublish   Operation = "publish"
	OpSubscribe Operation = "subscribe"
	OpClose     Operation = "close"
	OpMetrics   Operation = "metrics"
	OpAck       Operation = "ack"
	OpNack      Operation = "nack"
)

// Metrics captures timing and error information for broker actions.
type Metrics struct {
	Operation Operation
	Topic     string
	Duration  time.Duration
	Err       error
	Panic     bool
}

// MetricsObserver can be plugged into the broker to observe activity.
type MetricsObserver interface {
	ObserveMQ(ctx context.Context, operation, topic string, duration time.Duration, err error, panicked bool)
}

// PanicHandler is invoked when a broker operation panics.
type PanicHandler func(ctx context.Context, op Operation, recovered any)

// Broker defines the interface for message queue backends.
type Broker interface {
	Publish(ctx context.Context, topic string, msg Message) error
	Subscribe(ctx context.Context, topic string, opts SubOptions) (Subscription, error)
	Close() error
}

// ClusterPublisher is an optional interface that pubsub implementations may
// satisfy to enable cluster-wide message publishing. DistributedPubSub from
// the pubsub package implements this interface.
type ClusterPublisher interface {
	PublishGlobal(topic string, msg Message) error
}

// DefaultSubOptions exposes the default subscription settings.
var DefaultSubOptions = pubsub.DefaultSubOptions
