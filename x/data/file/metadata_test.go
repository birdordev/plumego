package file

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	storefile "github.com/spcent/plumego/store/file"
)

func TestNewDBMetadataManager_RejectsNilDB(t *testing.T) {
	m, err := NewDBMetadataManager(nil)
	if !errors.Is(err, ErrNilMetadataDB) {
		t.Fatalf("NewDBMetadataManager error = %v, want ErrNilMetadataDB", err)
	}
	if m != nil {
		t.Fatalf("manager = %#v, want nil", m)
	}
}

func TestDBMetadataManagerNilDBMethodsReturnSentinel(t *testing.T) {
	m := &DBMetadataManager{}
	ctx := t.Context()
	file := &File{File: storefile.File{ID: "f-1", Metadata: map[string]any{}}}

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "Save", run: func() error { return m.Save(ctx, file) }},
		{name: "Get", run: func() error { _, err := m.Get(ctx, "tenant-1", "f-1"); return err }},
		{name: "GetByPath", run: func() error { _, err := m.GetByPath(ctx, "tenant-1", "tenant/f-1"); return err }},
		{name: "GetByHash", run: func() error { _, err := m.GetByHash(ctx, "tenant-1", "hash"); return err }},
		{name: "List", run: func() error { _, _, err := m.List(ctx, Query{}); return err }},
		{name: "Delete", run: func() error { return m.Delete(ctx, "tenant-1", "f-1") }},
		{name: "UpdateAccessTime", run: func() error { return m.UpdateAccessTime(ctx, "tenant-1", "f-1") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, ErrNilMetadataDB) {
				t.Fatalf("error = %v, want ErrNilMetadataDB", err)
			}
		})
	}
}

func TestDBMetadataManagerUsesConfiguredClockForMutations(t *testing.T) {
	rec := &metadataExecRecorder{}
	db := sql.OpenDB(metadataConnector{rec: rec})
	defer db.Close()

	fixed := time.Date(2026, 4, 25, 12, 30, 0, 0, time.UTC)
	m, err := NewDBMetadataManager(db, WithMetadataClock(func() time.Time { return fixed }))
	if err != nil {
		t.Fatalf("NewDBMetadataManager error = %v", err)
	}

	if err := m.Delete(t.Context(), "tenant-1", "f-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := m.UpdateAccessTime(t.Context(), "tenant-1", "f-1"); err != nil {
		t.Fatalf("UpdateAccessTime() error = %v", err)
	}

	if len(rec.args) != 2 {
		t.Fatalf("recorded exec count = %d, want 2", len(rec.args))
	}
	for i, args := range rec.args {
		if len(args) == 0 {
			t.Fatalf("exec %d recorded no args", i)
		}
		got, ok := args[0].Value.(time.Time)
		if !ok {
			t.Fatalf("exec %d first arg type = %T, want time.Time", i, args[0].Value)
		}
		if !got.Equal(fixed) {
			t.Fatalf("exec %d timestamp = %v, want %v", i, got, fixed)
		}
	}
}

func TestDBMetadataManagerTenantScopedPredicates(t *testing.T) {
	rec := &metadataExecRecorder{}
	db := sql.OpenDB(metadataConnector{rec: rec})
	defer db.Close()

	m, err := NewDBMetadataManager(db)
	if err != nil {
		t.Fatalf("NewDBMetadataManager error = %v", err)
	}
	ctx := t.Context()

	_, _ = m.Get(ctx, "tenant-1", "f-1")
	_, _ = m.GetByPath(ctx, "tenant-1", "tenant-1/path.txt")
	_ = m.Delete(ctx, "tenant-1", "f-1")
	_ = m.UpdateAccessTime(ctx, "tenant-1", "f-1")

	wantQueries := []string{
		"WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL",
		"WHERE tenant_id = $1 AND path = $2 AND deleted_at IS NULL",
		"WHERE tenant_id = $2 AND id = $3 AND deleted_at IS NULL",
		"WHERE tenant_id = $2 AND id = $3 AND deleted_at IS NULL",
	}
	gotQueries := append([]string(nil), rec.queries...)
	gotQueries = append(gotQueries, rec.execs...)
	if len(gotQueries) != len(wantQueries) {
		t.Fatalf("recorded query count = %d, want %d: %#v", len(gotQueries), len(wantQueries), gotQueries)
	}
	for i, want := range wantQueries {
		if !strings.Contains(gotQueries[i], want) {
			t.Fatalf("query %d = %q, want to contain %q", i, gotQueries[i], want)
		}
	}

	if got := rec.qargs[0][0].Value; got != "tenant-1" {
		t.Fatalf("Get tenant arg = %v, want tenant-1", got)
	}
	if got := rec.qargs[1][0].Value; got != "tenant-1" {
		t.Fatalf("GetByPath tenant arg = %v, want tenant-1", got)
	}
	if got := rec.args[0][1].Value; got != "tenant-1" {
		t.Fatalf("Delete tenant arg = %v, want tenant-1", got)
	}
	if got := rec.args[1][1].Value; got != "tenant-1" {
		t.Fatalf("UpdateAccessTime tenant arg = %v, want tenant-1", got)
	}
}

func TestDBMetadataManagerListRequiresTenant(t *testing.T) {
	rec := &metadataExecRecorder{}
	db := sql.OpenDB(metadataConnector{rec: rec})
	defer db.Close()

	m, err := NewDBMetadataManager(db)
	if err != nil {
		t.Fatalf("NewDBMetadataManager error = %v", err)
	}

	_, _, err = m.List(t.Context(), Query{})
	if !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("List error = %v, want ErrTenantRequired", err)
	}
	if len(rec.queries) != 0 {
		t.Fatalf("recorded queries = %d, want 0", len(rec.queries))
	}
}

func TestDBMetadataManagerRejectsInvalidDirectInputs(t *testing.T) {
	rec := &metadataExecRecorder{}
	db := sql.OpenDB(metadataConnector{rec: rec})
	defer db.Close()

	m, err := NewDBMetadataManager(db)
	if err != nil {
		t.Fatalf("NewDBMetadataManager error = %v", err)
	}
	ctx := t.Context()

	validFile := &File{
		File:     storefile.File{ID: "f-1", Path: "tenant-1/path.txt", Hash: "hash"},
		TenantID: "tenant-1",
	}
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "Save nil file", run: func() error { return m.Save(ctx, nil) }},
		{name: "Save invalid tenant", run: func() error {
			f := *validFile
			f.TenantID = "../tenant"
			return m.Save(ctx, &f)
		}},
		{name: "Save invalid path", run: func() error {
			f := *validFile
			f.Path = "../path.txt"
			return m.Save(ctx, &f)
		}},
		{name: "Save missing hash", run: func() error {
			f := *validFile
			f.Hash = " "
			return m.Save(ctx, &f)
		}},
		{name: "Get invalid tenant", run: func() error { _, err := m.Get(ctx, "../tenant", "f-1"); return err }},
		{name: "Get empty id", run: func() error { _, err := m.Get(ctx, "tenant-1", " "); return err }},
		{name: "GetByPath invalid path", run: func() error {
			_, err := m.GetByPath(ctx, "tenant-1", "../path.txt")
			return err
		}},
		{name: "GetByHash invalid tenant", run: func() error {
			_, err := m.GetByHash(ctx, "../tenant", "hash")
			return err
		}},
		{name: "GetByHash empty hash", run: func() error {
			_, err := m.GetByHash(ctx, "tenant-1", " ")
			return err
		}},
		{name: "List invalid tenant", run: func() error {
			_, _, err := m.List(ctx, Query{TenantID: "../tenant"})
			return err
		}},
		{name: "Delete empty id", run: func() error { return m.Delete(ctx, "tenant-1", " ") }},
		{name: "UpdateAccessTime empty id", run: func() error {
			return m.UpdateAccessTime(ctx, "tenant-1", " ")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, storefile.ErrInvalidPath) {
				t.Fatalf("error = %v, want ErrInvalidPath", err)
			}
		})
	}
	if len(rec.queries) != 0 || len(rec.execs) != 0 {
		t.Fatalf("invalid inputs reached database: queries=%d execs=%d", len(rec.queries), len(rec.execs))
	}
}

func TestDBMetadataManagerListScopesByTenant(t *testing.T) {
	rec := &metadataExecRecorder{}
	db := sql.OpenDB(metadataConnector{rec: rec})
	defer db.Close()

	m, err := NewDBMetadataManager(db)
	if err != nil {
		t.Fatalf("NewDBMetadataManager error = %v", err)
	}

	_, _, _ = m.List(t.Context(), Query{TenantID: " tenant-1 "})
	if len(rec.queries) == 0 {
		t.Fatal("expected list query to be recorded")
	}
	if !strings.Contains(rec.queries[0], "tenant_id = $1") {
		t.Fatalf("query = %q, want tenant filter", rec.queries[0])
	}
	if got := rec.qargs[0][0].Value; got != "tenant-1" {
		t.Fatalf("tenant arg = %v, want tenant-1", got)
	}
}

func TestDBMetadataManagerListAllAllowsAdminGlobalQuery(t *testing.T) {
	rec := &metadataExecRecorder{}
	db := sql.OpenDB(metadataConnector{rec: rec})
	defer db.Close()

	m, err := NewDBMetadataManager(db)
	if err != nil {
		t.Fatalf("NewDBMetadataManager error = %v", err)
	}

	_, _, _ = m.ListAll(t.Context(), Query{})
	if len(rec.queries) == 0 {
		t.Fatal("expected list query to be recorded")
	}
	if strings.Contains(rec.queries[0], "tenant_id") {
		t.Fatalf("query = %q, want admin global query without tenant filter", rec.queries[0])
	}
}

func TestScanMetadataFileUnmarshalsMetadata(t *testing.T) {
	now := time.Date(2026, 4, 25, 13, 0, 0, 0, time.UTC)
	file, err := scanMetadataFile(func(dest ...any) error {
		values := []any{
			"f-1", "tenant-1", "avatar.png", "tenant-1/avatar.png", int64(12),
			"image/png", ".png", "hash", 10, 20,
			"tenant-1/thumb.png", "local", []byte(`{"kind":"avatar"}`),
			"user-1", now, now, (*time.Time)(nil), (*time.Time)(nil),
		}
		for i := range values {
			switch d := dest[i].(type) {
			case *string:
				*d = values[i].(string)
			case *int64:
				*d = values[i].(int64)
			case *int:
				*d = values[i].(int)
			case *[]byte:
				*d = values[i].([]byte)
			case *time.Time:
				*d = values[i].(time.Time)
			case **time.Time:
				*d = values[i].(*time.Time)
			default:
				t.Fatalf("unexpected destination type %T", d)
			}
		}
		return nil
	}, "test row")
	if err != nil {
		t.Fatalf("scanMetadataFile() error = %v", err)
	}
	if file.Metadata["kind"] != "avatar" {
		t.Fatalf("metadata kind = %v, want avatar", file.Metadata["kind"])
	}
}

type metadataExecRecorder struct {
	execs   []string
	args    [][]driver.NamedValue
	queries []string
	qargs   [][]driver.NamedValue
}

type metadataConnector struct {
	rec *metadataExecRecorder
}

func (c metadataConnector) Connect(context.Context) (driver.Conn, error) {
	return metadataConn(c), nil
}

func (c metadataConnector) Driver() driver.Driver {
	return metadataDriver{}
}

type metadataDriver struct{}

func (metadataDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("not used")
}

type metadataConn struct {
	rec *metadataExecRecorder
}

func (c metadataConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (c metadataConn) Close() error {
	return nil
}

func (c metadataConn) Begin() (driver.Tx, error) {
	return nil, errors.New("not implemented")
}

func (c metadataConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	copied := append([]driver.NamedValue(nil), args...)
	c.rec.execs = append(c.rec.execs, query)
	c.rec.args = append(c.rec.args, copied)
	return metadataResult(1), nil
}

func (c metadataConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	copied := append([]driver.NamedValue(nil), args...)
	c.rec.queries = append(c.rec.queries, query)
	c.rec.qargs = append(c.rec.qargs, copied)
	return metadataRows{}, nil
}

type metadataResult int64

func (r metadataResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r metadataResult) RowsAffected() (int64, error) {
	return int64(r), nil
}

type metadataRows struct{}

func (metadataRows) Columns() []string {
	return nil
}

func (metadataRows) Close() error {
	return nil
}

func (metadataRows) Next([]driver.Value) error {
	return io.EOF
}
