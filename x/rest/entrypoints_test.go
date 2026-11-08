package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
)

// --- RegisterResourceRoutes invalid args and edge cases ---

func TestRegisterResourceRoutes_InvalidArgsReturnErrors(t *testing.T) {
	tests := []struct {
		name       string
		router     *router.Router
		controller ResourceController
		want       error
	}{
		{
			name:       "nil router",
			router:     nil,
			controller: NewBaseResourceController("items"),
			want:       errRegisterNilRouter,
		},
		{
			name:       "nil controller",
			router:     router.NewRouter(),
			controller: nil,
			want:       errRegisterNilController,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RegisterResourceRoutes(tt.router, "/items", tt.controller, DefaultRouteOptions())
			if !errors.Is(err, tt.want) {
				t.Fatalf("RegisterResourceRoutes error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRegisterResourceRoutes_EmptyPrefix_NormalizesToSlash(t *testing.T) {
	r := router.NewRouter()
	if err := RegisterResourceRoutes(r, "", NewBaseResourceController("things"), RouteOptions{}); err != nil {
		t.Fatalf("empty prefix: %v", err)
	}

	assertRouteSet(t, r.Routes(), []routeKey{
		{"DELETE", "/:id"},
		{"GET", "/"},
		{"GET", "/:id"},
		{"PATCH", "/:id"},
		{"POST", "/"},
		{"PUT", "/:id"},
	})
}

func TestDefaultRouteOptions_EnablesAll(t *testing.T) {
	opts := DefaultRouteOptions()
	if !opts.EnableBatch {
		t.Error("expected EnableBatch = true")
	}
	if !opts.EnableHead {
		t.Error("expected EnableHead = true")
	}
	if !opts.EnableOptions {
		t.Error("expected EnableOptions = true")
	}
}

// --- BaseResourceController negative-path coverage ---

func TestBaseResourceController_AllMethodsReturn501(t *testing.T) {
	c := NewBaseResourceController("orders")
	methods := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
	}{
		{"Index", c.Index},
		{"Show", c.Show},
		{"Create", c.Create},
		{"Update", c.Update},
		{"Delete", c.Delete},
		{"Patch", c.Patch},
		{"BatchCreate", c.BatchCreate},
		{"BatchDelete", c.BatchDelete},
	}

	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/orders", nil)
			m.fn(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Errorf("%s: status = %d, want 501", m.name, rec.Code)
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
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("%s: decode: %v", m.name, err)
			}
			if resp.Error.Code != contract.CodeNotImplemented {
				t.Errorf("%s: code = %q, want %q", m.name, resp.Error.Code, contract.CodeNotImplemented)
			}
		})
	}
}

func TestBaseResourceController_Options_SetsHeaders(t *testing.T) {
	c := NewBaseResourceController("items")
	rec := httptest.NewRecorder()
	c.Options(rec, httptest.NewRequest(http.MethodOptions, "/items", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("Options: status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("Options: missing Access-Control-Allow-Methods header")
	}
}

func TestBaseResourceController_Head_Returns200(t *testing.T) {
	c := NewBaseResourceController("items")
	rec := httptest.NewRecorder()
	c.Head(rec, httptest.NewRequest(http.MethodHead, "/items", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("Head: status = %d, want 200", rec.Code)
	}
}

func TestBaseResourceController_NilReceiver_ResourceName(t *testing.T) {
	// resourceName() must not panic on nil or empty ResourceName.
	c := &BaseResourceController{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c.Index(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("empty ResourceName: status = %d, want 501", rec.Code)
	}
}

// --- NewPaginationMeta boundary cases ---

func TestNewPaginationMeta_FirstPage(t *testing.T) {
	meta := NewPaginationMeta(1, 10, 25)
	if meta.TotalPages != 3 {
		t.Errorf("TotalPages = %d, want 3", meta.TotalPages)
	}
	if meta.HasPrev {
		t.Error("HasPrev should be false on page 1")
	}
	if !meta.HasNext {
		t.Error("HasNext should be true on page 1 of 3")
	}
}

func TestNewPaginationMeta_LastPage(t *testing.T) {
	meta := NewPaginationMeta(3, 10, 25)
	if meta.HasNext {
		t.Error("HasNext should be false on last page")
	}
	if !meta.HasPrev {
		t.Error("HasPrev should be true on page 3")
	}
}

func TestNewPaginationMeta_ZeroItems(t *testing.T) {
	meta := NewPaginationMeta(1, 10, 0)
	if meta.TotalPages != 0 {
		t.Errorf("TotalPages = %d, want 0", meta.TotalPages)
	}
	if meta.HasNext || meta.HasPrev {
		t.Error("no navigation expected for zero items")
	}
}

// --- QueryBuilder negative-path cases ---

func TestQueryBuilder_PageSizeExceedsMax_Clamped(t *testing.T) {
	qb := NewQueryBuilder().WithPageSize(20, 50)
	req := httptest.NewRequest(http.MethodGet, "/?page_size=9999", nil)
	params := qb.Parse(req)
	if params.PageSize > 50 {
		t.Errorf("PageSize = %d, want ≤ 50", params.PageSize)
	}
}

func TestQueryBuilder_InvalidPage_UsesDefault(t *testing.T) {
	qb := NewQueryBuilder()
	req := httptest.NewRequest(http.MethodGet, "/?page=abc", nil)
	params := qb.Parse(req)
	if params.Page != 1 {
		t.Errorf("Page = %d, want 1 for invalid input", params.Page)
	}
}

func TestQueryBuilder_UnknownSortField_Ignored(t *testing.T) {
	qb := NewQueryBuilder().WithAllowedSorts("name", "created_at")
	req := httptest.NewRequest(http.MethodGet, "/?sort=evil,name", nil)
	params := qb.Parse(req)
	for _, sf := range params.Sort {
		if sf.Field == "evil" {
			t.Error("disallowed sort field 'evil' was not filtered out")
		}
	}
}

func TestQueryBuilder_UnknownFilterField_Ignored(t *testing.T) {
	qb := NewQueryBuilder().WithAllowedFilters("status")
	req := httptest.NewRequest(http.MethodGet, "/?status=active&evil=injected", nil)
	params := qb.Parse(req)
	if _, ok := params.Filters["evil"]; ok {
		t.Error("disallowed filter 'evil' was not filtered out")
	}
	if params.Filters["status"] != "active" {
		t.Errorf("Filters[status] = %q, want active", params.Filters["status"])
	}
}

func TestQueryBuilder_NegativePageZero_UsesDefault(t *testing.T) {
	qb := NewQueryBuilder()
	req := httptest.NewRequest(http.MethodGet, "/?page=0", nil)
	params := qb.Parse(req)
	if params.Page != 1 {
		t.Errorf("Page = %d, want 1 for page=0", params.Page)
	}
}

func TestQueryBuilder_DescendingSort(t *testing.T) {
	qb := NewQueryBuilder().WithAllowedSorts("name", "created_at")
	req := httptest.NewRequest(http.MethodGet, "/?sort=-created_at,name", nil)
	params := qb.Parse(req)
	if len(params.Sort) != 2 {
		t.Fatalf("Sort len = %d, want 2", len(params.Sort))
	}
	if !params.Sort[0].Desc || params.Sort[0].Field != "created_at" {
		t.Errorf("first sort = {%v %v}, want {created_at desc}", params.Sort[0].Field, params.Sort[0].Desc)
	}
}
