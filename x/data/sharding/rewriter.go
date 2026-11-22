package sharding

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

var ErrUnsafeSQLRewrite = errors.New("unsafe SQL rewrite")

var schemaQualifiedTablePattern = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|UPDATE|INTO)\s+[` + "`" + `"\[]?[a-zA-Z_][a-zA-Z0-9_]*[` + "`" + `"\]]?\s*\.\s*[` + "`" + `"\[]?[a-zA-Z_][a-zA-Z0-9_]*[` + "`" + `"\]]?`)

// SQLRewriter rewrites SQL queries by replacing logical table names with physical table names.
// This is essential for physical sharding scenarios where each shard has different physical tables.
//
// Example:
//
//	rewriter := NewSQLRewriter(registry)
//	rewritten, err := rewriter.Rewrite("SELECT * FROM users WHERE id = ?", 0)
//	// Returns: "SELECT * FROM users_0 WHERE id = ?"
type SQLRewriter struct {
	registry *ShardingRuleRegistry
	parser   *SQLParser
	cache    *rewriteCache
}

// rewriteCache caches SQL rewrite results for performance
type rewriteCache struct {
	mu       sync.RWMutex
	cache    map[string]map[int]string // query -> shardIndex -> rewritten query
	maxSize  int
	requests atomic.Uint64
	hits     atomic.Uint64
	misses   atomic.Uint64
}

// RewriteResult contains the result of SQL rewriting
type RewriteResult struct {
	// OriginalSQL is the original SQL query
	OriginalSQL string

	// RewrittenSQL is the rewritten SQL query with physical table names
	RewrittenSQL string

	// TableName is the logical table name that was rewritten
	TableName string

	// PhysicalTableName is the physical table name used in rewriting
	PhysicalTableName string

	// ShardIndex is the target shard index
	ShardIndex int

	// WasRewritten indicates if any rewriting occurred
	WasRewritten bool
}

// NewSQLRewriter creates a new SQL rewriter
func NewSQLRewriter(registry *ShardingRuleRegistry) *SQLRewriter {
	return &SQLRewriter{
		registry: registry,
		parser:   NewSQLParser(),
		cache:    newRewriteCache(1000), // Cache up to 1000 queries
	}
}

// NewSQLRewriterWithCacheSize creates a new SQL rewriter with custom cache size
func NewSQLRewriterWithCacheSize(registry *ShardingRuleRegistry, cacheSize int) *SQLRewriter {
	return &SQLRewriter{
		registry: registry,
		parser:   NewSQLParser(),
		cache:    newRewriteCache(cacheSize),
	}
}

// newRewriteCache creates a new rewrite cache
func newRewriteCache(maxSize int) *rewriteCache {
	return &rewriteCache{
		cache:   make(map[string]map[int]string),
		maxSize: maxSize,
	}
}

// Rewrite rewrites a SQL query by replacing the logical table name with the physical table name
// for the specified shard index.
//
// Example:
//
//	rewritten, err := rewriter.Rewrite("SELECT * FROM users WHERE id = ?", 0)
//	// Returns: "SELECT * FROM users_0 WHERE id = ?"
func (r *SQLRewriter) Rewrite(query string, shardIndex int) (string, error) {
	return r.rewriteParsed(query, shardIndex, nil)
}

func (r *SQLRewriter) rewriteParsed(query string, shardIndex int, parsed *ParsedSQL) (string, error) {
	// Check cache first
	if cached := r.cache.get(query, shardIndex); cached != "" {
		return cached, nil
	}
	if err := validateSafeRewriteQuery(query); err != nil {
		return "", err
	}

	// Parse the SQL query
	if parsed == nil {
		var err error
		parsed, err = r.parser.Parse(query)
		if err != nil {
			return "", fmt.Errorf("failed to parse SQL: %w", err)
		}
	}

	// Get the sharding rule for this table
	rule, err := r.registry.Get(parsed.TableName)
	if err != nil {
		// No sharding rule found - return original query
		// This might be a non-sharded table
		return query, nil
	}

	// Validate shard index
	if shardIndex < 0 || shardIndex >= rule.ShardCount {
		return "", fmt.Errorf("invalid shard index %d for table %s (shard count: %d)",
			shardIndex, parsed.TableName, rule.ShardCount)
	}

	// Get the physical table name for this shard
	physicalTableName := rule.GetActualTableName(shardIndex)

	// If physical table name is same as logical, no rewrite needed
	if physicalTableName == parsed.TableName {
		r.cache.put(query, shardIndex, query)
		return query, nil
	}

	// Rewrite the query
	rewritten := r.replaceTableName(query, parsed.TableName, physicalTableName)

	// Cache the result
	r.cache.put(query, shardIndex, rewritten)

	return rewritten, nil
}

// RewriteWithDetails rewrites a SQL query and returns detailed information
func (r *SQLRewriter) RewriteWithDetails(query string, shardIndex int) (*RewriteResult, error) {
	if err := validateSafeRewriteQuery(query); err != nil {
		return nil, err
	}

	// Parse the SQL query
	parsed, err := r.parser.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SQL: %w", err)
	}

	result := &RewriteResult{
		OriginalSQL:  query,
		RewrittenSQL: query,
		TableName:    parsed.TableName,
		ShardIndex:   shardIndex,
		WasRewritten: false,
	}

	// Get the sharding rule for this table
	rule, err := r.registry.Get(parsed.TableName)
	if err != nil {
		// No sharding rule found - return original query
		return result, nil
	}

	// Validate shard index
	if shardIndex < 0 || shardIndex >= rule.ShardCount {
		return nil, fmt.Errorf("invalid shard index %d for table %s (shard count: %d)",
			shardIndex, parsed.TableName, rule.ShardCount)
	}

	// Get the physical table name for this shard
	physicalTableName := rule.GetActualTableName(shardIndex)
	result.PhysicalTableName = physicalTableName

	// If physical table name is same as logical, no rewrite needed
	if physicalTableName == parsed.TableName {
		return result, nil
	}

	// Rewrite the query
	result.RewrittenSQL = r.replaceTableName(query, parsed.TableName, physicalTableName)
	result.WasRewritten = true

	return result, nil
}

// replaceTableName replaces all occurrences of the logical table name with the physical table name
// while preserving SQL semantics (quotes, case sensitivity, etc.)
func (r *SQLRewriter) replaceTableName(query, logicalTable, physicalTable string) string {
	return r.replaceWholeWord(query, logicalTable, physicalTable)
}

func validateSafeRewriteQuery(query string) error {
	trimmed := strings.TrimSpace(query)
	codeOnly := maskSQLLiteralsAndComments(trimmed)
	upper := strings.ToUpper(codeOnly)
	if strings.HasPrefix(upper, "WITH ") {
		return fmt.Errorf("%w: CTE queries are not supported", ErrUnsafeSQLRewrite)
	}
	withoutTrailingTerminator := strings.TrimSuffix(codeOnly, ";")
	if strings.Contains(withoutTrailingTerminator, ";") {
		return fmt.Errorf("%w: multiple statements are not supported", ErrUnsafeSQLRewrite)
	}
	if strings.Contains(upper, " UNION ") || strings.Contains(upper, "\nUNION ") || strings.Contains(upper, "\tUNION ") {
		return fmt.Errorf("%w: UNION queries are not supported", ErrUnsafeSQLRewrite)
	}
	if strings.Contains(upper, " JOIN ") || strings.Contains(upper, "\nJOIN ") || strings.Contains(upper, "\tJOIN ") {
		return fmt.Errorf("%w: JOIN queries are not supported", ErrUnsafeSQLRewrite)
	}
	if strings.Contains(upper, " HAVING ") || strings.Contains(upper, "\nHAVING ") || strings.Contains(upper, "\tHAVING ") {
		return fmt.Errorf("%w: HAVING queries are not supported", ErrUnsafeSQLRewrite)
	}
	if strings.Contains(upper, " RETURNING ") || strings.Contains(upper, "\nRETURNING ") || strings.Contains(upper, "\tRETURNING ") {
		return fmt.Errorf("%w: RETURNING queries are not supported", ErrUnsafeSQLRewrite)
	}
	if strings.Contains(upper, "(SELECT ") {
		return fmt.Errorf("%w: nested SELECT queries are not supported", ErrUnsafeSQLRewrite)
	}
	if strings.Count(upper, " SELECT ") > 1 {
		return fmt.Errorf("%w: multiple SELECT queries are not supported", ErrUnsafeSQLRewrite)
	}
	if schemaQualifiedTablePattern.FindStringIndex(codeOnly) != nil {
		return fmt.Errorf("%w: schema-qualified table names are not supported", ErrUnsafeSQLRewrite)
	}
	return nil
}

// replaceWholeWord replaces whole word occurrences only (not substrings)
// This prevents replacing "users" in "users_backup" for example
func (r *SQLRewriter) replaceWholeWord(text, from, to string) string {
	var result strings.Builder
	result.Grow(len(text))

	i := 0
	for i < len(text) {
		switch {
		case text[i] == '\'':
			next := consumeSingleQuoted(text, i)
			result.WriteString(text[i:next])
			i = next
			continue
		case i+1 < len(text) && text[i] == '-' && text[i+1] == '-':
			next := consumeLineComment(text, i)
			result.WriteString(text[i:next])
			i = next
			continue
		case i+1 < len(text) && text[i] == '/' && text[i+1] == '*':
			next := consumeBlockComment(text, i)
			result.WriteString(text[i:next])
			i = next
			continue
		case text[i] == '`':
			next, ok := replaceQuotedIdentifier(text, i, '`', '`', from, to, &result)
			if ok {
				i = next
				continue
			}
		case text[i] == '"':
			next, ok := replaceQuotedIdentifier(text, i, '"', '"', from, to, &result)
			if ok {
				i = next
				continue
			}
		case text[i] == '[':
			next, ok := replaceQuotedIdentifier(text, i, '[', ']', from, to, &result)
			if ok {
				i = next
				continue
			}
		}

		if matchesSQLIdentifier(text, i, from) {
			result.WriteString(to)
			i += len(from)
			continue
		}

		result.WriteByte(text[i])
		i++
	}

	return result.String()
}

func maskSQLLiteralsAndComments(text string) string {
	var result strings.Builder
	result.Grow(len(text))

	i := 0
	for i < len(text) {
		var next int
		switch {
		case text[i] == '\'':
			next = consumeSingleQuoted(text, i)
		case i+1 < len(text) && text[i] == '-' && text[i+1] == '-':
			next = consumeLineComment(text, i)
		case i+1 < len(text) && text[i] == '/' && text[i+1] == '*':
			next = consumeBlockComment(text, i)
		default:
			result.WriteByte(text[i])
			i++
			continue
		}
		for ; i < next; i++ {
			switch text[i] {
			case '\n', '\r', '\t':
				result.WriteByte(text[i])
			default:
				result.WriteByte(' ')
			}
		}
	}

	return result.String()
}

func consumeSingleQuoted(text string, start int) int {
	i := start + 1
	for i < len(text) {
		if text[i] != '\'' {
			i++
			continue
		}
		if i+1 < len(text) && text[i+1] == '\'' {
			i += 2
			continue
		}
		return i + 1
	}
	return len(text)
}

func consumeLineComment(text string, start int) int {
	i := start + 2
	for i < len(text) && text[i] != '\n' {
		i++
	}
	if i < len(text) {
		return i + 1
	}
	return i
}

func consumeBlockComment(text string, start int) int {
	i := start + 2
	for i+1 < len(text) {
		if text[i] == '*' && text[i+1] == '/' {
			return i + 2
		}
		i++
	}
	return len(text)
}

func replaceQuotedIdentifier(text string, start int, open, close byte, from, to string, result *strings.Builder) (int, bool) {
	i := start + 1
	for i < len(text) && text[i] != close {
		i++
	}
	if i >= len(text) {
		return start, false
	}

	identifier := text[start+1 : i]
	if strings.EqualFold(identifier, from) {
		result.WriteByte(open)
		result.WriteString(to)
		result.WriteByte(close)
		return i + 1, true
	}

	result.WriteString(text[start : i+1])
	return i + 1, true
}

func matchesSQLIdentifier(text string, start int, identifier string) bool {
	if start > 0 && isSQLIdentifierChar(text[start-1]) {
		return false
	}
	end := start + len(identifier)
	if end > len(text) || !strings.EqualFold(text[start:end], identifier) {
		return false
	}
	return end == len(text) || !isSQLIdentifierChar(text[end])
}

func isSQLIdentifierChar(b byte) bool {
	return b == '_' || b == '$' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

// ClearCache clears the rewrite cache
func (r *SQLRewriter) ClearCache() {
	r.cache.clear()
}

// CacheStats returns cache statistics
func (r *SQLRewriter) CacheStats() CacheStats {
	return r.cache.stats()
}

// CacheStats holds cache statistics
type CacheStats struct {
	Entries  int
	MaxSize  int
	HitRate  float64
	Requests uint64
	Hits     uint64
	Misses   uint64
}

// get retrieves a cached rewritten query
func (c *rewriteCache) get(query string, shardIndex int) string {
	c.requests.Add(1)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if shardMap, ok := c.cache[query]; ok {
		if rewritten, ok := shardMap[shardIndex]; ok {
			c.hits.Add(1)
			return rewritten
		}
	}

	c.misses.Add(1)
	return ""
}

// put stores a rewritten query in the cache
func (c *rewriteCache) put(query string, shardIndex int, rewritten string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we need to evict entries (simple size-based eviction)
	if len(c.cache) >= c.maxSize {
		// Evict a random entry (could be improved with LRU)
		for k := range c.cache {
			delete(c.cache, k)
			break
		}
	}

	// Store the rewritten query
	if _, ok := c.cache[query]; !ok {
		c.cache[query] = make(map[int]string)
	}
	c.cache[query][shardIndex] = rewritten
}

// clear clears the cache and resets all counters.
func (c *rewriteCache) clear() {
	c.mu.Lock()
	c.cache = make(map[string]map[int]string)
	c.mu.Unlock()

	c.requests.Store(0)
	c.hits.Store(0)
	c.misses.Store(0)
}

// stats returns cache statistics
func (c *rewriteCache) stats() CacheStats {
	c.mu.RLock()
	entries := 0
	for _, shardMap := range c.cache {
		entries += len(shardMap)
	}
	c.mu.RUnlock()

	requests := c.requests.Load()
	hits := c.hits.Load()
	var hitRate float64
	if requests > 0 {
		hitRate = float64(hits) / float64(requests)
	}

	return CacheStats{
		Entries:  entries,
		MaxSize:  c.maxSize,
		HitRate:  hitRate,
		Requests: requests,
		Hits:     hits,
		Misses:   c.misses.Load(),
	}
}

// BatchRewrite rewrites multiple queries for different shard indices
// This is useful for cross-shard queries
func (r *SQLRewriter) BatchRewrite(query string, shardIndices []int) (map[int]string, error) {
	results := make(map[int]string, len(shardIndices))

	for _, shardIdx := range shardIndices {
		rewritten, err := r.Rewrite(query, shardIdx)
		if err != nil {
			return nil, fmt.Errorf("failed to rewrite for shard %d: %w", shardIdx, err)
		}
		results[shardIdx] = rewritten
	}

	return results, nil
}
