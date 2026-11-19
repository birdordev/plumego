package tenant

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestInMemoryConfigManager_SetGet(t *testing.T) {
	mgr := NewInMemoryConfigManager()

	cfg := Config{
		TenantID: "test-tenant",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 100, Tokens: 5000}},
		},
		Policy: PolicyConfig{
			AllowedModels: []string{"gpt-4", "gpt-3.5"},
			AllowedTools:  []string{"search", "calculator"},
		},
		Metadata: map[string]string{
			"name":  "Test Tenant",
			"tier":  "premium",
			"email": "admin@test.com",
		},
	}

	// Set config
	mgr.SetTenantConfig(cfg)

	// Get config
	ctx := t.Context()
	retrieved, err := mgr.GetTenantConfig(ctx, "test-tenant")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify fields
	if retrieved.TenantID != cfg.TenantID {
		t.Errorf("expected tenant ID %s, got %s", cfg.TenantID, retrieved.TenantID)
	}
	if len(retrieved.Quota.Limits) == 0 || retrieved.Quota.Limits[0].Requests != 100 {
		t.Errorf("expected 100 requests/min, got %+v", retrieved.Quota.Limits)
	}
	if len(retrieved.Quota.Limits) == 0 || retrieved.Quota.Limits[0].Tokens != 5000 {
		t.Errorf("expected 5000 tokens/min, got %+v", retrieved.Quota.Limits)
	}
	if len(retrieved.Policy.AllowedModels) != len(cfg.Policy.AllowedModels) {
		t.Errorf("expected %d models, got %d", len(cfg.Policy.AllowedModels), len(retrieved.Policy.AllowedModels))
	}
	if len(retrieved.Metadata) != len(cfg.Metadata) {
		t.Errorf("expected %d metadata entries, got %d", len(cfg.Metadata), len(retrieved.Metadata))
	}
}

func TestInMemoryConfigManager_NotFound(t *testing.T) {
	mgr := NewInMemoryConfigManager()
	ctx := t.Context()

	_, err := mgr.GetTenantConfig(ctx, "non-existent")
	if err != ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestInMemoryConfigManager_UpdatedAt(t *testing.T) {
	mgr := NewInMemoryConfigManager()

	before := time.Now().UTC()
	cfg := Config{
		TenantID: "test-tenant",
	}
	mgr.SetTenantConfig(cfg)
	after := time.Now().UTC()

	ctx := t.Context()
	retrieved, err := mgr.GetTenantConfig(ctx, "test-tenant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved.UpdatedAt.Before(before) || retrieved.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt %v not between %v and %v", retrieved.UpdatedAt, before, after)
	}

	// Update config and verify timestamp changes
	time.Sleep(10 * time.Millisecond)
	beforeUpdate := time.Now().UTC()
	mgr.SetTenantConfig(cfg)
	retrieved2, _ := mgr.GetTenantConfig(ctx, "test-tenant")

	if !retrieved2.UpdatedAt.After(retrieved.UpdatedAt) {
		t.Errorf("expected UpdatedAt to increase on update")
	}
	if retrieved2.UpdatedAt.Before(beforeUpdate) {
		t.Errorf("UpdatedAt should be after second update time")
	}
}

func TestInMemoryConfigManager_Concurrent(t *testing.T) {
	mgr := NewInMemoryConfigManager()
	ctx := t.Context()

	// Concurrent writes
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cfg := Config{
				TenantID: "concurrent-test",
				Quota: QuotaConfig{
					Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: int64(id) * 10}},
				},
			}
			mgr.SetTenantConfig(cfg)
		}(i)
	}
	wg.Wait()

	// Should not panic and should have a value
	cfg, err := mgr.GetTenantConfig(ctx, "concurrent-test")
	if err != nil {
		t.Fatalf("expected config to exist after concurrent writes")
	}
	if cfg.TenantID != "concurrent-test" {
		t.Errorf("unexpected tenant ID: %s", cfg.TenantID)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := mgr.GetTenantConfig(ctx, "concurrent-test")
			if err != nil {
				t.Errorf("concurrent read failed: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestInMemoryConfigManager_QuotaProvider(t *testing.T) {
	mgr := NewInMemoryConfigManager()
	ctx := t.Context()

	cfg := Config{
		TenantID: "quota-test",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 50, Tokens: 2500}},
		},
	}
	mgr.SetTenantConfig(cfg)

	// Test QuotaConfigProvider interface
	quota, err := mgr.QuotaConfig(ctx, "quota-test")
	if err != nil {
		t.Fatalf("QuotaConfig failed: %v", err)
	}
	if len(quota.Limits) == 0 || quota.Limits[0].Requests != 50 {
		t.Errorf("expected 50 requests/min, got %+v", quota.Limits)
	}
	if len(quota.Limits) == 0 || quota.Limits[0].Tokens != 2500 {
		t.Errorf("expected 2500 tokens/min, got %+v", quota.Limits)
	}

	// Test non-existent tenant
	_, err = mgr.QuotaConfig(ctx, "non-existent")
	if err != ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestInMemoryConfigManager_PolicyProvider(t *testing.T) {
	mgr := NewInMemoryConfigManager()
	ctx := t.Context()

	cfg := Config{
		TenantID: "policy-test",
		Policy: PolicyConfig{
			AllowedModels: []string{"gpt-4"},
			AllowedTools:  []string{"search"},
		},
	}
	mgr.SetTenantConfig(cfg)

	// Test PolicyConfigProvider interface
	policy, err := mgr.PolicyConfig(ctx, "policy-test")
	if err != nil {
		t.Fatalf("PolicyConfig failed: %v", err)
	}
	if len(policy.AllowedModels) != 1 || policy.AllowedModels[0] != "gpt-4" {
		t.Errorf("unexpected allowed models: %v", policy.AllowedModels)
	}
	if len(policy.AllowedTools) != 1 || policy.AllowedTools[0] != "search" {
		t.Errorf("unexpected allowed tools: %v", policy.AllowedTools)
	}

	// Test non-existent tenant
	_, err = mgr.PolicyConfig(ctx, "non-existent")
	if err != ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestInMemoryConfigManager_NilSafety(t *testing.T) {
	var mgr *InMemoryConfigManager

	// SetTenantConfig should not panic
	mgr.SetTenantConfig(Config{TenantID: "test"})

	// GetTenantConfig should return error
	ctx := t.Context()
	_, err := mgr.GetTenantConfig(ctx, "test")
	if err != ErrTenantNotFound {
		t.Errorf("expected ErrTenantNotFound for nil manager, got %v", err)
	}
}

func TestInMemoryConfigManager_MultipleUpdates(t *testing.T) {
	mgr := NewInMemoryConfigManager()
	ctx := t.Context()

	// Initial config
	cfg1 := Config{
		TenantID: "update-test",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 10}},
		},
		Metadata: map[string]string{"version": "1"},
	}
	mgr.SetTenantConfig(cfg1)

	// Update config
	cfg2 := Config{
		TenantID: "update-test",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 20, Tokens: 1000}},
		},
		Metadata: map[string]string{"version": "2"},
	}
	mgr.SetTenantConfig(cfg2)

	// Verify latest config
	retrieved, err := mgr.GetTenantConfig(ctx, "update-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(retrieved.Quota.Limits) == 0 || retrieved.Quota.Limits[0].Requests != 20 {
		t.Errorf("expected 20 requests/min, got %+v", retrieved.Quota.Limits)
	}
	if len(retrieved.Quota.Limits) == 0 || retrieved.Quota.Limits[0].Tokens != 1000 {
		t.Errorf("expected 1000 tokens/min, got %+v", retrieved.Quota.Limits)
	}
	if retrieved.Metadata["version"] != "2" {
		t.Errorf("expected version 2, got %s", retrieved.Metadata["version"])
	}
}

func TestInMemoryConfigManagerOwnsSnapshots(t *testing.T) {
	manager := NewInMemoryConfigManager()
	newConfig := func() Config {
		return Config{
			TenantID: "sample",
			Metadata: map[string]string{"region": "original"},
			Quota:    QuotaConfig{Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 5}}},
			Policy: PolicyConfig{
				AllowedModels:  []string{"model"},
				AllowedTools:   []string{"tool"},
				AllowedMethods: []string{"GET"},
				AllowedPaths:   []string{"/items"},
			},
		}
	}
	mutate := func(cfg Config) {
		cfg.Metadata["region"] = "edited"
		cfg.Quota.Limits[0].Requests = 1
		cfg.Policy.AllowedModels[0] = "edited"
		cfg.Policy.AllowedTools[0] = "edited"
		cfg.Policy.AllowedMethods[0] = "POST"
		cfg.Policy.AllowedPaths[0] = "/other"
	}
	load := func() Config {
		t.Helper()
		got, err := manager.GetTenantConfig(t.Context(), "sample")
		if err != nil {
			t.Fatal(err)
		}
		if got.UpdatedAt.IsZero() {
			t.Fatal("missing update time")
		}
		got.UpdatedAt = time.Time{}
		return got
	}
	input := newConfig()
	manager.SetTenantConfig(input)
	mutate(input)
	mutate(load())
	if got := load(); !reflect.DeepEqual(got, newConfig()) {
		t.Fatalf("stored configuration changed: %+v", got)
	}

	policy, err := manager.PolicyConfig(t.Context(), "sample")
	if err != nil {
		t.Fatal(err)
	}
	policy.AllowedModels[0] = "edited"
	quota, err := manager.QuotaConfig(t.Context(), "sample")
	if err != nil {
		t.Fatal(err)
	}
	quota.Limits[0].Requests = 1
	if got := load(); !reflect.DeepEqual(got, newConfig()) {
		t.Fatalf("derived configuration changed stored result: %+v", got)
	}

	manager.SetTenantConfig(Config{TenantID: "sample"})
	if got := load(); !reflect.DeepEqual(got, Config{TenantID: "sample"}) {
		t.Fatalf("replacement config = %+v", got)
	}
}
