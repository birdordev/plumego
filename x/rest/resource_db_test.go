package rest

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
)

type dbResourceTestItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type dbResourceTestRepo struct {
	findAllErr  error
	findByIDErr error
	createErr   error
	updateErr   error
	deleteErr   error
}

func (r dbResourceTestRepo) FindAll(context.Context, *QueryParams) ([]dbResourceTestItem, int64, error) {
	if r.findAllErr != nil {
		return nil, 0, r.findAllErr
	}
	return []dbResourceTestItem{{ID: "1", Name: "one"}}, 1, nil
}

func (r dbResourceTestRepo) FindByID(context.Context, string) (*dbResourceTestItem, error) {
	if r.findByIDErr != nil {
		return nil, r.findByIDErr
	}
	return &dbResourceTestItem{ID: "1", Name: "one"}, nil
}

func (r dbResourceTestRepo) Create(context.Context, *dbResourceTestItem) error {
	return r.createErr
}

func (r dbResourceTestRepo) Update(context.Context, string, *dbResourceTestItem) error {
	return r.updateErr
}

func (r dbResourceTestRepo) Delete(context.Context, string) error {
	return r.deleteErr
}

func (r dbResourceTestRepo) Count(context.Context, *QueryParams) (int64, error) {
	return 1, nil
}

func (r dbResourceTestRepo) Exists(context.Context, string) (bool, error) {
	return true, nil
}

type dbResourceTestHooks struct {
	NoOpResourceHooks
	beforeListErr error
	afterListErr  error
}

func (h dbResourceTestHooks) BeforeList(context.Context, *QueryParams) error {
	return h.beforeListErr
}

func (h dbResourceTestHooks) AfterList(context.Context, *QueryParams, any) error {
	return h.afterListErr
}

type dbResourceTestValidator struct {
	err error
}

func (v dbResourceTestValidator) Validate(any) error {
	return v.err
}

func TestDBResourceControllerBeforeListErrorUsesSafeCode(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	controller.Hooks = &dbResourceTestHooks{beforeListErr: errors.New("secret before-list failure")}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)

	controller.Index(rec, req)

	assertDBResourceError(t, rec, http.StatusBadRequest, contract.CodeInvalidRequest, "list hook rejected request")
	assertBodyOmits(t, rec.Body.String(), "secret before-list failure")
}

func TestDBResourceControllerAfterListErrorUsesSafeCode(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	controller.Hooks = &dbResourceTestHooks{afterListErr: errors.New("secret after-list failure")}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)

	controller.Index(rec, req)

	assertDBResourceError(t, rec, http.StatusInternalServerError, contract.CodeInternalError, "post-list hook failed")
	assertBodyOmits(t, rec.Body.String(), "secret after-list failure")
}

func TestDBResourceControllerRepositoryErrorUsesStableMessage(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{
		findAllErr: errors.New("database secret"),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)

	controller.Index(rec, req)

	assertDBResourceError(t, rec, http.StatusInternalServerError, contract.CodeInternalError, "failed to fetch records")
	assertBodyOmits(t, rec.Body.String(), "database secret")
}

func TestDBResourceControllerValidationErrorUsesStableCode(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	controller.WithValidator(dbResourceTestValidator{err: errors.New("secret validation detail")})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", bytes.NewBufferString(`{"name":"one"}`))

	controller.Create(rec, req)

	assertDBResourceError(t, rec, http.StatusUnprocessableEntity, contract.CodeValidationError, "validation failed")
	assertBodyOmits(t, rec.Body.String(), "secret validation detail")
}

func TestDBResourceControllerNotFoundUsesStableCode(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{
		findByIDErr: sql.ErrNoRows,
	})

	rec := httptest.NewRecorder()
	req := dbResourceRequestWithID(http.MethodGet, "/items/1", nil, "1")

	controller.Show(rec, req)

	assertDBResourceError(t, rec, http.StatusNotFound, contract.CodeResourceNotFound, "record not found")
}

func dbResourceRequestWithID(method, target string, body *bytes.Buffer, id string) *http.Request {
	var reader *bytes.Buffer
	if body == nil {
		reader = bytes.NewBuffer(nil)
	} else {
		reader = body
	}
	req := httptest.NewRequest(method, target, reader)
	ctx := contract.WithRequestContext(req.Context(), contract.RequestContext{
		Params: map[string]string{"id": id},
	})
	return req.WithContext(ctx)
}

func assertDBResourceError(t *testing.T, rec *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v; body: %s", err, rec.Body.String())
	}
	if resp.Error.Code != code {
		t.Fatalf("code = %q, want %q; body: %s", resp.Error.Code, code, rec.Body.String())
	}
	if resp.Error.Message != message {
		t.Fatalf("message = %q, want %q; body: %s", resp.Error.Message, message, rec.Body.String())
	}
}

func assertBodyOmits(t *testing.T, body, value string) {
	t.Helper()
	if strings.Contains(body, value) {
		t.Fatalf("response body leaked %q: %s", value, body)
	}
}

func TestDBResourceControllerIndex_CanonicalContractShape(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)

	controller.Index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// Canonical contract shape: {"data": [...], "meta": {"pagination": {...}}}
	// Must NOT be the old double-nested {"data": {"data": [...], "pagination": {...}}}
	var resp struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Meta struct {
			Pagination *PaginationMeta `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v; body: %s", err, rec.Body.String())
	}
	if len(resp.Data) != 1 {
		t.Fatalf("data len = %d, want 1; body: %s", len(resp.Data), rec.Body.String())
	}
	if resp.Meta.Pagination == nil {
		t.Fatalf("meta.pagination missing; body: %s", rec.Body.String())
	}
	if resp.Meta.Pagination.TotalItems != 1 {
		t.Fatalf("pagination.total_items = %d, want 1", resp.Meta.Pagination.TotalItems)
	}
}

func TestSQLBuilderSearchConditions(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		columns              []string
		filters              map[string]string
		selectColumns, where string
		args                 []any
	}{
		{name: "no columns", selectColumns: "*"},
		{name: "filters without search columns", filters: map[string]string{"state": "open"}, selectColumns: "*", where: " WHERE state = ?", args: []any{"open"}},
		{name: "search columns", columns: []string{"id", "name"}, selectColumns: "id, name", where: " WHERE (id LIKE ? OR name LIKE ?)", args: []any{"%sample%", "%sample%"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder := NewSQLBuilder("items", "id").WithColumns(tc.columns...)
			params := &QueryParams{Search: "sample", Filters: tc.filters}
			selectSQL, selectArgs := builder.BuildSelectQuery(params)
			countSQL, countArgs := builder.BuildCountQuery(params)
			if selectSQL != "SELECT "+tc.selectColumns+" FROM items"+tc.where || !slices.Equal(selectArgs, tc.args) {
				t.Errorf("select = %q, args = %v", selectSQL, selectArgs)
			}
			if countSQL != "SELECT COUNT(*) FROM items"+tc.where || !slices.Equal(countArgs, tc.args) {
				t.Errorf("count = %q, args = %v", countSQL, countArgs)
			}
		})
	}
}

func TestRepositoryScanRowRejectsNilPointer(t *testing.T) {
	var nilItem *dbResourceTestItem
	value := dbResourceTestItem{ID: "item"}
	for _, input := range []any{nilItem, value, &value} {
		builder := NewSQLBuilder("items", "id").WithScanFunc(func(*sql.Rows) (any, error) { return input, nil })
		repo := NewBaseRepository[dbResourceTestItem](nil, builder)
		actual, err := repo.scanRow(nil)
		if input == nilItem {
			if err == nil || actual != nil {
				t.Fatalf("nil scan returned %v, %v", actual, err)
			}
		} else if err != nil || actual == nil || *actual != value {
			t.Fatalf("valid scan returned %v, %v", actual, err)
		}
	}
}

func TestSQLBuilderOwnsColumns(t *testing.T) {
	columns := []string{"id", "title"}
	b := NewSQLBuilder("items", "id").WithColumns(columns...)
	columns[1] = "description"
	params := &QueryParams{Search: "test"}
	query, _ := b.BuildSelectQuery(params)
	if query != "SELECT id, title FROM items WHERE (id LIKE ? OR title LIKE ?)" {
		t.Errorf("select = %q", query)
	}
	query, _ = b.BuildCountQuery(params)
	if query != "SELECT COUNT(*) FROM items WHERE (id LIKE ? OR title LIKE ?)" {
		t.Errorf("count = %q", query)
	}
	query, _ = b.WithColumns(columns...).BuildSelectQuery(params)
	if query != "SELECT id, description FROM items WHERE (id LIKE ? OR description LIKE ?)" {
		t.Errorf("updated query = %q", query)
	}
}

type repositoryListConnector struct {
	driver.Connector
	rows    *repositoryListRows
	observe func(string, []driver.NamedValue)
}

func (c repositoryListConnector) Connect(context.Context) (driver.Conn, error) {
	return repositoryListConn{rows: c.rows, observe: c.observe}, nil
}

type repositoryListConn struct {
	driver.Conn
	rows    *repositoryListRows
	observe func(string, []driver.NamedValue)
}

func (repositoryListConn) Close() error { return nil }

func (c repositoryListConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.observe != nil {
		c.observe(query, args)
	}
	if strings.HasPrefix(query, "SELECT COUNT(*)") {
		return &repositoryListRows{columns: []string{"count"}, value: int64(4)}, nil
	}
	return c.rows, nil
}

type repositoryListRows struct {
	columns    []string
	value      driver.Value
	endErr     error
	read       bool
	closeCalls int
}

func (r *repositoryListRows) Columns() []string { return r.columns }

func (r *repositoryListRows) Next(dest []driver.Value) error {
	if r.read {
		if r.endErr != nil {
			return r.endErr
		}
		return io.EOF
	}
	r.read = true
	dest[0] = r.value
	return nil
}

func (r *repositoryListRows) Close() error {
	r.closeCalls++
	return nil
}

func TestRepositoryFindAllPropagatesIterationFailure(t *testing.T) {
	sentinel := errors.New("test iteration failure")
	for _, tt := range []struct {
		name   string
		endErr error
	}{{"success", nil}, {"fails after a row", sentinel}} {
		t.Run(tt.name, func(t *testing.T) {
			rows := &repositoryListRows{columns: []string{"id"}, value: "item-1", endErr: tt.endErr}
			db := sql.OpenDB(repositoryListConnector{rows: rows})
			t.Cleanup(func() { _ = db.Close() })
			builder := NewSQLBuilder("items", "id").WithColumns("id").WithScanFunc(func(rows *sql.Rows) (any, error) {
				var id string
				err := rows.Scan(&id)
				return id, err
			})
			repo := NewBaseRepository[string](db, builder)
			items, count, err := repo.FindAll(t.Context(), &QueryParams{})
			if tt.endErr != nil {
				if items != nil || count != 0 || !errors.Is(err, sentinel) {
					t.Errorf("FindAll = %v, %d, %v; want iteration failure without partial values", items, count, err)
				}
			} else if !slices.Equal(items, []string{"item-1"}) || count != 4 || err != nil {
				t.Errorf("FindAll = %v, %d, %v; want one item and total count 4", items, count, err)
			}
			if !rows.read || rows.closeCalls != 1 {
				t.Errorf("cursor read = %t, close calls = %d", rows.read, rows.closeCalls)
			}
		})
	}
}

func TestSQLBuilderNilQueryParams(t *testing.T) {
	builder := NewSQLBuilder("items", "id").WithColumns("id").WithScanFunc(func(rows *sql.Rows) (any, error) {
		var id string
		err := rows.Scan(&id)
		return id, err
	})
	for _, build := range []func(*QueryParams) (string, []any){builder.BuildSelectQuery, builder.BuildCountQuery} {
		got, gotArgs := build(nil)
		want, wantArgs := build(&QueryParams{})
		if got != want || len(gotArgs) != 0 || len(wantArgs) != 0 {
			t.Errorf("nil query = %q %v; empty query = %q %v", got, gotArgs, want, wantArgs)
		}
	}
	query, args := builder.BuildSelectQuery(&QueryParams{Filters: map[string]string{"id": "item"}, Limit: 2})
	if !strings.Contains(query, "WHERE id = ?") || !strings.Contains(query, "LIMIT ?") || len(args) != 2 {
		t.Errorf("nonempty query = %q %v", query, args)
	}
	rows := &repositoryListRows{columns: []string{"id"}, value: "item"}
	db := sql.OpenDB(repositoryListConnector{rows: rows})
	t.Cleanup(func() { _ = db.Close() })
	repo := NewBaseRepository[string](db, builder)
	items, count, err := repo.FindAll(t.Context(), nil)
	if err != nil || count != 4 || !slices.Equal(items, []string{"item"}) {
		t.Errorf("FindAll with nil params = %v, %d, %v", items, count, err)
	}
}

type repositoryExecConnector struct {
	driver.Connector
	result  driver.Result
	err     error
	observe func(string, []driver.NamedValue)
}

func (c repositoryExecConnector) Connect(context.Context) (driver.Conn, error) {
	return repositoryExecConn(c), nil
}

type repositoryExecConn repositoryExecConnector

func (repositoryExecConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (repositoryExecConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (repositoryExecConn) Close() error                        { return nil }
func (c repositoryExecConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.observe != nil {
		c.observe(query, args)
	}
	return c.result, c.err
}

type repositoryRowsAffectedError struct {
	driver.Result
	err error
}

func (r repositoryRowsAffectedError) RowsAffected() (int64, error) { return 0, r.err }

func TestRepositoryUpdateClassifiesRowsAffected(t *testing.T) {
	sentinel := errors.New("test rows affected failure")
	for _, tt := range []struct {
		name    string
		result  driver.Result
		wantErr error
	}{
		{"one row", driver.RowsAffected(1), nil},
		{"zero rows", driver.RowsAffected(0), sql.ErrNoRows},
		{"driver error", repositoryRowsAffectedError{err: sentinel}, sentinel},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := sql.OpenDB(repositoryExecConnector{result: tt.result})
			t.Cleanup(func() { _ = db.Close() })
			b := NewSQLBuilder("items", "id").WithColumns("id", "name").
				WithUpdateFunc(func(data any) ([]any, error) { return []any{data.(*dbResourceTestItem).Name}, nil })
			repo := NewBaseRepository[dbResourceTestItem](db, b)
			if err := repo.Update(t.Context(), "item", &dbResourceTestItem{Name: "updated"}); !errors.Is(err, tt.wantErr) {
				t.Errorf("Update = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSQLBuilderUsesStableFilterOrdering(t *testing.T) {
	builder := NewSQLBuilder("items", "id").WithColumns("name")
	for i := range 20 {
		filters := make(map[string]string)
		if i%2 == 0 {
			filters["category"] = "books"
			filters["active"] = "yes"
		} else {
			filters["active"] = "yes"
			filters["category"] = "books"
		}
		params := &QueryParams{Search: "Go", Filters: filters, Limit: 5, Offset: 10}
		selectSQL, selectArgs := builder.BuildSelectQuery(params)
		countSQL, countArgs := builder.BuildCountQuery(params)
		const where = " WHERE (name LIKE ?) AND active = ? AND category = ?"
		if selectSQL != "SELECT name FROM items"+where+" LIMIT ? OFFSET ?" ||
			!slices.Equal(selectArgs, []any{"%Go%", "yes", "books", 5, 10}) {
			t.Fatalf("select = %s, args=%v", selectSQL, selectArgs)
		}
		if countSQL != "SELECT COUNT(*) FROM items"+where || !slices.Equal(countArgs, []any{"%Go%", "yes", "books"}) {
			t.Fatalf("count = %s, args=%v", countSQL, countArgs)
		}
	}
}

func TestRepositoryDeleteClassifiesRowsAffected(t *testing.T) {
	execErr := errors.New("test delete failed")
	rowsErr := errors.New("test rows affected failure")
	for _, tc := range []struct {
		name    string
		result  driver.Result
		execErr error
		want    error
	}{
		{"exec failure", nil, execErr, execErr},
		{"rows affected failure", repositoryRowsAffectedError{err: rowsErr}, nil, rowsErr},
		{"zero rows", driver.RowsAffected(0), nil, sql.ErrNoRows},
		{"one row", driver.RowsAffected(1), nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := sql.OpenDB(repositoryExecConnector{result: tc.result, err: tc.execErr})
			t.Cleanup(func() { _ = db.Close() })
			repo := NewBaseRepository[dbResourceTestItem](db, NewSQLBuilder("items", "id"))
			if err := repo.Delete(t.Context(), "item"); !errors.Is(err, tc.want) {
				t.Errorf("Delete = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRepositoryFindByIDClassifiesRowFailure(t *testing.T) {
	sentinel := errors.New("read first row")
	for _, tc := range []struct {
		name            string
		fail, errorWant error
	}{
		{"empty", nil, sql.ErrNoRows},
		{"driver failure", sentinel, sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := &repositoryListRows{columns: []string{"id"}, read: true, endErr: tc.fail}
			db := sql.OpenDB(repositoryListConnector{rows: rows})
			defer db.Close()
			repo := NewBaseRepository[string](db, NewSQLBuilder("items", "id").WithColumns("id"))
			got, err := repo.FindByID(t.Context(), "item-1")
			if got != nil || !errors.Is(err, tc.errorWant) || rows.closeCalls != 1 {
				t.Fatalf("FindByID = %v, %v; row close calls %d", got, err, rows.closeCalls)
			}
		})
	}
}

func TestRepositoryCreateBindsValuesAndPreservesErrors(t *testing.T) {
	extractErr := errors.New("test value extraction failed")
	execErr := errors.New("test INSERT failed")
	for _, tc := range []struct {
		name                         string
		extractErr, execErr, wantErr error
		wantCalls                    int
	}{
		{"success", nil, nil, nil, 1},
		{"extract failure", extractErr, nil, extractErr, 0},
		{"driver failure", nil, execErr, execErr, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := &dbResourceTestItem{ID: "new-id", Name: "new name"}
			calls := 0
			db := sql.OpenDB(repositoryExecConnector{
				result: driver.RowsAffected(1),
				err:    tc.execErr,
				observe: func(query string, args []driver.NamedValue) {
					calls++
					wantArgs := []driver.NamedValue{{Ordinal: 1, Value: item.ID}, {Ordinal: 2, Value: item.Name}}
					if query != "INSERT INTO items (id, name) VALUES (?, ?)" || !slices.Equal(args, wantArgs) {
						t.Errorf("execution = %q, %v", query, args)
					}
				},
			})
			t.Cleanup(func() { _ = db.Close() })
			builder := NewSQLBuilder("items", "id").WithColumns("id", "name").
				WithInsertFunc(func(data any) ([]any, error) {
					got := data.(*dbResourceTestItem)
					return []any{got.ID, got.Name}, tc.extractErr
				})
			repo := NewBaseRepository[dbResourceTestItem](db, builder)
			if err := repo.Create(t.Context(), item); !errors.Is(err, tc.wantErr) || calls != tc.wantCalls {
				t.Errorf("create error = %v, exec calls = %d; want %v, %d", err, calls, tc.wantErr, tc.wantCalls)
			}
		})
	}
}

func TestRepositoryExistsBindsIDAndPreservesErrors(t *testing.T) {
	sentinel := errors.New("first row failed")
	for _, tc := range []struct {
		name  string
		value bool
		err   error
	}{
		{"found", true, nil}, {"missing", false, nil}, {"scan failure", false, sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := &repositoryListRows{columns: []string{"exists"}, value: tc.value, read: tc.err != nil, endErr: tc.err}
			calls := 0
			db := sql.OpenDB(repositoryListConnector{rows: rows, observe: func(query string, args []driver.NamedValue) {
				calls++
				if query != "SELECT EXISTS(SELECT 1 FROM items WHERE id = ?)" || !slices.Equal(args, []driver.NamedValue{{Ordinal: 1, Value: "requested"}}) {
					t.Errorf("query = %q, args = %v", query, args)
				}
			}})
			t.Cleanup(func() { _ = db.Close() })
			repo := NewBaseRepository[string](db, NewSQLBuilder("items", "id"))
			if got, err := repo.Exists(t.Context(), "requested"); got != tc.value || !errors.Is(err, tc.err) || calls != 1 {
				t.Errorf("Exists = %t, %v, calls=%d", got, err, calls)
			}
		})
	}
}

func TestParamExtractorPreservesExplicitQueryValues(t *testing.T) {
	var p ParamExtractor
	for _, tc := range []struct {
		query   string
		count   int
		enabled bool
	}{
		{"", 9, true},
		{"?count=bad&enabled=bad", 9, true},
		{"?count=0&enabled=false", 0, false},
		{"?count=2&enabled=true", 2, true},
	} {
		r := httptest.NewRequest(http.MethodGet, "/"+tc.query, nil)
		if n, b := p.GetQueryInt(r, "count", 9), p.GetQueryBool(r, "enabled", true); n != tc.count || b != tc.enabled {
			t.Errorf("query %q yielded %d, %t", tc.query, n, b)
		}
	}
}

func TestParamExtractorUsesRouteContext(t *testing.T) {
	r := router.NewRouter()
	p := &ParamExtractor{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := p.GetParam(r, "id"); got != "route-id" {
			t.Errorf("path parameter=%q", got)
		}
		if got := p.GetParam(r, "missing"); got != "" {
			t.Errorf("missing parameter=%q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := r.AddRoute(http.MethodGet, "/items/:id", handler); err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	r.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/items/route-id?id=query-id", nil))
	if out.Code != http.StatusNoContent {
		t.Errorf("response=%d", out.Code)
	}
}

func TestParamExtractorReadsOnlyQueryValues(t *testing.T) {
	r := router.NewRouter()
	p := &ParamExtractor{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := p.GetParam(req, "id"); got != "route" {
			t.Errorf("path id = %q", got)
		}
		if got := p.GetQueryParam(req, "id"); got != "query value" {
			t.Errorf("query id = %q", got)
		}
		if got := p.GetQueryParam(req, "missing"); got != "" {
			t.Errorf("missing query = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := r.AddRoute(http.MethodGet, "/items/:id", handler); err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	r.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/items/route?id=query%20value&id=second", nil))
	if out.Code != http.StatusNoContent {
		t.Errorf("response status = %d", out.Code)
	}
}

func TestRepositoryUpdatePreservesPreparationAndExecErrors(t *testing.T) {
	prepareErr := errors.New("prepare values failed")
	execErr := errors.New("database update failed")
	for _, tc := range []struct {
		name             string
		prepareErr, want error
		writes           int
	}{
		{"prepare failure", prepareErr, prepareErr, 0},
		{"exec failure", nil, execErr, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			db := sql.OpenDB(repositoryExecConnector{err: execErr, observe: func(query string, args []driver.NamedValue) {
				writes++
				want := []driver.NamedValue{{Ordinal: 1, Value: "updated"}, {Ordinal: 2, Value: "item"}}
				if !strings.Contains(query, "WHERE id = ?") || !slices.Equal(args, want) {
					t.Errorf("update=%q %v", query, args)
				}
			}})
			defer db.Close()
			builder := NewSQLBuilder("items", "id").WithColumns("id", "name").WithUpdateFunc(func(data any) ([]any, error) {
				return []any{data.(*dbResourceTestItem).Name}, tc.prepareErr
			})
			repo := NewBaseRepository[dbResourceTestItem](db, builder)
			if err := repo.Update(t.Context(), "item", &dbResourceTestItem{Name: "updated"}); !errors.Is(err, tc.want) || writes != tc.writes {
				t.Errorf("Update=%v, writes=%d", err, writes)
			}
		})
	}
}

func TestIdentityTransformerPreservesSingleResource(t *testing.T) {
	identity := new(IdentityTransformer)
	resource := &dbResourceTestItem{ID: "1", Name: "one"}
	for _, input := range []any{resource, nil} {
		if got, err := identity.Transform(t.Context(), input); got != input || err != nil {
			t.Errorf("Transform=%v, %v", got, err)
		}
	}
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	out := httptest.NewRecorder()
	controller.Show(out, dbResourceRequestWithID(http.MethodGet, "/items/1", nil, "1"))
	var env struct {
		Data dbResourceTestItem `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &env); err != nil || out.Code != http.StatusOK || env.Data != *resource {
		t.Errorf("Show status=%d response=%q, error=%v", out.Code, out.Body.String(), err)
	}
}
