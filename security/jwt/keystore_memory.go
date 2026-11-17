package jwt

import (
	"context"
	"sync"
	"time"
)

// InMemoryKeyStore is a thread-safe, non-persistent KeyStore suitable for
// development, testing, and single-process deployments that do not require
// key durability across restarts. The zero value is ready to use.
//
// A positive TTL limits how long a key can be read or listed. An expired
// entry stays in memory until it is overwritten or the store is collected.
//
// For production deployments that need key durability or multi-instance
// coordination, use a persistent store (e.g. a database or distributed cache)
// that implements the KeyStore interface.
//
// Example:
//
//	store := jwt.NewInMemoryKeyStore()
//	manager, err := jwt.NewJWTManager(store, jwt.DefaultJWTConfig())
type InMemoryKeyStore struct {
	mu      sync.RWMutex
	entries map[string]memEntry
}

type memEntry struct {
	value  []byte
	expiry time.Time // zero means no expiry
}

func (e memEntry) expiredAt(now time.Time) bool {
	return !e.expiry.IsZero() && !now.Before(e.expiry)
}

// NewInMemoryKeyStore returns an initialised InMemoryKeyStore.
func NewInMemoryKeyStore() *InMemoryKeyStore {
	return &InMemoryKeyStore{entries: make(map[string]memEntry)}
}

// GetContext retrieves the value for key. It returns ErrUnknownKey if the key
// does not exist and ErrKeyExpired at or after the key's expiration time.
func (s *InMemoryKeyStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	e, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownKey
	}
	if e.expiredAt(time.Now()) {
		return nil, ErrKeyExpired
	}
	return append([]byte(nil), e.value...), nil
}

// SetContext stores value for key. A positive ttl sets the expiration time.
// A ttl of zero or less keeps the entry indefinitely.
func (s *InMemoryKeyStore) SetContext(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	var expiry time.Time
	if ttl > 0 {
		expiry = time.Now().Add(ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s.entries == nil {
		s.entries = make(map[string]memEntry)
	}
	s.entries[key] = memEntry{value: append([]byte(nil), value...), expiry: expiry}
	return nil
}

// KeysContext returns all non-expired keys currently in the store.
func (s *InMemoryKeyStore) KeysContext(ctx context.Context) ([]string, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	now := time.Now()
	s.mu.RLock()
	keys := make([]string, 0, len(s.entries))
	for k, e := range s.entries {
		if !e.expiredAt(now) {
			keys = append(keys, k)
		}
	}
	s.mu.RUnlock()
	return keys, nil
}
