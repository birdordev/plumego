package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
)

func TestGroupRouteRegistration(t *testing.T) {
	app := newTestApp()
	g := app.Group("/api/v1")

	type entry struct {
		method string
		path   string
		status int
		called bool
	}
	entries := []*entry{
		{method: http.MethodGet, path: "/items", status: http.StatusOK},
		{method: http.MethodPost, path: "/items", status: http.StatusCreated},
		{method: http.MethodGet, path: "/items/:id", status: http.StatusOK},
		{method: http.MethodDelete, path: "/items/:id", status: http.StatusNoContent},
	}

	for _, e := range entries {
		e := e
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			e.called = true
			w.WriteHeader(e.status)
		})
		switch e.method {
		case http.MethodGet:
			mustRegisterRoute(t, g.Get(e.path, handler))
		case http.MethodPost:
			mustRegisterRoute(t, g.Post(e.path, handler))
		case http.MethodDelete:
			mustRegisterRoute(t, g.Delete(e.path, handler))
		}
	}

	probes := []struct {
		method string
		url    string
		entry  *entry
	}{
		{http.MethodGet, "/api/v1/items", entries[0]},
		{http.MethodPost, "/api/v1/items", entries[1]},
		{http.MethodGet, "/api/v1/items/item-1", entries[2]},
		{http.MethodDelete, "/api/v1/items/item-1", entries[3]},
	}

	for _, p := range probes {
		p.entry.called = false
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(p.method, p.url, nil))
		if !p.entry.called {
			t.Errorf("%s %s: handler was not called", p.method, p.url)
		}
		if rec.Code != p.entry.status {
			t.Errorf("%s %s: status = %d, want %d", p.method, p.url, rec.Code, p.entry.status)
		}
	}
}

func TestNestedGroup(t *testing.T) {
	app := newTestApp()
	api := app.Group("/api")
	v1 := api.Group("/v1")

	called := false
	mustRegisterRoute(t, v1.Get("/hello", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/hello", nil))
	if !called {
		t.Error("nested group handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestGroupNilHandlerReturnsError(t *testing.T) {
	app := newTestApp()
	g := app.Group("/api/v1")
	if err := g.Get("/nil", nil); err == nil {
		t.Fatal("expected nil handler to return error")
	}
}

func TestGroupRegistrationFailsAfterPrepare(t *testing.T) {
	app := newTestApp()
	mustRegisterRoute(t, app.Get("/base", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	g := app.Group("/api/v1")
	err := g.Get("/items", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if err == nil {
		t.Fatal("expected group registration after Prepare to fail")
	}
	if !strings.Contains(err.Error(), "cannot register route after app has been prepared") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestGroupAddRouteWithNamedRoute(t *testing.T) {
	app := newTestApp()
	g := app.Group("/api/v1")
	mustRegisterRoute(t, g.AddRoute(http.MethodGet, "/items/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), router.WithRouteName("items.show")))

	if got, err := app.URL("items.show", "id", "42"); err != nil || got != "/api/v1/items/42" {
		t.Fatalf("URL = %q, err = %v, want /api/v1/items/42", got, err)
	}
}

func TestGetPostPutDeletePatch(t *testing.T) {
	tests := []struct {
		method   string
		path     string
		register func(*App, string, http.HandlerFunc) error
	}{
		{"GET", "/get", func(app *App, path string, h http.HandlerFunc) error { return app.Get(path, h) }},
		{"POST", "/post", func(app *App, path string, h http.HandlerFunc) error { return app.Post(path, h) }},
		{"PUT", "/put", func(app *App, path string, h http.HandlerFunc) error { return app.Put(path, h) }},
		{"DELETE", "/delete", func(app *App, path string, h http.HandlerFunc) error { return app.Delete(path, h) }},
		{"PATCH", "/patch", func(app *App, path string, h http.HandlerFunc) error { return app.Patch(path, h) }},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			app := newTestApp()
			called := false
			mustRegisterRoute(t, tt.register(app, tt.path, func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Method != tt.method {
					t.Errorf("expected method %s, got %s", tt.method, r.Method)
				}
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rr := httptest.NewRecorder()
			app.ServeHTTP(rr, req)

			if !called {
				t.Error("handler was not called")
			}
			if rr.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", rr.Code)
			}
		})
	}
}

func TestAny(t *testing.T) {
	app := newTestApp()

	called := false
	mustRegisterRoute(t, app.Any("/any/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), router.WithRouteName("any.show")))

	if got, err := app.URL("any.show", "id", "42"); err != nil || got != "/any/42" {
		t.Fatalf("named ANY route URL = %q, err = %v, want /any/42", got, err)
	}

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	for _, method := range methods {
		called = false
		req := httptest.NewRequest(method, "/any/42", nil)
		rr := httptest.NewRecorder()
		app.ServeHTTP(rr, req)

		if !called {
			t.Errorf("handler was not called for method %s", method)
		}
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200 for %s, got %d", method, rr.Code)
		}
	}
}

func TestRouterMethodNotAllowedConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Router.MethodNotAllowed = true
	app := New(cfg, AppDependencies{})
	mustRegisterRoute(t, app.Get("/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/only", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	if rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("expected Allow header to include GET and HEAD")
	}
}

func TestNewConfiguresOwnedRouterPolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Router.MethodNotAllowed = true
	app := New(cfg, AppDependencies{})
	mustRegisterRoute(t, app.Get("/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	if !app.router.MethodNotAllowedEnabled() {
		t.Fatal("expected owned router to have method-not-allowed enabled")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/only", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	if rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("expected Allow header to include GET and HEAD")
	}
}

func TestRouterReadPathsDoNotResyncPolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Router.MethodNotAllowed = true
	app := New(cfg, AppDependencies{})

	mustRegisterRoute(t, app.Get("/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	if !app.router.MethodNotAllowedEnabled() {
		t.Fatal("expected owned router to have method-not-allowed enabled from constructor ownership")
	}

	app.config.Router.MethodNotAllowed = false
	_ = app.Routes()
	_, _ = app.URL("missing")

	if !app.router.MethodNotAllowedEnabled() {
		t.Fatal("expected read paths to not re-sync router policy")
	}
}

func TestNamedRouteRegistration(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		route  string
	}{
		{
			name:   "get",
			method: http.MethodGet,
			path:   "/named/get/:id",
			route:  "named.get",
		},
		{
			name:   "post",
			method: http.MethodPost,
			path:   "/named/post/:id",
			route:  "named.post",
		},
		{
			name:   "put",
			method: http.MethodPut,
			path:   "/named/put/:id",
			route:  "named.put",
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			path:   "/named/delete/:id",
			route:  "named.delete",
		},
		{
			name:   "patch",
			method: http.MethodPatch,
			path:   "/named/patch/:id",
			route:  "named.patch",
		},
		{
			name:   "any",
			method: router.MethodAny,
			path:   "/named/any/:id",
			route:  "named.any",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp()
			called := false
			mustRegisterRoute(t, app.AddRoute(tt.method, tt.path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}), router.WithRouteName(tt.route)))

			url, urlErr := app.URL(tt.route, "id", "42")
			if urlErr != nil {
				t.Fatalf("URL() error for route %q: %v", tt.route, urlErr)
			}
			reqMethod := tt.method
			if tt.method == router.MethodAny {
				reqMethod = http.MethodGet
			}

			req := httptest.NewRequest(reqMethod, url, nil)
			rr := httptest.NewRecorder()
			app.ServeHTTP(rr, req)

			if !called {
				t.Fatalf("handler was not called for route %q", tt.route)
			}
			if rr.Code != http.StatusNoContent {
				t.Fatalf("expected status 204, got %d", rr.Code)
			}
		})
	}
}

func TestAddRouteReturnsRegistrationErrors(t *testing.T) {
	app := newTestApp()

	if err := app.AddRoute(http.MethodGet, "/strict", nil); err == nil {
		t.Fatalf("expected nil-handler registration error")
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if err := app.AddRoute(http.MethodGet, "/strict", handler); err != nil {
		t.Fatalf("unexpected add route error: %v", err)
	}

	if err := app.AddRoute(http.MethodGet, "/strict", handler); err == nil {
		t.Fatalf("expected duplicate route error")
	}

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if err := app.AddRoute(http.MethodGet, "/after-start", handler); err == nil {
		t.Fatalf("expected add route to fail after app started")
	}
}

func TestAddRouteWithRouteNameRegistersURLAndReturnsErrors(t *testing.T) {
	app := newTestApp()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	if err := app.AddRoute(http.MethodGet, "/users/:id", handler, router.WithRouteName("users.show")); err != nil {
		t.Fatalf("unexpected add named route error: %v", err)
	}

	if got, err := app.URL("users.show", "id", "42"); err != nil || got != "/users/42" {
		t.Fatalf("named route URL = %q, err = %v, want /users/42", got, err)
	}

	if err := app.AddRoute(http.MethodGet, "/users/:id", handler, router.WithRouteName("users.show")); err == nil {
		t.Fatalf("expected duplicate named route error")
	}
}

func TestMethodHelpersReturnRegistrationErrors(t *testing.T) {
	app := newTestApp()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if err := app.Get("/strict-get", handler); err != nil {
		t.Fatalf("unexpected get registration error: %v", err)
	}
	if err := app.Get("/strict-get", handler); err == nil {
		t.Fatalf("expected duplicate get registration error")
	}

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if err := app.Post("/after-start", handler); err == nil {
		t.Fatalf("expected post registration to fail after app started")
	}
}

func TestRouteRegistrationFailsAfterPrepare(t *testing.T) {
	app := newTestApp()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mustRegisterRoute(t, app.Get("/before-prepare", handler))
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	err := app.Get("/after-prepare", handler)
	if err == nil {
		t.Fatal("expected route registration after Prepare to fail")
	}
	if !strings.Contains(err.Error(), "cannot register route after app has been prepared") {
		t.Fatalf("registration error = %q", err.Error())
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/before-prepare", nil)
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected existing route to remain available, got status %d", rec.Code)
	}
}

func TestRouteRegistrationStatePrecedesNilHandlerValidation(t *testing.T) {
	app := newTestApp()
	mustRegisterRoute(t, app.Get("/before-prepare", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	err := app.Get("/after-prepare-nil", nil)
	if err == nil {
		t.Fatal("expected route registration after Prepare to fail")
	}
	if !strings.Contains(err.Error(), "cannot register route after app has been prepared") {
		t.Fatalf("expected prepared-state error before nil handler validation, got %v", err)
	}
	if errors.Is(err, contract.ErrHandlerNil) {
		t.Fatalf("expected nil handler not to be wrapped after app is prepared, got %v", err)
	}
}

func TestConcurrentRouteRegistrationAndPrepareDoesNotRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		app := newTestApp()
		mustRegisterRoute(t, app.Get("/base", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})))

		start := make(chan struct{})
		var wg sync.WaitGroup
		var routeErr, prepareErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			routeErr = app.Get("/raced", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			}))
		}()
		go func() {
			defer wg.Done()
			<-start
			prepareErr = app.Prepare()
		}()

		close(start)
		wg.Wait()

		if prepareErr != nil {
			t.Fatalf("Prepare returned error: %v", prepareErr)
		}
		if routeErr != nil && !strings.Contains(routeErr.Error(), "cannot register route after app has been prepared") {
			t.Fatalf("unexpected route error: %v", routeErr)
		}

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/base", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected base status 204, got %d", rec.Code)
		}
	}
}
