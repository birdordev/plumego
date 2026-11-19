package config

import (
	"container/list"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/tenant/core"
)

// DBTenantConfigManager loads tenant configurations from a database.
// It implements tenant.ConfigManager with optional LRU caching.
//
// SQL placeholders use the ? style (compatible with SQLite, MySQL).
// For PostgreSQL, use a driver that rebinds ? to $N (e.g. pgx with compatibility mode).
type DBTenantConfigManager struct {
	db    *sql.DB
	cache *tenantCache
}

// DBTenantConfigOption configures DBTenantConfigManager.
type DBTenantConfigOption func(*DBTenantConfigManager)

// WithTenantCache enables LRU caching with the specified capacity and TTL.
// The cache holds its own snapshots of tenant configurations.
func WithTenantCache(size int, ttl time.Duration) DBTenantConfigOption {
	return func(m *DBTenantConfigManager) {
		m.cache = newTenantCache(size, ttl)
	}
}

// NewDBTenantConfigManager creates a database-backed tenant config manager.
//
// Example:
//
//	db, _ := sql.Open("sqlite3", "./tenants.db")
//	manager := NewDBTenantConfigManager(db,
//	    WithTenantCache(1000, 5*time.Minute),
//	)
func NewDBTenantConfigManager(db *sql.DB, options ...DBTenantConfigOption) *DBTenantConfigManager {
	m := &DBTenantConfigManager{db: db}
	for _, opt := range options {
		opt(m)
	}
	return m
}

// GetTenantConfig retrieves tenant configuration from the database, with optional caching.
// A read overlapping a successful update or deletion can finish with its original
// result, but cannot repopulate the invalidated cache.
func (m *DBTenantConfigManager) GetTenantConfig(ctx context.Context, tenantID string) (tenant.Config, error) {
	if m == nil {
		return tenant.Config{}, tenant.ErrTenantNotFound
	}

	var generation uint64
	if m.cache != nil {
		cfg, ok, observed := m.cache.Get(tenantID)
		if ok {
			return cfg, nil
		}
		generation = observed
	}

	if m.db == nil {
		return tenant.Config{}, tenant.ErrTenantNotFound
	}

	row, err := scanTenantConfigRow(m.db.QueryRowContext(ctx, getTenantConfigQuery, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return tenant.Config{}, tenant.ErrTenantNotFound
	}
	if err != nil {
		return tenant.Config{}, err
	}

	cfg, err := row.config()
	if err != nil {
		return tenant.Config{}, err
	}

	if m.cache != nil {
		m.cache.setIfCurrent(tenantID, cfg, generation)
	}

	return cfg, nil
}

// SetTenantConfig inserts or updates tenant configuration in database.
func (m *DBTenantConfigManager) SetTenantConfig(ctx context.Context, cfg tenant.Config) error {
	if m == nil || m.db == nil {
		return errors.New("database connection not available")
	}

	quotaLimits, err := json.Marshal(cfg.Quota.Limits)
	if err != nil {
		return err
	}
	allowedModels, err := json.Marshal(cfg.Policy.AllowedModels)
	if err != nil {
		return err
	}
	allowedTools, err := json.Marshal(cfg.Policy.AllowedTools)
	if err != nil {
		return err
	}
	allowedMethods, err := json.Marshal(cfg.Policy.AllowedMethods)
	if err != nil {
		return err
	}
	allowedPaths, err := json.Marshal(cfg.Policy.AllowedPaths)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(cfg.Metadata)
	if err != nil {
		return err
	}

	_, err = m.db.ExecContext(ctx, upsertTenantConfigQuery,
		cfg.TenantID,
		string(quotaLimits),
		string(allowedModels),
		string(allowedTools),
		string(allowedMethods),
		string(allowedPaths),
		string(metadata),
		time.Now().UTC(),
	)
	if err != nil {
		return err
	}

	if m.cache != nil {
		m.cache.Delete(cfg.TenantID)
	}

	return nil
}

// DeleteTenantConfig removes a tenant configuration from database.
func (m *DBTenantConfigManager) DeleteTenantConfig(ctx context.Context, tenantID string) error {
	if m == nil || m.db == nil {
		return errors.New("database connection not available")
	}

	result, err := m.db.ExecContext(ctx, "DELETE FROM tenants WHERE id = ?", tenantID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return tenant.ErrTenantNotFound
	}

	if m.cache != nil {
		m.cache.Delete(tenantID)
	}

	return nil
}

// ListTenants returns a paginated list of tenant configurations.
func (m *DBTenantConfigManager) ListTenants(ctx context.Context, limit, offset int) ([]tenant.Config, error) {
	if m == nil || m.db == nil {
		return nil, errors.New("database connection not available")
	}

	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := m.db.QueryContext(ctx, listTenantConfigsQuery, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var configs []tenant.Config
	for rows.Next() {
		row, err := scanTenantConfigRow(rows)
		if err != nil {
			return nil, err
		}

		cfg, err := row.config()
		if err != nil {
			return nil, fmt.Errorf("parsing tenant %s config: %w", row.cfg.TenantID, err)
		}

		configs = append(configs, cfg)
	}

	return configs, rows.Err()
}

// QuotaConfig implements tenant.QuotaConfigProvider.
func (m *DBTenantConfigManager) QuotaConfig(ctx context.Context, tenantID string) (tenant.QuotaConfig, error) {
	cfg, err := m.GetTenantConfig(ctx, tenantID)
	if err != nil {
		return tenant.QuotaConfig{}, err
	}
	return cfg.Quota, nil
}

// PolicyConfig implements tenant.PolicyConfigProvider.
func (m *DBTenantConfigManager) PolicyConfig(ctx context.Context, tenantID string) (tenant.PolicyConfig, error) {
	cfg, err := m.GetTenantConfig(ctx, tenantID)
	if err != nil {
		return tenant.PolicyConfig{}, err
	}
	return cfg.Policy, nil
}

// ── LRU cache ──────────────────────────────────────────────────────────────

// tenantCache is a TTL-aware LRU cache for tenant configurations.
// It uses a doubly linked list to maintain access order, evicting the
// least-recently-used entry when the cache reaches capacity.
type tenantCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List // front = MRU (most recently used), back = LRU
	maxSize int
	ttl     time.Duration
	// A cache-wide generation bounds invalidation state even for absent tenants.
	generation uint64
}

type lruEntry struct {
	key       string
	config    tenant.Config
	expiresAt time.Time
}

func newTenantCache(maxSize int, ttl time.Duration) *tenantCache {
	return &tenantCache{
		entries: make(map[string]*list.Element),
		lru:     list.New(),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get returns the cached config and the generation observed under the cache lock.
// Expired entries are removed on access (lazy eviction).
func (c *tenantCache) Get(tenantID string) (tenant.Config, bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.entries[tenantID]
	if !ok {
		return tenant.Config{}, false, c.generation
	}

	entry := elem.Value.(*lruEntry)

	// Expired – remove entry and report cache miss.
	if time.Now().After(entry.expiresAt) {
		c.removeElement(elem)
		return tenant.Config{}, false, c.generation
	}

	// Move to front (mark as most recently used).
	c.lru.MoveToFront(elem)
	return cloneTenantConfig(entry.config), true, c.generation
}

// setIfCurrent stores a config only if no invalidation has occurred since lookup.
// If the cache is at capacity, the least-recently-used entry is evicted first.
func (c *tenantCache) setIfCurrent(tenantID string, config tenant.Config, generation uint64) {
	config = cloneTenantConfig(config)
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return
	}

	if elem, ok := c.entries[tenantID]; ok {
		// Refresh existing entry.
		c.lru.MoveToFront(elem)
		e := elem.Value.(*lruEntry)
		e.config = config
		e.expiresAt = time.Now().Add(c.ttl)
		return
	}

	// Evict LRU entry when at capacity.
	if c.lru.Len() >= c.maxSize {
		c.removeElement(c.lru.Back())
	}

	e := &lruEntry{
		key:       tenantID,
		config:    config,
		expiresAt: time.Now().Add(c.ttl),
	}
	elem := c.lru.PushFront(e)
	c.entries[tenantID] = elem
}

func cloneTenantConfig(cfg tenant.Config) tenant.Config {
	cfg.Metadata = maps.Clone(cfg.Metadata)
	cfg.Quota.Limits = slices.Clone(cfg.Quota.Limits)
	cfg.Policy.AllowedModels = slices.Clone(cfg.Policy.AllowedModels)
	cfg.Policy.AllowedTools = slices.Clone(cfg.Policy.AllowedTools)
	cfg.Policy.AllowedMethods = slices.Clone(cfg.Policy.AllowedMethods)
	cfg.Policy.AllowedPaths = slices.Clone(cfg.Policy.AllowedPaths)
	return cfg
}

// Delete removes a tenant entry and invalidates in-flight cache fills.
func (c *tenantCache) Delete(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++

	if elem, ok := c.entries[tenantID]; ok {
		c.removeElement(elem)
	}
}

// removeElement removes a list element and its corresponding map entry.
// Must be called with c.mu held.
func (c *tenantCache) removeElement(elem *list.Element) {
	if elem == nil {
		return
	}
	c.lru.Remove(elem)
	e := elem.Value.(*lruEntry)
	delete(c.entries, e.key)
}
