package pubsub

import (
	"context"
	"strings"
	"time"
)

// Publish sends a message to a topic.
func (b *InProcBroker) Publish(topic string, msg Message) error {
	return b.publishInternal(context.Background(), topic, msg, false)
}

// PublishWithContext publishes a message with context support.
func (b *InProcBroker) PublishWithContext(ctx context.Context, topic string, msg Message) error {
	return b.publishInternal(ctx, topic, msg, false)
}

// PublishAsync publishes without waiting for delivery (fire-and-forget).
func (b *InProcBroker) PublishAsync(topic string, msg Message) error {
	return b.publishInternal(context.Background(), topic, msg, true)
}

// PublishBatch publishes messages to a topic in slice order without modifying
// the caller's message envelopes. Each delivery uses the subscriber's policy.
func (b *InProcBroker) PublishBatch(topic string, msgs []Message) error {
	if b.closed.Load() {
		return newErr(ErrCodeClosed, "publish_batch", topic, "broker is closed", nil)
	}

	topic, err := normalizePublishTopic(topic, "publish_batch")
	if err != nil {
		return err
	}

	subs := b.shards.getTopicSubscribersIfAny(topic)
	var patterns []patternSnapshot
	if b.shards.hasAnyPatterns() {
		patterns = b.shards.getPatternCandidates(topic)
	}
	var mqttSubs []*subscriber
	if b.shards.hasAnyMQTTPatterns() {
		mqttSubs = b.shards.getMQTTPatternMatches(topic)
	}

	b.drainWg.Add(1)
	defer b.drainWg.Done()

	bgCtx := context.Background()
	for _, msg := range msgs {
		msg.Topic = topic
		if msg.Time.IsZero() {
			msg.Time = time.Now().UTC()
		}
		clonedMsg := cloneMessage(msg)
		b.metrics.incPublish(topic)
		b.notifyPublish(topic, &clonedMsg)

		for _, s := range subs {
			b.deliver(bgCtx, s, clonedMsg)
		}
		if len(patterns) > 0 {
			b.deliverToPatterns(bgCtx, patterns, topic, clonedMsg)
		}
		for _, s := range mqttSubs {
			b.deliver(bgCtx, s, clonedMsg)
		}
	}

	return nil
}

// PublishMulti publishes messages one topic at a time. An error can occur after
// other topics have already received messages.
func (b *InProcBroker) PublishMulti(msgs map[string][]Message) error {
	if b.closed.Load() {
		return newErr(ErrCodeClosed, "publish_multi", "", "broker is closed", nil)
	}

	for topic, topicMsgs := range msgs {
		if err := b.PublishBatch(topic, topicMsgs); err != nil {
			return err
		}
	}

	return nil
}

// publishInternal is the shared implementation for Publish and PublishAsync.
func (b *InProcBroker) publishInternal(ctx context.Context, topic string, msg Message, async bool) error {
	start := time.Now()
	var err error
	defer func() {
		op := "publish"
		if async {
			op = "publish_async"
		}
		b.recordMetrics(ctx, op, topic, time.Since(start), err)
	}()

	if ctx.Err() != nil {
		err = ctx.Err()
		return err
	}

	if b.closed.Load() {
		err = newErr(ErrCodeClosed, "publish", topic, "broker is closed", nil)
		return err
	}

	topic, err = normalizePublishTopic(topic, "publish")
	if err != nil {
		return err
	}

	msg.Topic = topic
	if msg.Time.IsZero() {
		msg.Time = time.Now().UTC()
	}
	msg = cloneMessage(msg)

	b.metrics.incPublish(topic)
	b.notifyPublish(topic, &msg)

	if async {
		b.drainWg.Add(1)
		submitted := b.workerPool.SubmitWithContext(ctx, func() {
			defer b.drainWg.Done()
			b.doPublish(ctx, topic, msg)
		})
		if !submitted {
			b.drainWg.Done()
			if ctx.Err() != nil {
				err = ctx.Err()
				return err
			}
			b.doPublish(ctx, topic, msg)
		}
	} else {
		b.drainWg.Add(1)
		defer b.drainWg.Done()
		b.doPublish(ctx, topic, msg)
	}

	return nil
}

func normalizePublishTopic(topic, op string) (string, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "", newErr(ErrCodeInvalidTopic, op, "", "topic must not be empty", nil)
	}
	return topic, nil
}
