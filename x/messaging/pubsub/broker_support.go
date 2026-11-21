package pubsub

import (
	"context"
	"time"
)

// normalizeSubOptions fills in defaults from the broker config.
func (b *InProcBroker) normalizeSubOptions(opts SubOptions) SubOptions {
	if opts.BufferSize <= 0 {
		opts.BufferSize = b.config.DefaultBufferSize
	}

	switch opts.Policy {
	case DropOldest, DropNewest, BlockWithTimeout, CloseSubscriber:
	default:
		opts.Policy = b.config.DefaultPolicy
	}

	if opts.Policy == BlockWithTimeout && opts.BlockTimeout <= 0 {
		opts.BlockTimeout = b.config.DefaultBlockTimeout
	}

	// Ring buffer is the default for DropOldest subscribers.
	if opts.Policy == DropOldest {
		opts.UseRingBuffer = true
	}

	return opts
}

// Snapshot returns a point-in-time view of pubsub metrics.
func (b *InProcBroker) Snapshot() MetricsSnapshot {
	return b.metrics.Snapshot()
}

// SetMetricsObserver replaces the external metrics sink at runtime. A call
// that has already selected an observer can finish using that observer.
func (b *InProcBroker) SetMetricsObserver(observer MetricsObserver) {
	b.observerMu.Lock()
	b.config.MetricsObserver = observer
	b.observerMu.Unlock()
}

// Config returns a copy of the broker's configuration.
func (b *InProcBroker) Config() Config {
	b.observerMu.RLock()
	defer b.observerMu.RUnlock()
	return b.config
}

// TopicShard returns the shard index for the given topic/pattern.
func (b *InProcBroker) TopicShard(topic string) int {
	return b.shards.getShardIndex(topic)
}

// ShardStats returns per-shard statistics.
func (b *InProcBroker) ShardStats() []ShardStat {
	return b.shards.shardStats()
}

// TopicShardMapping returns topic→shard-index mapping for all active topics.
func (b *InProcBroker) TopicShardMapping() map[string]int {
	return b.shards.topicShardMapping()
}

// recordMetrics forwards an operation observation to the external collector, if any.
func (b *InProcBroker) recordMetrics(ctx context.Context, operation, topic string, duration time.Duration, err error) {
	b.observerMu.RLock()
	observer := b.config.MetricsObserver
	b.observerMu.RUnlock()
	if observer != nil {
		observer.ObservePubSub(ctx, operation, topic, duration, err)
	}
}

// --- Observer notification helpers ---

func (b *InProcBroker) notifyPublish(topic string, msg *Message) {
	for _, o := range b.config.observers {
		o.OnPublish(topic, msg)
	}
}

func (b *InProcBroker) notifySubscribe(topic string, subID uint64) {
	for _, o := range b.config.observers {
		o.OnSubscribe(topic, subID)
	}
}

func (b *InProcBroker) notifyUnsubscribe(topic string, subID uint64) {
	for _, o := range b.config.observers {
		o.OnUnsubscribe(topic, subID)
	}
}

func (b *InProcBroker) notifyDeliver(topic string, subID uint64, msg *Message) {
	for _, o := range b.config.observers {
		o.OnDeliver(topic, subID, msg)
	}
}

func (b *InProcBroker) notifyDrop(topic string, subID uint64, msg *Message, policy BackpressurePolicy) {
	for _, o := range b.config.observers {
		o.OnDrop(topic, subID, msg, policy)
	}
}
