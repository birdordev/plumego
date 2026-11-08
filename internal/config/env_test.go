package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/log"
)

func TestLoadEnvFilePreservesExistingValues(t *testing.T) {
	// Create a temporary file to simulate .env
	content := `
# Comment line
DB_HOST=127.0.0.1
DB_USER=root
DB_PASS="secret"
EMPTY_KEY=
QUOTED_KEY='quoted_value'
`
	tmpFile := filepath.Join(t.TempDir(), "test.env")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	unsetEnvForTest(t, "DB_HOST", "DB_USER", "DB_PASS", "EMPTY_KEY", "QUOTED_KEY")

	// Set an existing variable to ensure it is not overwritten
	t.Setenv("DB_USER", "existing_user")

	// Call LoadEnvFile
	err = LoadEnvFile(tmpFile, false)
	if err != nil {
		t.Fatalf("LoadEnvFile execution failed: %v", err)
	}

	// Validate results
	tests := []struct {
		key      string
		expected string
	}{
		{"DB_HOST", "127.0.0.1"},
		{"DB_USER", "existing_user"}, // do not overwrite existing values
		{"DB_PASS", "secret"},
		{"EMPTY_KEY", ""},
		{"QUOTED_KEY", "quoted_value"},
	}

	for _, tt := range tests {
		got := os.Getenv(tt.key)
		if got != tt.expected {
			t.Errorf("Environment variable %s = %q, expected %q", tt.key, got, tt.expected)
		}
	}
}

func TestLoadEnvWithOverwrite(t *testing.T) {
	content := `
DB_HOST=127.0.0.1
DB_USER=root
`
	tmpFile := filepath.Join(t.TempDir(), "test_overwrite.env")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	// Scenario 1: do not overwrite existing values
	unsetEnvForTest(t, "DB_HOST", "DB_USER")
	t.Setenv("DB_USER", "existing_user")
	err = LoadEnvFile(tmpFile, false)
	if err != nil {
		t.Fatalf("LoadEnvFile execution failed: %v", err)
	}
	if got := os.Getenv("DB_USER"); got != "existing_user" {
		t.Errorf("DB_USER should remain existing_user, got %q", got)
	}

	// Scenario 2: overwrite existing values
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_USER", "existing_user")
	err = LoadEnvFile(tmpFile, true)
	if err != nil {
		t.Fatalf("LoadEnvFile execution failed: %v", err)
	}
	if got := os.Getenv("DB_USER"); got != "root" {
		t.Errorf("DB_USER should be overwritten to root, got %q", got)
	}
}

func TestLoadEnvAcceptsLongValue(t *testing.T) {
	value := strings.Repeat("a", 70*1024)
	tmpFile := filepath.Join(t.TempDir(), "long.env")
	if err := os.WriteFile(tmpFile, []byte("LONG_VALUE="+value+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write env file: %v", err)
	}

	unsetEnvForTest(t, "LONG_VALUE")
	if err := LoadEnvFile(tmpFile, true); err != nil {
		t.Fatalf("LoadEnvFile execution failed: %v", err)
	}
	if got := os.Getenv("LONG_VALUE"); got != value {
		t.Fatalf("LONG_VALUE length = %d, want %d", len(got), len(value))
	}
}

func TestLoadEnvFileNotFound(t *testing.T) {
	err := LoadEnvFile("nonexistent.env", false)
	if err == nil {
		t.Fatal("Expected LoadEnvFile to return an error, but it did not")
	}
}

func TestLoadEnvFileReturnsSetenvError(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "invalid.env")
	if err := os.WriteFile(tmpFile, []byte("BAD\x00KEY=value\n"), 0644); err != nil {
		t.Fatalf("failed to write env file: %v", err)
	}

	if err := LoadEnvFile(tmpFile, true); err == nil {
		t.Fatal("expected invalid environment key to return an error")
	}
}

func TestSourcesRespectCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := NewEnvSource("").Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnvSource.Load error = %v, want context.Canceled", err)
	}

	tmpFile := filepath.Join(t.TempDir(), "config.env")
	if err := os.WriteFile(tmpFile, []byte("APP_NAME=demo\n"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	if _, err := NewFileSource(tmpFile, FormatEnv, false).Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("FileSource.Load error = %v, want context.Canceled", err)
	}
}

func TestGetHelpers(t *testing.T) {
	// Create custom config instance
	cfg := NewManager(log.NewLogger())

	// Add environment variable source
	envSource := NewEnvSource("")
	_ = cfg.AddSource(envSource)

	// Load configuration
	ctx := t.Context()
	err := cfg.Load(ctx)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test default value
	if got := cfg.GetString("MISSING", "default"); got != "default" {
		t.Fatalf("GetString default fallback failed: %q", got)
	}

	// Set environment variable
	t.Setenv("TEST_STRING", "  spaced ")

	// Reload configuration to get new environment variables
	err = cfg.Load(ctx)
	if err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}

	// Test string
	if got := cfg.GetString("test_string", "default"); got != "spaced" {
		t.Fatalf("GetString should trim whitespace, got %q", got)
	}

	// Test integer
	t.Setenv("TEST_INT", "notanint")
	_ = cfg.Load(ctx)
	if got := cfg.GetInt("test_int", 5); got != 5 {
		t.Fatalf("GetInt should fallback to default on invalid input, got %d", got)
	}
	t.Setenv("TEST_INT", " 42 ")
	_ = cfg.Load(ctx)
	if got := cfg.GetInt("test_int", 5); got != 42 {
		t.Fatalf("GetInt should parse trimmed integer, got %d", got)
	}

	// Test boolean
	t.Setenv("TEST_BOOL_TRUE", "yes")
	_ = cfg.Load(ctx)
	if !cfg.GetBool("test_bool_true", false) {
		t.Fatalf("GetBool should parse affirmative values")
	}
	t.Setenv("TEST_BOOL_FALSE", "OFF")
	_ = cfg.Load(ctx)
	if cfg.GetBool("test_bool_false", true) {
		t.Fatalf("GetBool should parse negative values")
	}
	t.Setenv("TEST_BOOL_INVALID", "maybe")
	_ = cfg.Load(ctx)
	if !cfg.GetBool("test_bool_invalid", true) {
		t.Fatalf("GetBool should fallback to default on invalid input")
	}

	// Test float
	t.Setenv("TEST_FLOAT", " 1.5 ")
	_ = cfg.Load(ctx)
	if got := cfg.GetFloat("test_float", 0); got != 1.5 {
		t.Fatalf("GetFloat should parse trimmed float, got %f", got)
	}

	// Test duration
	t.Setenv("TEST_DURATION_MS", " 10 ")
	_ = cfg.Load(ctx)
	if got := cfg.GetDurationMs("test_duration_ms", 0); got != 10*time.Millisecond {
		t.Fatalf("GetDurationMs should parse milliseconds, got %s", got)
	}
}
