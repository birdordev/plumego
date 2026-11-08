package rest

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
)

// ================================================
// Resource registration
// ================================================

func TestDefaultResourceSpec_SetsCanonicalNameAndPrefix(t *testing.T) {
	spec := DefaultResourceSpec("articles")
	if spec.Name != "articles" {
		t.Fatalf("Name = %q, want %q", spec.Name, "articles")
	}
	if spec.Prefix != "/articles" {
		t.Fatalf("Prefix = %q, want %q", spec.Prefix, "/articles")
	}
	if spec.Options == nil {
		t.Fatal("Options should be initialized")
	}
}

func TestDefaultResourceSpec_WithPrefix_OverridesPrefix(t *testing.T) {
	spec := DefaultResourceSpec("users").WithPrefix("/api/v1/users")
	if spec.Prefix != "/api/v1/users" {
		t.Fatalf("Prefix = %q, want %q", spec.Prefix, "/api/v1/users")
	}
	if spec.Name != "users" {
		t.Fatalf("Name should be unchanged, got %q", spec.Name)
	}
}

func TestRegisterDBResource_RegistersRoutesAndReturnsController(t *testing.T) {
	r := router.NewRouter()
	spec := DefaultResourceSpec("items")
	repo := dbResourceTestRepo{}

	controller := RegisterDBResource[dbResourceTestItem](r, spec, repo)
	if controller == nil {
		t.Fatal("RegisterDBResource returned nil controller")
	}
	if controller.ResourceName != "items" {
		t.Fatalf("ResourceName = %q, want %q", controller.ResourceName, "items")
	}

	routes := r.Routes()
	if len(routes) == 0 {
		t.Fatal("no routes were registered")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /items: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// ================================================
// Nil resource behavior
// ================================================

func TestApplyResourceSpec_NilController_IsNoOp(t *testing.T) {
	// must not panic
	ApplyResourceSpec(nil, DefaultResourceSpec("items"))
}

func TestParseQueryParams_NilReceiver_ReturnsSafeDefault(t *testing.T) {
	var c *BaseContextResourceController
	req := httptest.NewRequest(http.MethodGet, "/?page=3", nil)
	params := c.ParseQueryParams(req)
	if params == nil {
		t.Fatal("ParseQueryParams on nil receiver returned nil")
	}
}

func TestDBResourceController_ApplySpec_NilReceiver_IsNoOp(t *testing.T) {
	var c *DBResourceController[dbResourceTestItem]
	result := c.ApplySpec(DefaultResourceSpec("items"))
	if result != nil {
		t.Fatal("ApplySpec on nil DBResourceController should return nil")
	}
}

func TestNewDBResource_NilSpec_NormalizedSafely(t *testing.T) {
	repo := dbResourceTestRepo{}
	controller := NewDBResource[dbResourceTestItem](ResourceSpec{}, repo)
	if controller == nil {
		t.Fatal("NewDBResource returned nil")
	}
	if controller.ResourceName == "" {
		t.Fatal("ResourceName should be normalized to a non-empty default")
	}
}

// ================================================
// Pagination metadata boundary cases
// ================================================

func TestNewPaginationMeta_ZeroPageSize_DoesNotPanic(t *testing.T) {
	meta := NewPaginationMeta(1, 0, 100)
	if meta == nil {
		t.Fatal("NewPaginationMeta with pageSize=0 returned nil")
	}
	// TotalPages must be a sane non-negative value
	if meta.TotalPages < 0 {
		t.Fatalf("TotalPages = %d, want ≥ 0", meta.TotalPages)
	}
}

func TestNewPaginationMeta_NegativePageSize_DoesNotPanic(t *testing.T) {
	meta := NewPaginationMeta(1, -5, 50)
	if meta == nil {
		t.Fatal("NewPaginationMeta with pageSize=-5 returned nil")
	}
}

func TestNewPaginationMeta_SinglePageExact_NoNavigation(t *testing.T) {
	meta := NewPaginationMeta(1, 10, 10)
	if meta.TotalPages != 1 {
		t.Fatalf("TotalPages = %d, want 1", meta.TotalPages)
	}
	if meta.HasNext {
		t.Error("HasNext should be false when on the only page")
	}
	if meta.HasPrev {
		t.Error("HasPrev should be false when on the only page")
	}
}

func TestNewPaginationMeta_ExactDivisor_NoExtraPage(t *testing.T) {
	meta := NewPaginationMeta(2, 5, 10)
	if meta.TotalPages != 2 {
		t.Fatalf("TotalPages = %d, want 2 (10 items / 5 per page)", meta.TotalPages)
	}
	if meta.HasNext {
		t.Error("HasNext should be false on the last page")
	}
}

// ================================================
// QueryBuilder input rejection
// ================================================

func TestQueryBuilder_NegativePageSize_UsesDefault(t *testing.T) {
	qb := NewQueryBuilder().WithPageSize(20, 50)
	req := httptest.NewRequest(http.MethodGet, "/?page_size=-10", nil)
	params := qb.Parse(req)
	if params.PageSize != 20 {
		t.Fatalf("PageSize = %d, want 20 (default) for negative input", params.PageSize)
	}
}

func TestQueryBuilder_ZeroPageSize_UsesDefault(t *testing.T) {
	qb := NewQueryBuilder().WithPageSize(20, 50)
	req := httptest.NewRequest(http.MethodGet, "/?page_size=0", nil)
	params := qb.Parse(req)
	if params.PageSize != 20 {
		t.Fatalf("PageSize = %d, want 20 (default) for zero input", params.PageSize)
	}
}

func TestQueryBuilder_BareDashSort_IsIgnored(t *testing.T) {
	qb := NewQueryBuilder()
	req := httptest.NewRequest(http.MethodGet, "/?sort=-", nil)
	params := qb.Parse(req)
	for _, sf := range params.Sort {
		if sf.Field == "" {
			t.Error("bare dash sort '-' must not produce an empty-field SortField")
		}
	}
}

func TestQueryBuilder_BareDashSortWithAllowedSorts_IsIgnored(t *testing.T) {
	qb := NewQueryBuilder().WithAllowedSorts("name", "created_at")
	req := httptest.NewRequest(http.MethodGet, "/?sort=-,name", nil)
	params := qb.Parse(req)
	for _, sf := range params.Sort {
		if sf.Field == "" {
			t.Error("bare dash sort '-' must not produce an empty-field SortField even with allowedSorts configured")
		}
	}
	if len(params.Sort) != 1 || params.Sort[0].Field != "name" {
		t.Fatalf("Sort = %v, want [{name false}]", params.Sort)
	}
}

func TestQueryBuilder_SearchQParamFallback(t *testing.T) {
	qb := NewQueryBuilder()
	req := httptest.NewRequest(http.MethodGet, "/?q=hello+world", nil)
	params := qb.Parse(req)
	if params.Search != "hello world" {
		t.Fatalf("Search = %q, want %q via q= fallback", params.Search, "hello world")
	}
}

func TestQueryBuilder_SearchParamTakesPrecedenceOverQ(t *testing.T) {
	qb := NewQueryBuilder()
	req := httptest.NewRequest(http.MethodGet, "/?search=primary&q=fallback", nil)
	params := qb.Parse(req)
	if params.Search != "primary" {
		t.Fatalf("Search = %q, want %q (search= should take precedence over q=)", params.Search, "primary")
	}
}

func TestQueryBuilder_FieldsAndIncludeParsing(t *testing.T) {
	qb := NewQueryBuilder()
	req := httptest.NewRequest(http.MethodGet, "/?fields=id,name,email&include=author,tags", nil)
	params := qb.Parse(req)

	if len(params.Fields) != 3 {
		t.Fatalf("Fields len = %d, want 3; got %v", len(params.Fields), params.Fields)
	}
	if params.Fields[0] != "id" || params.Fields[1] != "name" || params.Fields[2] != "email" {
		t.Fatalf("Fields = %v, want [id name email]", params.Fields)
	}
	if len(params.Include) != 2 {
		t.Fatalf("Include len = %d, want 2; got %v", len(params.Include), params.Include)
	}
	if params.Include[0] != "author" || params.Include[1] != "tags" {
		t.Fatalf("Include = %v, want [author tags]", params.Include)
	}
}

func TestQueryBuilder_ExplicitOffset_IsRespected(t *testing.T) {
	qb := NewQueryBuilder().WithPageSize(10, 100)
	req := httptest.NewRequest(http.MethodGet, "/?offset=30", nil)
	params := qb.Parse(req)
	if params.Offset != 30 {
		t.Fatalf("Offset = %d, want 30 for explicit offset=30", params.Offset)
	}
}

// ================================================
// Canonical contract error path
// ================================================

func TestDBResourceController_Show_EmptyID_ReturnsRequiredError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	rec := httptest.NewRecorder()
	// no id in request context → GetID returns ""
	req := httptest.NewRequest(http.MethodGet, "/items/", nil)
	controller.Show(rec, req)
	assertDBResourceError(t, rec, http.StatusBadRequest, contract.CodeRequired, "id is required")
}

func TestDBResourceController_Show_RepositoryError_ReturnsInternalError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{
		findByIDErr: errors.New("connection lost"),
	})
	rec := httptest.NewRecorder()
	req := dbResourceRequestWithID(http.MethodGet, "/items/42", nil, "42")
	controller.Show(rec, req)
	assertDBResourceError(t, rec, http.StatusInternalServerError, contract.CodeInternalError, "failed to fetch record")
	assertBodyOmits(t, rec.Body.String(), "connection lost")
}

func TestDBResourceController_Create_InvalidJSON_ReturnsValidationError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", bytes.NewBufferString(`{bad json`))
	controller.Create(rec, req)
	assertDBResourceError(t, rec, http.StatusBadRequest, contract.CodeInvalidRequest, "invalid request body")
}

func TestDBResourceController_Create_RepositoryError_ReturnsInternalError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{
		createErr: errors.New("db insert failed"),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", bytes.NewBufferString(`{"name":"x"}`))
	controller.Create(rec, req)
	assertDBResourceError(t, rec, http.StatusInternalServerError, contract.CodeInternalError, "failed to create record")
	assertBodyOmits(t, rec.Body.String(), "db insert failed")
}

func TestDBResourceController_Update_EmptyID_ReturnsRequiredError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/items/", bytes.NewBufferString(`{"name":"x"}`))
	controller.Update(rec, req)
	assertDBResourceError(t, rec, http.StatusBadRequest, contract.CodeRequired, "id is required")
}

func TestDBResourceController_Update_NotFound_ReturnsNotFound(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{
		updateErr: sql.ErrNoRows,
	})
	rec := httptest.NewRecorder()
	req := dbResourceRequestWithID(http.MethodPut, "/items/99", bytes.NewBufferString(`{"name":"x"}`), "99")
	controller.Update(rec, req)
	assertDBResourceError(t, rec, http.StatusNotFound, contract.CodeResourceNotFound, "record not found")
}

func TestDBResourceController_Delete_EmptyID_ReturnsRequiredError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/items/", nil)
	controller.Delete(rec, req)
	assertDBResourceError(t, rec, http.StatusBadRequest, contract.CodeRequired, "id is required")
}

func TestDBResourceController_Delete_NotFound_ReturnsNotFound(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{
		deleteErr: sql.ErrNoRows,
	})
	rec := httptest.NewRecorder()
	req := dbResourceRequestWithID(http.MethodDelete, "/items/99", nil, "99")
	controller.Delete(rec, req)
	assertDBResourceError(t, rec, http.StatusNotFound, contract.CodeResourceNotFound, "record not found")
}

func TestDBResourceController_Delete_Succeeds_Returns204(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	rec := httptest.NewRecorder()
	req := dbResourceRequestWithID(http.MethodDelete, "/items/1", nil, "1")
	controller.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}
}

// ================================================
// Hook: BeforeCreate / BeforeUpdate / BeforeDelete rejection
// ================================================

type productionTrustTestHooks struct {
	NoOpResourceHooks
	beforeCreateErr error
	beforeUpdateErr error
	beforeDeleteErr error
}

func (h *productionTrustTestHooks) BeforeCreate(_ context.Context, _ any) error {
	return h.beforeCreateErr
}

func (h *productionTrustTestHooks) BeforeUpdate(_ context.Context, _ string, _ any) error {
	return h.beforeUpdateErr
}

func (h *productionTrustTestHooks) BeforeDelete(_ context.Context, _ string) error {
	return h.beforeDeleteErr
}

func TestDBResourceController_BeforeCreate_RejectionUsesClientError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	controller.Hooks = &productionTrustTestHooks{beforeCreateErr: errors.New("hook says no")}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", bytes.NewBufferString(`{"name":"x"}`))
	controller.Create(rec, req)
	if rec.Code >= http.StatusInternalServerError {
		t.Fatalf("BeforeCreate hook rejection should be a 4xx client error, got %d", rec.Code)
	}
	assertBodyOmits(t, rec.Body.String(), "hook says no")
}

func TestDBResourceController_BeforeUpdate_RejectionUsesClientError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	controller.Hooks = &productionTrustTestHooks{beforeUpdateErr: errors.New("hook says no")}
	rec := httptest.NewRecorder()
	req := dbResourceRequestWithID(http.MethodPut, "/items/1", bytes.NewBufferString(`{"name":"x"}`), "1")
	controller.Update(rec, req)
	if rec.Code >= http.StatusInternalServerError {
		t.Fatalf("BeforeUpdate hook rejection should be a 4xx client error, got %d", rec.Code)
	}
	assertBodyOmits(t, rec.Body.String(), "hook says no")
}

func TestDBResourceController_BeforeDelete_RejectionUsesClientError(t *testing.T) {
	controller := NewDBResourceController("items", dbResourceTestRepo{})
	controller.Hooks = &productionTrustTestHooks{beforeDeleteErr: errors.New("hook says no")}
	rec := httptest.NewRecorder()
	req := dbResourceRequestWithID(http.MethodDelete, "/items/1", nil, "1")
	controller.Delete(rec, req)
	if rec.Code >= http.StatusInternalServerError {
		t.Fatalf("BeforeDelete hook rejection should be a 4xx client error, got %d", rec.Code)
	}
	assertBodyOmits(t, rec.Body.String(), "hook says no")
}
