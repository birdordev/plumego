package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spcent/plumego/x/data/kvengine"
)

// PersistenceBackend defines the interface for message persistence.
type PersistenceBackend interface {
	// SaveMessage persists a message to the backend
	SaveMessage(ctx context.Context, topic string, msg Message) error

	// GetMessages retrieves messages from a topic
	GetMessages(ctx context.Context, topic string, limit int) ([]Message, error)

	// DeleteMessage removes a message from persistence
	DeleteMessage(ctx context.Context, topic string, messageID string) error

	// SaveSubscription persists subscription metadata
	SaveSubscription(ctx context.Context, topic string, subID string, opts SubOptions) error

	// DeleteSubscription removes subscription metadata
	DeleteSubscription(ctx context.Context, topic string, subID string) error

	// GetSubscriptions retrieves all subscriptions for a topic
	GetSubscriptions(ctx context.Context, topic string) (map[string]SubOptions, error)

	// SaveAckState persists acknowledgment state
	SaveAckState(ctx context.Context, topic string, messageID string, acked bool) error

	// GetAckState retrieves acknowledgment state
	GetAckState(ctx context.Context, topic string, messageID string) (bool, error)

	// Close closes the persistence backend
	Close() error
}

// KVPersistence implements PersistenceBackend using store/kv.
type KVPersistence struct {
	store  *kvengine.KVStore
	mu     sync.RWMutex
	closed bool
}

// NewKVPersistence creates a new KV-based persistence backend.
func NewKVPersistence(dataDir string) (*KVPersistence, error) {
	opts := kvengine.Options{
		DataDir:           dataDir,
		MaxEntries:        100000,
		MaxMemoryMB:       500,
		FlushInterval:     100 * time.Millisecond,
		CleanInterval:     30 * time.Second,
		ShardCount:        16,
		EnableCompression: true,
		SerializerFormat:  kvengine.FormatBinary,
		AutoDetectMode:    kvengine.AutoDetectEnabled,
	}

	store, err := kvengine.NewKVStore(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV store: %w", err)
	}

	return &KVPersistence{
		store: store,
	}, nil
}

// SaveMessage persists a message to the backend.
func (p *KVPersistence) SaveMessage(ctx context.Context, topic string, msg Message) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return ErrBrokerClosed
	}

	// Serialize message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Generate key: msg:<topic>:<message-id>
	key := fmt.Sprintf("msg:%s:%s", topic, msg.ID)

	// Store with TTL (default 7 days)
	ttl := 7 * 24 * time.Hour

	return p.store.Set(key, data, ttl)
}

// GetMessages retrieves messages from a topic.
func (p *KVPersistence) GetMessages(ctx context.Context, topic string, limit int) ([]Message, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return nil, ErrBrokerClosed
	}

	// Get all keys and filter by prefix
	prefix := fmt.Sprintf("msg:%s:", topic)
	allKeys := p.store.Keys()

	// Filter keys with matching prefix
	var matchingKeys []string
	for _, key := range allKeys {
		if strings.HasPrefix(key, prefix) {
			matchingKeys = append(matchingKeys, key)
		}
	}

	// Limit the number of messages
	if limit > 0 && len(matchingKeys) > limit {
		matchingKeys = matchingKeys[:limit]
	}

	messages := make([]Message, 0, len(matchingKeys))
	for _, key := range matchingKeys {
		data, err := p.store.Get(key)
		if err != nil {
			if errors.Is(err, kvengine.ErrKeyNotFound) || errors.Is(err, kvengine.ErrKeyExpired) {
				continue
			}
			return nil, fmt.Errorf("failed to get message %s: %w", key, err)
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue // Skip invalid messages
		}

		messages = append(messages, msg)
	}

	return messages, nil
}

// DeleteMessage removes a message from persistence.
func (p *KVPersistence) DeleteMessage(ctx context.Context, topic string, messageID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return ErrBrokerClosed
	}

	key := fmt.Sprintf("msg:%s:%s", topic, messageID)
	return p.store.Delete(key)
}

// SaveSubscription persists subscription metadata.
func (p *KVPersistence) SaveSubscription(ctx context.Context, topic string, subID string, opts SubOptions) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return ErrBrokerClosed
	}

	// Serialize subscription options
	data, err := json.Marshal(opts)
	if err != nil {
		return fmt.Errorf("failed to marshal subscription: %w", err)
	}

	// Generate key: sub:<topic>:<sub-id>
	key := fmt.Sprintf("sub:%s:%s", topic, subID)

	// No TTL for subscriptions (persist indefinitely)
	return p.store.Set(key, data, 0)
}

// DeleteSubscription removes subscription metadata.
func (p *KVPersistence) DeleteSubscription(ctx context.Context, topic string, subID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return ErrBrokerClosed
	}

	key := fmt.Sprintf("sub:%s:%s", topic, subID)
	return p.store.Delete(key)
}

// GetSubscriptions retrieves all subscriptions for a topic.
func (p *KVPersistence) GetSubscriptions(ctx context.Context, topic string) (map[string]SubOptions, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return nil, ErrBrokerClosed
	}

	// Get all keys and filter by prefix
	prefix := fmt.Sprintf("sub:%s:", topic)
	allKeys := p.store.Keys()

	subscriptions := make(map[string]SubOptions)
	for _, key := range allKeys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		data, err := p.store.Get(key)
		if err != nil {
			if errors.Is(err, kvengine.ErrKeyNotFound) || errors.Is(err, kvengine.ErrKeyExpired) {
				continue
			}
			return nil, fmt.Errorf("failed to get subscription %s: %w", key, err)
		}

		var opts SubOptions
		if err := json.Unmarshal(data, &opts); err != nil {
			continue // Skip invalid subscriptions
		}

		// Extract subID from key (sub:<topic>:<sub-id>)
		parts := strings.Split(key, ":")
		if len(parts) >= 3 {
			subID := parts[2]
			subscriptions[subID] = opts
		}
	}

	return subscriptions, nil
}

// SaveAckState persists acknowledgment state.
func (p *KVPersistence) SaveAckState(ctx context.Context, topic string, messageID string, acked bool) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return ErrBrokerClosed
	}

	// Generate key: ack:<topic>:<message-id>
	key := fmt.Sprintf("ack:%s:%s", topic, messageID)

	if acked {
		// Store ack state with TTL (1 hour)
		data := []byte{1}
		return p.store.Set(key, data, 1*time.Hour)
	}

	// Delete if not acked (rollback)
	return p.store.Delete(key)
}

// GetAckState retrieves acknowledgment state.
func (p *KVPersistence) GetAckState(ctx context.Context, topic string, messageID string) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return false, ErrBrokerClosed
	}

	key := fmt.Sprintf("ack:%s:%s", topic, messageID)
	_, err := p.store.Get(key)
	if err != nil {
		if errors.Is(err, kvengine.ErrKeyNotFound) || errors.Is(err, kvengine.ErrKeyExpired) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// Close closes the persistence backend.
func (p *KVPersistence) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	p.closed = true
	return p.store.Close()
}

// persistenceManager manages the persistence backend for the broker.
type persistenceManager struct {
	backend PersistenceBackend
	broker  *InProcBroker
	mu      sync.RWMutex
	closed  bool
}

// newPersistenceManager creates a new persistence manager.
func newPersistenceManager(broker *InProcBroker, backend PersistenceBackend) *persistenceManager {
	return &persistenceManager{
		backend: backend,
		broker:  broker,
	}
}

// saveMessage persists a message.
func (pm *persistenceManager) saveMessage(ctx context.Context, topic string, msg Message) error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.closed {
		return ErrBrokerClosed
	}

	return pm.backend.SaveMessage(ctx, topic, msg)
}

// loadMessages loads messages from persistence for recovery.
func (pm *persistenceManager) loadMessages(ctx context.Context, topic string, limit int) ([]Message, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.closed {
		return nil, ErrBrokerClosed
	}

	return pm.backend.GetMessages(ctx, topic, limit)
}

// close shuts down the persistence manager.
func (pm *persistenceManager) close() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.closed {
		return nil
	}

	pm.closed = true
	if pm.backend != nil {
		return pm.backend.Close()
	}

	return nil
}
