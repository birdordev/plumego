package sharding

import (
	"errors"
	"testing"
)

func setupTestResolver() (*ShardKeyResolver, *ShardingRuleRegistry) {
	registry := NewShardingRuleRegistry()

	// Register users table with mod strategy
	_ = registry.RegisterWithStrategy("users", "user_id", NewModStrategy(), 4)

	// Register orders table with hash strategy
	_ = registry.RegisterWithStrategy("orders", "order_id", NewHashStrategy(), 4)

	resolver := NewShardKeyResolver(registry)
	return resolver, registry
}

func TestShardKeyResolver_Resolve_Select(t *testing.T) {
	resolver, _ := setupTestResolver()

	tests := []struct {
		name           string
		query          string
		args           []any
		wantShardIndex int
		wantShardKey   any
		wantErr        bool
	}{
		{
			name:           "simple select with user_id",
			query:          "SELECT * FROM users WHERE user_id = ?",
			args:           []any{100},
			wantShardIndex: 0, // 100 % 4 = 0
			wantShardKey:   100,
			wantErr:        false,
		},
		{
			name:           "select with user_id = 1",
			query:          "SELECT * FROM users WHERE user_id = ?",
			args:           []any{1},
			wantShardIndex: 1, // 1 % 4 = 1
			wantShardKey:   1,
			wantErr:        false,
		},
		{
			name:           "select with user_id = 2",
			query:          "SELECT * FROM users WHERE user_id = ?",
			args:           []any{2},
			wantShardIndex: 2, // 2 % 4 = 2
			wantShardKey:   2,
			wantErr:        false,
		},
		{
			name:           "select with multiple conditions",
			query:          "SELECT * FROM users WHERE user_id = ? AND status = ?",
			args:           []any{5, "active"},
			wantShardIndex: 1, // 5 % 4 = 1
			wantShardKey:   5,
			wantErr:        false,
		},
		{
			name:           "select with postgres placeholders after non-shard condition",
			query:          "SELECT * FROM users WHERE status = $1 AND user_id = $2",
			args:           []any{"active", 6},
			wantShardIndex: 2, // 6 % 4 = 2
			wantShardKey:   6,
			wantErr:        false,
		},
		{
			name:           "select with trailing lock clause",
			query:          "SELECT * FROM users WHERE user_id = ? FOR UPDATE",
			args:           []any{42},
			wantShardIndex: 2, // 42 % 4 = 2
			wantShardKey:   42,
			wantErr:        false,
		},
		{
			name:           "select with out-of-order postgres shard placeholder",
			query:          "SELECT * FROM users WHERE user_id = $2 AND status = $1",
			args:           []any{"active", 9},
			wantShardIndex: 1, // 9 % 4 = 1
			wantShardKey:   9,
			wantErr:        false,
		},
		{
			name:    "select without shard key",
			query:   "SELECT * FROM users WHERE email = ?",
			args:    []any{"test@example.com"},
			wantErr: true,
		},
		{
			name:    "select without where clause",
			query:   "SELECT * FROM users",
			args:    []any{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolver.Resolve(tt.query, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Resolve() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Resolve() unexpected error: %v", err)
			}

			if resolved.ShardIndex != tt.wantShardIndex {
				t.Errorf("ShardIndex = %d, want %d", resolved.ShardIndex, tt.wantShardIndex)
			}

			if resolved.ShardKey != tt.wantShardKey {
				t.Errorf("ShardKey = %v, want %v", resolved.ShardKey, tt.wantShardKey)
			}

			if resolved.TableName != "users" {
				t.Errorf("TableName = %q, want users", resolved.TableName)
			}
		})
	}
}

func TestShardKeyResolver_Resolve_Insert(t *testing.T) {
	resolver, _ := setupTestResolver()

	tests := []struct {
		name           string
		query          string
		args           []any
		wantShardIndex int
		wantShardKey   any
		wantErr        bool
	}{
		{
			name:           "insert with user_id",
			query:          "INSERT INTO users (user_id, name, email) VALUES (?, ?, ?)",
			args:           []any{100, "John", "john@example.com"},
			wantShardIndex: 0, // 100 % 4 = 0
			wantShardKey:   100,
			wantErr:        false,
		},
		{
			name:           "insert with user_id = 5",
			query:          "INSERT INTO users (user_id, email) VALUES (?, ?)",
			args:           []any{5, "test@example.com"},
			wantShardIndex: 1, // 5 % 4 = 1
			wantShardKey:   5,
			wantErr:        false,
		},
		{
			name:    "insert without shard key column",
			query:   "INSERT INTO users (name, email) VALUES (?, ?)",
			args:    []any{"John", "john@example.com"},
			wantErr: true,
		},
		{
			name:    "insert with insufficient args",
			query:   "INSERT INTO users (user_id, name) VALUES (?, ?)",
			args:    []any{100}, // Missing second arg
			wantErr: true,
		},
		{
			name:    "insert expression value fails closed",
			query:   "INSERT INTO users (user_id, created_at) VALUES (?, now())",
			args:    []any{100},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolver.Resolve(tt.query, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Resolve() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Resolve() unexpected error: %v", err)
			}

			if resolved.ShardIndex != tt.wantShardIndex {
				t.Errorf("ShardIndex = %d, want %d", resolved.ShardIndex, tt.wantShardIndex)
			}

			if resolved.ShardKey != tt.wantShardKey {
				t.Errorf("ShardKey = %v, want %v", resolved.ShardKey, tt.wantShardKey)
			}
		})
	}
}

func TestShardKeyResolver_Resolve_Update(t *testing.T) {
	resolver, _ := setupTestResolver()

	tests := []struct {
		name           string
		query          string
		args           []any
		wantShardIndex int
		wantShardKey   any
		wantErr        bool
	}{
		{
			name:           "update with user_id",
			query:          "UPDATE users SET name = ? WHERE user_id = ?",
			args:           []any{"NewName", 100},
			wantShardIndex: 0, // 100 % 4 = 0
			wantShardKey:   100,
			wantErr:        false,
		},
		{
			name:           "update with multiple conditions",
			query:          "UPDATE users SET status = ? WHERE user_id = ? AND email = ?",
			args:           []any{"active", 7, "test@example.com"},
			wantShardIndex: 3, // 7 % 4 = 3
			wantShardKey:   7,
			wantErr:        false,
		},
		{
			name:           "update with postgres placeholders",
			query:          "UPDATE users SET status = $1 WHERE user_id = $2",
			args:           []any{"active", 10},
			wantShardIndex: 2, // 10 % 4 = 2
			wantShardKey:   10,
			wantErr:        false,
		},
		{
			name:           "update with postgres placeholders after non-shard where condition",
			query:          "UPDATE users SET status = $1 WHERE email = $2 AND user_id = $3",
			args:           []any{"active", "test@example.com", 11},
			wantShardIndex: 3, // 11 % 4 = 3
			wantShardKey:   11,
			wantErr:        false,
		},
		{
			name:    "update without shard key in where",
			query:   "UPDATE users SET name = ? WHERE email = ?",
			args:    []any{"NewName", "test@example.com"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolver.Resolve(tt.query, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Resolve() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Resolve() unexpected error: %v", err)
			}

			if resolved.ShardIndex != tt.wantShardIndex {
				t.Errorf("ShardIndex = %d, want %d", resolved.ShardIndex, tt.wantShardIndex)
			}

			if resolved.ShardKey != tt.wantShardKey {
				t.Errorf("ShardKey = %v, want %v", resolved.ShardKey, tt.wantShardKey)
			}
		})
	}
}

func TestShardKeyResolver_Resolve_Delete(t *testing.T) {
	resolver, _ := setupTestResolver()

	tests := []struct {
		name           string
		query          string
		args           []any
		wantShardIndex int
		wantShardKey   any
		wantErr        bool
	}{
		{
			name:           "delete with user_id",
			query:          "DELETE FROM users WHERE user_id = ?",
			args:           []any{100},
			wantShardIndex: 0, // 100 % 4 = 0
			wantShardKey:   100,
			wantErr:        false,
		},
		{
			name:           "delete with multiple conditions",
			query:          "DELETE FROM users WHERE user_id = ? AND status = ?",
			args:           []any{9, "inactive"},
			wantShardIndex: 1, // 9 % 4 = 1
			wantShardKey:   9,
			wantErr:        false,
		},
		{
			name:    "delete without shard key",
			query:   "DELETE FROM users WHERE email = ?",
			args:    []any{"test@example.com"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolver.Resolve(tt.query, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Resolve() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Resolve() unexpected error: %v", err)
			}

			if resolved.ShardIndex != tt.wantShardIndex {
				t.Errorf("ShardIndex = %d, want %d", resolved.ShardIndex, tt.wantShardIndex)
			}

			if resolved.ShardKey != tt.wantShardKey {
				t.Errorf("ShardKey = %v, want %v", resolved.ShardKey, tt.wantShardKey)
			}
		})
	}
}

func TestShardKeyResolver_Resolve_DefaultShard(t *testing.T) {
	registry := NewShardingRuleRegistry()

	rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
	_ = rule.SetDefaultShard(2)
	if err := registry.Register(rule); err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	resolver := NewShardKeyResolver(registry)

	t.Run("use default shard when shard key not found", func(t *testing.T) {
		resolved, err := resolver.Resolve("SELECT * FROM users WHERE email = ?", []any{"test@example.com"})

		if err != nil {
			t.Fatalf("Resolve() unexpected error: %v", err)
		}

		if resolved.ShardIndex != 2 {
			t.Errorf("ShardIndex = %d, want 2 (default)", resolved.ShardIndex)
		}

		if resolved.ShardKey != nil {
			t.Errorf("ShardKey = %v, want nil", resolved.ShardKey)
		}
	})

	t.Run("use calculated shard when shard key present", func(t *testing.T) {
		resolved, err := resolver.Resolve("SELECT * FROM users WHERE user_id = ?", []any{100})

		if err != nil {
			t.Fatalf("Resolve() unexpected error: %v", err)
		}

		if resolved.ShardIndex != 0 {
			t.Errorf("ShardIndex = %d, want 0 (calculated, not default)", resolved.ShardIndex)
		}
	})
}

func TestShardKeyResolver_Resolve_NoShardingRule(t *testing.T) {
	resolver, _ := setupTestResolver()

	_, err := resolver.Resolve("SELECT * FROM unknown_table WHERE id = ?", []any{1})

	if err == nil {
		t.Errorf("Resolve() expected error for unknown table, got nil")
	}

	if !errors.Is(err, ErrNoShardingRule) {
		t.Errorf("Resolve() error = %v, want %v", err, ErrNoShardingRule)
	}
}

func TestShardKeyResolver_ResolveTableOnly(t *testing.T) {
	resolver, _ := setupTestResolver()

	tests := []struct {
		name      string
		query     string
		wantTable string
		wantErr   bool
	}{
		{
			name:      "select",
			query:     "SELECT * FROM users WHERE id = ?",
			wantTable: "users",
			wantErr:   false,
		},
		{
			name:      "insert",
			query:     "INSERT INTO orders (id) VALUES (?)",
			wantTable: "orders",
			wantErr:   false,
		},
		{
			name:      "update",
			query:     "UPDATE users SET name = ?",
			wantTable: "users",
			wantErr:   false,
		},
		{
			name:      "delete",
			query:     "DELETE FROM orders WHERE id = ?",
			wantTable: "orders",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tableName, err := resolver.ResolveTableOnly(tt.query)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveTableOnly() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ResolveTableOnly() unexpected error: %v", err)
			}

			if tableName != tt.wantTable {
				t.Errorf("ResolveTableOnly() = %q, want %q", tableName, tt.wantTable)
			}
		})
	}
}

func TestShardKeyResolver_CanResolve(t *testing.T) {
	resolver, _ := setupTestResolver()

	tests := []struct {
		name    string
		query   string
		wantCan bool
		wantErr bool
	}{
		{
			name:    "select with shard key",
			query:   "SELECT * FROM users WHERE user_id = ?",
			wantCan: true,
			wantErr: false,
		},
		{
			name:    "select without shard key",
			query:   "SELECT * FROM users WHERE email = ?",
			wantCan: false,
			wantErr: false,
		},
		{
			name:    "select with shard key range",
			query:   "SELECT * FROM users WHERE user_id >= ? AND user_id <= ?",
			wantCan: true,
			wantErr: false,
		},
		{
			name:    "insert with shard key",
			query:   "INSERT INTO users (user_id, name) VALUES (?, ?)",
			wantCan: true,
			wantErr: false,
		},
		{
			name:    "insert without shard key",
			query:   "INSERT INTO users (name, email) VALUES (?, ?)",
			wantCan: false,
			wantErr: false,
		},
		{
			name:    "unknown table",
			query:   "SELECT * FROM unknown WHERE id = ?",
			wantCan: false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			can, err := resolver.CanResolve(tt.query)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CanResolve() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("CanResolve() unexpected error: %v", err)
			}

			if can != tt.wantCan {
				t.Errorf("CanResolve() = %v, want %v", can, tt.wantCan)
			}
		})
	}

	// Test with default shard configured
	t.Run("can resolve with default shard", func(t *testing.T) {
		registry := NewShardingRuleRegistry()
		rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
		_ = rule.SetDefaultShard(0)
		if err := registry.Register(rule); err != nil {
			t.Fatalf("Register() unexpected error: %v", err)
		}
		resolver := NewShardKeyResolver(registry)

		can, err := resolver.CanResolve("SELECT * FROM users WHERE email = ?")
		if err != nil {
			t.Fatalf("CanResolve() unexpected error: %v", err)
		}

		if !can {
			t.Errorf("CanResolve() = false, want true (with default shard)")
		}
	})
}

func TestShardKeyResolver_ResolveMultiple(t *testing.T) {
	resolver, _ := setupTestResolver()

	t.Run("single shard", func(t *testing.T) {
		resolved, err := resolver.ResolveMultiple("SELECT * FROM users WHERE user_id = ?", []any{100})
		if err != nil {
			t.Fatalf("ResolveMultiple() unexpected error: %v", err)
		}

		if len(resolved) != 1 {
			t.Errorf("ResolveMultiple() returned %d shards, want 1", len(resolved))
		}

		if resolved[0].ShardIndex != 0 {
			t.Errorf("ShardIndex = %d, want 0", resolved[0].ShardIndex)
		}
	})

	t.Run("in clause", func(t *testing.T) {
		resolved, err := resolver.ResolveMultiple("SELECT * FROM users WHERE user_id IN (?, ?, ?)", []any{1, 2, 3})
		if err != nil {
			t.Fatalf("ResolveMultiple() unexpected error: %v", err)
		}

		if len(resolved) != 3 {
			t.Errorf("ResolveMultiple() returned %d shards, want 3", len(resolved))
		}

		got := []int{resolved[0].ShardIndex, resolved[1].ShardIndex, resolved[2].ShardIndex}
		want := []int{1, 2, 3}
		for i, shard := range want {
			if got[i] != shard {
				t.Errorf("ShardIndex[%d] = %d, want %d", i, got[i], shard)
			}
		}
	})

	t.Run("range query", func(t *testing.T) {
		resolved, err := resolver.ResolveMultiple("SELECT * FROM users WHERE user_id >= ? AND user_id <= ?", []any{1, 2})
		if err != nil {
			t.Fatalf("ResolveMultiple() unexpected error: %v", err)
		}

		if len(resolved) != 2 {
			t.Errorf("ResolveMultiple() returned %d shards, want 2", len(resolved))
		}

		got := []int{resolved[0].ShardIndex, resolved[1].ShardIndex}
		want := []int{1, 2}
		for i, shard := range want {
			if got[i] != shard {
				t.Errorf("ShardIndex[%d] = %d, want %d", i, got[i], shard)
			}
		}
	})
}

func TestResolvedShard_GetActualTableName(t *testing.T) {
	resolver, _ := setupTestResolver()

	t.Run("without physical table names", func(t *testing.T) {
		resolved, err := resolver.Resolve("SELECT * FROM users WHERE user_id = ?", []any{100})
		if err != nil {
			t.Fatalf("Resolve() unexpected error: %v", err)
		}

		actualName := resolved.GetActualTableName()
		if actualName != "users" {
			t.Errorf("GetActualTableName() = %q, want users", actualName)
		}
	})

	t.Run("with physical table names", func(t *testing.T) {
		registry := NewShardingRuleRegistry()
		rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
		rule.SetActualTableName(0, "users_0")
		rule.SetActualTableName(1, "users_1")
		if err := registry.Register(rule); err != nil {
			t.Fatalf("Register() unexpected error: %v", err)
		}
		resolver := NewShardKeyResolver(registry)

		resolved, err := resolver.Resolve("SELECT * FROM users WHERE user_id = ?", []any{100})
		if err != nil {
			t.Fatalf("Resolve() unexpected error: %v", err)
		}

		actualName := resolved.GetActualTableName()
		if actualName != "users_0" {
			t.Errorf("GetActualTableName() = %q, want users_0", actualName)
		}
	})
}

func TestShardKeyResolver_Resolve_WithHashStrategy(t *testing.T) {
	resolver, _ := setupTestResolver()

	// Test with orders table which uses hash strategy
	resolved, err := resolver.Resolve("SELECT * FROM orders WHERE order_id = ?", []any{"order-123"})

	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}

	// Just verify it resolved to a valid shard
	if resolved.ShardIndex < 0 || resolved.ShardIndex >= 4 {
		t.Errorf("ShardIndex = %d, want 0-3", resolved.ShardIndex)
	}

	if resolved.ShardKey != "order-123" {
		t.Errorf("ShardKey = %v, want order-123", resolved.ShardKey)
	}

	if resolved.TableName != "orders" {
		t.Errorf("TableName = %q, want orders", resolved.TableName)
	}
}

func TestResolvedShard_String(t *testing.T) {
	resolver, _ := setupTestResolver()

	resolved, _ := resolver.Resolve("SELECT * FROM users WHERE user_id = ?", []any{100})

	str := resolved.String()
	if str == "" {
		t.Errorf("String() returned empty string")
	}

	// Just verify it contains key information
	if !contains(str, "users") {
		t.Errorf("String() = %q, should contain table name", str)
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			len(s) > len(substr)+1 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
