package pubsub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Replay errors

// ReplayConfig configures the message replay store
type ReplayConfig struct {
	// Enabled enables message replay
	Enabled bool

	// MaxMessages is the maximum number of messages to retain in memory
	MaxMessages int

	// RetentionPeriod is how long to keep messages before archiving
	RetentionPeriod time.Duration

	// ArchiveEnabled enables archiving old messages to disk
	ArchiveEnabled bool

	// ArchiveDir stores archived messages. Its filesystem must support hard links
	// so a complete batch can be published without replacing another archive.
	ArchiveDir string

	// ArchiveCompression enables gzip compression for archives
	ArchiveCompression bool

	// ArchiveInterval is how often to run archiving
	ArchiveInterval time.Duration

	// TopicFilter restricts replay to specific topics (empty = all topics)
	TopicFilter []string

	// IncludeData whether to store full message data (may consume memory)
	IncludeData bool
}

// DefaultReplayConfig returns default replay configuration
func DefaultReplayConfig() ReplayConfig {
	return ReplayConfig{
		Enabled:            true,
		MaxMessages:        100000,
		RetentionPeriod:    24 * time.Hour,
		ArchiveEnabled:     true,
		ArchiveDir:         "./replay-archives",
		ArchiveCompression: true,
		ArchiveInterval:    1 * time.Hour,
		TopicFilter:        nil,
		IncludeData:        true,
	}
}

// ReplayMessage represents a stored message for replay
type ReplayMessage struct {
	ID        string
	Topic     string
	Data      any
	Timestamp time.Time
	Size      int
	Metadata  map[string]string

	// Replay metadata
	ReplayCount int
	LastReplay  time.Time
	IsArchived  bool
}

// ReplayQuery specifies query filters for message replay
type ReplayQuery struct {
	// Time range
	StartTime time.Time
	EndTime   time.Time

	// Topic filters
	Topics []string

	// Message IDs
	MessageIDs []string

	// Limit and offset for pagination
	Limit  int
	Offset int

	// Include archived messages
	IncludeArchived bool

	// Sort order (timestamp, topic, size)
	SortBy    string
	Ascending bool
}

// ReplayStats provides statistics about the replay store
type ReplayStats struct {
	TotalMessages    atomic.Uint64
	ArchivedMessages atomic.Uint64
	ReplayedMessages atomic.Uint64
	TotalSize        atomic.Uint64
	OldestMessage    time.Time
	NewestMessage    time.Time
	TopicCount       int
}

// ReplayStore manages message storage and replay.
// Expired messages remain queryable until their archive is written successfully.
// The next archive pass retries failures while those messages remain retained.
type ReplayStore struct {
	ps     *InProcBroker
	config ReplayConfig

	// messagesMu protects messages and both indexes.
	messages   map[string]*ReplayMessage
	messagesMu sync.RWMutex

	// Topic index
	topicIndex map[string][]string // topic -> message IDs

	// Time index (sorted by timestamp)
	timeIndex []*ReplayMessage

	// Archived messages
	archived   []*ReplayMessage
	archivedMu sync.RWMutex

	// Statistics
	stats ReplayStats

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed atomic.Bool
}

// NewReplayStore creates a new message replay store
func NewReplayStore(ps *InProcBroker, config ReplayConfig) (*ReplayStore, error) {
	if !config.Enabled {
		return nil, errors.New("replay is not enabled")
	}

	ctx, cancel := newBackgroundLifecycle()

	rs := &ReplayStore{
		ps:         ps,
		config:     config,
		messages:   make(map[string]*ReplayMessage),
		topicIndex: make(map[string][]string),
		timeIndex:  make([]*ReplayMessage, 0),
		archived:   make([]*ReplayMessage, 0),
		ctx:        ctx,
		cancel:     cancel,
	}

	// Create archive directory if needed
	if config.ArchiveEnabled {
		if err := os.MkdirAll(config.ArchiveDir, 0755); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create archive directory: %w", err)
		}
	}

	// Start background workers
	rs.wg.Add(1)
	go rs.archiveWorker()

	// Subscribe to all topics for capturing
	if err := rs.startCapturing(); err != nil {
		stopBackground(cancel, &rs.wg)
		return nil, err
	}

	return rs, nil
}

// startCapturing starts capturing published messages
func (rs *ReplayStore) startCapturing() error {
	// Subscribe to topics based on filter
	topics := rs.config.TopicFilter
	usePattern := false

	if len(topics) == 0 {
		// Subscribe to all topics using wildcard
		topics = []string{"*"}
		usePattern = true
	}

	for _, topic := range topics {
		var sub Subscription
		var err error

		if usePattern {
			sub, err = rs.ps.SubscribePattern(topic, SubOptions{
				BufferSize: 1000,
			})
		} else {
			sub, err = rs.ps.Subscribe(rs.ctx, topic, SubOptions{
				BufferSize: 1000,
			})
		}

		if err != nil {
			return err
		}

		// Start consumer
		rs.wg.Add(1)
		go func(s Subscription) {
			defer rs.wg.Done()
			for {
				select {
				case msg := <-s.C():
					rs.storeMessage(msg)
				case <-s.Done():
					return
				case <-rs.ctx.Done():
					s.Cancel()
					return
				}
			}
		}(sub)
	}

	return nil
}

// Replay publishes messages matching the query and returns the number published.
// It continues after a publish failure and reports the failure count in an error
// that wraps the first publish error. Retrying the same query can publish
// successful messages again.
func (rs *ReplayStore) Replay(query ReplayQuery) (int, error) {
	messages, err := rs.Query(query)
	if err != nil {
		return 0, err
	}

	replayed, failed := 0, 0
	var firstPublishErr error
	for _, msg := range messages {
		// Reconstruct original message
		original := Message{
			ID:    msg.ID,
			Topic: msg.Topic,
			Data:  msg.Data,
			Time:  msg.Timestamp,
		}

		if err := rs.ps.Publish(msg.Topic, original); err != nil {
			if firstPublishErr == nil {
				firstPublishErr = err
			}
			failed++
			continue
		}

		rs.messagesMu.Lock()
		if stored, ok := rs.messages[msg.ID]; ok {
			stored.ReplayCount++
			stored.LastReplay = time.Now()
		}
		rs.messagesMu.Unlock()

		replayed++
		rs.stats.ReplayedMessages.Add(1)
	}

	if firstPublishErr != nil {
		return replayed, fmt.Errorf("replay: failed to publish %d of %d messages: %w", failed, len(messages), firstPublishErr)
	}
	return replayed, nil
}

// Stats returns replay store statistics.
func (rs *ReplayStore) Stats() *ReplayStats {
	stats := &ReplayStats{}
	stats.TotalMessages.Store(rs.stats.TotalMessages.Load())
	stats.ArchivedMessages.Store(rs.stats.ArchivedMessages.Load())
	stats.ReplayedMessages.Store(rs.stats.ReplayedMessages.Load())
	stats.TotalSize.Store(rs.stats.TotalSize.Load())

	// Calculate oldest and newest
	rs.messagesMu.RLock()
	if len(rs.timeIndex) > 0 {
		stats.OldestMessage = rs.timeIndex[0].Timestamp
		stats.NewestMessage = rs.timeIndex[len(rs.timeIndex)-1].Timestamp
	}

	// Count topics
	stats.TopicCount = len(rs.topicIndex)
	rs.messagesMu.RUnlock()

	return stats
}

// Close stops the replay store
func (rs *ReplayStore) Close() error {
	if !rs.closed.CompareAndSwap(false, true) {
		return nil
	}

	stopBackground(rs.cancel, &rs.wg)

	return nil
}
