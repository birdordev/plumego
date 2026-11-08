package mq

import (
	"context"
	"fmt"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

// PublishToCluster publishes a message to the cluster (replicates to other nodes).
func (b *InProcBroker) PublishToCluster(ctx context.Context, topic string, msg Message) error {
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

		// Check if cluster mode is enabled
		if !b.config.EnableCluster {
			return fmt.Errorf("%w: cluster mode is disabled", ErrClusterDisabled)
		}

		// Validate topic
		if err := validateTopic(topic); err != nil {
			return err
		}

		// Validate message
		if err := validateMessage(msg); err != nil {
			return err
		}

		// Check memory limit
		if err := b.checkMemoryLimit(); err != nil {
			return err
		}

		// Delegate to DistributedPubSub when available so the message
		// is broadcast to all cluster nodes, not just the local instance.
		if cp, ok := b.ps.(ClusterPublisher); ok {
			return cp.PublishGlobal(topic, msg)
		}

		// Fallback: local-only publish (no actual peer replication).
		return b.ps.Publish(topic, msg)
	})
}

// SubscribeFromCluster subscribes to messages from the cluster.
func (b *InProcBroker) SubscribeFromCluster(ctx context.Context, topic string, opts SubOptions) (Subscription, error) {
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

		// Check if cluster mode is enabled
		if !b.config.EnableCluster {
			return fmt.Errorf("%w: cluster mode is disabled", ErrClusterDisabled)
		}

		// Validate topic
		if err := validateTopic(topic); err != nil {
			return err
		}

		// Subscribe locally
		subscription, err := b.ps.Subscribe(context.Background(), topic, opts)
		if err != nil {
			return err
		}

		sub = subscription
		return nil
	})

	return sub, err
}

// RecoverMessages recovers persisted messages for a topic.
// This is useful for replaying messages after broker restart.
func (b *InProcBroker) RecoverMessages(ctx context.Context, topic string, limit int) ([]Message, error) {
	if b == nil || b.ps == nil {
		return nil, ErrNotInitialized
	}

	if !b.config.EnablePersistence {
		return nil, fmt.Errorf("persistence is not enabled")
	}

	if b.persistenceManager == nil {
		return nil, fmt.Errorf("%w: persistence manager not initialized", ErrNotInitialized)
	}

	return b.persistenceManager.loadMessages(ctx, topic, limit)
}

// ReplayMessages replays persisted messages to subscribers.
// This is useful for recovering messages after broker restart.
func (b *InProcBroker) ReplayMessages(ctx context.Context, topic string, limit int) error {
	messages, err := b.RecoverMessages(ctx, topic, limit)
	if err != nil {
		return err
	}

	// Republish each message
	for _, msg := range messages {
		if err := b.Publish(ctx, topic, msg); err != nil {
			return fmt.Errorf("failed to replay message %s: %w", msg.ID, err)
		}
	}

	return nil
}

// ConsumerGroupManager returns the consumer group manager for the underlying
// InProcPubSub backend. Returns nil when the backend does not support consumer
// groups (e.g. a custom pubsub.Broker implementation).
func (b *InProcBroker) ConsumerGroupManager() *pubsub.ConsumerGroupManager {
	if b == nil {
		return nil
	}
	return b.consumerGroupMgr
}
