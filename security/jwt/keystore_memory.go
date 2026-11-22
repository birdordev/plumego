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
	mu           sync.RWMutex
	entries      map[string]memEntry
	keys         []string
	keysExpireAt time.Time
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
// If ctx is canceled before the read starts, including while waiting for the
// read lock, GetContext returns the context error and no value.
func (s *InMemoryKeyStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	if err := contextErr(ctx); err != nil {
		s.mu.RUnlock()
		return nil, err
	}
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

// SetContext stores value for key. A positive ttl starts after the write lock
// is acquired and the context is checked.
// A ttl of zero or less keeps the entry indefinitely.
func (s *InMemoryKeyStore) SetContext(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextErr(ctx); err != nil {
		return err
	}
	var expiry time.Time
	if ttl > 0 {
		expiry = time.Now().Add(ttl)
	}
	if s.entries == nil {
		s.entries = make(map[string]memEntry)
	}
	s.keys = nil
	s.entries[key] = memEntry{value: append([]byte(nil), value...), expiry: expiry}
	return nil
}

// KeysContext returns all keys that are unexpired when the store is read.
// It uses the same time for all entries, sampled while holding the read lock.
// If ctx is canceled before the read starts, including while waiting for the
// read lock, KeysContext returns the context error and no keys.
func (s *InMemoryKeyStore) KeysContext(ctx context.Context) ([]string, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	if err := contextErr(ctx); err != nil {
		s.mu.RUnlock()
		return nil, err
	}
	now := time.Now()
	if s.keys != nil && (s.keysExpireAt.IsZero() || now.Before(s.keysExpireAt)) {
		keys := s.keys
		s.mu.RUnlock()
		return append([]string{}, keys...), nil
	}
	s.mu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshKeysLocked(ctx)
}

func (s *InMemoryKeyStore) refreshKeysLocked(ctx context.Context) ([]string, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	now := time.Now()
	if s.keys != nil && (s.keysExpireAt.IsZero() || now.Before(s.keysExpireAt)) {
		return append([]string{}, s.keys...), nil
	}
	var earliest time.Time
	// Buffer small live sets without reserving space for expired entries.
	var small [32]string
	var keys []string
	count, expired := 0, 0
	for k, e := range s.entries {
		if e.expiredAt(now) {
			expired++
			continue
		}
		if !e.expiry.IsZero() && (earliest.IsZero() || e.expiry.Before(earliest)) {
			earliest = e.expiry
		}
		if keys == nil {
			if count < len(small) {
				small[count] = k
				count++
				continue
			}
			// Only the entries not yet seen can need additional capacity.
			keys = make([]string, count, len(s.entries)-expired)
			copy(keys, small[:])
		}
		keys = append(keys, k)
	}
	if keys == nil {
		keys = append([]string{}, small[:count]...)
	}
	s.keys, s.keysExpireAt = keys, earliest
	return append([]string{}, keys...), nil
}
