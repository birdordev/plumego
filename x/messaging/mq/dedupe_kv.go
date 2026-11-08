package mq

import (
	"context"
	"errors"
	"strings"
	"time"

	kvstore "github.com/birdordev/plumego/store/kv"
)

// KVDeduperConfig configures the KV-backed task deduper.
type KVDeduperConfig struct {
	Prefix     string
	DefaultTTL time.Duration
}

// KVDeduper is a TaskDeduper backed by store/kv with TTL-based entries.
type KVDeduper struct {
	store      *kvstore.KVStore
	prefix     string
	defaultTTL time.Duration
}

// NewKVDeduper creates a KV-backed deduper.
// If DefaultTTL is not set, it defaults to 24 hours.
func NewKVDeduper(store *kvstore.KVStore, cfg KVDeduperConfig) *KVDeduper {
	ttl := cfg.DefaultTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &KVDeduper{
		store:      store,
		prefix:     normalizePrefix(cfg.Prefix),
		defaultTTL: ttl,
	}
}

// IsCompleted checks whether the key has already been processed.
func (d *KVDeduper) IsCompleted(ctx context.Context, key string) (bool, error) {
	if d == nil || d.store == nil {
		return false, ErrNotInitialized
	}

	dedupeKey := d.buildKey(key)
	if dedupeKey == "" {
		return false, ErrInvalidConfig
	}

	_, err := d.store.Get(dedupeKey)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, kvstore.ErrKeyNotFound) || errors.Is(err, kvstore.ErrKeyExpired) {
		return false, nil
	}
	return false, err
}

// MarkCompleted marks the key as processed with a TTL.
func (d *KVDeduper) MarkCompleted(ctx context.Context, key string, ttl time.Duration) error {
	if d == nil || d.store == nil {
		return ErrNotInitialized
	}

	dedupeKey := d.buildKey(key)
	if dedupeKey == "" {
		return ErrInvalidConfig
	}

	if ttl <= 0 {
		ttl = d.defaultTTL
	}

	return d.store.Set(dedupeKey, []byte{1}, ttl)
}

func (d *KVDeduper) buildKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if d.prefix == "" {
		return key
	}
	return d.prefix + key
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	if strings.HasSuffix(prefix, ":") {
		return prefix
	}
	return prefix + ":"
}
