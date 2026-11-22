package pubsub

import (
	"context"
	"path"
	"sort"
)

// doPublish performs the actual fan-out delivery.
func (b *InProcBroker) doPublish(ctx context.Context, topic string, msg Message) {
	if ctx.Err() != nil {
		return
	}

	hasPatternSubs := b.shards.hasAnyPatterns()
	hasMQTTSubs := b.shards.hasAnyMQTTPatterns()

	subs := b.shards.getTopicSubscribersIfAny(topic)
	if subs == nil && !hasPatternSubs && !hasMQTTSubs {
		return
	}

	for _, s := range subs {
		if ctx.Err() != nil {
			return
		}
		b.deliver(ctx, s, msg)
	}

	if hasPatternSubs {
		b.deliverToPatterns(ctx, b.shards.getPatternCandidates(topic), topic, msg)
	}

	if hasMQTTSubs {
		for _, s := range b.shards.getMQTTPatternMatches(topic) {
			if ctx.Err() != nil {
				return
			}
			b.deliver(ctx, s, msg)
		}
	}
}

// deliverToPatterns delivers a message to all matching glob-pattern subscribers.
func (b *InProcBroker) deliverToPatterns(ctx context.Context, patterns []patternSnapshot, topic string, msg Message) {
	for _, entry := range patterns {
		if ctx.Err() != nil {
			return
		}

		var matched bool
		if entry.isGlob {
			var matchErr error
			matched, matchErr = path.Match(entry.pattern, topic)
			if matchErr != nil {
				matched = false
			}
		} else {
			matched = entry.pattern == topic
		}

		if matched {
			for _, s := range entry.subs {
				if ctx.Err() != nil {
					return
				}
				b.deliver(ctx, s, msg)
			}
		}
	}
}

// GetSubscriberCount returns the number of exact-topic subscribers.
func (b *InProcBroker) GetSubscriberCount(topic string) int {
	return b.shards.getTopicSubscriberCount(topic)
}

// GetPatternSubscriberCount returns the number of glob-pattern subscribers.
func (b *InProcBroker) GetPatternSubscriberCount(pattern string) int {
	return b.shards.getPatternSubscriberCount(pattern)
}

// GetMQTTSubscriberCount returns the number of MQTT-pattern subscribers.
func (b *InProcBroker) GetMQTTSubscriberCount(pattern string) int {
	return b.shards.getMQTTPatternSubscriberCount(pattern)
}

// TopicExists reports whether the topic has any active exact subscribers.
func (b *InProcBroker) TopicExists(topic string) bool {
	return b.shards.topicExists(topic)
}

// PatternExists reports whether the glob pattern has any active subscribers.
func (b *InProcBroker) PatternExists(pattern string) bool {
	return b.shards.patternExists(pattern)
}

// MQTTPatternExists reports whether the MQTT pattern has any active subscribers.
func (b *InProcBroker) MQTTPatternExists(pattern string) bool {
	return b.shards.mqttPatternExists(pattern)
}

// HasSubscribers reports whether publishing to topic would reach any subscriber
// (exact, glob-pattern, or MQTT-pattern).
func (b *InProcBroker) HasSubscribers(topic string) bool {
	if b.shards.topicExists(topic) {
		return true
	}
	if b.shards.hasAnyPatterns() {
		for _, entry := range b.shards.getPatternCandidates(topic) {
			if entry.isGlob {
				if matched, err := path.Match(entry.pattern, topic); err == nil && matched {
					return true
				}
			} else if entry.pattern == topic {
				return true
			}
		}
	}
	if b.shards.hasAnyMQTTPatterns() {
		if len(b.shards.getMQTTPatternMatches(topic)) > 0 {
			return true
		}
	}
	return false
}

// ListTopics returns all active exact-topic names, sorted.
func (b *InProcBroker) ListTopics() []string {
	topics := b.shards.listTopics()
	sort.Strings(topics)
	return topics
}

// ListPatterns returns all active glob-pattern subscriptions, sorted.
func (b *InProcBroker) ListPatterns() []string {
	patterns := b.shards.listPatterns()
	sort.Strings(patterns)
	return patterns
}

// ListMQTTPatterns returns all active MQTT-pattern subscriptions, sorted.
func (b *InProcBroker) ListMQTTPatterns() []string {
	patterns := b.shards.listMQTTPatterns()
	sort.Strings(patterns)
	return patterns
}

// removeSubscriber removes a subscription from the sharded map.
func (b *InProcBroker) removeSubscriber(topic string, id uint64, kind subKind) {
	switch kind {
	case subKindPattern:
		b.shards.removePattern(topic, id)
	case subKindMQTT:
		b.shards.removeMQTTPattern(topic, id)
	default:
		b.shards.removeTopic(topic, id)
	}
	b.metrics.addSubs(topic, -1)
}
