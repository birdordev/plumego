package idempotency

import (
	"testing"
	"time"

	kvstore "github.com/birdordev/plumego/store/kv"
)

// --- helpers ---

func newKV(t *testing.T) *kvstore.KVStore {
	t.Helper()
	store, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open kv: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newIdem(t *testing.T) *KVStore {
	t.Helper()
	return NewKVStore(newKV(t), DefaultKVConfig())
}

// --- KVStore tests ---

func TestKVStore_Get_EmptyKey(t *testing.T) {
	s := newIdem(t)
	_, _, err := s.Get(t.Context(), "")
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestKVStore_Get_NotFound(t *testing.T) {
	s := newIdem(t)
	_, found, err := s.Get(t.Context(), "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected not found")
	}
}

func TestKVStore_PutIfAbsent_EmptyKey(t *testing.T) {
	s := newIdem(t)
	_, err := s.PutIfAbsent(t.Context(), Record{Key: "  "})
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestKVStore_PutIfAbsent_Duplicate(t *testing.T) {
	s := newIdem(t)
	ctx := t.Context()
	rec := Record{
		Key:       "dup-key",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	created, err := s.PutIfAbsent(ctx, rec)
	if err != nil || !created {
		t.Fatalf("first PutIfAbsent: created=%v err=%v", created, err)
	}

	created, err = s.PutIfAbsent(ctx, rec)
	if err != nil {
		t.Fatalf("duplicate PutIfAbsent: %v", err)
	}
	if created {
		t.Fatal("expected created=false for duplicate")
	}
}

func TestKVStore_PutIfAbsent_DefaultsStatus(t *testing.T) {
	s := newIdem(t)
	ctx := t.Context()

	created, err := s.PutIfAbsent(ctx, Record{Key: "no-status", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil || !created {
		t.Fatalf("PutIfAbsent: %v %v", created, err)
	}

	got, found, err := s.Get(ctx, "no-status")
	if err != nil || !found {
		t.Fatalf("Get: %v %v", found, err)
	}
	if got.Status != StatusInProgress {
		t.Fatalf("expected StatusInProgress, got %s", got.Status)
	}
}

func TestKVStore_PutIfAbsent_NoExpiry(t *testing.T) {
	s := newIdem(t)
	ctx := t.Context()

	// Record without ExpiresAt should not fail with ErrExpired
	created, err := s.PutIfAbsent(ctx, Record{Key: "no-expiry"})
	if err != nil {
		t.Fatalf("PutIfAbsent no expiry: %v", err)
	}
	if !created {
		t.Fatal("expected created=true")
	}
}

func TestKVStore_Complete_EmptyKey(t *testing.T) {
	s := newIdem(t)
	err := s.Complete(t.Context(), "", nil)
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestKVStore_Complete_NotFound(t *testing.T) {
	s := newIdem(t)
	err := s.Complete(t.Context(), "ghost", []byte("resp"))
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestKVStore_Complete_AlreadyExpiredDuringComplete(t *testing.T) {
	// Use controlled clock: far past so record expires immediately on Complete.
	now := time.Now()
	s := NewKVStore(newKV(t), KVConfig{Prefix: "test:", Now: func() time.Time { return now }})
	ctx := t.Context()

	// Insert record with expiry one minute in future relative to "now".
	rec := Record{
		Key:       "expiring",
		ExpiresAt: now.Add(time.Minute),
	}
	created, err := s.PutIfAbsent(ctx, rec)
	if err != nil || !created {
		t.Fatalf("PutIfAbsent: %v %v", created, err)
	}

	// Advance clock past expiry.
	future := now.Add(2 * time.Minute)
	s2 := NewKVStore(s.store, KVConfig{Prefix: "test:", Now: func() time.Time { return future }})

	err = s2.Complete(ctx, "expiring", []byte("resp"))
	if err != ErrNotFound {
		// The record is expired, so Get returns not-found, thus ErrNotFound from Complete.
		t.Fatalf("expected ErrNotFound for expired record, got %v", err)
	}
}

func TestKVStore_Delete_EmptyKey(t *testing.T) {
	s := newIdem(t)
	err := s.Delete(t.Context(), "")
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestKVStore_Delete_ExistingRecord(t *testing.T) {
	s := newIdem(t)
	ctx := t.Context()

	_, err := s.PutIfAbsent(ctx, Record{Key: "del-me", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	if err := s.Delete(ctx, "del-me"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, found, err := s.Get(ctx, "del-me")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if found {
		t.Fatal("record should not exist after delete")
	}
}

func TestKVStore_NilStore(t *testing.T) {
	var s *KVStore
	ctx := t.Context()

	if _, _, err := s.Get(ctx, "k"); err != ErrNotFound {
		t.Fatalf("nil Get: want ErrNotFound, got %v", err)
	}
	if _, err := s.PutIfAbsent(ctx, Record{Key: "k"}); err != ErrNotFound {
		t.Fatalf("nil PutIfAbsent: want ErrNotFound, got %v", err)
	}
	if err := s.Complete(ctx, "k", nil); err != ErrNotFound {
		t.Fatalf("nil Complete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, "k"); err != ErrNotFound {
		t.Fatalf("nil Delete: want ErrNotFound, got %v", err)
	}
}

func TestKVConfig_Defaults(t *testing.T) {
	cfg := DefaultKVConfig()
	if cfg.Prefix != "idem:" {
		t.Fatalf("expected prefix 'idem:', got %q", cfg.Prefix)
	}
	if cfg.Now == nil {
		t.Fatal("expected non-nil Now")
	}
}

func TestNewKVStore_DefaultsOnEmptyConfig(t *testing.T) {
	store := newKV(t)
	// Empty prefix and nil Now should be filled in by NewKVStore.
	s := NewKVStore(store, KVConfig{})
	if s.prefix != "idem:" {
		t.Fatalf("expected prefix 'idem:', got %q", s.prefix)
	}
	if s.now == nil {
		t.Fatal("expected non-nil now func")
	}
}

func TestKVStore_Get_WithWhitespaceKey(t *testing.T) {
	s := newIdem(t)
	_, _, err := s.Get(t.Context(), "   ")
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey for whitespace-only key, got %v", err)
	}
}

func TestKVStore_PutIfAbsent_SetsTimestamps(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewKVStore(newKV(t), KVConfig{Prefix: "ts:", Now: func() time.Time { return now }})
	ctx := t.Context()

	_, err := s.PutIfAbsent(ctx, Record{Key: "ts-test", ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	got, found, err := s.Get(ctx, "ts-test")
	if err != nil || !found {
		t.Fatalf("Get: %v %v", found, err)
	}
	if !got.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, now)
	}
}

func TestKVStore_PutIfAbsent_PreservesExistingCreatedAt(t *testing.T) {
	s := newIdem(t)
	ctx := t.Context()
	customTime := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)

	_, err := s.PutIfAbsent(ctx, Record{
		Key:       "custom-ts",
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: customTime,
	})
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	got, _, err := s.Get(ctx, "custom-ts")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.CreatedAt.Equal(customTime) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, customTime)
	}
}

// --- SQLConfig/SQLStore unit tests (no DB required for config and placeholder logic) ---

func TestDefaultSQLConfig(t *testing.T) {
	cfg := DefaultSQLConfig()
	if cfg.Table != "idempotency_keys" {
		t.Fatalf("want table 'idempotency_keys', got %q", cfg.Table)
	}
	if cfg.Dialect != DialectPostgres {
		t.Fatalf("want DialectPostgres, got %q", cfg.Dialect)
	}
	if cfg.Now == nil {
		t.Fatal("expected non-nil Now")
	}
}

func TestSQLStore_NilDB(t *testing.T) {
	s := NewSQLStore(nil, DefaultSQLConfig())
	ctx := t.Context()

	if _, _, err := s.Get(ctx, "k"); err != ErrNotFound {
		t.Fatalf("nil DB Get: want ErrNotFound, got %v", err)
	}
	if _, err := s.PutIfAbsent(ctx, Record{Key: "k"}); err != ErrNotFound {
		t.Fatalf("nil DB PutIfAbsent: want ErrNotFound, got %v", err)
	}
	if err := s.Complete(ctx, "k", nil); err != ErrNotFound {
		t.Fatalf("nil DB Complete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, "k"); err != ErrNotFound {
		t.Fatalf("nil DB Delete: want ErrNotFound, got %v", err)
	}
}

func TestSQLStore_NilDBPointer(t *testing.T) {
	var s *SQLStore
	ctx := t.Context()

	if _, _, err := s.Get(ctx, "k"); err != ErrNotFound {
		t.Fatalf("nil SQLStore Get: want ErrNotFound, got %v", err)
	}
	if _, err := s.PutIfAbsent(ctx, Record{Key: "k"}); err != ErrNotFound {
		t.Fatalf("nil SQLStore PutIfAbsent: want ErrNotFound, got %v", err)
	}
	if err := s.Complete(ctx, "k", nil); err != ErrNotFound {
		t.Fatalf("nil SQLStore Complete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete(ctx, "k"); err != ErrNotFound {
		t.Fatalf("nil SQLStore Delete: want ErrNotFound, got %v", err)
	}
}

func TestSQLStore_EmptyKeyValidation(t *testing.T) {
	s := NewSQLStore(nil, DefaultSQLConfig())
	ctx := t.Context()

	// A nil DB short-circuits to ErrNotFound before key validation runs.
	if _, _, err := s.Get(ctx, ""); err != ErrNotFound {
		t.Fatalf("Get with nil DB: err = %v, want %v", err, ErrNotFound)
	}
}

func TestSQLStore_DefaultsOnEmptyConfig(t *testing.T) {
	s := NewSQLStore(nil, SQLConfig{})
	if s.cfg.Table != "idempotency_keys" {
		t.Fatalf("expected default table, got %q", s.cfg.Table)
	}
	if s.now == nil {
		t.Fatal("expected non-nil now")
	}
}

func TestIsDuplicateError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"duplicate key value violates unique constraint", true},
		{"unique constraint violated", true},
		{"UNIQUE constraint failed", true},
		{"check constraint failed", false},
		{"not-null constraint failed", false},
		{"some other error", false},
		{"", false},
	}
	for _, tt := range tests {
		err := func() error {
			if tt.msg == "" {
				return nil
			}
			return &testError{tt.msg}
		}()
		if got := isDuplicateError(err); got != tt.want {
			t.Errorf("isDuplicateError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestNullTime(t *testing.T) {
	zero := time.Time{}
	if got := nullTime(zero); got != nil {
		t.Fatalf("nullTime(zero) = %v, want nil", got)
	}
	ts := time.Now()
	if got := nullTime(ts); got != ts {
		t.Fatalf("nullTime(ts) = %v, want %v", got, ts)
	}
}

func TestSQLStorePlaceholder(t *testing.T) {
	pg := NewSQLStore(nil, SQLConfig{Dialect: DialectPostgres, Table: "t"})
	if got := pg.placeholder(1); got != "$1" {
		t.Fatalf("postgres placeholder(1) = %q, want $1", got)
	}
	if got := pg.placeholder(3); got != "$3" {
		t.Fatalf("postgres placeholder(3) = %q, want $3", got)
	}

	my := NewSQLStore(nil, SQLConfig{Dialect: DialectMySQL, Table: "t"})
	if got := my.placeholder(1); got != "?" {
		t.Fatalf("mysql placeholder = %q, want ?", got)
	}
}

// testError is a minimal error type for isDuplicateError tests.
type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
