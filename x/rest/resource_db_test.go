package rest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
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
