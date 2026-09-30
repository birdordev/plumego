package sharding

import (
	"errors"
	"strings"
	"testing"
)

func TestSQLRewriter_Rewrite(t *testing.T) {
	// Create registry with test sharding rules
	registry := NewShardingRuleRegistry()

	// Add users table with physical table names
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
	usersRule.SetActualTableName(0, "users_0")
	usersRule.SetActualTableName(1, "users_1")
	usersRule.SetActualTableName(2, "users_2")
	usersRule.SetActualTableName(3, "users_3")
	registry.Register(usersRule)

	// Add orders table without physical table names (logical = physical)
	ordersRule, _ := NewShardingRule("orders", "order_id", NewModStrategy(), 4)
	registry.Register(ordersRule)

	rewriter := NewSQLRewriter(registry)

	tests := []struct {
		name       string
		query      string
		shardIndex int
		want       string
		wantErr    bool
	}{
		{
			name:       "simple SELECT",
			query:      "SELECT * FROM users WHERE user_id = ?",
			shardIndex: 0,
			want:       "SELECT * FROM users_0 WHERE user_id = ?",
			wantErr:    false,
		},
		{
			name:       "SELECT with backticks",
			query:      "SELECT * FROM `users` WHERE `user_id` = ?",
			shardIndex: 1,
			want:       "SELECT * FROM `users_1` WHERE `user_id` = ?",
			wantErr:    false,
		},
		{
			name:       "INSERT statement",
			query:      "INSERT INTO users (user_id, name) VALUES (?, ?)",
			shardIndex: 2,
			want:       "INSERT INTO users_2 (user_id, name) VALUES (?, ?)",
			wantErr:    false,
		},
		{
			name:       "UPDATE statement",
			query:      "UPDATE users SET name = ? WHERE user_id = ?",
			shardIndex: 3,
			want:       "UPDATE users_3 SET name = ? WHERE user_id = ?",
			wantErr:    false,
		},
		{
			name:       "DELETE statement",
			query:      "DELETE FROM users WHERE user_id = ?",
			shardIndex: 0,
			want:       "DELETE FROM users_0 WHERE user_id = ?",
			wantErr:    false,
		},
		{
			name:       "table without physical names",
			query:      "SELECT * FROM orders WHERE order_id = ?",
			shardIndex: 0,
			want:       "SELECT * FROM orders WHERE order_id = ?",
			wantErr:    false,
		},
		{
			name:       "unknown table",
			query:      "SELECT * FROM unknown WHERE id = ?",
			shardIndex: 0,
			want:       "SELECT * FROM unknown WHERE id = ?",
			wantErr:    false,
		},
		{
			name:       "invalid shard index",
			query:      "SELECT * FROM users WHERE user_id = ?",
			shardIndex: 999,
			want:       "",
			wantErr:    true,
		},
		{
			name:       "SELECT with JOIN",
			query:      "SELECT * FROM users u JOIN orders o ON u.user_id = o.user_id",
			shardIndex: 0,
			wantErr:    true,
		},
		{
			name:       "SELECT with alias",
			query:      "SELECT u.* FROM users u WHERE u.user_id = ?",
			shardIndex: 1,
			want:       "SELECT u.* FROM users_1 u WHERE u.user_id = ?",
			wantErr:    false,
		},
		{
			name:       "SELECT with AS alias",
			query:      "SELECT u.* FROM users AS u WHERE u.user_id = ?",
			shardIndex: 2,
			want:       "SELECT u.* FROM users_2 AS u WHERE u.user_id = ?",
			wantErr:    false,
		},
		{
			name:       "complex SELECT",
			query:      "SELECT * FROM users WHERE user_id = ? ORDER BY created_at DESC LIMIT 10",
			shardIndex: 0,
			want:       "SELECT * FROM users_0 WHERE user_id = ? ORDER BY created_at DESC LIMIT 10",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriter.Rewrite(tt.query, tt.shardIndex)
			if (err != nil) != tt.wantErr {
				t.Errorf("Rewrite() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Errorf("Rewrite() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLRewriter_RegisteredRuleMutationDoesNotStaleCache(t *testing.T) {
	registry := NewShardingRuleRegistry()
	rule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 2)
	rule.SetActualTableName(0, "users_0")
	if err := registry.Register(rule); err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}

	rewriter := NewSQLRewriter(registry)
	first, err := rewriter.Rewrite("SELECT * FROM users WHERE user_id = ?", 0)
	if err != nil {
		t.Fatalf("Rewrite() unexpected error: %v", err)
	}
	if first != "SELECT * FROM users_0 WHERE user_id = ?" {
		t.Fatalf("Rewrite() = %q, want users_0 rewrite", first)
	}

	rule.SetActualTableName(0, "users_mutated")
	second, err := rewriter.Rewrite("SELECT id FROM users WHERE user_id = ?", 0)
	if err != nil {
		t.Fatalf("Rewrite() unexpected error: %v", err)
	}
	if second != "SELECT id FROM users_0 WHERE user_id = ?" {
		t.Fatalf("Rewrite() observed caller mutation, got %q", second)
	}
}

func TestSQLRewriter_RewriteWithDetails(t *testing.T) {
	registry := NewShardingRuleRegistry()
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 2)
	usersRule.SetActualTableName(0, "users_0")
	usersRule.SetActualTableName(1, "users_1")
	registry.Register(usersRule)

	rewriter := NewSQLRewriter(registry)

	t.Run("with rewrite", func(t *testing.T) {
		query := "SELECT * FROM users WHERE user_id = ?"
		result, err := rewriter.RewriteWithDetails(query, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.OriginalSQL != query {
			t.Errorf("OriginalSQL = %q, want %q", result.OriginalSQL, query)
		}

		if result.RewrittenSQL != "SELECT * FROM users_0 WHERE user_id = ?" {
			t.Errorf("RewrittenSQL = %q, want %q", result.RewrittenSQL, "SELECT * FROM users_0 WHERE user_id = ?")
		}

		if result.TableName != "users" {
			t.Errorf("TableName = %q, want %q", result.TableName, "users")
		}

		if result.PhysicalTableName != "users_0" {
			t.Errorf("PhysicalTableName = %q, want %q", result.PhysicalTableName, "users_0")
		}

		if !result.WasRewritten {
			t.Error("WasRewritten = false, want true")
		}
	})

	t.Run("without rewrite", func(t *testing.T) {
		query := "SELECT * FROM unknown WHERE id = ?"
		result, err := rewriter.RewriteWithDetails(query, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.WasRewritten {
			t.Error("WasRewritten = true, want false")
		}

		if result.OriginalSQL != result.RewrittenSQL {
			t.Errorf("SQL was rewritten when it shouldn't be")
		}
	})
}

func TestSQLRewriter_replaceWholeWord(t *testing.T) {
	registry := NewShardingRuleRegistry()
	rewriter := NewSQLRewriter(registry)

	tests := []struct {
		name string
		text string
		from string
		to   string
		want string
	}{
		{
			name: "simple replacement",
			text: "SELECT * FROM users WHERE id = ?",
			from: "users",
			to:   "users_0",
			want: "SELECT * FROM users_0 WHERE id = ?",
		},
		{
			name: "no replacement of substring",
			text: "SELECT * FROM users_backup WHERE id = ?",
			from: "users",
			to:   "users_0",
			want: "SELECT * FROM users_backup WHERE id = ?",
		},
		{
			name: "multiple occurrences",
			text: "SELECT users.* FROM users WHERE users.id = ?",
			from: "users",
			to:   "users_0",
			want: "SELECT users_0.* FROM users_0 WHERE users_0.id = ?",
		},
		{
			name: "case insensitive",
			text: "SELECT * FROM Users WHERE id = ?",
			from: "users",
			to:   "users_0",
			want: "SELECT * FROM users_0 WHERE id = ?",
		},
		{
			name: "with parentheses",
			text: "INSERT INTO users(id, name) VALUES (?, ?)",
			from: "users",
			to:   "users_0",
			want: "INSERT INTO users_0(id, name) VALUES (?, ?)",
		},
		{
			name: "with comma",
			text: "SELECT * FROM users, orders WHERE users.id = orders.user_id",
			from: "users",
			to:   "users_0",
			want: "SELECT * FROM users_0, orders WHERE users_0.id = orders.user_id",
		},
		{
			name: "does not rewrite single-quoted literal",
			text: "SELECT * FROM users WHERE note = 'users should stay'",
			from: "users",
			to:   "users_0",
			want: "SELECT * FROM users_0 WHERE note = 'users should stay'",
		},
		{
			name: "does not rewrite line comment",
			text: "SELECT * FROM users -- users should stay\nWHERE id = ?",
			from: "users",
			to:   "users_0",
			want: "SELECT * FROM users_0 -- users should stay\nWHERE id = ?",
		},
		{
			name: "does not rewrite block comment",
			text: "SELECT * FROM users /* users should stay */ WHERE id = ?",
			from: "users",
			to:   "users_0",
			want: "SELECT * FROM users_0 /* users should stay */ WHERE id = ?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriter.replaceWholeWord(tt.text, tt.from, tt.to)
			if got != tt.want {
				t.Errorf("replaceWholeWord() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLRewriter_Cache(t *testing.T) {
	registry := NewShardingRuleRegistry()
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 2)
	usersRule.SetActualTableName(0, "users_0")
	usersRule.SetActualTableName(1, "users_1")
	registry.Register(usersRule)

	rewriter := NewSQLRewriter(registry)

	query := "SELECT * FROM users WHERE user_id = ?"

	// First call - should cache
	result1, err := rewriter.Rewrite(query, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call - should use cache
	result2, err := rewriter.Rewrite(query, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result1 != result2 {
		t.Errorf("cached result differs: %q != %q", result1, result2)
	}

	// Check cache stats
	stats := rewriter.CacheStats()
	if stats.Entries == 0 {
		t.Error("expected cache to have entries")
	}

	// Clear cache
	rewriter.ClearCache()
	stats = rewriter.CacheStats()
	if stats.Entries != 0 {
		t.Errorf("expected cache to be empty after clear, got %d entries", stats.Entries)
	}
}

func TestSQLRewriter_BatchRewrite(t *testing.T) {
	registry := NewShardingRuleRegistry()
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
	for i := 0; i < 4; i++ {
		usersRule.SetActualTableName(i, "users_"+string(rune('0'+i)))
	}
	registry.Register(usersRule)

	rewriter := NewSQLRewriter(registry)

	query := "SELECT * FROM users WHERE user_id = ?"
	shardIndices := []int{0, 1, 2, 3}

	results, err := rewriter.BatchRewrite(query, shardIndices)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 4 {
		t.Errorf("expected 4 results, got %d", len(results))
	}

	for i := 0; i < 4; i++ {
		expected := "SELECT * FROM users_" + string(rune('0'+i)) + " WHERE user_id = ?"
		if results[i] != expected {
			t.Errorf("shard %d: got %q, want %q", i, results[i], expected)
		}
	}
}

func TestSQLRewriter_QuotedIdentifiers(t *testing.T) {
	registry := NewShardingRuleRegistry()
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 2)
	usersRule.SetActualTableName(0, "users_0")
	registry.Register(usersRule)

	rewriter := NewSQLRewriter(registry)

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "backticks",
			query: "SELECT * FROM `users` WHERE `user_id` = ?",
			want:  "SELECT * FROM `users_0` WHERE `user_id` = ?",
		},
		{
			name:  "mixed quotes",
			query: "SELECT * FROM `users` u WHERE u.user_id = ?",
			want:  "SELECT * FROM `users_0` u WHERE u.user_id = ?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriter.Rewrite(tt.query, 0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Rewrite() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLRewriter_ComplexQueries(t *testing.T) {
	registry := NewShardingRuleRegistry()
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 2)
	usersRule.SetActualTableName(0, "users_0")
	registry.Register(usersRule)

	rewriter := NewSQLRewriter(registry)

	tests := []struct {
		name    string
		query   string
		want    string
		wantErr bool
	}{
		{
			name:    "subquery",
			query:   "SELECT * FROM users WHERE user_id IN (SELECT user_id FROM orders)",
			wantErr: true,
		},
		{
			name:    "UNION",
			query:   "SELECT * FROM users WHERE active = 1 UNION SELECT * FROM users WHERE active = 0",
			wantErr: true,
		},
		{
			name:    "CTE",
			query:   "WITH active_users AS (SELECT * FROM users) SELECT * FROM active_users",
			wantErr: true,
		},
		{
			name:    "multiple statements",
			query:   "SELECT * FROM users WHERE user_id = ?; SELECT * FROM users WHERE user_id = ?",
			wantErr: true,
		},
		{
			name:    "schema-qualified SELECT",
			query:   "SELECT * FROM public.users WHERE user_id = ?",
			wantErr: true,
		},
		{
			name:    "schema-qualified UPDATE",
			query:   "UPDATE public.users SET name = ? WHERE user_id = ?",
			wantErr: true,
		},
		{
			name:    "quoted schema-qualified INSERT",
			query:   `INSERT INTO "public"."users" (user_id, name) VALUES (?, ?)`,
			wantErr: true,
		},
		{
			name:  "GROUP BY",
			query: "SELECT country, COUNT(*) FROM users GROUP BY country",
			want:  "SELECT country, COUNT(*) FROM users_0 GROUP BY country",
		},
		{
			name:    "HAVING",
			query:   "SELECT country, COUNT(*) FROM users GROUP BY country HAVING COUNT(*) > 10",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriter.Rewrite(tt.query, 0)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Rewrite() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if !errors.Is(err, ErrUnsafeSQLRewrite) {
					t.Fatalf("Rewrite() error = %v, want ErrUnsafeSQLRewrite", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Rewrite() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLRewriter_ValidationIgnoresLiteralsAndComments(t *testing.T) {
	registry := NewShardingRuleRegistry()
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 2)
	usersRule.SetActualTableName(0, "users_0")
	registry.Register(usersRule)

	rewriter := NewSQLRewriter(registry)

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "semicolon and union in literal",
			query: "SELECT * FROM users WHERE note = 'first; UNION second'",
			want:  "SELECT * FROM users_0 WHERE note = 'first; UNION second'",
		},
		{
			name:  "nested select in comment",
			query: "SELECT * FROM users /* (SELECT * FROM users) */ WHERE user_id = ?",
			want:  "SELECT * FROM users_0 /* (SELECT * FROM users) */ WHERE user_id = ?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rewriter.Rewrite(tt.query, 0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Rewrite() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLRewriter_CacheSize(t *testing.T) {
	registry := NewShardingRuleRegistry()
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 2)
	usersRule.SetActualTableName(0, "users_0")
	registry.Register(usersRule)

	// Create rewriter with small cache size
	rewriter := NewSQLRewriterWithCacheSize(registry, 2)

	// Add 3 queries (should evict the first one)
	queries := []string{
		"SELECT * FROM users WHERE id = 1",
		"SELECT * FROM users WHERE id = 2",
		"SELECT * FROM users WHERE id = 3",
	}

	for _, q := range queries {
		_, err := rewriter.Rewrite(q, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	stats := rewriter.CacheStats()
	if stats.Entries > 2 {
		t.Errorf("expected at most 2 cache entries, got %d", stats.Entries)
	}
}

func BenchmarkSQLRewriter_Rewrite(b *testing.B) {
	registry := NewShardingRuleRegistry()
	usersRule, _ := NewShardingRule("users", "user_id", NewModStrategy(), 4)
	for i := 0; i < 4; i++ {
		usersRule.SetActualTableName(i, "users_"+string(rune('0'+i)))
	}
	registry.Register(usersRule)

	rewriter := NewSQLRewriter(registry)
	query := "SELECT * FROM users WHERE user_id = ?"

	b.Run("with_cache", func(b *testing.B) {
		// Prime the cache
		_, _ = rewriter.Rewrite(query, 0)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = rewriter.Rewrite(query, 0)
		}
	})

	b.Run("without_cache", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rewriter.ClearCache()
			_, _ = rewriter.Rewrite(query, 0)
		}
	})
}

func BenchmarkSQLRewriter_replaceWholeWord(b *testing.B) {
	registry := NewShardingRuleRegistry()
	rewriter := NewSQLRewriter(registry)

	text := strings.Repeat("SELECT users.* FROM users WHERE users.id = ? AND users.active = 1 ", 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rewriter.replaceWholeWord(text, "users", "users_0")
	}
}
