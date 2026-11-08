package config

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/log"
)

type stubSource struct {
	name string
	data map[string]any
	err  error
}

func (s *stubSource) Load(context.Context) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.data, nil
}

func (s *stubSource) Watch(context.Context) <-chan WatchResult {
	results := make(chan WatchResult)
	close(results)
	return results
}

func (s *stubSource) Name() string { return s.name }

func unsetEnvForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		key := key
		original, existed := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if existed {
				if err := os.Setenv(key, original); err != nil {
					t.Fatalf("restore %s: %v", key, err)
				}
				return
			}
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("restore unset %s: %v", key, err)
			}
		})
	}
}

// TestConfigBasic tests basic Config functionality
func TestConfigBasic(t *testing.T) {
	cfg := NewManager(log.NewLogger())

	// Test empty config
	if cfg.Get("missing") != "" {
		t.Error("Empty config should return empty string for missing keys")
	}

	if cfg.GetString("missing", "default") != "default" {
		t.Error("GetString should return default value for missing keys")
	}

	if cfg.GetInt("missing", 42) != 42 {
		t.Error("GetInt should return default value for missing keys")
	}

	if cfg.GetBool("missing", true) != true {
		t.Error("GetBool should return default value for missing keys")
	}
}

func TestGetBoolNumericValues(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	cfg.data = map[string]any{
		"int_zero":   int8(0),
		"int_one":    int16(1),
		"float_zero": float32(0),
		"float_one":  float64(2.5),
	}

	if cfg.GetBool("int_zero", true) {
		t.Error("expected int_zero to be false")
	}
	if !cfg.GetBool("int_one", false) {
		t.Error("expected int_one to be true")
	}
	if cfg.GetBool("float_zero", true) {
		t.Error("expected float_zero to be false")
	}
	if !cfg.GetBool("float_one", false) {
		t.Error("expected float_one to be true")
	}
}

func TestToIntRejectsOverflow(t *testing.T) {
	const fallback = -7

	if got := toInt(uint64(maxIntValue)+1, fallback); got != fallback {
		t.Fatalf("overflowing uint64 toInt = %d, want fallback %d", got, fallback)
	}
	if got := toInt(math.Inf(1), fallback); got != fallback {
		t.Fatalf("infinite float toInt = %d, want fallback %d", got, fallback)
	}
	if got := toInt(math.NaN(), fallback); got != fallback {
		t.Fatalf("NaN float toInt = %d, want fallback %d", got, fallback)
	}
}

func TestToIntPreservesInRangeConversions(t *testing.T) {
	const fallback = -7

	cases := []struct {
		name  string
		value any
		want  int
	}{
		{name: "int64", value: int64(42), want: 42},
		{name: "uint64", value: uint64(42), want: 42},
		{name: "float64 truncates", value: 42.9, want: 42},
		{name: "bool true", value: true, want: 1},
		{name: "string", value: " 42 ", want: 42},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toInt(tc.value, fallback); got != tc.want {
				t.Fatalf("toInt(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

// TestConfigWithData tests Config with actual data
func TestConfigWithData(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	ctx := t.Context()

	// Create a simple JSON config file
	configData := map[string]any{
		"app_name":        "TestApp",
		"app_version":     "1.0.0",
		"debug_mode":      true,
		"max_connections": 100,
		"timeout_ms":      5000,
		"api_url":         "https://api.example.com",
	}

	tmpFile := "test_config.json"
	content, err := json.MarshalIndent(configData, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	err = os.WriteFile(tmpFile, content, 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile) }()

	// Add file source
	_ = cfg.AddSource(NewFileSource(tmpFile, FormatJSON, false))

	// Load config
	err = cfg.Load(ctx)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test values
	if cfg.GetString("app_name", "") != "TestApp" {
		t.Errorf("Expected app_name to be 'TestApp', got '%s'", cfg.GetString("app_name", ""))
	}

	if !cfg.GetBool("debug_mode", false) {
		t.Error("Expected debug_mode to be true")
	}

	if cfg.GetInt("max_connections", 0) != 100 {
		t.Errorf("Expected max_connections to be 100, got %d", cfg.GetInt("max_connections", 0))
	}

	if cfg.GetDurationMs("timeout_ms", 0) != 5000*time.Millisecond {
		t.Errorf("Expected timeout_ms to be 5000ms, got %v", cfg.GetDurationMs("timeout_ms", 0))
	}

	if cfg.GetString("api_url", "") != "https://api.example.com" {
		t.Errorf("Expected api_url to be 'https://api.example.com', got '%s'", cfg.GetString("api_url", ""))
	}
}

func TestConfigKeyNormalization(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	ctx := t.Context()

	src := &stubSource{
		name: "test",
		data: map[string]any{
			"FooBar": "value",
		},
	}
	if err := cfg.AddSource(src); err != nil {
		t.Fatalf("add source: %v", err)
	}
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}

	if got := cfg.Get("foo_bar"); got != "value" {
		t.Fatalf("expected normalized lookup, got %q", got)
	}
	if got := cfg.Get("FOO_BAR"); got != "value" {
		t.Fatalf("expected case-insensitive lookup, got %q", got)
	}
	if got := cfg.Get("FooBar"); got != "value" {
		t.Fatalf("expected original lookup, got %q", got)
	}

	all := cfg.GetAll()
	if _, ok := all["foo_bar"]; !ok {
		t.Fatalf("expected normalized key in GetAll, got %v", all)
	}
}

func TestConfigUnmarshalNestedStruct(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	cfg.data = map[string]any{
		"app_name":      "demo",
		"db_host":       "localhost",
		"db_port":       "5432",
		"cache_enabled": "true",
	}

	type dbConfig struct {
		Host string `config:"db_host"`
		Port int    `config:"db_port"`
	}

	type cacheConfig struct {
		Enabled bool `config:"cache_enabled"`
	}

	type appConfig struct {
		AppName string `config:"app_name"`
		DB      dbConfig
		Cache   *cacheConfig
	}

	var target appConfig
	if err := cfg.Unmarshal(&target); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if target.AppName != "demo" {
		t.Fatalf("unexpected app name: %s", target.AppName)
	}
	if target.DB.Host != "localhost" || target.DB.Port != 5432 {
		t.Fatalf("unexpected db config: %+v", target.DB)
	}
	if target.Cache == nil || target.Cache.Enabled != true {
		t.Fatalf("unexpected cache config: %#v", target.Cache)
	}
}

func TestLoadBestEffort(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	_ = cfg.AddSource(&stubSource{name: "bad", err: errors.New("boom")})
	_ = cfg.AddSource(&stubSource{name: "ok", data: map[string]any{"app_name": "demo"}})

	if err := cfg.LoadBestEffort(t.Context()); err != nil {
		t.Fatalf("expected best effort load to succeed, got %v", err)
	}

	if got := cfg.GetString("app_name", ""); got != "demo" {
		t.Fatalf("unexpected value: %q", got)
	}
}

func TestLoadBestEffortAllFail(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	_ = cfg.AddSource(&stubSource{name: "bad1", err: errors.New("boom1")})
	_ = cfg.AddSource(&stubSource{name: "bad2", err: errors.New("boom2")})

	if err := cfg.LoadBestEffort(t.Context()); err == nil {
		t.Fatalf("expected error when all sources fail")
	}
}

func TestReloadWithValidation(t *testing.T) {
	source := &stubSource{name: "test", data: map[string]any{"value": "ok"}}
	cfg := NewManager(log.NewLogger())
	_ = cfg.AddSource(source)

	if err := cfg.Load(t.Context()); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	source.data = map[string]any{"value": "bad"}
	err := cfg.ReloadWithValidation(t.Context(), func(data map[string]any) error {
		if data["value"] == "bad" {
			return errors.New("invalid config")
		}
		return nil
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if got := cfg.GetString("value", ""); got != "ok" {
		t.Fatalf("expected rollback to previous value, got %q", got)
	}

	source.data = map[string]any{"value": "good"}
	if err := cfg.ReloadWithValidation(t.Context(), func(data map[string]any) error {
		return nil
	}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if got := cfg.GetString("value", ""); got != "good" {
		t.Fatalf("expected updated value, got %q", got)
	}
}

// TestConfigUnmarshal tests struct unmarshalling
func TestConfigUnmarshal(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	ctx := t.Context()

	// Set up test data
	t.Setenv("APP_NAME", "TestApp")
	t.Setenv("APP_VERSION", "2.0.0")
	t.Setenv("DEBUG_MODE", "true")
	t.Setenv("MAX_CONNECTIONS", "200")

	_ = cfg.AddSource(NewEnvSource(""))

	err := cfg.Load(ctx)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Define test struct
	type AppConfig struct {
		AppName        string `config:"app_name"`
		AppVersion     string `config:"app_version"`
		DebugMode      bool   `config:"debug_mode"`
		MaxConnections int    `config:"max_connections"`
	}

	var config AppConfig
	err = cfg.Unmarshal(&config)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if config.AppName != "TestApp" {
		t.Errorf("Expected AppName 'TestApp', got '%s'", config.AppName)
	}

	if config.AppVersion != "2.0.0" {
		t.Errorf("Expected AppVersion '2.0.0', got '%s'", config.AppVersion)
	}

	if !config.DebugMode {
		t.Error("Expected DebugMode to be true")
	}

	if config.MaxConnections != 200 {
		t.Errorf("Expected MaxConnections 200, got %d", config.MaxConnections)
	}
}

func TestReloadNotifiesWatchers(t *testing.T) {
	source := &stubSource{name: "test", data: map[string]any{"value": "v1"}}
	cfg := NewManager(log.NewLogger())
	_ = cfg.AddSource(source)
	_ = cfg.Load(t.Context())

	got := make(chan string, 1)
	_ = cfg.Watch("value", func(_, newVal any) {
		got <- toString(newVal)
	})

	source.data = map[string]any{"value": "v2"}
	if err := cfg.Reload(t.Context()); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	select {
	case v := <-got:
		if v != "v2" {
			t.Fatalf("expected watcher to receive v2, got %q", v)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("watcher not fired after Reload")
	}
}

func TestReloadWithValidationWatcherOnlyOnSuccess(t *testing.T) {
	source := &stubSource{name: "test", data: map[string]any{"value": "ok"}}
	cfg := NewManager(log.NewLogger())
	_ = cfg.AddSource(source)
	_ = cfg.Load(t.Context())

	fired := make(chan struct{}, 1)
	_ = cfg.Watch("value", func(_, _ any) { fired <- struct{}{} })

	// Failed validation — watcher must NOT fire
	source.data = map[string]any{"value": "bad"}
	_ = cfg.ReloadWithValidation(t.Context(), func(map[string]any) error {
		return errors.New("rejected")
	})
	select {
	case <-fired:
		t.Fatal("watcher fired despite validation failure")
	case <-time.After(50 * time.Millisecond):
	}
	if got := cfg.GetString("value", ""); got != "ok" {
		t.Fatalf("expected rollback to 'ok', got %q", got)
	}

	// Successful validation — watcher MUST fire
	source.data = map[string]any{"value": "good"}
	_ = cfg.ReloadWithValidation(t.Context(), func(map[string]any) error { return nil })
	select {
	case <-fired:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("watcher not fired after successful ReloadWithValidation")
	}
}

func TestUnmarshalDurationAndSlice(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	cfg.data = map[string]any{
		"timeout":  "5s",
		"tags":     "alpha,beta, gamma",
		"fallback": "2000",
	}

	type T struct {
		Timeout  time.Duration `config:"timeout"`
		Tags     []string      `config:"tags"`
		Fallback time.Duration `config:"fallback"`
	}

	var target T
	if err := cfg.Unmarshal(&target); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if target.Timeout != 5*time.Second {
		t.Errorf("timeout: want 5s, got %v", target.Timeout)
	}
	if len(target.Tags) != 3 || target.Tags[1] != "beta" {
		t.Errorf("tags: want [alpha beta gamma], got %v", target.Tags)
	}
	if target.Fallback != 2000*time.Millisecond {
		t.Errorf("fallback: want 2000ms, got %v", target.Fallback)
	}
}

func TestUnmarshalDurationMillisecondsOverflowReturnsError(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	cfg.data = map[string]any{
		"timeout": strconv.FormatInt(maxDurationMilliseconds+1, 10),
	}

	type T struct {
		Timeout time.Duration `config:"timeout"`
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Unmarshal panicked on duration overflow: %v", r)
		}
	}()

	var target T
	err := cfg.Unmarshal(&target)
	if err == nil {
		t.Fatal("expected overflow error")
	}
	if !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("expected overflow error, got %v", err)
	}
}

func TestUnmarshalNumericOverflowReturnsError(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		dst  any
	}{
		{
			name: "int8 overflow",
			data: map[string]any{"value": "128"},
			dst: &struct {
				Value int8 `config:"value"`
			}{},
		},
		{
			name: "uint8 overflow",
			data: map[string]any{"value": "256"},
			dst: &struct {
				Value uint8 `config:"value"`
			}{},
		},
		{
			name: "float32 overflow",
			data: map[string]any{"value": "3.5e39"},
			dst: &struct {
				Value float32 `config:"value"`
			}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewManager(log.NewLogger())
			cfg.data = tt.data

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Unmarshal panicked on overflow: %v", r)
				}
			}()

			err := cfg.Unmarshal(tt.dst)
			if err == nil {
				t.Fatal("expected overflow error")
			}
			if !strings.Contains(err.Error(), "overflows") {
				t.Fatalf("expected overflow error, got %v", err)
			}
		})
	}
}

func TestGetStringSliceAndHas(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	cfg.data = map[string]any{
		"hosts": "a.com,b.com, c.com",
	}

	if !cfg.Has("hosts") {
		t.Error("Has should return true for existing key")
	}
	if cfg.Has("missing") {
		t.Error("Has should return false for missing key")
	}

	got := cfg.GetStringSlice("hosts", ",", nil)
	if len(got) != 3 || got[2] != "c.com" {
		t.Errorf("GetStringSlice: want [a.com b.com c.com], got %v", got)
	}
	if def := cfg.GetStringSlice("missing", ",", []string{"x"}); len(def) != 1 || def[0] != "x" {
		t.Errorf("GetStringSlice: missing key should return default, got %v", def)
	}
}

func TestGetDuration(t *testing.T) {
	cfg := NewManager(log.NewLogger())
	cfg.data = map[string]any{
		"go_dur":  "10s",
		"bad_dur": "3000",
	}
	if d := cfg.GetDuration("go_dur", 0); d != 10*time.Second {
		t.Errorf("want 10s, got %v", d)
	}
	if d := cfg.GetDuration("bad_dur", 2*time.Second); d != 2*time.Second {
		t.Errorf("invalid duration should return default, got %v", d)
	}
	if d := cfg.GetDuration("missing", 5*time.Second); d != 5*time.Second {
		t.Errorf("want 5s default, got %v", d)
	}
}

func TestNewManagerPanicsForCompatibility(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("expected panic for nil logger")
		}
	}()
	_ = NewManager(nil)
}

// TestFileSource tests FileSource functionality
func TestFileSource(t *testing.T) {
	// Create a test JSON file
	configData := map[string]any{
		"server_port":  8080,
		"database_url": "postgres://localhost:5432/test",
		"enable_ssl":   false,
	}

	tmpFile := filepath.Join(t.TempDir(), "test_config.json")
	content, err := json.Marshal(configData)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	err = os.WriteFile(tmpFile, content, 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	source := NewFileSource(tmpFile, FormatJSON, false)

	// Test Load
	data, err := source.Load(t.Context())
	if err != nil {
		t.Fatalf("FileSource Load failed: %v", err)
	}

	// Check server_port (numbers in JSON are parsed as float64)
	serverPort, ok := data["server_port"].(float64)
	if !ok || int(serverPort) != 8080 {
		t.Errorf("Expected server_port 8080, got %v (type: %T)", data["server_port"], data["server_port"])
	} else {
		t.Logf("server_port correctly loaded: %v", int(serverPort))
	}

	// Check database_url
	databaseURL, ok := data["database_url"].(string)
	if !ok || databaseURL != "postgres://localhost:5432/test" {
		t.Errorf("Expected database_url 'postgres://localhost:5432/test', got %v", data["database_url"])
	}

	// Check enable_ssl
	enableSSL, ok := data["enable_ssl"].(bool)
	if !ok || enableSSL != false {
		t.Errorf("Expected enable_ssl false, got %v", data["enable_ssl"])
	}
}
