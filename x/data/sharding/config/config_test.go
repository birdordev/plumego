package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.CrossShardPolicy != "deny" {
		t.Errorf("expected default cross-shard policy 'deny', got %s", config.CrossShardPolicy)
	}

	if config.DefaultShardIndex != -1 {
		t.Errorf("expected default shard index -1, got %d", config.DefaultShardIndex)
	}

	if !config.EnableMetrics {
		t.Error("expected metrics to be enabled by default")
	}

	if config.EnableTracing {
		t.Error("expected tracing to be disabled by default")
	}

	if config.LogLevel != "info" {
		t.Errorf("expected default log level 'info', got %s", config.LogLevel)
	}
}

func TestShardingConfig_Validate(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		config := ShardingConfig{}
		err := config.Validate()
		if err == nil {
			t.Error("expected validation error for empty config")
		}
	})

	t.Run("no shards", func(t *testing.T) {
		config := ShardingConfig{
			ShardingRules: []ShardingRuleConfig{
				{TableName: "users", ShardKeyColumn: "id", Strategy: "mod"},
			},
		}
		err := config.Validate()
		if err == nil {
			t.Error("expected validation error for no shards")
		}
	})

	t.Run("no sharding rules", func(t *testing.T) {
		config := ShardingConfig{
			Shards: []ShardConfig{
				{Name: "shard0", Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"}},
			},
		}
		err := config.Validate()
		if err == nil {
			t.Error("expected validation error for no sharding rules")
		}
	})

	t.Run("invalid cross-shard policy", func(t *testing.T) {
		config := ShardingConfig{
			Shards: []ShardConfig{
				{Name: "shard0", Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"}},
			},
			ShardingRules: []ShardingRuleConfig{
				{TableName: "users", ShardKeyColumn: "id", Strategy: "mod"},
			},
			CrossShardPolicy: "invalid",
		}
		err := config.Validate()
		if err == nil {
			t.Error("expected validation error for invalid cross-shard policy")
		}
	})

	t.Run("invalid log level", func(t *testing.T) {
		config := ShardingConfig{
			Shards: []ShardConfig{
				{Name: "shard0", Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"}},
			},
			ShardingRules: []ShardingRuleConfig{
				{TableName: "users", ShardKeyColumn: "id", Strategy: "mod"},
			},
			CrossShardPolicy: "deny",
			LogLevel:         "invalid",
		}
		err := config.Validate()
		if err == nil {
			t.Error("expected validation error for invalid log level")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		config := ShardingConfig{
			Shards: []ShardConfig{
				{Name: "shard0", Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"}},
			},
			ShardingRules: []ShardingRuleConfig{
				{TableName: "users", ShardKeyColumn: "id", Strategy: "mod"},
			},
			CrossShardPolicy:  "deny",
			DefaultShardIndex: -1,
			LogLevel:          "info",
		}
		err := config.Validate()
		if err != nil {
			t.Errorf("expected valid config, got error: %v", err)
		}
	})
}

func TestShardConfig_Validate(t *testing.T) {
	t.Run("empty name", func(t *testing.T) {
		shard := ShardConfig{
			Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"},
		}
		err := shard.Validate()
		if err == nil {
			t.Error("expected validation error for empty name")
		}
	})

	t.Run("invalid primary", func(t *testing.T) {
		shard := ShardConfig{
			Name:    "shard0",
			Primary: DatabaseConfig{}, // Invalid
		}
		err := shard.Validate()
		if err == nil {
			t.Error("expected validation error for invalid primary")
		}
	})

	t.Run("mismatched replica weights", func(t *testing.T) {
		shard := ShardConfig{
			Name:    "shard0",
			Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"},
			Replicas: []DatabaseConfig{
				{Driver: "mysql", Host: "replica1", Database: "test"},
			},
			ReplicaWeights: []int{1, 2}, // 2 weights but 1 replica
		}
		err := shard.Validate()
		if err == nil {
			t.Error("expected validation error for mismatched replica weights")
		}
	})

	t.Run("valid shard", func(t *testing.T) {
		shard := ShardConfig{
			Name:    "shard0",
			Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"},
			Replicas: []DatabaseConfig{
				{Driver: "mysql", Host: "replica1", Database: "test"},
			},
			ReplicaWeights: []int{1},
		}
		err := shard.Validate()
		if err != nil {
			t.Errorf("expected valid shard, got error: %v", err)
		}
	})
}

func TestDatabaseConfig_Validate(t *testing.T) {
	t.Run("with DSN", func(t *testing.T) {
		db := DatabaseConfig{DSN: "mysql://user:pass@localhost/test"}
		err := db.Validate()
		if err != nil {
			t.Errorf("expected valid config with DSN, got error: %v", err)
		}
	})

	t.Run("missing driver", func(t *testing.T) {
		db := DatabaseConfig{Host: "localhost", Database: "test"}
		err := db.Validate()
		if err == nil {
			t.Error("expected validation error for missing driver")
		}
	})

	t.Run("missing host", func(t *testing.T) {
		db := DatabaseConfig{Driver: "mysql", Database: "test"}
		err := db.Validate()
		if err == nil {
			t.Error("expected validation error for missing host")
		}
	})

	t.Run("postgres missing host", func(t *testing.T) {
		db := DatabaseConfig{Driver: "postgres", Database: "test"}
		err := db.Validate()
		if err == nil {
			t.Error("expected validation error for missing postgres host")
		}
	})

	t.Run("missing database", func(t *testing.T) {
		db := DatabaseConfig{Driver: "mysql", Host: "localhost"}
		err := db.Validate()
		if err == nil {
			t.Error("expected validation error for missing database")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		db := DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"}
		err := db.Validate()
		if err != nil {
			t.Errorf("expected valid config, got error: %v", err)
		}
	})

	t.Run("valid sqlite without host", func(t *testing.T) {
		db := DatabaseConfig{Driver: "sqlite3", Database: "/tmp/test.sqlite"}
		err := db.Validate()
		if err != nil {
			t.Errorf("expected valid sqlite config without host, got error: %v", err)
		}
	})

	t.Run("valid postgres ssl mode", func(t *testing.T) {
		db := DatabaseConfig{Driver: "postgres", Host: "localhost", Database: "test", SSLMode: "verify-full"}
		err := db.Validate()
		if err != nil {
			t.Errorf("expected valid postgres ssl_mode, got error: %v", err)
		}
	})

	t.Run("invalid postgres ssl mode", func(t *testing.T) {
		db := DatabaseConfig{Driver: "postgres", Host: "localhost", Database: "test", SSLMode: "invalid"}
		err := db.Validate()
		if err == nil || !strings.Contains(err.Error(), "invalid postgres ssl_mode") {
			t.Errorf("expected invalid postgres ssl_mode error, got %v", err)
		}
	})

	t.Run("mysql rejects ssl mode", func(t *testing.T) {
		db := DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test", SSLMode: "require"}
		err := db.Validate()
		if err == nil || !strings.Contains(err.Error(), "only supported for postgres") {
			t.Errorf("expected mysql ssl_mode error, got %v", err)
		}
	})

	t.Run("sqlite missing database", func(t *testing.T) {
		db := DatabaseConfig{Driver: "sqlite3"}
		err := db.Validate()
		if err == nil {
			t.Error("expected validation error for sqlite missing database")
		}
	})

	t.Run("unsupported driver", func(t *testing.T) {
		db := DatabaseConfig{Driver: "oracle", Host: "localhost", Database: "test"}
		err := db.Validate()
		if err == nil || !strings.Contains(err.Error(), "unsupported driver") {
			t.Errorf("expected unsupported driver error, got %v", err)
		}
	})
}

func TestDatabaseConfig_BuildDSN(t *testing.T) {
	t.Run("mysql with password", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "mysql",
			Host:     "localhost",
			Port:     3306,
			Database: "testdb",
			Username: "user",
			Password: "pass",
		}
		dsn := db.BuildDSN()
		expected := "user:pass@tcp(localhost:3306)/testdb"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("mysql without password", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "mysql",
			Host:     "localhost",
			Port:     3306,
			Database: "testdb",
			Username: "user",
		}
		dsn := db.BuildDSN()
		expected := "user@tcp(localhost:3306)/testdb"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("mysql default port", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "mysql",
			Host:     "localhost",
			Database: "testdb",
			Username: "user",
		}
		dsn := db.BuildDSN()
		expected := "user@tcp(localhost:3306)/testdb"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("mysql escapes credentials and database", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "mysql",
			Host:     "localhost",
			Database: "test db",
			Username: "user:name",
			Password: "p@ ss",
		}
		dsn := db.BuildDSN()
		expected := "user%3Aname:p%40%20ss@tcp(localhost:3306)/test%20db"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("postgres", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "postgres",
			Host:     "localhost",
			Port:     5432,
			Database: "testdb",
			Username: "user",
			Password: "pass",
		}
		dsn := db.BuildDSN()
		expected := "host=localhost port=5432 dbname=testdb user=user password=pass sslmode=disable"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("postgres configured ssl mode", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "postgres",
			Host:     "localhost",
			Database: "testdb",
			Username: "user",
			SSLMode:  "verify-full",
		}
		dsn := db.BuildDSN()
		expected := "host=localhost port=5432 dbname=testdb user=user sslmode=verify-full"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("postgres default port", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "postgres",
			Host:     "localhost",
			Database: "testdb",
			Username: "user",
		}
		dsn := db.BuildDSN()
		expected := "host=localhost port=5432 dbname=testdb user=user sslmode=disable"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("postgres quotes special values", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "postgres",
			Host:     "localhost",
			Database: "test db",
			Username: "user",
			Password: `pa ss'word\`,
		}
		dsn := db.BuildDSN()
		expected := `host=localhost port=5432 dbname='test db' user=user password='pa ss\'word\\' sslmode=disable`
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("sqlite3", func(t *testing.T) {
		db := DatabaseConfig{
			Driver:   "sqlite3",
			Database: "/path/to/db.sqlite",
		}
		dsn := db.BuildDSN()
		expected := "/path/to/db.sqlite"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})

	t.Run("with custom DSN", func(t *testing.T) {
		db := DatabaseConfig{
			DSN: "custom://dsn",
		}
		dsn := db.BuildDSN()
		expected := "custom://dsn"
		if dsn != expected {
			t.Errorf("expected DSN %s, got %s", expected, dsn)
		}
	})
}

func TestShardingRuleConfig_Validate(t *testing.T) {
	t.Run("missing table name", func(t *testing.T) {
		rule := ShardingRuleConfig{
			ShardKeyColumn: "id",
			Strategy:       "mod",
		}
		err := rule.Validate()
		if err == nil {
			t.Error("expected validation error for missing table name")
		}
	})

	t.Run("missing shard key column", func(t *testing.T) {
		rule := ShardingRuleConfig{
			TableName: "users",
			Strategy:  "mod",
		}
		err := rule.Validate()
		if err == nil {
			t.Error("expected validation error for missing shard key column")
		}
	})

	t.Run("invalid strategy", func(t *testing.T) {
		rule := ShardingRuleConfig{
			TableName:      "users",
			ShardKeyColumn: "id",
			Strategy:       "invalid",
		}
		err := rule.Validate()
		if err == nil {
			t.Error("expected validation error for invalid strategy")
		}
	})

	t.Run("range strategy without ranges", func(t *testing.T) {
		rule := ShardingRuleConfig{
			TableName:      "users",
			ShardKeyColumn: "id",
			Strategy:       "range",
		}
		err := rule.Validate()
		if err == nil {
			t.Error("expected validation error for range strategy without ranges")
		}
	})

	t.Run("valid rule", func(t *testing.T) {
		rule := ShardingRuleConfig{
			TableName:      "users",
			ShardKeyColumn: "id",
			Strategy:       "mod",
		}
		err := rule.Validate()
		if err != nil {
			t.Errorf("expected valid rule, got error: %v", err)
		}
	})
}

func TestLoadFromJSON(t *testing.T) {
	jsonData := []byte(`{
		"shards": [
			{
				"name": "shard0",
				"primary": {
					"driver": "mysql",
					"host": "localhost",
					"database": "test"
				}
			}
		],
		"sharding_rules": [
			{
				"table_name": "users",
				"shard_key_column": "id",
				"strategy": "mod"
			}
		],
		"cross_shard_policy": "deny",
		"log_level": "info"
	}`)

	config, err := LoadFromJSON(jsonData)
	if err != nil {
		t.Fatalf("failed to load JSON: %v", err)
	}

	if len(config.Shards) != 1 {
		t.Errorf("expected 1 shard, got %d", len(config.Shards))
	}

	if config.Shards[0].Name != "shard0" {
		t.Errorf("expected shard name 'shard0', got %s", config.Shards[0].Name)
	}

	if len(config.ShardingRules) != 1 {
		t.Errorf("expected 1 sharding rule, got %d", len(config.ShardingRules))
	}

	if config.ShardingRules[0].TableName != "users" {
		t.Errorf("expected table name 'users', got %s", config.ShardingRules[0].TableName)
	}
}

func TestLoadFromJSONFile(t *testing.T) {
	// Create temp JSON file
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

	config, err := LoadFromJSONFile(tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to load from JSON file: %v", err)
	}

	if len(config.Shards) != 1 {
		t.Errorf("expected 1 shard, got %d", len(config.Shards))
	}
}

func TestMergeWithEnv(t *testing.T) {
	// Save and restore original env vars
	oldPolicy := os.Getenv("DB_SHARD_CROSS_SHARD_POLICY")
	oldIndex := os.Getenv("DB_SHARD_DEFAULT_INDEX")
	oldMetrics := os.Getenv("DB_SHARD_ENABLE_METRICS")
	oldTracing := os.Getenv("DB_SHARD_ENABLE_TRACING")
	oldLogLevel := os.Getenv("DB_SHARD_LOG_LEVEL")

	defer func() {
		os.Setenv("DB_SHARD_CROSS_SHARD_POLICY", oldPolicy)
		os.Setenv("DB_SHARD_DEFAULT_INDEX", oldIndex)
		os.Setenv("DB_SHARD_ENABLE_METRICS", oldMetrics)
		os.Setenv("DB_SHARD_ENABLE_TRACING", oldTracing)
		os.Setenv("DB_SHARD_LOG_LEVEL", oldLogLevel)
	}()

	config := DefaultConfig()
	config.Shards = []ShardConfig{
		{Name: "shard0", Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"}},
	}
	config.ShardingRules = []ShardingRuleConfig{
		{TableName: "users", ShardKeyColumn: "id", Strategy: "mod"},
	}

	// Set environment variables
	os.Setenv("DB_SHARD_CROSS_SHARD_POLICY", "first_success")
	os.Setenv("DB_SHARD_DEFAULT_INDEX", "0")
	os.Setenv("DB_SHARD_ENABLE_METRICS", "false")
	os.Setenv("DB_SHARD_ENABLE_TRACING", "true")
	os.Setenv("DB_SHARD_LOG_LEVEL", "debug")

	err := config.MergeWithEnv()
	if err != nil {
		t.Fatalf("failed to merge with env: %v", err)
	}

	if config.CrossShardPolicy != "first_success" {
		t.Errorf("expected cross-shard policy 'first_success', got %s", config.CrossShardPolicy)
	}

	if config.DefaultShardIndex != 0 {
		t.Errorf("expected default shard index 0, got %d", config.DefaultShardIndex)
	}

	if config.EnableMetrics {
		t.Error("expected metrics to be disabled")
	}

	if !config.EnableTracing {
		t.Error("expected tracing to be enabled")
	}

	if config.LogLevel != "debug" {
		t.Errorf("expected log level 'debug', got %s", config.LogLevel)
	}
}

func TestMergeWithEnvValidatesMergedConfig(t *testing.T) {
	t.Setenv("DB_SHARD_CROSS_SHARD_POLICY", "unsafe")

	config := DefaultConfig()
	config.Shards = []ShardConfig{
		{Name: "shard0", Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"}},
	}
	config.ShardingRules = []ShardingRuleConfig{
		{TableName: "users", ShardKeyColumn: "id", Strategy: "mod"},
	}

	err := config.MergeWithEnv()
	if err == nil || !strings.Contains(err.Error(), "invalid merged configuration") {
		t.Fatalf("MergeWithEnv() error = %v, want validation error", err)
	}
}

func TestMergeWithEnvRejectsInvalidPrimitiveValues(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "default index", key: "DB_SHARD_DEFAULT_INDEX", val: "nope"},
		{name: "metrics", key: "DB_SHARD_ENABLE_METRICS", val: "maybe"},
		{name: "tracing", key: "DB_SHARD_ENABLE_TRACING", val: "maybe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.val)
			config := DefaultConfig()
			config.Shards = []ShardConfig{
				{Name: "shard0", Primary: DatabaseConfig{Driver: "mysql", Host: "localhost", Database: "test"}},
			}
			config.ShardingRules = []ShardingRuleConfig{
				{TableName: "users", ShardKeyColumn: "id", Strategy: "mod"},
			}
			if err := config.MergeWithEnv(); err == nil {
				t.Fatalf("MergeWithEnv() error = nil, want invalid env error")
			}
		})
	}
}

func TestParseBool(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"YES", true},
		{"on", true},
		{"ON", true},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"no", false},
		{"off", false},
		{"invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseBool(tt.input)
			if result != tt.expected {
				t.Errorf("parseBool(%s) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDatabaseConfigParsesConnectionDurations(t *testing.T) {
	for _, tc := range []struct {
		name, life, idle     string
		wantLife, wantIdle   time.Duration
		lifeError, idleError bool
	}{
		{name: "empty"},
		{name: "valid", life: "1h2m3s", idle: "1500ms", wantLife: time.Hour + 2*time.Minute + 3*time.Second, wantIdle: 1500 * time.Millisecond},
		{name: "invalid lifetime", life: "invalid", idle: "2s", wantIdle: 2 * time.Second, lifeError: true},
		{name: "invalid idle", life: "2m", idle: "invalid", wantLife: 2 * time.Minute, idleError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DatabaseConfig{ConnMaxLifetime: tc.life, ConnMaxIdleTime: tc.idle}
			if got, err := cfg.ParseConnMaxLifetime(); got != tc.wantLife || (err != nil) != tc.lifeError {
				t.Errorf("lifetime=%v, %v", got, err)
			}
			if got, err := cfg.ParseConnMaxIdleTime(); got != tc.wantIdle || (err != nil) != tc.idleError {
				t.Errorf("idle=%v, %v", got, err)
			}
		})
	}
}
