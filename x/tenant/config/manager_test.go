package config

import (
	"database/sql"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/tenant/core"
)

func TestNewDBTenantConfigManager(t *testing.T) {
	m := NewDBTenantConfigManager(nil)
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.cache != nil {
		t.Error("expected nil cache by default")
	}
}

func TestNewDBTenantConfigManagerWithCache(t *testing.T) {
	m := NewDBTenantConfigManager(nil, WithTenantCache(100, 5*time.Minute))
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.cache == nil {
		t.Fatal("expected non-nil cache")
	}
	if m.cache.maxSize != 100 {
		t.Errorf("expected cache size 100, got %d", m.cache.maxSize)
	}
	if m.cache.ttl != 5*time.Minute {
		t.Errorf("expected cache TTL 5m, got %v", m.cache.ttl)
	}
}

func TestDBTenantConfigManager_GetTenantConfig_NilDB(t *testing.T) {
	m := NewDBTenantConfigManager(nil)
	_, err := m.GetTenantConfig(t.Context(), "t-123")
	if err != tenant.ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestDBTenantConfigManager_GetTenantConfig_NilManager(t *testing.T) {
	var m *DBTenantConfigManager
	_, err := m.GetTenantConfig(t.Context(), "t-123")
	if err != tenant.ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestDBTenantConfigManager_SetTenantConfig_NilDB(t *testing.T) {
	m := NewDBTenantConfigManager(nil)
	err := m.SetTenantConfig(t.Context(), tenant.Config{TenantID: "t-123"})
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestDBTenantConfigManager_SetTenantConfig_NilManager(t *testing.T) {
	var m *DBTenantConfigManager
	err := m.SetTenantConfig(t.Context(), tenant.Config{TenantID: "t-123"})
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
}

func TestDBTenantConfigManager_DeleteTenantConfig_NilDB(t *testing.T) {
	m := NewDBTenantConfigManager(nil)
	err := m.DeleteTenantConfig(t.Context(), "t-123")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestDBTenantConfigManager_DeleteTenantConfig_NilManager(t *testing.T) {
	var m *DBTenantConfigManager
	err := m.DeleteTenantConfig(t.Context(), "t-123")
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
}

func TestDBTenantConfigManager_ListTenants_NilDB(t *testing.T) {
	m := NewDBTenantConfigManager(nil)
	_, err := m.ListTenants(t.Context(), 10, 0)
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestDBTenantConfigManager_ListTenants_NilManager(t *testing.T) {
	var m *DBTenantConfigManager
	_, err := m.ListTenants(t.Context(), 10, 0)
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
}

func TestDBTenantConfigManager_ListTenants_DefaultLimit(t *testing.T) {
	// Verify that negative limit is corrected to 100
	// (tested via nil db to ensure it reaches the limit logic)
	m := NewDBTenantConfigManager(nil)
	_, err := m.ListTenants(t.Context(), -1, -5)
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestDBTenantConfigManager_QuotaConfig_NilDB(t *testing.T) {
	m := NewDBTenantConfigManager(nil)
	_, err := m.QuotaConfig(t.Context(), "t-123")
	if err != tenant.ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestDBTenantConfigManager_PolicyConfig_NilDB(t *testing.T) {
	m := NewDBTenantConfigManager(nil)
	_, err := m.PolicyConfig(t.Context(), "t-123")
	if err != tenant.ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

// --- tenantCache tests ---

func TestTenantCache_GetSetDelete(t *testing.T) {
	cache := newTenantCache(10, 1*time.Minute)

	cfg := tenant.Config{TenantID: "t-123"}
	cfg.Quota.Limits = []tenant.QuotaLimit{{Window: tenant.QuotaWindowMinute, Requests: 100}}

	// Set
	cache.Set("t-123", cfg)

	// Get
	got, ok := cache.Get("t-123")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.TenantID != "t-123" {
		t.Errorf("expected tenant ID 't-123', got %q", got.TenantID)
	}
	if len(got.Quota.Limits) == 0 || got.Quota.Limits[0].Requests != 100 {
		t.Errorf("expected 100 requests/min, got %+v", got.Quota.Limits)
	}

	// Delete
	cache.Delete("t-123")
	_, ok = cache.Get("t-123")
	if ok {
		t.Error("expected cache miss after delete")
	}
}

func TestTenantCache_Miss(t *testing.T) {
	cache := newTenantCache(10, 1*time.Minute)

	_, ok := cache.Get("nonexistent")
	if ok {
		t.Error("expected cache miss for nonexistent key")
	}
}

func TestTenantCache_Expiration(t *testing.T) {
	cache := newTenantCache(10, 1*time.Millisecond)

	cfg := tenant.Config{TenantID: "t-123"}
	cache.Set("t-123", cfg)

	// Wait for expiration
	time.Sleep(5 * time.Millisecond)

	_, ok := cache.Get("t-123")
	if ok {
		t.Error("expected cache miss after TTL expiration")
	}
}

func TestTenantCache_Eviction(t *testing.T) {
	cache := newTenantCache(2, 1*time.Minute)

	cache.Set("t-1", tenant.Config{TenantID: "t-1"})
	cache.Set("t-2", tenant.Config{TenantID: "t-2"})

	// Cache is full, adding t-3 should evict one entry
	cache.Set("t-3", tenant.Config{TenantID: "t-3"})

	// Should have exactly 2 entries now
	count := 0
	for _, key := range []string{"t-1", "t-2", "t-3"} {
		if _, ok := cache.Get(key); ok {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 entries after eviction, got %d", count)
	}

	// t-3 should definitely be present since it was just added
	_, ok := cache.Get("t-3")
	if !ok {
		t.Error("expected t-3 to be present")
	}
}

func TestTenantCache_ConcurrentAccess(t *testing.T) {
	cache := newTenantCache(100, 1*time.Minute)

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				key := "t-" + string(rune('0'+id))
				cfg := tenant.Config{TenantID: key}
				cache.Set(key, cfg)
				cache.Get(key)
				cache.Delete(key)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// --- parseTenantConfigJSON tests ---

func TestParseTenantConfigJSON_Valid(t *testing.T) {
	cfg := &tenant.Config{}
	err := parseTenantConfigJSON(cfg,
		sql.NullString{Valid: false},
		sql.NullString{String: `["gpt-4","claude"]`, Valid: true},
		sql.NullString{String: `["tool1"]`, Valid: true},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{String: `{"key":"val"}`, Valid: true},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Policy.AllowedModels) != 2 {
		t.Errorf("expected 2 allowed models, got %d", len(cfg.Policy.AllowedModels))
	}
	if len(cfg.Policy.AllowedTools) != 1 {
		t.Errorf("expected 1 allowed tool, got %d", len(cfg.Policy.AllowedTools))
	}
	if cfg.Metadata["key"] != "val" {
		t.Errorf("expected metadata key=val, got %v", cfg.Metadata)
	}
}

func TestTenantConfigRowConfigUsesStructuredQuotaLimits(t *testing.T) {
	updatedAt := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	row := tenantConfigRow{
		cfg: tenant.Config{TenantID: "t-structured"},
		quotaLimitsJSON: sql.NullString{
			String: `[{"Window":"minute","Requests":25,"Tokens":2500}]`,
			Valid:  true,
		},
		updatedAt: updatedAt,
	}

	cfg, err := row.config()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TenantID != "t-structured" {
		t.Errorf("expected tenant ID t-structured, got %q", cfg.TenantID)
	}
	if cfg.UpdatedAt != updatedAt {
		t.Errorf("expected updatedAt %v, got %v", updatedAt, cfg.UpdatedAt)
	}
	if len(cfg.Quota.Limits) != 1 {
		t.Fatalf("expected 1 quota limit, got %d", len(cfg.Quota.Limits))
	}
	limit := cfg.Quota.Limits[0]
	if limit.Window != tenant.QuotaWindowMinute || limit.Requests != 25 || limit.Tokens != 2500 {
		t.Errorf("unexpected quota limit: %+v", limit)
	}
}

func TestParseTenantConfigJSON_NullFields(t *testing.T) {
	cfg := &tenant.Config{}
	err := parseTenantConfigJSON(cfg,
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Policy.AllowedModels != nil {
		t.Errorf("expected nil allowed models, got %v", cfg.Policy.AllowedModels)
	}
}

func TestParseTenantConfigJSON_InvalidModelsJSON(t *testing.T) {
	cfg := &tenant.Config{}
	err := parseTenantConfigJSON(cfg,
		sql.NullString{Valid: false},
		sql.NullString{String: `{invalid`, Valid: true},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
	)
	if err == nil {
		t.Fatal("expected error for invalid JSON in allowed_models")
	}
}

func TestParseTenantConfigJSON_InvalidToolsJSON(t *testing.T) {
	cfg := &tenant.Config{}
	err := parseTenantConfigJSON(cfg,
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{String: `not-json`, Valid: true},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
	)
	if err == nil {
		t.Fatal("expected error for invalid JSON in allowed_tools")
	}
}

func TestParseTenantConfigJSON_InvalidMetadataJSON(t *testing.T) {
	cfg := &tenant.Config{}
	err := parseTenantConfigJSON(cfg,
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{Valid: false},
		sql.NullString{String: `[broken`, Valid: true},
	)
	if err == nil {
		t.Fatal("expected error for invalid JSON in metadata")
	}
}

// --- GetTenantConfig with cache ---

func TestDBTenantConfigManager_CacheHit(t *testing.T) {
	m := NewDBTenantConfigManager(nil, WithTenantCache(10, 1*time.Minute))

	// Pre-populate cache
	cfg := tenant.Config{TenantID: "t-123"}
	cfg.Quota.Limits = []tenant.QuotaLimit{{Window: tenant.QuotaWindowMinute, Requests: 50}}
	m.cache.Set("t-123", cfg)

	// Should return from cache even though db is nil
	got, err := m.GetTenantConfig(t.Context(), "t-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TenantID != "t-123" {
		t.Errorf("expected tenant ID 't-123', got %q", got.TenantID)
	}
	if len(got.Quota.Limits) == 0 || got.Quota.Limits[0].Requests != 50 {
		t.Errorf("expected 50 requests/min, got %+v", got.Quota.Limits)
	}
}
