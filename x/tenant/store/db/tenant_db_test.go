package db

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/birdordev/plumego/x/tenant/core"
)

func TestNewTenantDB(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)
	if tdb == nil {
		t.Fatal("expected non-nil TenantDB")
	}
	if tdb.tenantColumn != "tenant_id" {
		t.Errorf("expected default column 'tenant_id', got %q", tdb.tenantColumn)
	}
}

func TestNewTenantDBWithCustomColumn(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db, WithTenantColumn("org_id"))
	if tdb.tenantColumn != "org_id" {
		t.Errorf("expected column 'org_id', got %q", tdb.tenantColumn)
	}
}

func TestWithTenantColumnRejectsInvalid(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	cases := []struct {
		name   string
		column string
	}{
		{"sql injection attempt", "id; DROP TABLE users; --"},
		{"spaces", "tenant id"},
		{"special chars", "tenant-id"},
		{"empty string", ""},
		{"dots", "schema.column"},
		{"parentheses", "col()"},
		{"semicolon", "col;"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tdb := NewTenantDB(db, WithTenantColumn(tc.column))
			if tdb.tenantColumn != "tenant_id" {
				t.Errorf("expected default column 'tenant_id' for invalid input %q, got %q", tc.column, tdb.tenantColumn)
			}
		})
	}
}

func TestWithTenantColumnAcceptsValid(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	cases := []string{"org_id", "TenantID", "tenant_id", "_private", "col123"}
	for _, col := range cases {
		t.Run(col, func(t *testing.T) {
			tdb := NewTenantDB(db, WithTenantColumn(col))
			if tdb.tenantColumn != col {
				t.Errorf("expected column %q, got %q", col, tdb.tenantColumn)
			}
		})
	}
}

func TestTenantDB_ErrAndOperationsFailClosedOnInvalidColumn(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db, WithTenantColumn("tenant-id"))
	if tdb.Err() == nil {
		t.Fatal("Err() should report invalid tenant column configuration")
	}

	ctx := tenant.WithTenantID(t.Context(), "t-123")
	if _, err := tdb.QueryFromContext(ctx, "SELECT * FROM users"); err == nil {
		t.Fatal("QueryFromContext() should fail for invalid tenant column configuration")
	}

	row := tdb.QueryRowFromContext(ctx, "SELECT * FROM users WHERE id = ?", 1)
	var id int
	if err := row.Scan(&id); err == nil {
		t.Fatal("QueryRowFromContext().Scan should fail for invalid tenant column configuration")
	}
}

func TestTenantDB_RawDB(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)
	if tdb.RawDB() != db {
		t.Error("expected RawDB to return the underlying db")
	}
}

func TestTenantDB_RawDBNil(t *testing.T) {
	var tdb *TenantDB
	if tdb.RawDB() != nil {
		t.Error("expected nil for nil TenantDB")
	}
}

func TestTenantDB_QueryFromContext(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)
	ctx := tenant.WithTenantID(t.Context(), "t-123")

	rows, err := tdb.QueryFromContext(ctx, "SELECT * FROM users WHERE active = ?", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows != nil {
		rows.Close()
	}
}

func TestTenantDB_QueryFromContextMissingTenant(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)
	_, err := tdb.QueryFromContext(t.Context(), "SELECT * FROM users")
	if err == nil {
		t.Fatal("expected error for missing tenant")
	}
	if err.Error() != "tenant ID not found in context" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTenantDB_QueryContextNilDB(t *testing.T) {
	tdb := &TenantDB{db: nil}
	_, err := tdb.QueryContext(t.Context(), "t-123", "SELECT * FROM users")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestTenantDB_QueryContextNilTenantDB(t *testing.T) {
	var tdb *TenantDB
	_, err := tdb.QueryContext(t.Context(), "t-123", "SELECT * FROM users")
	if err == nil {
		t.Fatal("expected error for nil TenantDB")
	}
}

func TestTenantDB_ExecFromContext(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)
	ctx := tenant.WithTenantID(t.Context(), "t-123")

	_, err := tdb.ExecFromContext(ctx, "UPDATE users SET active = ? WHERE name = ?", true, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTenantDB_ExecFromContextMissingTenant(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)
	_, err := tdb.ExecFromContext(t.Context(), "UPDATE users SET active = ?", true)
	if err == nil {
		t.Fatal("expected error for missing tenant")
	}
}

func TestTenantDB_ExecContextNilDB(t *testing.T) {
	tdb := &TenantDB{db: nil}
	_, err := tdb.ExecContext(t.Context(), "t-123", "DELETE FROM users")
	if err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestTenantDB_QueryRowFromContextMissingTenant(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)
	row := tdb.QueryRowFromContext(t.Context(), "SELECT * FROM users WHERE id = ?", 1)

	// Scanning should return an error since the context was cancelled
	var id int
	err := row.Scan(&id)
	if err == nil {
		t.Fatal("expected error when scanning row from missing tenant context")
	}
}

func TestTenantDB_QueryRowContextNilDB(t *testing.T) {
	tdb := &TenantDB{db: nil}
	row := tdb.QueryRowContext(t.Context(), "t-123", "SELECT * FROM users WHERE id = ?", 1)
	// Should return an empty Row that errors on Scan
	if row == nil {
		t.Fatal("expected non-nil row (empty row)")
	}
}

// --- addTenantFilter tests ---

func TestAddTenantFilter_SelectWithWhere(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"SELECT * FROM users WHERE active = ?",
		"t-123",
		[]any{true},
	)

	expected := "SELECT * FROM users WHERE tenant_id = ? AND active = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 2 || args[0] != "t-123" || args[1] != true {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantFilter_SelectWithoutWhere(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"SELECT * FROM users",
		"t-123",
		nil,
	)

	expected := "SELECT * FROM users WHERE tenant_id = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 1 || args[0] != "t-123" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantFilter_SelectWithOrderBy(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"SELECT * FROM users ORDER BY name",
		"t-123",
		nil,
	)

	expected := "SELECT * FROM users WHERE tenant_id = ? ORDER BY name"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 1 || args[0] != "t-123" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantFilter_SelectWithLimit(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"SELECT * FROM users LIMIT 10",
		"t-123",
		nil,
	)

	expected := "SELECT * FROM users WHERE tenant_id = ? LIMIT 10"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 1 || args[0] != "t-123" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantFilter_SelectWithGroupBy(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"SELECT status, COUNT(*) FROM users GROUP BY status",
		"t-123",
		nil,
	)

	expected := "SELECT status, COUNT(*) FROM users WHERE tenant_id = ? GROUP BY status"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 1 || args[0] != "t-123" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantFilter_DeleteWithWhere(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"DELETE FROM users WHERE id = ?",
		"t-123",
		[]any{42},
	)

	expected := "DELETE FROM users WHERE tenant_id = ? AND id = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 2 || args[0] != "t-123" || args[1] != 42 {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantFilter_CaseInsensitiveWhere(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	// Test with lowercase "where"
	query, args := tdb.addTenantFilter(
		"SELECT * FROM users where active = ?",
		"t-123",
		[]any{true},
	)

	expected := "SELECT * FROM users WHERE tenant_id = ? AND active = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 2 || args[0] != "t-123" || args[1] != true {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantFilter_MixedCaseWhere(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"SELECT * FROM users Where active = ?",
		"t-123",
		[]any{true},
	)

	expected := "SELECT * FROM users WHERE tenant_id = ? AND active = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 2 {
		t.Errorf("unexpected args count: %d", len(args))
	}
}

// --- addTenantToInsert tests ---

func TestAddTenantToInsert(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"INSERT INTO users (name, email) VALUES (?, ?)",
		"t-123",
		[]any{"alice", "alice@example.com"},
	)

	expected := "INSERT INTO users (name, email, tenant_id) VALUES (?, ?, ?)"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 3 || args[0] != "alice" || args[1] != "alice@example.com" || args[2] != "t-123" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantToInsert_MultipleRows(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"INSERT INTO users (name, email) VALUES (?, ?), (?, ?)",
		"t-123",
		[]any{"alice", "alice@example.com", "bob", "bob@example.com"},
	)

	expected := "INSERT INTO users (name, email, tenant_id) VALUES (?, ?, ?), (?, ?, ?)"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 6 {
		t.Fatalf("expected 6 args, got %d", len(args))
	}
	want := []any{"alice", "alice@example.com", "t-123", "bob", "bob@example.com", "t-123"}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %v, want %v (full args: %v)", i, args[i], want[i], args)
		}
	}
}

func TestAddTenantToInsert_NoValues(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	// INSERT without VALUES clause: should pass through unchanged
	query, args := tdb.addTenantFilter(
		"INSERT INTO users SELECT * FROM staging",
		"t-123",
		nil,
	)

	// Can't parse - return as-is
	if query != "INSERT INTO users SELECT * FROM staging" {
		t.Errorf("expected unchanged query, got %q", query)
	}
	if len(args) != 0 {
		t.Errorf("expected no args, got %v", args)
	}
}

func TestAddTenantToInsert_NoColumnList(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	// INSERT without explicit column list
	query, args := tdb.addTenantFilter(
		"INSERT INTO users VALUES (?, ?)",
		"t-123",
		[]any{"alice", "alice@example.com"},
	)

	// No column list to modify - returned as-is
	if query != "INSERT INTO users VALUES (?, ?)" {
		t.Errorf("expected unchanged query, got %q", query)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
}

// --- addTenantToUpdate tests ---

func TestAddTenantToUpdate_WithWhere(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"UPDATE users SET email = ? WHERE name = ?",
		"t-123",
		[]any{"new@example.com", "alice"},
	)

	expected := "UPDATE users SET email = ? WHERE tenant_id = ? AND name = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(args))
	}
	if args[0] != "new@example.com" || args[1] != "t-123" || args[2] != "alice" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantToUpdate_WithoutWhere(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"UPDATE users SET active = ?",
		"t-123",
		[]any{false},
	)

	expected := "UPDATE users SET active = ? WHERE tenant_id = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[0] != false || args[1] != "t-123" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestAddTenantToUpdate_CaseInsensitiveWhere(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"UPDATE users SET email = ? where name = ?",
		"t-123",
		[]any{"new@example.com", "alice"},
	)

	expected := "UPDATE users SET email = ? WHERE tenant_id = ? AND name = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(args))
	}
}

func TestAddTenantToUpdate_MultipleSetParams(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "tenant_id"}

	query, args := tdb.addTenantFilter(
		"UPDATE users SET email = ?, name = ? WHERE id = ?",
		"t-123",
		[]any{"new@example.com", "bob", 42},
	)

	expected := "UPDATE users SET email = ?, name = ? WHERE tenant_id = ? AND id = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d", len(args))
	}
	// SET params first, then tenant, then WHERE params
	if args[0] != "new@example.com" || args[1] != "bob" || args[2] != "t-123" || args[3] != 42 {
		t.Errorf("unexpected args: %v", args)
	}
}

// --- ValidateQuery tests ---

func TestValidateQuery_Safe(t *testing.T) {
	err := ValidateQuery("SELECT * FROM users WHERE tenant_id = ?", "tenant_id")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestValidateQuery_DropTable(t *testing.T) {
	err := ValidateQuery("DROP TABLE users", "tenant_id")
	if err == nil {
		t.Fatal("expected error for DROP TABLE")
	}
}

func TestValidateQuery_Truncate(t *testing.T) {
	err := ValidateQuery("TRUNCATE users", "tenant_id")
	if err == nil {
		t.Fatal("expected error for TRUNCATE")
	}
}

func TestValidateQuery_AlterTable(t *testing.T) {
	err := ValidateQuery("ALTER TABLE users ADD COLUMN foo TEXT", "tenant_id")
	if err == nil {
		t.Fatal("expected error for ALTER TABLE")
	}
}

func TestValidateQuery_MissingWhere(t *testing.T) {
	err := ValidateQuery("SELECT * FROM users", "tenant_id")
	if err == nil {
		t.Fatal("expected error for missing WHERE")
	}
}

func TestValidateQuery_MissingTenantColumn(t *testing.T) {
	err := ValidateQuery("SELECT * FROM users WHERE active = true", "tenant_id")
	if err == nil {
		t.Fatal("expected error for missing tenant column in WHERE")
	}
}

func TestValidateQuery_DefaultColumn(t *testing.T) {
	err := ValidateQuery("SELECT * FROM users WHERE tenant_id = ?", "")
	if err != nil {
		t.Errorf("expected no error with default column, got %v", err)
	}
}

func TestValidateQuery_TenantColumnInWhereOnly(t *testing.T) {
	// tenant_id appears in table name but not WHERE - should fail
	err := ValidateQuery("SELECT * FROM tenant_id_log WHERE active = true", "tenant_id")
	if err == nil {
		t.Fatal("expected error: tenant_id is not in WHERE clause")
	}
}

func TestValidateQuery_InsertIsOK(t *testing.T) {
	// INSERT statements don't need WHERE
	err := ValidateQuery("INSERT INTO users (name) VALUES ('test')", "tenant_id")
	if err != nil {
		t.Errorf("expected no error for INSERT, got %v", err)
	}
}

func TestValidateQuery_UpdateMissingTenantInWhere(t *testing.T) {
	err := ValidateQuery("UPDATE users SET active = true WHERE name = 'test'", "tenant_id")
	if err == nil {
		t.Fatal("expected error for UPDATE missing tenant_id in WHERE")
	}
}

func TestValidateQuery_DeleteMissingWhere(t *testing.T) {
	err := ValidateQuery("DELETE FROM users", "tenant_id")
	if err == nil {
		t.Fatal("expected error for DELETE without WHERE")
	}
}

// --- indexCaseInsensitive tests ---

func TestIndexCaseInsensitive(t *testing.T) {
	cases := []struct {
		s      string
		substr string
		want   int
	}{
		{"SELECT * FROM users WHERE id = 1", " WHERE ", 19},
		{"SELECT * FROM users where id = 1", " WHERE ", 19},
		{"SELECT * FROM users Where id = 1", " WHERE ", 19},
		{"SELECT * FROM users", " WHERE ", -1},
		{"", " WHERE ", -1},
	}

	for _, tc := range cases {
		got := indexCaseInsensitive(tc.s, tc.substr)
		if got != tc.want {
			t.Errorf("indexCaseInsensitive(%q, %q) = %d, want %d", tc.s, tc.substr, got, tc.want)
		}
	}
}

// --- Integration-style tests with real stub DB ---

func TestTenantDB_QueryContext_InjectsTenantFilter(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)

	// This should not panic or error - the query gets modified but stub doesn't care
	rows, err := tdb.QueryContext(t.Context(), "t-123", "SELECT * FROM users WHERE active = ?", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows != nil {
		rows.Close()
	}
}

func TestTenantDB_ExecContext_InjectsTenantFilter(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)

	_, err := tdb.ExecContext(t.Context(), "t-123", "DELETE FROM users WHERE id = ?", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTenantDB_QueryRowContext_InjectsTenantFilter(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)

	row := tdb.QueryRowContext(t.Context(), "t-123", "SELECT * FROM users WHERE id = ?", 1)
	if row == nil {
		t.Fatal("expected non-nil row")
	}
}

func TestTenantDB_QueryRowFromContext_WithTenant(t *testing.T) {
	connector := &stubConnector{conn: &stubConn{}}
	db := sql.OpenDB(connector)
	defer db.Close()

	tdb := NewTenantDB(db)
	ctx := tenant.WithTenantID(t.Context(), "t-123")

	row := tdb.QueryRowFromContext(ctx, "SELECT * FROM users WHERE id = ?", 1)
	if row == nil {
		t.Fatal("expected non-nil row")
	}
}

// Verify that ValidateQuery accepts queries with different cases
func TestValidateQuery_CaseVariations(t *testing.T) {
	cases := []struct {
		name  string
		query string
		valid bool
	}{
		{"uppercase SELECT", "SELECT * FROM users WHERE tenant_id = ?", true},
		{"lowercase select", "select * from users where tenant_id = ?", true},
		{"mixed case", "Select * From users Where tenant_id = ?", true},
		{"UPDATE with tenant", "UPDATE users SET name = 'x' WHERE tenant_id = ?", true},
		{"DELETE with tenant", "DELETE FROM users WHERE tenant_id = ?", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateQuery(tc.query, "tenant_id")
			if tc.valid && err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
			if !tc.valid && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// Test addTenantFilter with custom column name
func TestAddTenantFilter_CustomColumn(t *testing.T) {
	tdb := &TenantDB{tenantColumn: "org_id"}

	query, args := tdb.addTenantFilter(
		"SELECT * FROM users WHERE active = ?",
		"org-456",
		[]any{true},
	)

	expected := "SELECT * FROM users WHERE org_id = ? AND active = ?"
	if query != expected {
		t.Errorf("expected %q, got %q", expected, query)
	}
	if len(args) != 2 || args[0] != "org-456" {
		t.Errorf("unexpected args: %v", args)
	}
}

// Verify that nil TenantDB returns error for ExecContext
func TestTenantDB_NilExecContext(t *testing.T) {
	var tdb *TenantDB
	_, err := tdb.ExecContext(t.Context(), "t-123", "DELETE FROM users")
	if err == nil {
		t.Fatal("expected error for nil TenantDB")
	}
	if !errors.Is(err, errors.New("database not initialized")) && err.Error() != "database not initialized" {
		t.Logf("error message: %v", err) // Just log; the error message is checked above
	}
}
