package pubsub

import (
	"strings"
	"sync"
	"sync/atomic"
)

// shard represents a partition of topics/patterns.
// Each shard has its own lock to reduce contention.
type shard struct {
	mu           sync.RWMutex
	topics       map[string]map[uint64]*subscriber
	patterns     map[string]map[uint64]*subscriber
	mqttPatterns *mqttPatternSubscribers
}

// shardedMap manages multiple shards for reduced lock contention.
type shardedMap struct {
	shards     []*shard
	shardCount int
	shardMask  uint32

	// Atomic counters for fast existence checks without scanning all shards.
	patternCount     atomic.Int64
	mqttPatternCount atomic.Int64
}

// newShardedMap creates a new sharded map with the given number of shards.
// For best performance, shardCount should be a power of 2.
func newShardedMap(shardCount int) *shardedMap {
	if shardCount <= 0 {
		shardCount = 16
	}

	// Round up to the next power of 2
	shardCount = nextPowerOf2(shardCount)

	shards := make([]*shard, shardCount)
	for i := 0; i < shardCount; i++ {
		shards[i] = &shard{
			topics:       make(map[string]map[uint64]*subscriber),
			patterns:     make(map[string]map[uint64]*subscriber),
			mqttPatterns: newMQTTPatternSubscribers(),
		}
	}

	return &shardedMap{
		shards:     shards,
		shardCount: shardCount,
		shardMask:  uint32(shardCount - 1),
	}
}

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	return n + 1
}

// fnv32a computes an inline FNV-1a 32-bit hash of a string without allocating
// a hasher object or converting the string to []byte.
func fnv32a(key string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	h := uint32(offset32)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= prime32
	}
	return h
}

// getShard returns the shard for a given topic/pattern.
func (sm *shardedMap) getShard(key string) *shard {
	return sm.shards[fnv32a(key)&sm.shardMask]
}

// getShardIndex returns the shard index for a given topic/pattern.
func (sm *shardedMap) getShardIndex(key string) int {
	return int(fnv32a(key) & sm.shardMask)
}

// addTopic adds a subscriber to a topic in the appropriate shard.
func (sm *shardedMap) addTopic(topic string, id uint64, sub *subscriber) {
	s := sm.getShard(topic)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.topics[topic] == nil {
		s.topics[topic] = make(map[uint64]*subscriber)
	}
	s.topics[topic][id] = sub
}

// addPattern adds a subscriber to a pattern in the appropriate shard.
func (sm *shardedMap) addPattern(pattern string, id uint64, sub *subscriber) {
	s := sm.getShard(pattern)
	s.mu.Lock()
	isNew := s.patterns[pattern] == nil
	if isNew {
		s.patterns[pattern] = make(map[uint64]*subscriber)
	}
	s.patterns[pattern][id] = sub
	s.mu.Unlock()

	if isNew {
		sm.patternCount.Add(1)
	}
}

// removeTopic removes a subscriber from a topic.
// Returns true if the topic map is now empty.
func (sm *shardedMap) removeTopic(topic string, id uint64) bool {
	s := sm.getShard(topic)
	s.mu.Lock()
	defer s.mu.Unlock()

	subs := s.topics[topic]
	if subs == nil {
		return true
	}

	delete(subs, id)

	if len(subs) == 0 {
		delete(s.topics, topic)
		return true
	}
	return false
}

// removePattern removes a subscriber from a pattern.
// Returns true if the pattern map is now empty.
func (sm *shardedMap) removePattern(pattern string, id uint64) bool {
	s := sm.getShard(pattern)
	s.mu.Lock()

	subs := s.patterns[pattern]
	if subs == nil {
		s.mu.Unlock()
		return true
	}

	delete(subs, id)

	isEmpty := len(subs) == 0
	if isEmpty {
		delete(s.patterns, pattern)
	}
	s.mu.Unlock()

	if isEmpty {
		sm.patternCount.Add(-1)
	}
	return isEmpty
}

// addMQTTPattern adds a subscriber for an MQTT pattern in the appropriate shard.
func (sm *shardedMap) addMQTTPattern(pattern string, id uint64, sub *subscriber) {
	s := sm.getShard(pattern)
	s.mu.Lock()
	isNew := s.mqttPatterns.Count(pattern) == 0
	s.mqttPatterns.Add(pattern, id, sub)
	s.mu.Unlock()

	if isNew {
		sm.mqttPatternCount.Add(1)
	}
}

// removeMQTTPattern removes a subscriber from an MQTT pattern.
func (sm *shardedMap) removeMQTTPattern(pattern string, id uint64) bool {
	s := sm.getShard(pattern)
	s.mu.Lock()
	isEmpty := s.mqttPatterns.Remove(pattern, id)
	s.mu.Unlock()

	if isEmpty {
		sm.mqttPatternCount.Add(-1)
	}
	return isEmpty
}

// getMQTTPatternMatches returns all MQTT pattern subscribers that match a given topic.
// This scans all shards since MQTT patterns can be in any shard.
func (sm *shardedMap) getMQTTPatternMatches(topic string) []*subscriber {
	var result []*subscriber
	for _, s := range sm.shards {
		s.mu.RLock()
		matched := s.mqttPatterns.Match(topic)
		s.mu.RUnlock()
		result = append(result, matched...)
	}
	return result
}

// getMQTTPatternSubscriberCount returns the number of subscribers for an MQTT pattern.
func (sm *shardedMap) getMQTTPatternSubscriberCount(pattern string) int {
	s := sm.getShard(pattern)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mqttPatterns.Count(pattern)
}

// mqttPatternExists checks if an MQTT pattern has any subscribers.
func (sm *shardedMap) mqttPatternExists(pattern string) bool {
	s := sm.getShard(pattern)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mqttPatterns.Count(pattern) > 0
}

// hasAnyMQTTPatterns checks if any MQTT pattern subscriptions exist.
// This is an O(1) atomic read instead of scanning all shards.
func (sm *shardedMap) hasAnyMQTTPatterns() bool {
	return sm.mqttPatternCount.Load() > 0
}

// listMQTTPatterns returns all MQTT patterns with subscribers.
func (sm *shardedMap) listMQTTPatterns() []string {
	var patterns []string
	for _, s := range sm.shards {
		s.mu.RLock()
		patterns = append(patterns, s.mqttPatterns.List()...)
		s.mu.RUnlock()
	}
	return patterns
}

// getTopicSubscriberCount returns the number of subscribers for a topic.
func (sm *shardedMap) getTopicSubscriberCount(topic string) int {
	s := sm.getShard(topic)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.topics[topic])
}

// getPatternSubscriberCount returns the number of subscribers for a pattern.
func (sm *shardedMap) getPatternSubscriberCount(pattern string) int {
	s := sm.getShard(pattern)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.patterns[pattern])
}

// getAllPatterns returns all patterns and their subscribers across all shards.
// This requires locking all shards, so use sparingly.
func (sm *shardedMap) getAllPatterns() []patternSnapshot {
	// Collect from all shards
	var result []patternSnapshot

	for _, s := range sm.shards {
		s.mu.RLock()
		for pattern, subs := range s.patterns {
			if len(subs) == 0 {
				continue
			}
			snapshot := make([]*subscriber, 0, len(subs))
			for _, sub := range subs {
				snapshot = append(snapshot, sub)
			}
			result = append(result, patternSnapshot{
				pattern: pattern,
				isGlob:  strings.ContainsAny(pattern, "*?[]\\"),
				subs:    snapshot,
			})
		}
		s.mu.RUnlock()
	}

	return result
}

// listTopics returns all topics with subscribers.
func (sm *shardedMap) listTopics() []string {
	var topics []string

	for _, s := range sm.shards {
		s.mu.RLock()
		for topic := range s.topics {
			topics = append(topics, topic)
		}
		s.mu.RUnlock()
	}

	return topics
}

// listPatterns returns all patterns with subscribers.
func (sm *shardedMap) listPatterns() []string {
	var patterns []string

	for _, s := range sm.shards {
		s.mu.RLock()
		for pattern := range s.patterns {
			patterns = append(patterns, pattern)
		}
		s.mu.RUnlock()
	}

	return patterns
}

// collectAllSubscribers returns all subscribers from all shards.
// This is used during Close() to cancel all subscriptions.
func (sm *shardedMap) collectAllSubscribers() []*subscriber {
	var all []*subscriber

	for _, s := range sm.shards {
		s.mu.Lock()
		for _, subs := range s.topics {
			for _, sub := range subs {
				all = append(all, sub)
			}
		}
		for _, subs := range s.patterns {
			for _, sub := range subs {
				all = append(all, sub)
			}
		}
		all = append(all, s.mqttPatterns.All()...)
		// Clear maps
		s.topics = make(map[string]map[uint64]*subscriber)
		s.patterns = make(map[string]map[uint64]*subscriber)
		s.mqttPatterns.Clear()
		s.mu.Unlock()
	}

	// Reset atomic counters now that all maps are cleared.
	sm.patternCount.Store(0)
	sm.mqttPatternCount.Store(0)

	return all
}

// topicExists checks if a topic has any subscribers.
func (sm *shardedMap) topicExists(topic string) bool {
	s := sm.getShard(topic)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.topics[topic]) > 0
}

// getTopicSubscribersIfAny returns a subscriber snapshot in a single lock
// acquisition, combining the existence check and snapshot into one operation.
// Returns nil when there are no subscribers (avoids a second lock in callers
// that would otherwise call topicExists then getTopicSubscribers separately).
func (sm *shardedMap) getTopicSubscribersIfAny(topic string) []*subscriber {
	s := sm.getShard(topic)
	s.mu.RLock()
	defer s.mu.RUnlock()

	subs := s.topics[topic]
	if len(subs) == 0 {
		return nil
	}
	result := make([]*subscriber, 0, len(subs))
	for _, sub := range subs {
		result = append(result, sub)
	}
	return result
}

// patternExists checks if a pattern has any subscribers.
func (sm *shardedMap) patternExists(pattern string) bool {
	s := sm.getShard(pattern)
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.patterns[pattern]) > 0
}

// hasAnyPatterns checks if any glob pattern subscriptions exist.
// This is an O(1) atomic read instead of scanning all shards.
func (sm *shardedMap) hasAnyPatterns() bool {
	return sm.patternCount.Load() > 0
}

// ShardStat contains statistics for a single shard.
type ShardStat struct {
	// Index is the shard index.
	Index int `json:"index"`

	// TopicCount is the number of distinct topics in this shard.
	TopicCount int `json:"topic_count"`

	// PatternCount is the number of distinct patterns (glob + MQTT) in this shard.
	PatternCount int `json:"pattern_count"`

	// SubscriberCount is the total number of subscribers (topic + pattern + MQTT) in this shard.
	SubscriberCount int `json:"subscriber_count"`
}

// shardStats returns per-shard statistics across all shards.
func (sm *shardedMap) shardStats() []ShardStat {
	stats := make([]ShardStat, sm.shardCount)
	for i, s := range sm.shards {
		s.mu.RLock()
		subCount := 0
		for _, subs := range s.topics {
			subCount += len(subs)
		}
		for _, subs := range s.patterns {
			subCount += len(subs)
		}
		mqttPatternCount := len(s.mqttPatterns.patterns)
		mqttSubCount := len(s.mqttPatterns.All())
		subCount += mqttSubCount
		stats[i] = ShardStat{
			Index:           i,
			TopicCount:      len(s.topics),
			PatternCount:    len(s.patterns) + mqttPatternCount,
			SubscriberCount: subCount,
		}
		s.mu.RUnlock()
	}
	return stats
}

// topicShardMapping returns a map of active topic/pattern names to their shard indices.
func (sm *shardedMap) topicShardMapping() map[string]int {
	mapping := make(map[string]int)
	for _, s := range sm.shards {
		s.mu.RLock()
		for topic := range s.topics {
			mapping[topic] = sm.getShardIndex(topic)
		}
		for pattern := range s.patterns {
			mapping[pattern] = sm.getShardIndex(pattern)
		}
		for _, mqttPattern := range s.mqttPatterns.List() {
			mapping[mqttPattern] = sm.getShardIndex(mqttPattern)
		}
		s.mu.RUnlock()
	}
	return mapping
}
