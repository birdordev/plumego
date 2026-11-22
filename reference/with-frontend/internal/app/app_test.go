package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-frontend/internal/config"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("API_PREFIX", "/api")
	t.Setenv("UI_PREFIX", "/")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	return a
}

func TestRegisterRoutesIncludesAPIAndFrontend(t *testing.T) {
	a := newTestApp(t)

	routes := a.Core.Routes()
	if len(routes) == 0 {
		t.Fatal("expected at least one route")
	}

	hasAPI := false
	for _, r := range routes {
		if r.Path == "/api/*apiPath" {
			hasAPI = true
		}
	}
	if !hasAPI {
		t.Errorf("API namespace mount not found; routes = %v", routes)
	}
}

func TestAPIStatusEndpoint(t *testing.T) {
	a := newTestApp(t)
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestFrontendIndexServed(t *testing.T) {
	a := newTestApp(t)
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body == "" {
		t.Error("expected non-empty HTML response for /")
	}
}

func TestAPINamespaceKeepsErrorsSeparateFromSPAFallback(t *testing.T) {
	a := newTestApp(t)
	for _, tc := range []struct {
		method, path, contentType, allow string
		status                           int
	}{
		{http.MethodGet, "/api", "application/json", "", 404},
		{http.MethodGet, "/api/unknown", "application/json", "", 404},
		{http.MethodHead, "/api/unknown", "application/json", "", 404},
		{http.MethodPost, "/api/status", "application/json", "GET, HEAD", 405},
		{http.MethodGet, "/apiary/page", "text/html", "", 200},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		a.Core.ServeHTTP(w, r)
		if w.Code != tc.status || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) ||
			w.Header().Get("Allow") != tc.allow || (tc.method == http.MethodHead && w.Body.Len() != 0) {
			t.Errorf("%s %s: status=%d headers=%v body=%q", tc.method, tc.path, w.Code, w.Header(), w.Body)
		}
	}
}
