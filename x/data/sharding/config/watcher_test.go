package config

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewConfigWatcher(t *testing.T) {
	// Create temp config file
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	watcher, err := NewConfigWatcher(tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	if watcher.path != tmpfile.Name() {
		t.Errorf("expected path %s, got %s", tmpfile.Name(), watcher.path)
	}

	if watcher.interval != 5*time.Second {
		t.Errorf("expected default interval 5s, got %v", watcher.interval)
	}

	config := watcher.Get()
	if len(config.Shards) != 1 {
		t.Errorf("expected 1 shard, got %d", len(config.Shards))
	}
}

func TestConfigWatcher_WithOptions(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	watcher, err := NewConfigWatcher(
		tmpfile.Name(),
		WithWatchInterval(1*time.Second),
		WithOnChange(func(c *ShardingConfig) {
			// Callback registered for testing
		}),
	)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	if watcher.interval != 1*time.Second {
		t.Errorf("expected interval 1s, got %v", watcher.interval)
	}

	if len(watcher.onChange) != 1 {
		t.Errorf("expected 1 onChange callback, got %d", len(watcher.onChange))
	}
}

func TestConfigWatcher_Reload(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	// Initial config
	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	watcher, err := NewConfigWatcher(tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Verify initial config
	config := watcher.Get()
	if config.CrossShardPolicy != "deny" {
		t.Errorf("expected policy 'deny', got %s", config.CrossShardPolicy)
	}

	// Update config file
	time.Sleep(10 * time.Millisecond) // Ensure different mtime

	newJsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "first_success",
		"log_level": "debug"
	}`)

	if err := os.WriteFile(tmpfile.Name(), newJsonData, 0644); err != nil {
		t.Fatal(err)
	}

	// Manually reload
	if err := watcher.Reload(); err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	// Verify updated config
	config = watcher.Get()
	if config.CrossShardPolicy != "first_success" {
		t.Errorf("expected policy 'first_success', got %s", config.CrossShardPolicy)
	}

	if config.LogLevel != "debug" {
		t.Errorf("expected log level 'debug', got %s", config.LogLevel)
	}
}

func TestConfigWatcher_ReloadRejectsInvalidEnvMergedConfig(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)
	if err := os.WriteFile(tmpfile.Name(), jsonData, 0644); err != nil {
		t.Fatal(err)
	}

	var callbackCount int
	watcher, err := NewConfigWatcher(tmpfile.Name(), WithOnChange(func(*ShardingConfig) {
		callbackCount++
	}))
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	newJSONData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "first_success",
		"log_level": "debug"
	}`)
	if err := os.WriteFile(tmpfile.Name(), newJSONData, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_SHARD_CROSS_SHARD_POLICY", "invalid")

	if err := watcher.Reload(); err == nil {
		t.Fatalf("Reload() error = nil, want invalid merged config")
	}
	if got := watcher.Get().CrossShardPolicy; got != "deny" {
		t.Fatalf("watcher policy = %q, want original deny", got)
	}
	if callbackCount != 0 {
		t.Fatalf("callback count = %d, want 0", callbackCount)
	}
}

func TestConfigWatcher_OnChange(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	callCount := 0
	var lastConfig *ShardingConfig

	watcher, err := NewConfigWatcher(
		tmpfile.Name(),
		WithOnChange(func(c *ShardingConfig) {
			callCount++
			lastConfig = c
		}),
	)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Update config file
	time.Sleep(10 * time.Millisecond)

	newJsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "first_success",
		"log_level": "info"
	}`)

	if err := os.WriteFile(tmpfile.Name(), newJsonData, 0644); err != nil {
		t.Fatal(err)
	}

	// Reload
	if err := watcher.Reload(); err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	// Verify callback was called
	if callCount != 1 {
		t.Errorf("expected 1 callback, got %d", callCount)
	}

	if lastConfig == nil {
		t.Fatal("expected config in callback")
	}

	if lastConfig.CrossShardPolicy != "first_success" {
		t.Errorf("expected policy 'first_success' in callback, got %s", lastConfig.CrossShardPolicy)
	}
}

func TestConfigWatcher_AddOnChange(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	watcher, err := NewConfigWatcher(tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	callCount := 0
	watcher.AddOnChange(func(c *ShardingConfig) {
		callCount++
	})

	// Update and reload
	time.Sleep(10 * time.Millisecond)
	newJsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "first_success",
		"log_level": "info"
	}`)

	if err := os.WriteFile(tmpfile.Name(), newJsonData, 0644); err != nil {
		t.Fatal(err)
	}

	if err := watcher.Reload(); err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 callback, got %d", callCount)
	}
}

func TestConfigWatcher_Start(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	// Use a channel to safely communicate callback invocations
	callCh := make(chan struct{}, 10)
	watcher, err := NewConfigWatcher(
		tmpfile.Name(),
		WithWatchInterval(100*time.Millisecond),
		WithOnChange(func(c *ShardingConfig) {
			callCh <- struct{}{}
		}),
	)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Start watcher in background
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		_ = watcher.Start(ctx)
	}()

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Update config file
	newJsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "first_success",
		"log_level": "info"
	}`)

	if err := os.WriteFile(tmpfile.Name(), newJsonData, 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for callback notification
	select {
	case <-callCh:
		// Expected callback received
	case <-time.After(300 * time.Millisecond):
		t.Error("expected callback from auto-reload, but none received")
	}

	// Verify config was updated
	config := watcher.Get()
	if config.CrossShardPolicy != "first_success" {
		t.Errorf("expected policy 'first_success' after auto-reload, got %s", config.CrossShardPolicy)
	}

	// Stop watcher
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestConfigWatcher_Stop(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	watcher, err := NewConfigWatcher(tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Start watcher
	ctx := t.Context()
	go func() {
		_ = watcher.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Stop watcher
	watcher.Stop()
	watcher.Stop()

	// Verify watcher stopped
	time.Sleep(50 * time.Millisecond)
}

func TestConfigWatcher_StartTwiceReturnsError(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	watcher, err := NewConfigWatcher(tmpfile.Name(), WithWatchInterval(time.Second))
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- watcher.Start(ctx)
	}()

	deadline := time.Now().Add(100 * time.Millisecond)
	for !watcher.started.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !watcher.started.Load() {
		t.Fatal("watcher did not start")
	}

	if err := watcher.Start(ctx); err == nil {
		t.Fatal("expected repeated Start to return an error")
	}
	watcher.Stop()

	if err := <-errCh; err != nil {
		t.Fatalf("first Start returned %v, want nil after Stop", err)
	}
}

func TestConfigReloader(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	reloader, err := NewConfigReloader(tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to create reloader: %v", err)
	}

	// Verify initial config
	config := reloader.Get()
	if len(config.Shards) != 1 {
		t.Errorf("expected 1 shard, got %d", len(config.Shards))
	}

	// Add onChange callback
	callCount := 0
	reloader.OnChange(func(c *ShardingConfig) {
		callCount++
	})

	// Update config
	time.Sleep(10 * time.Millisecond)
	newJsonData := []byte(`{
		"shards": [
			{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}},
			{"name": "shard1", "primary": {"driver": "mysql", "host": "localhost", "database": "test2"}}
		],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if err := os.WriteFile(tmpfile.Name(), newJsonData, 0644); err != nil {
		t.Fatal(err)
	}

	// Manually reload
	if err := reloader.Reload(); err != nil {
		t.Fatalf("failed to reload: %v", err)
	}

	// Verify callback was called
	if callCount != 1 {
		t.Errorf("expected 1 callback, got %d", callCount)
	}

	// Verify config was updated
	config = reloader.Get()
	if len(config.Shards) != 2 {
		t.Errorf("expected 2 shards after reload, got %d", len(config.Shards))
	}
}

func BenchmarkConfigWatcher_Get(b *testing.B) {
	tmpfile, err := os.CreateTemp("", "config*.json")
	if err != nil {
		b.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	jsonData := []byte(`{
		"shards": [{"name": "shard0", "primary": {"driver": "mysql", "host": "localhost", "database": "test"}}],
		"sharding_rules": [{"table_name": "users", "shard_key_column": "id", "strategy": "mod"}],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	if _, err := tmpfile.Write(jsonData); err != nil {
		b.Fatal(err)
	}
	tmpfile.Close()

	watcher, err := NewConfigWatcher(tmpfile.Name())
	if err != nil {
		b.Fatalf("failed to create watcher: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = watcher.Get()
	}
}

func TestConcurrentReloadPublishesVersionOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
		"shards": [{"name":"shard0","primary":{"driver":"sqlite3","database":"test"}}],
		"sharding_rules": [{"table_name":"items","shard_key_column":"id","strategy":"hash"}],
		"cross_shard_policy":"deny",
		"log_level":"info"
	}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	w, err := NewConfigWatcher(path)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	w.AddOnChange(func(*ShardingConfig) {
		if got := w.Get().Shards[0].Name; got != "shard0" {
			t.Errorf("published shard = %q", got)
		}
		calls.Add(1)
	})
	next := w.lastMod.Add(time.Minute)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			<-start
			if err := w.Reload(); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	group.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("callback count = %d, want 1", got)
	}
}

func TestWatcherSnapshotsAreIsolated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
		"shards": [{"name":"original","primary":{"driver":"sqlite3","database":"test"}}],
		"sharding_rules": [{"table_name":"items","shard_key_column":"id","strategy":"range",
			"actual_table_names":{"0":"items_0"},
			"strategy_config":{"ranges":[{"start":{"value":[1]},"end":10,"shard":0}]}}],
		"cross_shard_policy":"deny",
		"log_level":"info"
	}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	w, err := NewConfigWatcher(path)
	if err != nil {
		t.Fatal(err)
	}
	edit := func(cfg *ShardingConfig) {
		cfg.Shards[0].Name = "edited"
		rule := &cfg.ShardingRules[0]
		rule.ActualTableNames[0] = "edited"
		rule.StrategyConfig.Ranges[0].Start.(map[string]any)["value"].([]any)[0] = 0.0
	}
	check := func(cfg *ShardingConfig) {
		rule := &cfg.ShardingRules[0]
		if cfg.Shards[0].Name != "original" || rule.ActualTableNames[0] != "items_0" ||
			rule.StrategyConfig.Ranges[0].Start.(map[string]any)["value"].([]any)[0] != 1.0 {
			t.Error("configuration snapshot has been mutated")
		}
	}
	edit(w.Get())
	check(w.Get())
	w.AddOnChange(edit)
	w.AddOnChange(check)
	next := w.lastMod.Add(time.Minute)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
	if err := w.Reload(); err != nil {
		t.Fatal(err)
	}
	check(w.Get())
}
