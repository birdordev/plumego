package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-rest/internal/config"
)

func TestUserListPaginatesRepositoryResults(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"name":"Grace"}`))
	create.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Core.ServeHTTP(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", w.Code, w.Body)
	}

	w = httptest.NewRecorder()
	a.Core.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/users?offset=1&limit=1", nil))
	var page struct {
		Data []struct{ Name string }
		Meta struct {
			Limit      int
			Offset     int
			Pagination struct {
				TotalItems int `json:"total_items"`
			}
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != http.StatusOK ||
		len(page.Data) != 1 || page.Data[0].Name != "Grace" || page.Meta.Pagination.TotalItems != 2 ||
		page.Meta.Offset != 1 || page.Meta.Limit != 1 {
		t.Fatalf("list: status = %d, body = %s, error = %v", w.Code, w.Body, err)
	}
}

func TestRegisterRoutesCanonicalShape(t *testing.T) {
	cfg := config.Defaults()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}

	routes := a.Core.Routes()
	want := []struct {
		Method string
		Path   string
	}{
		{http.MethodDelete, "/api/users/:id"},
		{http.MethodGet, "/api/hello"},
		{http.MethodGet, "/api/users"},
		{http.MethodGet, "/api/users/:id"},
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/readyz"},
		{http.MethodPost, "/api/items"},
		{http.MethodPost, "/api/users"},
		{http.MethodPut, "/api/users/:id"},
	}

	if len(routes) != len(want) {
		t.Fatalf("routes count = %d, want %d; routes = %v", len(routes), len(want), routes)
	}

	index := make(map[string]bool, len(routes))
	for _, r := range routes {
		index[r.Method+":"+r.Path] = true
	}
	for _, w := range want {
		if !index[w.Method+":"+w.Path] {
			t.Errorf("route %s %s not found", w.Method, w.Path)
		}
	}
}
