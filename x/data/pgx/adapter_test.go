package pgx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestQueryRowReturnsScannedValue(t *testing.T) {
	pool := &fakePool{
		row: fakeRow{values: []any{"plumego"}},
	}
	db := NewWithPool(pool)

	var got string
	if err := db.QueryRow(t.Context(), "select name").Scan(&got); err != nil {
		t.Fatalf("scan row: %v", err)
	}
	if got != "plumego" {
		t.Fatalf("got %q, want plumego", got)
	}
	if pool.queryRowSQL != "select name" {
		t.Fatalf("query = %q, want select name", pool.queryRowSQL)
	}
}

func TestQueryReturnsMultipleRows(t *testing.T) {
	pool := &fakePool{
		rows: &fakeRows{values: [][]any{{"alpha"}, {"beta"}}},
	}
	db := NewWithPool(pool)

	rows, err := db.Query(t.Context(), "select name")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
}

func TestExecAffectsRowCount(t *testing.T) {
	pool := &fakePool{
		execTag: fakeCommandTag{rows: 3},
	}
	db := NewWithPool(pool)

	tag, err := db.Exec(t.Context(), "update widgets set active = true")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got := tag.RowsAffected(); got != 3 {
		t.Fatalf("rows affected = %d, want 3", got)
	}
}

func TestBeginTxCommitSucceeds(t *testing.T) {
	tx := &fakeTx{}
	db := NewWithPool(&fakePool{tx: tx})

	started, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := started.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !tx.committed {
		t.Fatal("expected transaction to be committed")
	}
}

func TestBeginTxRollbackSucceeds(t *testing.T) {
	tx := &fakeTx{}
	db := NewWithPool(&fakePool{tx: tx})

	started, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := started.Rollback(t.Context()); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !tx.rolledBack {
		t.Fatal("expected transaction to be rolled back")
	}
}

func TestNewPingsPool(t *testing.T) {
	pool := &fakePool{}
	db, err := New(t.Context(), pool)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if db.Pool() != pool {
		t.Fatal("New() did not wrap pool")
	}
	if !pool.pinged {
		t.Fatal("expected pool to be pinged")
	}
}

type fakePool struct {
	row         Row
	rows        Rows
	tx          Tx
	execTag     CommandTag
	queryRowSQL string
	pinged      bool
}

func (p *fakePool) QueryRow(_ context.Context, query string, _ ...any) Row {
	p.queryRowSQL = query
	if p.row == nil {
		return fakeRow{err: errors.New("row not configured")}
	}
	return p.row
}

func (p *fakePool) Query(context.Context, string, ...any) (Rows, error) {
	if p.rows == nil {
		return nil, errors.New("rows not configured")
	}
	return p.rows, nil
}

func (p *fakePool) Exec(context.Context, string, ...any) (CommandTag, error) {
	return p.execTag, nil
}

func (p *fakePool) BeginTx(context.Context, TxOptions) (Tx, error) {
	if p.tx == nil {
		return nil, errors.New("tx not configured")
	}
	return p.tx, nil
}

func (p *fakePool) Ping(context.Context) error {
	p.pinged = true
	return nil
}

func (p *fakePool) Close() {}

type fakeCommandTag struct {
	rows int64
}

func (t fakeCommandTag) RowsAffected() int64 {
	return t.rows
}

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanValues(dest, r.values)
}

type fakeRows struct {
	values [][]any
	index  int
	closed bool
	err    error
}

func (r *fakeRows) Close() {
	r.closed = true
}

func (r *fakeRows) Err() error {
	return r.err
}

func (r *fakeRows) Next() bool {
	if r.index >= len(r.values) {
		r.closed = true
		return false
	}
	r.index++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.index == 0 || r.index > len(r.values) {
		return errors.New("scan called without current row")
	}
	return scanValues(dest, r.values[r.index-1])
}

type fakeTx struct {
	committed  bool
	rolledBack bool
}

func (t *fakeTx) Commit(context.Context) error {
	t.committed = true
	return nil
}

func (t *fakeTx) Rollback(context.Context) error {
	t.rolledBack = true
	return nil
}

func (t *fakeTx) QueryRow(context.Context, string, ...any) Row {
	return fakeRow{}
}

func (t *fakeTx) Query(context.Context, string, ...any) (Rows, error) {
	return &fakeRows{}, nil
}

func (t *fakeTx) Exec(context.Context, string, ...any) (CommandTag, error) {
	return fakeCommandTag{}, nil
}

func scanValues(dest []any, values []any) error {
	if len(dest) != len(values) {
		return fmt.Errorf("scan destination count %d does not match value count %d", len(dest), len(values))
	}
	for i, value := range values {
		target := reflect.ValueOf(dest[i])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return fmt.Errorf("destination %d is not a non-nil pointer", i)
		}
		source := reflect.ValueOf(value)
		if !source.Type().AssignableTo(target.Elem().Type()) {
			return fmt.Errorf("cannot assign %T to %T", value, dest[i])
		}
		target.Elem().Set(source)
	}
	return nil
}
