package idempotency

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// --- minimal in-memory SQL driver for testing ---

func init() {
	sql.Register("idemmock", &mockDriver{})
}

type mockDriver struct{}

func (d *mockDriver) Open(name string) (driver.Conn, error) {
	return &mockConn{db: newMockDB()}, nil
}

// mockDB holds rows keyed by idempotency key.
type mockDB struct {
	rows           map[string]mockRow
	deleteErr      error
	deleteCountErr error
}

type mockRow struct {
	key         string
	requestHash string
	status      string
	response    []byte
	createdAt   time.Time
	updatedAt   time.Time
	expiresAt   *time.Time
}

func newMockDB() *mockDB {
	return &mockDB{rows: make(map[string]mockRow)}
}

type mockConn struct {
	db *mockDB
}

func (c *mockConn) Prepare(query string) (driver.Stmt, error) {
	return &mockStmt{conn: c, query: query}, nil
}

func (c *mockConn) Close() error { return nil }
func (c *mockConn) Begin() (driver.Tx, error) {
	return &mockTx{}, nil
}

type mockTx struct{}

func (tx *mockTx) Commit() error   { return nil }
func (tx *mockTx) Rollback() error { return nil }

type mockStmt struct {
	conn  *mockConn
	query string
}

func (s *mockStmt) Close() error  { return nil }
func (s *mockStmt) NumInput() int { return -1 }
func (s *mockStmt) Exec(args []driver.Value) (driver.Result, error) {
	q := strings.ToUpper(s.query)
	db := s.conn.db
	switch {
	case strings.HasPrefix(q, "INSERT"):
		// args: key, request_hash, status, response, created_at, updated_at, expires_at
		if len(args) < 7 {
			return nil, errors.New("mock: not enough args for INSERT")
		}
		key := args[0].(string)
		if _, exists := db.rows[key]; exists {
			return nil, errors.New("unique constraint failed")
		}
		row := mockRow{
			key:         key,
			requestHash: args[1].(string),
			status:      args[2].(string),
			createdAt:   args[4].(time.Time),
			updatedAt:   args[5].(time.Time),
		}
		if args[3] != nil {
			if b, ok := args[3].([]byte); ok {
				row.response = b
			}
		}
		if args[6] != nil {
			if t, ok := args[6].(time.Time); ok {
				row.expiresAt = &t
			}
		}
		db.rows[key] = row
		return mockResult{1}, nil

	case strings.HasPrefix(q, "UPDATE"):
		// args: status, response, updated_at, key[, expected_status, now]
		if len(args) < 4 {
			return nil, errors.New("mock: not enough args for UPDATE")
		}
		key := args[3].(string)
		row, exists := db.rows[key]
		if !exists {
			return mockResult{0}, nil
		}
		if len(args) >= 6 {
			expectedStatus := args[4].(string)
			now := args[5].(time.Time)
			if row.status != expectedStatus {
				return mockResult{0}, nil
			}
			if row.expiresAt != nil && !row.expiresAt.After(now) {
				return mockResult{0}, nil
			}
		}
		row.status = args[0].(string)
		if args[1] != nil {
			if b, ok := args[1].([]byte); ok {
				row.response = b
			}
		}
		row.updatedAt = args[2].(time.Time)
		db.rows[key] = row
		return mockResult{1}, nil

	case strings.HasPrefix(q, "DELETE"):
		if len(args) < 1 {
			return nil, errors.New("mock: not enough args for DELETE")
		}
		if db.deleteErr != nil {
			return nil, db.deleteErr
		}
		key := args[0].(string)
		row, exists := db.rows[key]
		if !exists {
			return mockResult{0}, nil
		}
		if len(args) == 2 {
			if !strings.Contains(q, "AND EXPIRES_AT <=") {
				return nil, errors.New("mock: DELETE missing expiry condition")
			}
			if row.expiresAt == nil || row.expiresAt.After(args[1].(time.Time)) {
				return mockResult{0}, nil
			}
		}
		delete(db.rows, key)
		if db.deleteCountErr != nil {
			return mockCountError{db.deleteCountErr}, nil
		}
		return mockResult{1}, nil
	}
	return mockResult{0}, nil
}

func (s *mockStmt) Query(args []driver.Value) (driver.Rows, error) {
	if len(args) < 1 {
		return nil, errors.New("mock: missing key arg")
	}
	key, ok := args[0].(string)
	if !ok {
		return nil, errors.New("mock: key must be string")
	}
	row, exists := s.conn.db.rows[key]
	if !exists {
		return &mockRows{}, nil
	}
	return &mockRows{rows: []mockRow{row}, pos: -1}, nil
}

type mockResult struct{ affected int64 }

func (r mockResult) LastInsertId() (int64, error) { return 0, nil }
func (r mockResult) RowsAffected() (int64, error) { return r.affected, nil }

type mockCountError struct{ error }

func (r mockCountError) LastInsertId() (int64, error) { return 0, nil }
func (r mockCountError) RowsAffected() (int64, error) { return 0, r.error }

type mockRows struct {
	rows []mockRow
	pos  int
}

func (r *mockRows) Columns() []string {
	return []string{"key", "request_hash", "status", "response", "created_at", "updated_at", "expires_at"}
}

func (r *mockRows) Close() error { return nil }

func (r *mockRows) Next(dest []driver.Value) error {
	r.pos++
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.pos]
	dest[0] = row.key
	dest[1] = row.requestHash
	dest[2] = row.status
	dest[3] = row.response
	dest[4] = row.createdAt
	dest[5] = row.updatedAt
	if row.expiresAt != nil {
		dest[6] = *row.expiresAt
	} else {
		dest[6] = nil
	}
	return nil
}

// --- helper to open a mock SQL store ---

func newSQLStore(t *testing.T) (*SQLStore, *sql.DB) {
	t.Helper()
	db, err := sql.Open("idemmock", "test")
	if err != nil {
		t.Fatalf("open mock db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := SQLConfig{
		Dialect: DialectMySQL,
		Table:   "idempotency_keys",
		Now:     time.Now,
	}
	return NewSQLStore(db, cfg), db
}

// --- SQL store tests ---

func TestSQLStore_PutAndGet(t *testing.T) {
	s, _ := newSQLStore(t)
	ctx := t.Context()

	rec := Record{
		Key:         "sql-key-1",
		RequestHash: "hash-1",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	created, err := s.PutIfAbsent(ctx, rec)
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	if !created {
		t.Fatal("expected created=true")
	}

	got, found, err := s.Get(ctx, "sql-key-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("expected found")
	}
	if got.Key != "sql-key-1" {
		t.Fatalf("Key = %q, want sql-key-1", got.Key)
	}
	if got.Status != StatusInProgress {
		t.Fatalf("Status = %q, want %s", got.Status, StatusInProgress)
	}
}

func TestSQLStore_PutIfAbsent_Duplicate(t *testing.T) {
	s, _ := newSQLStore(t)
	ctx := t.Context()

	rec := Record{Key: "sql-dup", ExpiresAt: time.Now().Add(time.Hour)}
	created, err := s.PutIfAbsent(ctx, rec)
	if err != nil || !created {
		t.Fatalf("first put: %v %v", created, err)
	}

	created, err = s.PutIfAbsent(ctx, rec)
	if err != nil {
		t.Fatalf("duplicate put error: %v", err)
	}
	if created {
		t.Fatal("expected created=false for duplicate")
	}
}

func TestSQLStore_CustomDuplicateClassifier(t *testing.T) {
	driverDuplicate := errors.New("driver duplicate code")
	s := NewSQLStore(nil, SQLConfig{
		DuplicateError: func(err error) bool {
			return errors.Is(err, driverDuplicate)
		},
	})

	if !s.isDuplicateError(errors.Join(errors.New("wrapped"), driverDuplicate)) {
		t.Fatal("expected configured duplicate classifier to match")
	}
	if !s.isDuplicateError(errors.New("unique constraint failed")) {
		t.Fatal("expected built-in duplicate fallback to remain compatible")
	}
	if s.isDuplicateError(sql.ErrNoRows) {
		t.Fatal("sql.ErrNoRows must not be treated as a duplicate error")
	}
}

func TestSQLStore_PutIfAbsent_EmptyKey(t *testing.T) {
	s, _ := newSQLStore(t)
	_, err := s.PutIfAbsent(t.Context(), Record{Key: ""})
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestSQLStore_PutIfAbsent_Expired(t *testing.T) {
	s, _ := newSQLStore(t)
	rec := Record{Key: "sql-expired", ExpiresAt: time.Now().Add(-time.Minute)}
	_, err := s.PutIfAbsent(t.Context(), rec)
	if err != ErrExpired {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func TestSQLStore_Complete(t *testing.T) {
	s, _ := newSQLStore(t)
	ctx := t.Context()

	_, err := s.PutIfAbsent(ctx, Record{Key: "sql-complete", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	if err := s.Complete(ctx, "sql-complete", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, found, err := s.Get(ctx, "sql-complete")
	if err != nil || !found {
		t.Fatalf("Get after complete: %v %v", found, err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("Status = %q, want completed", got.Status)
	}
	if string(got.Response) != `{"ok":true}` {
		t.Fatalf("Response = %q, want json", got.Response)
	}
}

func TestSQLStore_Complete_EmptyKey(t *testing.T) {
	s, _ := newSQLStore(t)
	err := s.Complete(t.Context(), "", nil)
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestSQLStore_Complete_NotFound(t *testing.T) {
	s, _ := newSQLStore(t)
	err := s.Complete(t.Context(), "ghost", nil)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLStore_Complete_OnlyInProgress(t *testing.T) {
	s, _ := newSQLStore(t)
	ctx := t.Context()

	_, err := s.PutIfAbsent(ctx, Record{Key: "sql-complete-once", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	if err := s.Complete(ctx, "sql-complete-once", []byte("ok")); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if err := s.Complete(ctx, "sql-complete-once", []byte("again")); err != ErrNotFound {
		t.Fatalf("second Complete = %v, want ErrNotFound", err)
	}
}

func TestSQLStore_Complete_ExpiredRecord(t *testing.T) {
	now := time.Now()
	db, err := sql.Open("idemmock", "test")
	if err != nil {
		t.Fatalf("open mock db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s := NewSQLStore(db, SQLConfig{
		Dialect: DialectMySQL,
		Table:   "idempotency_keys",
		Now:     func() time.Time { return now },
	})
	ctx := t.Context()
	_, err = s.PutIfAbsent(ctx, Record{Key: "sql-expire-before-complete", ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	now = now.Add(2 * time.Minute)
	if err := s.Complete(ctx, "sql-expire-before-complete", []byte("late")); err != ErrNotFound {
		t.Fatalf("Complete expired = %v, want ErrNotFound", err)
	}
}

func TestSQLStore_Delete(t *testing.T) {
	s, _ := newSQLStore(t)
	ctx := t.Context()

	_, err := s.PutIfAbsent(ctx, Record{Key: "sql-del", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	if err := s.Delete(ctx, "sql-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, found, err := s.Get(ctx, "sql-del")
	if err != nil || found {
		t.Fatalf("expected not found after delete, err=%v found=%v", err, found)
	}
}

func TestSQLStore_Delete_EmptyKey(t *testing.T) {
	s, _ := newSQLStore(t)
	err := s.Delete(t.Context(), "")
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestSQLStore_Get_EmptyKey(t *testing.T) {
	s, _ := newSQLStore(t)
	_, _, err := s.Get(t.Context(), "")
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestSQLStore_Get_NotFound(t *testing.T) {
	s, _ := newSQLStore(t)
	_, found, err := s.Get(t.Context(), "missing-sql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func TestSQLStore_BuildInsert_Postgres(t *testing.T) {
	s := NewSQLStore(nil, SQLConfig{Dialect: DialectPostgres, Table: "keys"})
	rec := Record{
		Key:         "k",
		RequestHash: "h",
		Status:      StatusInProgress,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	query, args := s.insertStatement(s.cfg.Table, rec)
	if !strings.Contains(query, "$1") {
		t.Fatalf("postgres query should use $N placeholders: %s", query)
	}
	if len(args) != 7 {
		t.Fatalf("expected 7 args, got %d", len(args))
	}
}

func TestSQLStore_BuildInsert_MySQL(t *testing.T) {
	s := NewSQLStore(nil, SQLConfig{Dialect: DialectMySQL, Table: "keys"})
	rec := Record{Key: "k", Status: StatusInProgress, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	query, args := s.insertStatement(s.cfg.Table, rec)
	if !strings.Contains(query, "?") {
		t.Fatalf("mysql query should use ? placeholders: %s", query)
	}
	if len(args) != 7 {
		t.Fatalf("expected 7 args, got %d", len(args))
	}
}

func TestSQLStore_DefaultDialectIsPostgres(t *testing.T) {
	s := NewSQLStore(nil, SQLConfig{Table: "keys"})
	query, _ := s.insertStatement(s.cfg.Table, Record{Key: "k"})
	if !strings.Contains(query, "$1") {
		t.Fatalf("zero-value dialect should default to postgres placeholders: %s", query)
	}
}

func TestSQLStore_InvalidTableName(t *testing.T) {
	db, err := sql.Open("idemmock", "test")
	if err != nil {
		t.Fatalf("open mock db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s := NewSQLStore(db, SQLConfig{Dialect: DialectMySQL, Table: "keys; DROP TABLE users"})
	_, _, err = s.Get(t.Context(), "k")
	if err == nil || !strings.Contains(err.Error(), "invalid table name") {
		t.Fatalf("expected invalid table name error, got %v", err)
	}
}

// Ensure JSONmarshal round-trip for Record works (supports kv serialization).
func TestRecord_JSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	rec := Record{
		Key:         "round-trip",
		RequestHash: "abc123",
		Status:      StatusCompleted,
		Response:    []byte("body"),
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(time.Hour),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Key != rec.Key || got.Status != rec.Status {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestSQLStoreReclaimsOnlyExpiredKeys(t *testing.T) {
	for _, dialect := range []Dialect{DialectPostgres, DialectMySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			_, db := newSQLStore(t)
			// The in-memory driver stores its rows on the connection.
			db.SetMaxOpenConns(1)
			now := time.Now()
			store := NewSQLStore(db, SQLConfig{Dialect: dialect, Now: func() time.Time { return now }})
			ctx := t.Context()

			for _, record := range []Record{
				{Key: "expired", ExpiresAt: now.Add(time.Minute)},
				{Key: "active", ExpiresAt: now.Add(time.Hour)},
				{Key: "permanent"},
			} {
				if claimed, err := store.PutIfAbsent(ctx, record); err != nil || !claimed {
					t.Fatalf("seed %q = %t, %v", record.Key, claimed, err)
				}
			}
			now = now.Add(time.Minute)
			for _, key := range []string{"active", "permanent"} {
				if claimed, err := store.PutIfAbsent(ctx, Record{Key: key}); err != nil || claimed {
					t.Fatalf("reclaim %q = %t, %v", key, claimed, err)
				}
			}

			type claimResult struct {
				claimed bool
				err     error
			}
			start := make(chan struct{})
			results := make(chan claimResult, 2)
			for range 2 {
				go func() {
					<-start
					claimed, err := store.PutIfAbsent(ctx, Record{Key: "expired", RequestHash: "replacement"})
					results <- claimResult{claimed, err}
				}()
			}
			close(start)
			winners := 0
			for range 2 {
				result := <-results
				if result.err != nil {
					t.Fatal(result.err)
				}
				if result.claimed {
					winners++
				}
			}
			record, found, err := store.Get(ctx, "expired")
			if winners != 1 || err != nil || !found || record.RequestHash != "replacement" {
				t.Fatalf("winners = %d, read = %+v, %t, %v", winners, record, found, err)
			}
			if err := store.deleteExpired(ctx, store.cfg.Table, "expired", now); err != nil {
				t.Fatal(err)
			}
			if _, found, err := store.Get(ctx, "expired"); !found || err != nil {
				t.Fatalf("stale cleanup removed a valid claim: %t, %v", found, err)
			}
		})
	}
}

func TestSQLStoreUsesConsistentKeys(t *testing.T) {
	store, db := newSQLStore(t)
	ctx := t.Context()

	// Simulate an older raw key. Upgrading does not merge or delete that row.
	query, args := store.insertStatement(store.cfg.Table, Record{Key: " order ", Status: StatusInProgress, RequestHash: "legacy"})
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	const key = " order "
	if inserted, err := store.PutIfAbsent(ctx, Record{Key: key, RequestHash: "canonical"}); !inserted || err != nil {
		t.Fatalf("PutIfAbsent = %t, %v", inserted, err)
	}
	if inserted, err := store.PutIfAbsent(ctx, Record{Key: "order"}); inserted || err != nil {
		t.Fatalf("second canonical claim = %t, %v", inserted, err)
	}
	if err := store.Complete(ctx, key, []byte("done")); err != nil {
		t.Fatal(err)
	}
	record, found, err := store.Get(ctx, key)
	if !found || err != nil || record.Key != "order" || record.RequestHash != "canonical" ||
		record.Status != StatusCompleted || string(record.Response) != "done" {
		t.Fatalf("Get = %+v, %t, %v", record, found, err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	var legacyKey string
	err = db.QueryRowContext(ctx, "SELECT key, request_hash, status, response, created_at, updated_at, expires_at FROM idempotency_keys WHERE key = ?", key).Scan(
		&legacyKey, new(string), new(string), new([]byte), new(time.Time), new(time.Time), new(sql.NullTime),
	)
	if err != nil || legacyKey != key {
		t.Fatalf("legacy key = %q, %v", legacyKey, err)
	}
}

func TestSQLStoreDeleteReportsMissingAndDriverErrors(t *testing.T) {
	store, db := newSQLStore(t)
	ctx := t.Context()
	if err := store.Delete(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing = %v", err)
	}
	if inserted, err := store.PutIfAbsent(ctx, Record{Key: "present"}); !inserted || err != nil {
		t.Fatalf("seed = %t, %v", inserted, err)
	}
	injected := errors.New("cannot read affected row count")
	withMock := func(ctx context.Context, set func(*mockDB)) {
		t.Helper()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := conn.Raw(func(raw any) error {
			set(raw.(*mockConn).db)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	withMock(ctx, func(db *mockDB) { db.deleteErr = injected })
	if err := store.Delete(ctx, "present"); !errors.Is(err, injected) {
		t.Fatalf("Delete execution error = %v", err)
	}
	withMock(ctx, func(db *mockDB) {
		db.deleteErr = nil
		db.deleteCountErr = injected
	})
	if err := store.Delete(ctx, "present"); !errors.Is(err, injected) {
		t.Fatalf("Delete row count error = %v", err)
	}
	if err := store.Delete(ctx, "present"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete again = %v", err)
	}
}
