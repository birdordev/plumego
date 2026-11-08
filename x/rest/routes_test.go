package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
)

func TestRegisterResourceRoutesUsesCanonicalRouteSurface(t *testing.T) {
	r := router.NewRouter()

	if err := RegisterResourceRoutes(r, "/users/", NewBaseResourceController("users"), DefaultRouteOptions()); err != nil {
		t.Fatalf("RegisterResourceRoutes: %v", err)
	}

	assertRouteSet(t, r.Routes(), []routeKey{
		{"DELETE", "/users/:id"},
		{"DELETE", "/users/batch"},
		{"GET", "/users"},
		{"GET", "/users/:id"},
		{"HEAD", "/users"},
		{"HEAD", "/users/:id"},
		{"OPTIONS", "/users"},
		{"OPTIONS", "/users/:id"},
		{"PATCH", "/users/:id"},
		{"POST", "/users"},
		{"POST", "/users/batch"},
		{"PUT", "/users/:id"},
	})
}

func TestRegisterResourceRoutesRespectsRouteOptions(t *testing.T) {
	r := router.NewRouter()

	if err := RegisterResourceRoutes(r, "users", NewBaseResourceController("users"), RouteOptions{}); err != nil {
		t.Fatalf("RegisterResourceRoutes: %v", err)
	}

	assertRouteSet(t, r.Routes(), []routeKey{
		{"DELETE", "/users/:id"},
		{"GET", "/users"},
		{"GET", "/users/:id"},
		{"PATCH", "/users/:id"},
		{"POST", "/users"},
		{"PUT", "/users/:id"},
	})
}

func TestRegisterResourceRoutesWorksWithBaseContextResourceController(t *testing.T) {
	r := router.NewRouter()

	if err := RegisterResourceRoutes(r, "users/", NewBaseContextResourceController("users"), DefaultRouteOptions()); err != nil {
		t.Fatalf("RegisterResourceRoutes: %v", err)
	}

	assertRouteSet(t, r.Routes(), []routeKey{
		{"DELETE", "/users/:id"},
		{"DELETE", "/users/batch"},
		{"GET", "/users"},
		{"GET", "/users/:id"},
		{"HEAD", "/users"},
		{"HEAD", "/users/:id"},
		{"OPTIONS", "/users"},
		{"OPTIONS", "/users/:id"},
		{"PATCH", "/users/:id"},
		{"POST", "/users"},
		{"POST", "/users/batch"},
		{"PUT", "/users/:id"},
	})
}

func TestBaseResourceControllerUsesContractNotImplementedError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users", nil)

	NewBaseResourceController("users").Index(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
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
		t.Fatalf("decode response: %v", err)
	}

	if resp.Error.Code != contract.CodeNotImplemented {
		t.Fatalf("error code = %q, want %q", resp.Error.Code, contract.CodeNotImplemented)
	}
	if resp.Error.Type != contract.TypeNotImplemented {
		t.Fatalf("error type = %q, want %q", resp.Error.Type, contract.TypeNotImplemented)
	}
	if resp.Error.Category != contract.CategoryServer {
		t.Fatalf("error category = %q, want %q", resp.Error.Category, contract.CategoryServer)
	}
	if resp.Error.Message != "the Index method is not implemented for the users resource" {
		t.Fatalf("error message = %q", resp.Error.Message)
	}
	if got := resp.Error.Details["method"]; got != "Index" {
		t.Fatalf("method detail = %v, want %q", got, "Index")
	}
	if got := resp.Error.Details["resource"]; got != "users" {
		t.Fatalf("resource detail = %v, want %q", got, "users")
	}
}

type routeKey struct {
	method string
	path   string
}

func assertRouteSet(t *testing.T, routes []router.RouteInfo, want []routeKey) {
	t.Helper()

	if len(routes) != len(want) {
		t.Fatalf("expected %d routes, got %d: %#v", len(want), len(routes), routes)
	}

	for i, route := range routes {
		if route.Method != want[i].method || route.Path != want[i].path {
			t.Fatalf("route %d: expected %s %s, got %s %s", i, want[i].method, want[i].path, route.Method, route.Path)
		}
	}
}
