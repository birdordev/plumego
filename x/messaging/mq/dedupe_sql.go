package mq

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/birdordev/plumego/x/data/idempotency"
)

// SQLDeduperConfig configures the SQL-backed task deduper.
type SQLDeduperConfig struct {
	Dialect     idempotency.Dialect
	Table       string
	Prefix      string
	DefaultTTL  time.Duration
	RequestHash string
	Now         func() time.Time
}

// SQLDeduper is a TaskDeduper backed by a SQL table (idempotency_keys by default).
type SQLDeduper struct {
	store       *idempotency.SQLStore
	prefix      string
	defaultTTL  time.Duration
	requestHash string
	now         func() time.Time
}

// NewSQLDeduper creates a SQL-backed deduper.
// It uses the idempotency_keys table by default.
func NewSQLDeduper(db *sql.DB, cfg SQLDeduperConfig) *SQLDeduper {
	idemCfg := idempotency.DefaultSQLConfig()
	if cfg.Dialect != "" {
		idemCfg.Dialect = cfg.Dialect
	}
	if strings.TrimSpace(cfg.Table) != "" {
		idemCfg.Table = strings.TrimSpace(cfg.Table)
	}
	if cfg.Now != nil {
		idemCfg.Now = cfg.Now
	}

	ttl := cfg.DefaultTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	hash := strings.TrimSpace(cfg.RequestHash)
	if hash == "" {
		hash = "mq_task_dedupe"
	}

	return &SQLDeduper{
		store:       idempotency.NewSQLStore(db, idemCfg),
		prefix:      normalizePrefix(cfg.Prefix),
		defaultTTL:  ttl,
		requestHash: hash,
		now:         idemCfg.Now,
	}
}

// IsCompleted checks whether the key has already been processed.
func (d *SQLDeduper) IsCompleted(ctx context.Context, key string) (bool, error) {
	if d == nil || d.store == nil {
		return false, ErrNotInitialized
	}

	dedupeKey := d.buildKey(key)
	if dedupeKey == "" {
		return false, ErrInvalidConfig
	}

	rec, found, err := d.store.Get(ctx, dedupeKey)
	if err != nil {
		if errors.Is(err, idempotency.ErrInvalidKey) {
			return false, err
		}
		return false, err
	}
	if !found {
		return false, nil
	}
	return rec.Status == idempotency.StatusCompleted, nil
}

// MarkCompleted marks the key as processed with a TTL.
func (d *SQLDeduper) MarkCompleted(ctx context.Context, key string, ttl time.Duration) error {
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

	now := time.Now().UTC()
	if d.now != nil {
		now = d.now().UTC()
	}

	record := idempotency.Record{
		Key:         dedupeKey,
		RequestHash: d.requestHash,
		Status:      idempotency.StatusCompleted,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if ttl > 0 {
		record.ExpiresAt = now.Add(ttl)
	}

	_, err := d.store.PutIfAbsent(ctx, record)
	return err
}

func (d *SQLDeduper) buildKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if d.prefix == "" {
		return key
	}
	return d.prefix + key
}
