package config

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
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
	storeTenantCache(cache, "t-123", cfg)

	// Get
	got, ok, _ := cache.Get("t-123")
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
	_, ok, _ = cache.Get("t-123")
	if ok {
		t.Error("expected cache miss after delete")
	}
}

func TestTenantCache_Miss(t *testing.T) {
	cache := newTenantCache(10, 1*time.Minute)

	_, ok, _ := cache.Get("nonexistent")
	if ok {
		t.Error("expected cache miss for nonexistent key")
	}
}

func TestTenantCache_Expiration(t *testing.T) {
	cache := newTenantCache(10, 1*time.Millisecond)

	cfg := tenant.Config{TenantID: "t-123"}
	storeTenantCache(cache, "t-123", cfg)

	// Wait for expiration
	time.Sleep(5 * time.Millisecond)

	_, ok, _ := cache.Get("t-123")
	if ok {
		t.Error("expected cache miss after TTL expiration")
	}
}

func TestTenantCache_Eviction(t *testing.T) {
	cache := newTenantCache(2, 1*time.Minute)

	storeTenantCache(cache, "t-1", tenant.Config{TenantID: "t-1"})
	storeTenantCache(cache, "t-2", tenant.Config{TenantID: "t-2"})

	// Cache is full, adding t-3 should evict one entry
	storeTenantCache(cache, "t-3", tenant.Config{TenantID: "t-3"})

	// Should have exactly 2 entries now
	count := 0
	for _, key := range []string{"t-1", "t-2", "t-3"} {
		if _, ok, _ := cache.Get(key); ok {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 entries after eviction, got %d", count)
	}

	// t-3 should definitely be present since it was just added
	_, ok, _ := cache.Get("t-3")
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
				storeTenantCache(cache, key, cfg)
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
	storeTenantCache(m.cache, "t-123", cfg)

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

func storeTenantCache(cache *tenantCache, key string, cfg tenant.Config) {
	_, _, generation := cache.Get(key)
	cache.setIfCurrent(key, cfg, generation)
}

type snapshotConnector struct {
	driver.Connector
	mu          sync.Mutex
	queries     int
	metadata    string
	deleted     bool
	writeErr    error
	writeResult driver.Result
	firstRead   chan struct{}
	releaseRead chan struct{}
	nextErr     error
	rowsClosed  int
}

func (c *snapshotConnector) Connect(context.Context) (driver.Conn, error) {
	return &snapshotConn{connector: c}, nil
}

type snapshotConn struct {
	driver.Conn
	connector *snapshotConnector
}

func (*snapshotConn) Close() error { return nil }

func (c *snapshotConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	state := c.connector
	state.mu.Lock()
	state.queries++
	first := state.queries == 1
	rows := &snapshotRows{metadata: state.metadata, read: state.deleted, nextErr: state.nextErr, onClose: func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.rowsClosed++
	}}
	state.mu.Unlock()
	if first && state.firstRead != nil {
		close(state.firstRead)
		select {
		case <-state.releaseRead:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return rows, nil
}

func (c *snapshotConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	state := c.connector
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.writeErr != nil {
		return nil, state.writeErr
	}
	if strings.HasPrefix(query, "DELETE") {
		state.deleted = true
	} else {
		state.metadata = args[6].Value.(string)
	}
	if state.writeResult != nil {
		return state.writeResult, nil
	}
	return driver.RowsAffected(1), nil
}

type snapshotRows struct {
	read     bool
	metadata string
	nextErr  error
	onClose  func()
}

func (*snapshotRows) Columns() []string {
	return []string{"id", "quota_limits", "allowed_models", "allowed_tools", "allowed_methods", "allowed_paths", "metadata", "updated_at"}
}
func (r *snapshotRows) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}
func (r *snapshotRows) Next(dest []driver.Value) error {
	if r.read {
		if r.nextErr != nil {
			return r.nextErr
		}
		return io.EOF
	}
	r.read = true
	copy(dest, []driver.Value{"sample", nil, `["model"]`, nil, nil, nil, r.metadata, time.Unix(1, 0)})
	return nil
}

func TestManagerCacheIsolatesReturnedSnapshots(t *testing.T) {
	connector := &snapshotConnector{metadata: `{"region":"original"}`}
	db := sql.OpenDB(connector)
	defer db.Close()
	m := NewDBTenantConfigManager(db, WithTenantCache(2, time.Minute))
	for range 3 {
		cfg, err := m.GetTenantConfig(t.Context(), "sample")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Metadata["region"] != "original" || len(cfg.Policy.AllowedModels) != 1 ||
			cfg.Policy.AllowedModels[0] != "model" || cfg.Policy.AllowedTools != nil || cfg.Quota.Limits != nil {
			t.Fatalf("unexpected config: %+v", cfg)
		}
		cfg.Metadata["region"] = "changed"
		cfg.Policy.AllowedModels[0] = "changed"
	}
	if connector.queries != 1 {
		t.Fatalf("queried database %d times", connector.queries)
	}
}

func TestTenantConfigInvalidationFencesPendingFill(t *testing.T) {
	writeFailure := errors.New("write failed")
	for _, scenario := range []struct {
		name        string
		cache       bool
		remove      bool
		writeErr    error
		wantRegion  string
		wantQueries int
	}{
		{name: "update", cache: true, wantRegion: "new", wantQueries: 2},
		{name: "delete", cache: true, remove: true, wantQueries: 3},
		{name: "failed update", cache: true, writeErr: writeFailure, wantRegion: "old", wantQueries: 1},
		{name: "cache disabled", wantRegion: "new", wantQueries: 3},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			connector := &snapshotConnector{
				metadata:    `{"region":"old"}`,
				writeErr:    scenario.writeErr,
				firstRead:   make(chan struct{}),
				releaseRead: make(chan struct{}),
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			manager := NewDBTenantConfigManager(db)
			if scenario.cache {
				manager = NewDBTenantConfigManager(db, WithTenantCache(2, time.Minute))
			}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(connector.releaseRead) }) }
			defer release()
			type result struct {
				cfg tenant.Config
				err error
			}
			pending := make(chan result, 1)
			go func() {
				cfg, err := manager.GetTenantConfig(t.Context(), "sample")
				pending <- result{cfg, err}
			}()
			select {
			case <-connector.firstRead:
			case early := <-pending:
				t.Fatalf("initial read returned early: %+v", early)
			}
			var err error
			if scenario.remove {
				err = manager.DeleteTenantConfig(t.Context(), "sample")
			} else {
				err = manager.SetTenantConfig(t.Context(), tenant.Config{TenantID: "sample", Metadata: map[string]string{"region": "new"}})
			}
			if !errors.Is(err, scenario.writeErr) {
				t.Fatalf("write error = %v, want %v", err, scenario.writeErr)
			}
			release()
			if old := <-pending; old.err != nil || old.cfg.Metadata["region"] != "old" {
				t.Fatalf("overlapping read = %+v", old)
			}
			for range 2 {
				cfg, err := manager.GetTenantConfig(t.Context(), "sample")
				if scenario.remove {
					if !errors.Is(err, tenant.ErrTenantNotFound) {
						t.Fatalf("read after delete: %v", err)
					}
				} else if err != nil || cfg.Metadata["region"] != scenario.wantRegion {
					t.Fatalf("read after write = %+v, %v", cfg, err)
				}
			}
			if connector.queries != scenario.wantQueries {
				t.Errorf("queries = %d, want %d", connector.queries, scenario.wantQueries)
			}
		})
	}
}

func TestListTenantsPreservesPartialRowsAndError(t *testing.T) {
	readFailure := errors.New("tenant row iteration failed")
	connector := &snapshotConnector{metadata: `{"region":"test"}`, nextErr: readFailure}
	database := sql.OpenDB(connector)
	t.Cleanup(func() { _ = database.Close() })

	manager := NewDBTenantConfigManager(database)
	result, err := manager.ListTenants(t.Context(), 5, 0)
	if !errors.Is(err, readFailure) || len(result) != 1 || result[0].TenantID != "sample" {
		t.Fatalf("ListTenants = %+v, %v", result, err)
	}
	connector.mu.Lock()
	defer connector.mu.Unlock()
	if connector.rowsClosed != 1 {
		t.Fatalf("rows closed %d times", connector.rowsClosed)
	}
}

type failedTenantRowsAffected struct {
	driver.Result
	err error
}

func (r failedTenantRowsAffected) RowsAffected() (int64, error) { return 0, r.err }

func TestTenantConfigDeleteReportsDriverFailures(t *testing.T) {
	writeErr := errors.New("delete failed")
	rowsErr := errors.New("rows affected failed")
	for _, tc := range []struct {
		name   string
		err    error
		result driver.Result
		want   error
	}{
		{"execution failure", writeErr, nil, writeErr},
		{"rows affected failure", nil, failedTenantRowsAffected{err: rowsErr}, rowsErr},
		{"missing tenant", nil, driver.RowsAffected(0), tenant.ErrTenantNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &snapshotConnector{metadata: `{"region":"old"}`, writeErr: tc.err, writeResult: tc.result}
			db := sql.OpenDB(connector)
			t.Cleanup(func() { _ = db.Close() })
			manager := NewDBTenantConfigManager(db)
			if tc.err != nil {
				manager = NewDBTenantConfigManager(db, WithTenantCache(2, time.Minute))
				if _, err := manager.GetTenantConfig(t.Context(), "sample"); err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.DeleteTenantConfig(t.Context(), "sample"); !errors.Is(err, tc.want) {
				t.Errorf("DeleteTenantConfig = %v, want %v", err, tc.want)
			}
			if tc.err != nil {
				cfg, err := manager.GetTenantConfig(t.Context(), "sample")
				if err != nil || cfg.Metadata["region"] != "old" || connector.queries != 1 {
					t.Errorf("cached lookup = %+v, %v, queries = %d", cfg, err, connector.queries)
				}
			}
		})
	}
}

func TestConfigProvidersReturnOwnedSections(t *testing.T) {
	manager := NewDBTenantConfigManager(nil, WithTenantCache(3, time.Minute))
	for _, id := range []string{"first", "second"} {
		cfg := tenant.Config{TenantID: id}
		cfg.Quota.Limits = []tenant.QuotaLimit{{Window: tenant.QuotaWindowMinute, Requests: 10}}
		if id == "second" {
			cfg.Quota.Limits[0].Requests = 20
		}
		cfg.Policy.AllowedModels = []string{id}
		storeTenantCache(manager.cache, id, cfg)
	}
	for range 2 {
		for id, requests := range map[string]int64{"first": 10, "second": 20} {
			q, err := manager.QuotaConfig(t.Context(), id)
			if err != nil || len(q.Limits) != 1 || q.Limits[0].Requests != requests || q.Limits[0].Window != tenant.QuotaWindowMinute {
				t.Fatalf("%s quota = %+v, %v", id, q, err)
			}
			p, err := manager.PolicyConfig(t.Context(), id)
			if err != nil || len(p.AllowedModels) != 1 || p.AllowedModels[0] != id {
				t.Fatalf("%s policy = %+v, %v", id, p, err)
			}
			q.Limits[0].Requests = 0
			p.AllowedModels[0] = "changed"
		}
	}
	if _, err := manager.QuotaConfig(t.Context(), "missing"); !errors.Is(err, tenant.ErrTenantNotFound) {
		t.Errorf("missing quota = %v", err)
	}
	if _, err := manager.PolicyConfig(t.Context(), "missing"); !errors.Is(err, tenant.ErrTenantNotFound) {
		t.Errorf("missing policy = %v", err)
	}
}

func TestListTenantsReportsInvalidConfigAndClosesRows(t *testing.T) {
	conn := &snapshotConnector{metadata: "{"}
	db := sql.OpenDB(conn)
	defer db.Close()
	m := NewDBTenantConfigManager(db)
	items, err := m.ListTenants(t.Context(), 10, 0)
	var syntax *json.SyntaxError
	if len(items) != 0 || !errors.As(err, &syntax) || conn.rowsClosed != 1 {
		t.Errorf("ListTenants=%+v, %v, closed=%d", items, err, conn.rowsClosed)
	}
	conn.metadata = `{"region":"valid"}`
	items, err = m.ListTenants(t.Context(), 10, 0)
	if err != nil || len(items) != 1 || items[0].Metadata["region"] != "valid" || conn.rowsClosed != 2 {
		t.Errorf("next ListTenants=%+v, %v, closed=%d", items, err, conn.rowsClosed)
	}
}
