package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/birdordev/plumego/x/tenant/core"
)

// validIdentifier matches safe SQL identifier names (alphanumeric and underscores only).
var validIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// errConnector is a driver.Connector whose connections always fail with a fixed error.
type errConnector struct{ err error }

func (c errConnector) Connect(context.Context) (driver.Conn, error) { return nil, c.err }
func (c errConnector) Driver() driver.Driver                        { return errDriver(c) }

type errDriver struct{ err error }

func (d errDriver) Open(string) (driver.Conn, error) { return nil, d.err }

// sentinel databases that return meaningful errors from (*sql.Row).Scan
// without requiring a real database connection.
var (
	errNoTenantIDDB = sql.OpenDB(errConnector{errors.New("tenant ID not found in context")})
	errNotInitDB    = sql.OpenDB(errConnector{errors.New("database not initialized")})
	errBadColumnDB  = sql.OpenDB(errConnector{errors.New("invalid tenant column name")})
)

// TenantDB wraps sql.DB with automatic tenant filtering for queries.
// It ensures that all queries are scoped to a specific tenant by automatically
// injecting WHERE tenant_id = ? clauses.
//
// Simple single-statement SQL (INSERT/UPDATE/SELECT/DELETE) is supported.
// Complex SQL (CTEs, UNION, sub-queries that span multiple statements) should
// be executed via RawDB() with manual tenant filtering.
type TenantDB struct {
	db           *sql.DB
	tenantColumn string
	initErr      error // non-nil when construction options were invalid
}

// TenantDBOption configures TenantDB behavior.
type TenantDBOption func(*TenantDB)

// WithTenantColumn sets the column name for tenant filtering (default: "tenant_id").
// The column name must be a valid SQL identifier (alphanumeric and underscores only).
// If the column name is invalid, all TenantDB operations will return an error.
func WithTenantColumn(column string) TenantDBOption {
	return func(tdb *TenantDB) {
		if !validIdentifier.MatchString(column) {
			tdb.initErr = fmt.Errorf("WithTenantColumn: %q is not a valid SQL identifier (must match [a-zA-Z_][a-zA-Z0-9_]*)", column)
			return
		}
		tdb.tenantColumn = column
	}
}

// NewTenantDB creates a tenant-aware database wrapper.
//
// Example:
//
//	tdb := NewTenantDB(db)
//	rows, err := tdb.QueryFromContext(ctx, "SELECT * FROM users WHERE active = ?", true)
//	// Automatically becomes: SELECT * FROM users WHERE tenant_id = 'current-tenant' AND active = true
func NewTenantDB(db *sql.DB, options ...TenantDBOption) *TenantDB {
	tdb := &TenantDB{
		db:           db,
		tenantColumn: "tenant_id",
	}

	for _, opt := range options {
		opt(tdb)
	}

	return tdb
}

// Err returns a non-nil error if TenantDB was misconfigured during construction
// (e.g., an invalid column name was passed to WithTenantColumn).
func (tdb *TenantDB) Err() error {
	if tdb == nil {
		return errors.New("database not initialized")
	}
	return tdb.initErr
}

// QueryFromContext extracts tenant from context and executes a query with automatic filtering.
func (tdb *TenantDB) QueryFromContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tdb == nil || tdb.db == nil {
		return nil, errors.New("database not initialized")
	}
	if tdb.initErr != nil {
		return nil, tdb.initErr
	}
	tenantID := tenant.TenantIDFromContext(ctx)
	if tenantID == "" {
		return nil, errors.New("tenant ID not found in context")
	}

	return tdb.QueryContext(ctx, tenantID, query, args...)
}

// QueryContext executes a query with automatic tenant filtering.
func (tdb *TenantDB) QueryContext(ctx context.Context, tenantID string, query string, args ...any) (*sql.Rows, error) {
	if tdb == nil || tdb.db == nil {
		return nil, errors.New("database not initialized")
	}
	if tdb.initErr != nil {
		return nil, tdb.initErr
	}

	filteredQuery, filteredArgs := tdb.addTenantFilter(query, tenantID, args)
	return tdb.db.QueryContext(ctx, filteredQuery, filteredArgs...)
}

// QueryRowFromContext extracts tenant from context and executes a single-row query.
// Returns an error-producing Row (with a descriptive message) if tenant ID is not
// found in context or if TenantDB was misconfigured.
func (tdb *TenantDB) QueryRowFromContext(ctx context.Context, query string, args ...any) *sql.Row {
	if tdb == nil || tdb.db == nil {
		return errNotInitDB.QueryRowContext(ctx, "SELECT 1")
	}
	if tdb.initErr != nil {
		return errBadColumnDB.QueryRowContext(ctx, "SELECT 1")
	}
	tenantID := tenant.TenantIDFromContext(ctx)
	if tenantID == "" {
		// Return a Row that produces a clear "tenant ID not found in context" error on Scan.
		return errNoTenantIDDB.QueryRowContext(ctx, "SELECT 1")
	}
	return tdb.QueryRowContext(ctx, tenantID, query, args...)
}

// QueryRowContext executes a single-row query with automatic tenant filtering.
func (tdb *TenantDB) QueryRowContext(ctx context.Context, tenantID string, query string, args ...any) *sql.Row {
	if tdb == nil || tdb.db == nil {
		return errNotInitDB.QueryRowContext(ctx, "SELECT 1")
	}
	if tdb.initErr != nil {
		return errBadColumnDB.QueryRowContext(ctx, "SELECT 1")
	}

	filteredQuery, filteredArgs := tdb.addTenantFilter(query, tenantID, args)
	return tdb.db.QueryRowContext(ctx, filteredQuery, filteredArgs...)
}

// ExecFromContext extracts tenant from context and executes a statement.
func (tdb *TenantDB) ExecFromContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tdb == nil || tdb.db == nil {
		return nil, errors.New("database not initialized")
	}
	if tdb.initErr != nil {
		return nil, tdb.initErr
	}
	tenantID := tenant.TenantIDFromContext(ctx)
	if tenantID == "" {
		return nil, errors.New("tenant ID not found in context")
	}

	return tdb.ExecContext(ctx, tenantID, query, args...)
}

// ExecContext executes a statement with automatic tenant filtering.
func (tdb *TenantDB) ExecContext(ctx context.Context, tenantID string, query string, args ...any) (sql.Result, error) {
	if tdb == nil || tdb.db == nil {
		return nil, errors.New("database not initialized")
	}
	if tdb.initErr != nil {
		return nil, tdb.initErr
	}

	filteredQuery, filteredArgs := tdb.addTenantFilter(query, tenantID, args)
	return tdb.db.ExecContext(ctx, filteredQuery, filteredArgs...)
}

// addTenantFilter injects tenant_id filter into SQL query.
func (tdb *TenantDB) addTenantFilter(query string, tenantID string, args []any) (string, []any) {
	queryLower := strings.ToLower(strings.TrimSpace(query))

	if strings.HasPrefix(queryLower, "insert") {
		return tdb.addTenantToInsert(query, tenantID, args)
	}

	if strings.HasPrefix(queryLower, "update") {
		return tdb.addTenantToUpdate(query, tenantID, args)
	}

	// SELECT, DELETE — add WHERE clause with tenant_id at the front.
	whereClause := fmt.Sprintf("%s = ?", tdb.tenantColumn)
	newArgs := append([]any{tenantID}, args...)

	if whereIdx := indexCaseInsensitive(query, " WHERE "); whereIdx != -1 {
		keywordStart := whereIdx + 1 // skip leading space
		keywordEnd := keywordStart + 5
		filteredQuery := query[:keywordStart] + "WHERE " + whereClause + " AND " + query[keywordEnd+1:]
		return filteredQuery, newArgs
	}

	// No WHERE clause — insert one before ORDER BY / LIMIT / GROUP BY etc.
	insertPos := len(query)
	for _, keyword := range []string{" ORDER BY", " LIMIT", " GROUP BY", " HAVING", " FOR UPDATE"} {
		if idx := strings.Index(strings.ToUpper(query), keyword); idx != -1 && idx < insertPos {
			insertPos = idx
		}
	}

	filteredQuery := query[:insertPos] + " WHERE " + whereClause + query[insertPos:]
	return filteredQuery, newArgs
}

// addTenantToUpdate adds tenant_id filter to UPDATE statements.
func (tdb *TenantDB) addTenantToUpdate(query string, tenantID string, args []any) (string, []any) {
	whereClause := fmt.Sprintf("%s = ?", tdb.tenantColumn)

	whereIdx := indexCaseInsensitive(query, " WHERE ")
	if whereIdx != -1 {
		keywordStart := whereIdx + 1
		beforeWhere := query[:keywordStart]
		setPlaceholders := strings.Count(beforeWhere, "?")

		newArgs := make([]any, 0, len(args)+1)
		newArgs = append(newArgs, args[:setPlaceholders]...)
		newArgs = append(newArgs, tenantID)
		newArgs = append(newArgs, args[setPlaceholders:]...)

		filteredQuery := query[:keywordStart] + "WHERE " + whereClause + " AND " + query[keywordStart+6:]
		return filteredQuery, newArgs
	}

	newArgs := append(args, tenantID)
	filteredQuery := query + " WHERE " + whereClause
	return filteredQuery, newArgs
}

// addTenantToInsert adds tenant_id to INSERT statements, including multi-row inserts.
//
// Supports:
//
//	INSERT INTO t (a, b) VALUES (?, ?)
//	INSERT INTO t (a, b) VALUES (?, ?), (?, ?)
//
// Returns the query unchanged if it cannot be parsed safely.
func (tdb *TenantDB) addTenantToInsert(query string, tenantID string, args []any) (string, []any) {
	queryUpper := strings.ToUpper(query)
	valuesIdx := strings.Index(queryUpper, "VALUES")
	if valuesIdx == -1 {
		return query, args
	}

	beforeValues := query[:valuesIdx]
	afterValues := query[valuesIdx+6:] // skip "VALUES"

	// Find column list: INSERT INTO table (col1, col2)
	colStart := strings.Index(beforeValues, "(")
	if colStart == -1 {
		return query, args
	}
	colEnd := findMatchingParen(beforeValues, colStart)
	if colEnd == -1 {
		return query, args
	}

	// Add tenant column to the column list.
	modifiedBefore := beforeValues[:colEnd] + ", " + tdb.tenantColumn + beforeValues[colEnd:]

	// Parse all VALUES row groups: "(?, ?), (?, ?), ..."
	rows, ok := parseInsertRows(afterValues)
	if !ok || len(rows) == 0 {
		return query, args
	}

	rowCount := len(rows)
	// Count original placeholder count per row from the first row.
	colCount := strings.Count(rows[0], "?")

	// Rebuild VALUES section: add ", ?" inside each row group.
	var sb strings.Builder
	for i, row := range rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		// row is "(?, ?)" — insert ", ?" before the closing paren.
		closeIdx := findMatchingParen(row, 0)
		if closeIdx == -1 {
			return query, args
		}
		sb.WriteString(row[:closeIdx])
		sb.WriteString(", ?")
		sb.WriteString(row[closeIdx:])
	}

	// Reorder args: for each row, append the original row args then tenantID.
	newArgs := make([]any, 0, len(args)+rowCount)
	for i := 0; i < rowCount; i++ {
		start := i * colCount
		end := start + colCount
		if end > len(args) {
			end = len(args)
		}
		newArgs = append(newArgs, args[start:end]...)
		newArgs = append(newArgs, tenantID)
	}
	// Append any trailing args that don't fit into rows (e.g., named params edge cases).
	if consumed := rowCount * colCount; consumed < len(args) {
		newArgs = append(newArgs, args[consumed:]...)
	}

	return modifiedBefore + "VALUES " + sb.String(), newArgs
}

// findMatchingParen returns the index of the closing ')' that matches the '(' at openIdx.
// Returns -1 if not found or if s[openIdx] is not '('.
func findMatchingParen(s string, openIdx int) int {
	if openIdx < 0 || openIdx >= len(s) || s[openIdx] != '(' {
		return -1
	}
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseInsertRows extracts all top-level "(…)" groups from the VALUES suffix.
// suffix is everything after the "VALUES" keyword.
// Returns the list of raw row strings and true on success; nil, false on parse failure.
func parseInsertRows(suffix string) ([]string, bool) {
	var rows []string
	i := 0
	n := len(suffix)
	for i < n {
		// Skip whitespace and commas between rows.
		for i < n && (suffix[i] == ' ' || suffix[i] == '\t' || suffix[i] == '\n' || suffix[i] == '\r' || suffix[i] == ',') {
			i++
		}
		if i >= n {
			break
		}
		if suffix[i] != '(' {
			// Unexpected character — cannot safely parse (e.g., sub-select).
			return nil, false
		}
		end := findMatchingParen(suffix, i)
		if end == -1 {
			return nil, false
		}
		rows = append(rows, suffix[i:end+1])
		i = end + 1
	}
	if len(rows) == 0 {
		return nil, false
	}
	return rows, true
}

// ValidateQuery checks if a query is safe for multi-tenant use.
// It warns about queries that might leak data across tenants.
func ValidateQuery(query string, tenantColumn string) error {
	if tenantColumn == "" {
		tenantColumn = "tenant_id"
	}

	queryLower := strings.ToLower(strings.TrimSpace(query))

	if strings.Contains(queryLower, "drop table") {
		return errors.New("DROP TABLE not allowed in tenant-scoped queries")
	}
	if strings.Contains(queryLower, "truncate") {
		return errors.New("TRUNCATE not allowed in tenant-scoped queries")
	}
	if strings.Contains(queryLower, "alter table") {
		return errors.New("ALTER TABLE not allowed in tenant-scoped queries")
	}

	if strings.HasPrefix(queryLower, "select") ||
		strings.HasPrefix(queryLower, "update") ||
		strings.HasPrefix(queryLower, "delete") {

		if !strings.Contains(queryLower, "where") {
			return fmt.Errorf("query missing WHERE clause - potential cross-tenant data leak")
		}

		whereIdx := strings.Index(queryLower, "where")
		if whereIdx == -1 {
			return fmt.Errorf("query missing %s filter - potential cross-tenant data leak", tenantColumn)
		}

		afterWhere := queryLower[whereIdx:]
		if !strings.Contains(afterWhere, strings.ToLower(tenantColumn)) {
			return fmt.Errorf("query missing %s filter in WHERE clause - potential cross-tenant data leak", tenantColumn)
		}
	}

	return nil
}

// indexCaseInsensitive finds the first occurrence of substr in s, case-insensitively.
func indexCaseInsensitive(s, substr string) int {
	return strings.Index(strings.ToUpper(s), strings.ToUpper(substr))
}

// RawDB returns the underlying *sql.DB for admin queries that bypass tenant filtering.
// Use with caution — this bypasses all tenant isolation!
//
// Example (admin operation):
//
//	rows, err := tdb.RawDB().QueryContext(ctx, "SELECT COUNT(*) FROM users")
func (tdb *TenantDB) RawDB() *sql.DB {
	if tdb == nil {
		return nil
	}
	return tdb.db
}
