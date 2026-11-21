package router

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/internal/testutil/wait"
)

func TestWithTagsSnapshotsReusableOption(t *testing.T) {
	tags := []string{"reports"}
	option := WithTags(tags...)
	tags[0] = "changed"
	r := NewRouter()
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, path := range []string{"/one", "/two"} {
		if err := r.AddRoute(http.MethodGet, path, handler, option); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range r.Routes() {
		if got := route.Meta.Tags; len(got) != 1 || got[0] != "reports" {
			t.Errorf("route %s tags=%q, want [reports]", route.Path, got)
		}
	}
}

func TestMethodMismatchReturnsNotFound(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	rec := serveRouter(r, http.MethodPost, "/only")
	assertResponseStatus(t, rec, http.StatusNotFound)
	assertResponseBody(t, rec, "404 page not found\n")
	assertResponseHeader(t, rec, "Allow", "")
}

func TestRouteMissUsesStdlibNotFoundContract(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := serveRouter(r, http.MethodGet, "/missing")
	assertResponseStatus(t, rec, http.StatusNotFound)
	assertResponseBody(t, rec, "404 page not found\n")
	if contentType := rec.Header().Get(contract.HeaderContentType); strings.Contains(contentType, "application/json") {
		t.Fatalf("404 content type = %q, want non-JSON stdlib response", contentType)
	}
}

func TestMethodNotAllowedUsesStructuredContractError(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	mustAddRoute(r, http.MethodGet, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := serveRouter(r, http.MethodPost, "/only")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	assertResponseHeader(t, rec, "Allow", "GET, HEAD")
	assertResponseHeader(t, rec, contract.HeaderContentType, contract.ContentTypeJSON)

	var body struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode method-not-allowed body: %v", err)
	}
	if body.Error.Type != contract.TypeMethodNotAllowed {
		t.Fatalf("error type = %q, want %q", body.Error.Type, contract.TypeMethodNotAllowed)
	}
	if body.Error.Code != contract.CodeMethodNotAllowed {
		t.Fatalf("error code = %q, want %q", body.Error.Code, contract.CodeMethodNotAllowed)
	}
	if body.Error.Message != "method not allowed" {
		t.Fatalf("error message = %q, want %q", body.Error.Message, "method not allowed")
	}
}

func TestMethodNotAllowedUsesCompleteRouteMatches(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("route handler called for an unmatched method")
	})
	mustAddRoute(r, http.MethodGet, "/", handler)
	mustAddRoute(r, http.MethodGet, "/items/fixed/other", handler)
	mustAddRoute(r, http.MethodGet, "/items/:id/details", handler)
	mustAddRoute(r, http.MethodPut, "/items/*rest", handler)
	r.Freeze()

	for _, tt := range []struct {
		path   string
		status int
		allow  string
	}{
		{"/", http.StatusMethodNotAllowed, "GET, HEAD"},
		{"/items/fixed/details", http.StatusMethodNotAllowed, "GET, HEAD, PUT"},
		{"/items/other/nested/item", http.StatusMethodNotAllowed, "PUT"},
		{"/missing", http.StatusNotFound, ""},
	} {
		t.Run(tt.path, func(t *testing.T) {
			rec := serveRouter(r, http.MethodPost, tt.path)
			assertResponseStatus(t, rec, tt.status)
			assertResponseHeader(t, rec, "Allow", tt.allow)
		})
	}
}

func TestServeHTTPUninitializedOrNilRequestUsesStdlibUnavailable(t *testing.T) {
	cases := []struct {
		name string
		r    *Router
		req  *http.Request
	}{
		{
			name: "nil router",
			r:    nil,
			req:  httptest.NewRequest(http.MethodGet, "/users", nil),
		},
		{
			name: "zero router",
			r:    &Router{},
			req:  httptest.NewRequest(http.MethodGet, "/users", nil),
		},
		{
			name: "nil request",
			r:    NewRouter(),
			req:  nil,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.r.ServeHTTP(rec, tt.req)

			assertResponseStatus(t, rec, http.StatusServiceUnavailable)
			assertResponseBody(t, rec, "router not initialized\n")
			if contentType := rec.Header().Get(contract.HeaderContentType); strings.Contains(contentType, "application/json") {
				t.Fatalf("service-unavailable content type = %q, want non-JSON stdlib response", contentType)
			}
		})
	}
}

func TestAddRouteRejectsMalformedParamAndWildcardPatterns(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{name: "empty param name", pattern: "/users/:"},
		{name: "param starts with digit", pattern: "/users/:1id"},
		{name: "param contains punctuation", pattern: "/users/:id-name"},
		{name: "param contains colon", pattern: "/users/:id:name"},
		{name: "empty wildcard name", pattern: "/files/*"},
		{name: "wildcard starts with digit", pattern: "/files/*1path"},
		{name: "wildcard contains punctuation", pattern: "/files/*file.path"},
		{name: "non-terminal wildcard", pattern: "/files/*path/edit"},
		{name: "empty path segment", pattern: "/files//readme"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRouter()
			err := r.AddRoute(http.MethodGet, tt.pattern, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			if err == nil {
				t.Fatalf("expected AddRoute(%q) to fail", tt.pattern)
			}
		})
	}
}

func TestAddRouteRejectsMalformedMethods(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{name: "empty", method: ""},
		{name: "leading space", method: " GET"},
		{name: "trailing space", method: "GET "},
		{name: "embedded space", method: "GE T"},
		{name: "newline", method: "GET\n"},
		{name: "slash separator", method: "GET/POST"},
		{name: "comma separator", method: "GET,POST"},
		{name: "colon separator", method: "GET:POST"},
		{name: "paren separator", method: "GET(POST)"},
		{name: "at separator", method: "GET@POST"},
		{name: "del control", method: "GET\x7f"},
		{name: "non ascii", method: "GÉT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRouter()
			err := r.AddRoute(tt.method, "/users/:id", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			if err == nil {
				t.Fatalf("expected AddRoute method %q to fail", tt.method)
			}
		})
	}
}

func TestAddRouteAllowsCustomMethodAndIdentifierParams(t *testing.T) {
	r := NewRouter()
	err := r.AddRoute("MKCOL", "/files/:file_id/*restPath", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(Param(req, "file_id") + "|" + Param(req, "restPath")))
	}))
	if err != nil {
		t.Fatalf("add custom method route failed: %v", err)
	}

	rec := serveRouter(r, "MKCOL", "/files/root/a/b")
	assertResponseStatus(t, rec, http.StatusOK)
	assertResponseBody(t, rec, "root|a/b")
}

func TestAddRouteRejectsNilHandler(t *testing.T) {
	r := NewRouter()

	err := r.AddRoute(http.MethodGet, "/nil", nil)
	if !errors.Is(err, contract.ErrHandlerNil) {
		t.Fatalf("expected nil handler registration to fail")
	}
	if !strings.Contains(err.Error(), "nil handler") {
		t.Fatalf("expected nil handler error, got %v", err)
	}
}

func TestAddRouteRejectsNilHandlerFuncWithoutPublishing(t *testing.T) {
	r := NewRouter()
	const path, name = "/nil", "nil-retry"
	if err := r.AddRoute(http.MethodGet, path, http.HandlerFunc(nil), WithRouteName(name)); !errors.Is(err, contract.ErrHandlerNil) {
		t.Fatalf("nil HandlerFunc error = %v, want ErrHandlerNil", err)
	}
	if r.HasRoute(name) || len(r.Routes()) != 0 {
		t.Fatal("failed registration published a route")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if err := r.AddRoute(http.MethodGet, path, handler, WithRouteName(name)); err != nil {
		t.Fatal(err)
	}
	assertResponseStatus(t, serveRouter(r, http.MethodGet, path), http.StatusNoContent)
}

func TestAddRouteLifecyclePrecedesInputValidation(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	var nilRouter *Router
	err := nilRouter.AddRoute("", "/nil", nil)
	if err == nil || !strings.Contains(err.Error(), "router is not initialized") {
		t.Fatalf("nil router error = %v, want not initialized", err)
	}

	zeroRouter := &Router{}
	err = zeroRouter.AddRoute("", "/zero", nil)
	if err == nil || !strings.Contains(err.Error(), "router is not initialized") {
		t.Fatalf("zero router error = %v, want not initialized", err)
	}

	r := NewRouter()
	r.Freeze()
	err = r.AddRoute("", "/frozen", handler)
	if err == nil || !strings.Contains(err.Error(), "router is frozen") {
		t.Fatalf("frozen router error = %v, want frozen", err)
	}

	err = NewRouter().AddRoute("", "/bad-method", handler)
	if err == nil || !strings.Contains(err.Error(), "empty method") {
		t.Fatalf("ready router error = %v, want method validation", err)
	}
}

func TestNilAndZeroValueRouterPublicMethodsDoNotPanic(t *testing.T) {
	run := func(name string, fn func()) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("unexpected panic: %v", recovered)
				}
			}()
			fn()
		})
	}

	run("new_router_nil_options", func() {
		r := NewRouter(nil, WithMethodNotAllowed(true), nil)
		if r == nil {
			t.Fatalf("expected router")
		}
		if !r.MethodNotAllowedEnabled() {
			t.Fatalf("expected non-nil option to apply")
		}
	})

	for _, tt := range []struct {
		name string
		r    *Router
	}{
		{name: "nil", r: nil},
		{name: "zero", r: &Router{}},
	} {
		r := tt.r
		run(tt.name+"/add_route", func() {
			err := r.AddRoute(http.MethodGet, "/users", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			if err == nil {
				t.Fatalf("expected add route error")
			}
		})
		run(tt.name+"/group_add_route", func() {
			err := r.Group("/api").AddRoute(http.MethodGet, "/users", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			if err == nil {
				t.Fatalf("expected grouped add route error")
			}
		})
		run(tt.name+"/serve_http", func() {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users", nil))
			assertResponseStatus(t, rec, http.StatusServiceUnavailable)
		})
		run(tt.name+"/metadata", func() {
			r.Freeze()
			r.SetMethodNotAllowed(true)
			if r.MethodNotAllowedEnabled() {
				t.Fatalf("expected method-not-allowed to remain disabled")
			}
			if got, err := r.URL("missing"); err == nil || got != "" {
				t.Fatalf("expected error and empty URL for unknown route, got %q err %v", got, err)
			}
			if r.HasRoute("missing") {
				t.Fatalf("expected missing route")
			}
			if routes := r.Routes(); routes == nil || len(routes) != 0 {
				t.Fatalf("expected no routes, got %d", len(routes))
			}
			if named := r.NamedRoutes(); named == nil || len(named) != 0 {
				t.Fatalf("expected no named routes, got %d", len(named))
			}
			r.Print(nil)
			var out strings.Builder
			r.Print(&out)
			if !strings.Contains(out.String(), "Registered Routes:") {
				t.Fatalf("expected Print header, got %q", out.String())
			}
		})
	}

	run("ready_print_nil_writer", func() {
		r := NewRouter()
		mustAddRoute(r, http.MethodGet, "/users", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		r.Print(nil)
	})

	run("nil_param", func() {
		if got := Param(nil, "id"); got != "" {
			t.Fatalf("expected empty nil-request param, got %q", got)
		}
	})
}

func TestServeHTTPRejectsNilRequestURL(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	req.URL = nil
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertResponseStatus(t, rec, http.StatusBadRequest)
	assertResponseBody(t, rec, "bad request\n")
	if contentType := rec.Header().Get(contract.HeaderContentType); strings.Contains(contentType, "application/json") {
		t.Fatalf("bad-request content type = %q, want non-JSON stdlib response", contentType)
	}
}

func TestHeadEarlyErrorsPreserveStatusAndHeadersWithoutBody(t *testing.T) {
	tests := []struct {
		name    string
		router  *Router
		nilURL  bool
		status  int
		getBody string
	}{
		{"nil router", nil, false, http.StatusServiceUnavailable, "router not initialized\n"},
		{"zero router", &Router{}, false, http.StatusServiceUnavailable, "router not initialized\n"},
		{"nil URL", NewRouter(), true, http.StatusBadRequest, "bad request\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serve := func(method string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, "/users", nil)
				if tt.nilURL {
					req.URL = nil
				}
				rec := httptest.NewRecorder()
				tt.router.ServeHTTP(rec, req)
				assertResponseStatus(t, rec, tt.status)
				return rec
			}

			get := serve(http.MethodGet)
			assertResponseBody(t, get, tt.getBody)
			head := serve(http.MethodHead)
			assertResponseBody(t, head, "")
			assertResponseHeader(t, head, "Content-Length", strconv.Itoa(len(tt.getBody)))
			for _, header := range []string{contract.HeaderContentType, "X-Content-Type-Options"} {
				assertResponseHeader(t, head, header, get.Header().Get(header))
			}
		})
	}
}

func TestFreezeLocksMethodNotAllowedPolicy(t *testing.T) {
	r := NewRouter()
	if r.MethodNotAllowedEnabled() {
		t.Fatal("method-not-allowed should be disabled by default")
	}

	r.SetMethodNotAllowed(true)
	if !r.MethodNotAllowedEnabled() {
		t.Fatal("method-not-allowed should be enabled before freeze")
	}

	r.Freeze()
	r.SetMethodNotAllowed(false)
	if !r.MethodNotAllowedEnabled() {
		t.Fatal("method-not-allowed changed after freeze")
	}
}

func TestFreezePreventsEnablingMethodNotAllowedPolicy(t *testing.T) {
	r := NewRouter()
	r.Freeze()
	r.SetMethodNotAllowed(true)
	if r.MethodNotAllowedEnabled() {
		t.Fatal("method-not-allowed enabled after freeze")
	}
}

func TestFreezePreventsMethodNotAllowedOptionMutation(t *testing.T) {
	r := NewRouter()
	r.Freeze()

	WithMethodNotAllowed(true)(r)
	if r.MethodNotAllowedEnabled() {
		t.Fatal("method-not-allowed option changed policy after freeze")
	}
}

func TestCacheCapacityOptionDuringDispatch(t *testing.T) {
	r := NewRouter(WithCacheCapacity(2))
	mustAddRoute(r, http.MethodGet, "/ready", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	applyWhileServing := func() {
		var wg sync.WaitGroup
		wg.Go(func() {
			for range 100 {
				WithCacheCapacity(4)(r)
			}
		})
		wg.Go(func() {
			for range 100 {
				if status := serveRouter(r, http.MethodGet, "/ready").Code; status != http.StatusNoContent {
					t.Errorf("dispatch status = %d, want 204", status)
					return
				}
			}
		})
		wg.Wait()
	}
	applyWhileServing()
	cache := r.state.matchCache
	if cache.capacity != 4 {
		t.Fatalf("cache capacity = %d, want 4", cache.capacity)
	}
	r.Freeze()
	applyWhileServing()
	if r.state.matchCache != cache {
		t.Fatal("cache replaced after Freeze")
	}
}

func TestAddRouteRejectsRelativeRootPath(t *testing.T) {
	r := NewRouter()
	err := r.AddRoute(http.MethodGet, "users/:id", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(Param(req, "id")))
	}), WithRouteName("users.show"))
	if err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Fatalf("relative route error = %v, want must start with /", err)
	}
}

func TestAddRouteRejectsRelativeGroupPath(t *testing.T) {
	r := NewRouter()
	api := r.Group("/api")

	err := api.AddRoute(http.MethodGet, "users/:id", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(Param(req, "id")))
	}), WithRouteName("api.users.show"))
	if err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Fatalf("relative grouped route error = %v, want must start with /", err)
	}
}

func TestGroupRejectsRelativePrefix(t *testing.T) {
	r := NewRouter()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected relative group prefix to panic")
		}
		if !strings.Contains(recovered.(string), "must start with /") {
			t.Fatalf("panic = %v, want must start with /", recovered)
		}
	}()

	_ = r.Group("api")
}

func TestAddRouteCanonicalizesRepeatedLeadingSlashes(t *testing.T) {
	r := NewRouter()
	err := r.AddRoute(http.MethodGet, "///users/:id", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rc := contract.RequestContextFromContext(req.Context())
		if rc.RoutePattern != "/users/:id" {
			t.Fatalf("expected route pattern %q, got %q", "/users/:id", rc.RoutePattern)
		}
		_, _ = w.Write([]byte(Param(req, "id")))
	}), WithRouteName("users.show"))
	if err != nil {
		t.Fatalf("add route with repeated leading slash failed: %v", err)
	}

	rec := serveRouter(r, http.MethodGet, "/users/42")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "42")

	routes := r.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Path != "/users/:id" {
		t.Fatalf("stored route path = %q, want %q", routes[0].Path, "/users/:id")
	}
	if got, err := r.URL("users.show", "id", "42"); err != nil || got != "/users/42" {
		t.Fatalf("URL() = %q, err = %v, want /users/42", got, err)
	}
}

func TestGroupCanonicalizesRepeatedLeadingSlashes(t *testing.T) {
	r := NewRouter()
	api := r.Group("///api/")

	err := api.AddRoute(http.MethodGet, "///users/:id", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rc := contract.RequestContextFromContext(req.Context())
		if rc.RoutePattern != "/api/users/:id" {
			t.Fatalf("expected route pattern %q, got %q", "/api/users/:id", rc.RoutePattern)
		}
		_, _ = w.Write([]byte(Param(req, "id")))
	}), WithRouteName("api.users.show"))
	if err != nil {
		t.Fatalf("add grouped route with repeated leading slash failed: %v", err)
	}

	rec := serveRouter(r, http.MethodGet, "/api/users/42")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "42")

	routes := r.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Path != "/api/users/:id" {
		t.Fatalf("stored route path = %q, want %q", routes[0].Path, "/api/users/:id")
	}
	if got, err := r.URL("api.users.show", "id", "42"); err != nil || got != "/api/users/42" {
		t.Fatalf("URL() = %q, err = %v, want /api/users/42", got, err)
	}
}

func TestAddRouteClearsStaleExactCache(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/files/*path", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("wild:" + Param(req, "path")))
	}))

	rec := serveRouter(r, http.MethodGet, "/files/readme")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "wild:readme")

	mustAddRoute(r, http.MethodGet, "/files/readme", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("exact"))
	}))

	rec = serveRouter(r, http.MethodGet, "/files/readme")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "exact")
}

func TestRegistrationCannotInvalidateInFlightCacheFill(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/items/:id", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	served := make(chan int, 1)
	registered := make(chan error, 1)
	func() {
		// Block the cache lookup and confirm dispatch holds the route read lock.
		r.state.matchCache.mu.Lock()
		defer r.state.matchCache.mu.Unlock()
		go func() { served <- serveRouter(r, http.MethodGet, "/items/new").Code }()
		wait.ForCondition(t, 2*time.Second, "dispatch to hold the route read lock", func() bool {
			if !r.state.mu.TryLock() {
				return true
			}
			r.state.mu.Unlock()
			return false
		})
		go func() {
			registered <- r.AddRoute(http.MethodGet, "/items/new", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
		}()
	}()
	if status := <-served; status != http.StatusAccepted {
		t.Fatalf("in-flight status = %d, want 202", status)
	}
	if err := <-registered; err != nil {
		t.Fatalf("AddRoute: %v", err)
	}
	r.Freeze()
	for range 2 {
		assertResponseStatus(t, serveRouter(r, http.MethodGet, "/items/new"), http.StatusNoContent)
	}
}

func TestWarmCachePreservesTrieSpecificity(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/files/*path", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("wild:" + Param(req, "path")))
	}))
	mustAddRoute(r, http.MethodGet, "/files/public/:id", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("param:" + Param(req, "id")))
	}))
	mustAddRoute(r, http.MethodGet, "/files/public/readme", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("static"))
	}))

	rec := serveRouter(r, http.MethodGet, "/files/other")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "wild:other")

	rec = serveRouter(r, http.MethodGet, "/files/public/42")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "param:42")

	rec = serveRouter(r, http.MethodGet, "/files/public/readme")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "static")

	rec = serveRouter(r, http.MethodGet, "/files/public/42")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "param:42")

	rec = serveRouter(r, http.MethodGet, "/files/public/readme")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "static")
}

func TestDispatchBacktracksFromDeadEnds(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	for _, route := range []struct {
		method  string
		pattern string
		name    string
	}{
		{http.MethodGet, "/items/new/edit", "static"},
		{http.MethodGet, "/items/:id/edit", "param-edit"},
		{http.MethodGet, "/items/:id/detail", "param"},
		{http.MethodGet, "/items/*path", "wildcard"},
		{http.MethodPost, "/items/new/detail", "post"},
	} {
		mustAddRoute(r, route.method, route.pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			meta := contract.RequestContextFromContext(req.Context())
			_, _ = io.WriteString(w, strings.Join([]string{meta.RouteName, meta.RoutePattern, Param(req, "id"), Param(req, "path")}, "|"))
		}), WithRouteName(route.name))
	}
	r.Freeze()

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"complete static beats param and wildcard", "/items/new/edit", "static|/items/new/edit||"},
		{"static dead end falls back to param", "/items/new/detail", "param|/items/:id/detail|new|"},
		{"static and param dead ends fall back to wildcard", "/items/new/other", "wildcard|/items/*path||new/other"},
		{"param without terminal handler falls back to wildcard", "/items/42", "wildcard|/items/*path||42"},
		{"literal colon remains a parameter value", "/items/:/detail", "param|/items/:id/detail|:|"},
		{"literal star remains a wildcard value", "/items/*", "wildcard|/items/*path||*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 2 {
				rec := serveRouter(r, http.MethodGet, tc.path)
				assertResponseStatus(t, rec, http.StatusOK)
				assertResponseBody(t, rec, tc.want)
			}
		})
	}
}

func TestMethodNotAllowedIncludesBacktrackedRoute(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mustAddRoute(r, http.MethodGet, "/items/new/edit", handler)
	mustAddRoute(r, http.MethodGet, "/items/:id/detail", handler)
	mustAddRoute(r, http.MethodPost, "/items/new/detail", handler)
	r.Freeze()

	for range 2 {
		rec := serveRouter(r, http.MethodDelete, "/items/new/detail")
		assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
		assertResponseHeader(t, rec, "Allow", "GET, HEAD, POST")
		assertResponseStatus(t, serveRouter(r, http.MethodGet, "/items/new/detail"), http.StatusNoContent)
	}
}

func TestHeadUnmatchedRouteSuppressesErrorBody(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unexpected route dispatch")
	})
	mustAddRoute(r, http.MethodPost, "/only", handler)
	mustAddRoute(r, http.MethodDelete, "/only", handler)
	r.Freeze()

	for _, tc := range []struct {
		path   string
		status int
		allow  string
	}{
		{"/missing", http.StatusNotFound, ""},
		{"/only", http.StatusMethodNotAllowed, "DELETE, POST"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			get := serveRouter(r, http.MethodGet, tc.path)
			assertResponseStatus(t, get, tc.status)
			if get.Body.Len() == 0 {
				t.Fatal("GET error response is missing its body")
			}

			head := serveRouter(r, http.MethodHead, tc.path)
			assertResponseStatus(t, head, tc.status)
			assertResponseBody(t, head, "")
			assertResponseHeader(t, head, "Allow", tc.allow)
			assertResponseHeader(t, head, "Content-Type", get.Header().Get("Content-Type"))
			assertResponseHeader(t, head, "Content-Length", strconv.Itoa(get.Body.Len()))
		})
	}
}

func TestMethodNotAllowedWhenEnabled(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	mustAddRoute(r, http.MethodGet, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	rec := serveRouter(r, http.MethodPost, "/only")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	assertResponseHeader(t, rec, "Allow", "GET, HEAD")
}

func TestMethodNotAllowedRoot(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	mustAddRoute(r, http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root"))
	}))

	rec := serveRouter(r, http.MethodPost, "/")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	assertResponseHeader(t, rec, "Allow", "GET, HEAD")
}

func TestMethodNotAllowedRemainsWithCache(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true), withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	rec := serveRouter(r, http.MethodPost, "/only")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	assertResponseHeader(t, rec, "Allow", "GET, HEAD")

	rec = serveRouter(r, http.MethodPost, "/only")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	assertResponseHeader(t, rec, "Allow", "GET, HEAD")
}

func TestMethodNotAllowedAllowHeaderSorted(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	mustAddRoute(r, http.MethodGet, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	mustAddRoute(r, http.MethodPost, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	mustAddRoute(r, http.MethodPut, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	rec := serveRouter(r, http.MethodDelete, "/only")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	assertResponseHeader(t, rec, "Allow", "GET, HEAD, POST, PUT")
}

func TestMethodNotAllowedAllowHeaderDoesNotDuplicateExplicitHead(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	mustAddRoute(r, http.MethodGet, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mustAddRoute(r, http.MethodHead, "/only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := serveRouter(r, http.MethodPost, "/only")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	assertResponseHeader(t, rec, "Allow", "GET, HEAD")
}

func TestAnyFallbackWhenMethodTreeMissing(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	}))
	mustAddRoute(r, MethodAny, "/fallback", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("any"))
	}))

	rec := serveRouter(r, http.MethodGet, "/fallback")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "any")
}

func TestIncomingANYMethodUsesAnyFallback(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, MethodAny, "/fallback", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rc := contract.RequestContextFromContext(req.Context())
		if rc.RoutePattern != "/fallback" {
			t.Fatalf("route pattern = %q, want %q", rc.RoutePattern, "/fallback")
		}
		w.Write([]byte("any"))
	}), WithRouteName("any.fallback"))

	rec := serveRouter(r, MethodAny, "/fallback")
	assertResponseStatus(t, rec, http.StatusOK)
	assertTrimmedResponseBody(t, rec, "any")
}

func TestAnyFallbackWithCache(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, MethodAny, "/fallback", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("any"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/fallback", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "any" {
		t.Fatalf("expected %q, got %q", "any", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/fallback", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected cached 200, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "any" {
		t.Fatalf("expected cached %q, got %q", "any", body)
	}
}

func TestParameterizedAnyFallbackContextUsesAnyMethodWithCache(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, MethodAny, "/fallback/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := contract.RequestContextFromContext(r.Context())
		if rc.RoutePattern != "/fallback/:id" {
			t.Fatalf("expected route pattern %q, got %q", "/fallback/:id", rc.RoutePattern)
		}
		w.Write([]byte(Param(r, "id")))
	}))

	for _, id := range []string{"one", "two"} {
		req := httptest.NewRequest(http.MethodPost, "/fallback/"+id, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != id {
			t.Fatalf("expected body %q, got %q", id, body)
		}
	}
}

func TestHeadFallbackParameterizedRouteSuppressesBodyWithCache(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := contract.RequestContextFromContext(r.Context())
		if rc.RoutePattern != "/users/:id" {
			t.Fatalf("expected route pattern %q, got %q", "/users/:id", rc.RoutePattern)
		}
		w.Header().Set("X-User-ID", Param(r, "id"))
		n, err := w.Write([]byte("body"))
		if err != nil {
			t.Fatalf("HEAD fallback write returned error: %v", err)
		}
		if n != 4 {
			t.Fatalf("suppressed write count = %d, want 4", n)
		}
	}))

	for _, id := range []string{"one", "two"} {
		req := httptest.NewRequest(http.MethodHead, "/users/"+id, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("X-User-ID"); got != id {
			t.Fatalf("expected X-User-ID %q, got %q", id, got)
		}
		if body := rec.Body.String(); body != "" {
			t.Fatalf("expected empty HEAD body, got %q", body)
		}
	}
}

func TestHeadFallbackReadFromDrainsAndUnwrapsResponseWriter(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/stream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(w, strings.NewReader("stream-body"))
		if err != nil {
			t.Fatalf("HEAD fallback ReadFrom returned error: %v", err)
		}
		if n != 11 {
			t.Fatalf("suppressed copy count = %d, want 11", n)
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatalf("HEAD fallback ResponseController Flush returned error: %v", err)
		}
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodHead, "/stream", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rec.Code)
		}
		if body := rec.Body.String(); body != "" {
			t.Fatalf("request %d: expected empty HEAD body, got %q", i, body)
		}
	}
}

func TestHeadAnyFallbackSuppressesBodyWithCache(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, MethodAny, "/any/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := contract.RequestContextFromContext(r.Context())
		if rc.RoutePattern != "/any/:id" {
			t.Fatalf("expected route pattern %q, got %q", "/any/:id", rc.RoutePattern)
		}
		w.Header().Set("X-User-ID", Param(r, "id"))
		n, err := w.Write([]byte("body"))
		if err != nil {
			t.Fatalf("HEAD any fallback write returned error: %v", err)
		}
		if n != 4 {
			t.Fatalf("suppressed write count = %d, want 4", n)
		}
	}))

	for _, id := range []string{"one", "two"} {
		req := httptest.NewRequest(http.MethodHead, "/any/"+id, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("X-User-ID"); got != id {
			t.Fatalf("expected X-User-ID %q, got %q", id, got)
		}
		if body := rec.Body.String(); body != "" {
			t.Fatalf("expected empty HEAD body, got %q", body)
		}
	}
}

func TestTrailingSlashNormalization(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Param(r, "id")
		w.Write([]byte(id))
	}))

	for _, path := range []string{"/users/123/", "/users/123//"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != "123" {
			t.Fatalf("%s: expected id %q, got %q", path, "123", body)
		}
	}
}

func TestInternalDoubleSlashNotNormalized(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/users//123", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestWildcardParamCapturesRemainder(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/files/*path", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := Param(r, "path")
		w.Write([]byte(path))
	}))

	req := httptest.NewRequest(http.MethodGet, "/files/a/b/c.txt", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "a/b/c.txt" {
		t.Fatalf("expected %q, got %q", "a/b/c.txt", body)
	}
}

func TestWildcardParamRejectsInternalDoubleSlash(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/files/*path", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(Param(r, "path")))
	}))

	req := httptest.NewRequest(http.MethodGet, "/files//a", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestEncodedSpaceParamIsDecoded(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Param(r, "id")
		w.Write([]byte(id))
	}))

	req := httptest.NewRequest(http.MethodGet, "/users/a%20b", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "a b" {
		t.Fatalf("expected %q, got %q", "a b", body)
	}
}

func TestEncodedSlashDoesNotMatchSingleSegmentParam(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/users/a%2Fb", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestEncodedSlashMatchesWildcardParam(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/files/*path", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := Param(r, "path")
		w.Write([]byte(path))
	}))

	req := httptest.NewRequest(http.MethodGet, "/files/a%2Fb", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "a/b" {
		t.Fatalf("expected %q, got %q", "a/b", body)
	}
}

func TestDuplicateParamKeysRejected(t *testing.T) {
	r := NewRouter()
	err := r.AddRoute(http.MethodGet, "/teams/:id/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err == nil {
		t.Fatal("expected duplicate param name to fail registration")
	}
}

func TestGroupParamKeysRejected(t *testing.T) {
	r := NewRouter()
	group := r.Group("/orgs/:id")
	err := group.AddRoute(http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err == nil {
		t.Fatal("expected duplicate grouped param name to fail registration")
	}
}

func TestGroupParamDoesNotMatchEncodedSlash(t *testing.T) {
	r := NewRouter()
	group := r.Group("/orgs/:id")
	mustAddRoute(group, http.MethodGet, "/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/orgs/a%2Fb/users", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestCachedRouteDoesNotLeakParams(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Param(r, "id")
		w.Write([]byte(id))
	}))

	req := httptest.NewRequest(http.MethodGet, "/users/one", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "one" {
		t.Fatalf("expected %q, got %q", "one", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/users/two", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "two" {
		t.Fatalf("expected %q, got %q", "two", body)
	}
}

func TestRouteParamsOverrideExistingContext(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Param(r, "id")
		rc := contract.RequestContextFromContext(r.Context())

		if id != "123" {
			t.Fatalf("expected Param id %q, got %q", "123", id)
		}
		if rc.Params["id"] != "123" {
			t.Fatalf("expected RequestContext param %q, got %q", "123", rc.Params["id"])
		}
		w.Write([]byte("ok"))
	}))

	baseReq := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	ctx := contract.WithRequestContext(baseReq.Context(), contract.RequestContext{
		Params: map[string]string{"id": "ctx2"},
	})
	req := baseReq.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRouteWithoutParamsClearsExistingContextParams(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := Param(r, "id"); got != "" {
			t.Fatalf("expected no route param id, got %q", got)
		}
		rc := contract.RequestContextFromContext(r.Context())
		if len(rc.Params) != 0 {
			t.Fatalf("expected no route params, got %v", rc.Params)
		}
		w.Write([]byte("ok"))
	}))

	baseReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	ctx := contract.WithRequestContext(baseReq.Context(), contract.RequestContext{
		Params: map[string]string{"id": "stale"},
	})
	req := baseReq.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assertResponseStatus(t, rec, http.StatusOK)
	assertResponseBody(t, rec, "ok")
}

func TestUnnamedRouteClearsExistingContextRouteName(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := contract.RequestContextFromContext(r.Context())
		if rc.RouteName != "" {
			t.Fatalf("expected route name to be cleared, got %q", rc.RouteName)
		}
		if rc.RoutePattern != "/healthz" {
			t.Fatalf("expected route pattern %q, got %q", "/healthz", rc.RoutePattern)
		}
		w.Write([]byte("ok"))
	}))

	baseReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	ctx := contract.WithRequestContext(baseReq.Context(), contract.RequestContext{
		RouteName:    "stale.route",
		RoutePattern: "/stale",
	})
	req := baseReq.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assertResponseStatus(t, rec, http.StatusOK)
	assertResponseBody(t, rec, "ok")
}

func TestRequestContextIncludesRoutePatternAndName(t *testing.T) {
	r := NewRouter()
	err := r.AddRoute(http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := contract.RequestContextFromContext(r.Context())
		if rc.RoutePattern != "/users/:id" {
			t.Fatalf("expected route pattern %q, got %q", "/users/:id", rc.RoutePattern)
		}
		if rc.RouteName != "user_show" {
			t.Fatalf("expected route name %q, got %q", "user_show", rc.RouteName)
		}
		w.WriteHeader(http.StatusOK)
	}), WithRouteName("user_show"))
	if err != nil {
		t.Fatalf("add route failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRouteMetadataConcurrentRegistrationThenServe(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rc := contract.RequestContextFromContext(req.Context())
		if rc.RouteName != "users.show" {
			t.Errorf("route name = %q, want %q", rc.RouteName, "users.show")
		}
		w.WriteHeader(http.StatusOK)
	}), WithRouteName("users.show"))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "/extra/" + strconv.Itoa(i) + "/:id"
			name := "extra." + strconv.Itoa(i)
			err := r.AddRoute(http.MethodGet, path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				rc := contract.RequestContextFromContext(req.Context())
				if rc.RoutePattern != path {
					t.Errorf("route pattern = %q, want %q", rc.RoutePattern, path)
				}
				if rc.RouteName != name {
					t.Errorf("route name = %q, want %q", rc.RouteName, name)
				}
				w.WriteHeader(http.StatusOK)
			}), WithRouteName(name))
			if err != nil {
				t.Errorf("add route %s failed: %v", path, err)
			}
		}(i)
	}
	wg.Wait()

	r.Freeze()

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/users/"+strconv.Itoa(i), nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("serve status = %d, want %d", rec.Code, http.StatusOK)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/extra/"+strconv.Itoa(i)+"/value", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("serve extra status = %d, want %d", rec.Code, http.StatusOK)
			}
		}(i)
	}
	wg.Wait()
}

func TestCachedRouteNormalizesTrailingSlash(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/users/123/", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if count := matchCacheEntryCount(r.state.matchCache); count != 1 {
		t.Fatalf("expected cache entry count 1, got %d", count)
	}
}

func TestCachedRouteNormalizesRepeatedLeadingSlash(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(Param(r, "id")))
	}))

	req := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertResponseStatus(t, rec, http.StatusOK)
	assertResponseBody(t, rec, "123")

	req = httptest.NewRequest(http.MethodGet, "//users/123", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertResponseStatus(t, rec, http.StatusOK)
	assertResponseBody(t, rec, "123")

	if count := matchCacheEntryCount(r.state.matchCache); count != 1 {
		t.Fatalf("expected cache entry count 1, got %d", count)
	}
}

func TestCachedRouteKeyUsesMethodAndNormalizedPath(t *testing.T) {
	r := NewRouter(withCacheCapacity(10))
	mustAddRoute(r, http.MethodGet, "/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mustAddRoute(r, http.MethodPost, "/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Host = "one.example"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Host = "two.example"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/ping", nil)
	req.Host = "one.example"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	count := matchCacheEntryCount(r.state.matchCache)
	if count != 2 {
		t.Fatalf("expected method/path cache entry count 2, got %d", count)
	}
}

func TestRouteOptionsCanInspectAndFreezeRouter(t *testing.T) {
	for _, freeze := range []bool{false, true} {
		t.Run(strconv.FormatBool(freeze), func(t *testing.T) {
			r := NewRouter()
			done := make(chan error, 1)
			go func() {
				done <- r.AddRoute(http.MethodGet, "/new", http.NotFoundHandler(), func(meta *RouteMeta) {
					_ = r.Routes()
					meta.Name = "new"
					if freeze {
						r.Freeze()
					}
				})
			}()
			select {
			case err := <-done:
				if freeze {
					if err == nil || len(r.Routes()) != 0 || r.HasRoute("new") {
						t.Fatalf("registration after freeze: error = %v, routes = %v", err, r.Routes())
					}
				} else if err != nil || !r.HasRoute("new") {
					t.Fatalf("registration: error = %v, routes = %v", err, r.Routes())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("route option could not inspect the router")
			}
		})
	}
}

func TestRoutingMatrixBeforeAndAfterFreeze(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	add := func(method, path, name string) {
		t.Helper()
		if err := r.AddRoute(method, path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Route", name)
			w.Header().Set("X-Param", Param(req, "id"))
			_, _ = io.WriteString(w, name)
		})); err != nil {
			t.Fatal(err)
		}
	}
	add(http.MethodGet, "/items/:id", "get")
	add(http.MethodHead, "/items/fixed", "head")
	add(MethodAny, "/other/:id", "any")

	cases := []struct {
		method, path, route, param, allow string
		status                            int
	}{
		{http.MethodGet, "/items/fixed", "get", "fixed", "", http.StatusOK},
		{http.MethodHead, "/items/fixed", "head", "", "", http.StatusOK},
		{http.MethodHead, "/items/17", "get", "17", "", http.StatusOK},
		{http.MethodHead, "/other/17", "any", "17", "", http.StatusOK},
		{http.MethodPost, "/items/17", "", "", "GET, HEAD", http.StatusMethodNotAllowed},
		{http.MethodHead, "/missing", "", "", "", http.StatusNotFound},
		{http.MethodPost, "///items/17/", "", "", "GET, HEAD", http.StatusMethodNotAllowed},
		{http.MethodHead, "///items/17/", "get", "17", "", http.StatusOK},
		{http.MethodHead, "///missing/", "", "", "", http.StatusNotFound},
	}
	for _, stage := range []string{"mutable cold", "mutable warm", "frozen", "frozen warm"} {
		if stage == "frozen" {
			r.Freeze()
		}
		for _, tc := range cases {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status || rec.Header().Get("X-Route") != tc.route ||
				rec.Header().Get("X-Param") != tc.param || rec.Header().Get("Allow") != tc.allow ||
				(tc.method == http.MethodHead && rec.Body.Len() != 0) {
				t.Fatalf("%s %s %s: status=%d headers=%v body=%q", stage, tc.method, tc.path, rec.Code, rec.Header(), rec.Body.String())
			}
		}
	}
}

func TestAnyFallbackDoesNotPolluteAllowForUnrelatedBranches(t *testing.T) {
	for _, freeze := range []bool{false, true} {
		r := NewRouter(WithMethodNotAllowed(true))
		labeled := func(value string) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Handler", value)
			})
		}
		mustAddRoute(r, MethodAny, "/other/:id", labeled("any"))
		mustAddRoute(r, http.MethodGet, "/items/fixed", labeled("get"))
		mustAddRoute(r, http.MethodPost, "/items/:id/detail", labeled("post"))
		if freeze {
			r.Freeze()
		}
		if w := serveRouter(r, http.MethodHead, "/items/fixed"); w.Code != 200 || w.Header().Get("X-Handler") != "get" {
			t.Fatalf("HEAD matched %d: %v", w.Code, w.Header())
		}
		if w := serveRouter(r, http.MethodDelete, "/items/fixed"); w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("Allow status=%d headers=%v", w.Code, w.Header())
		}
		if w := serveRouter(r, http.MethodPatch, "/other/1"); w.Code != 200 || w.Header().Get("X-Handler") != "any" {
			t.Fatalf("Any fallback=%d: %v", w.Code, w.Header())
		}
	}
}

func TestNamedWildcardRoundTripsEncodedValueWithInnerEmptySegment(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, "GET", "/files/*item", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Pattern", contract.RequestContextFromContext(r.Context()).RoutePattern)
		_, _ = io.WriteString(w, Param(r, "item"))
	}), WithRouteName("file"))
	for _, value := range []string{"start//世界", "space test/a"} {
		path, err := r.URL("file", "item", value)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			rec := serveRouter(r, "GET", path)
			if rec.Code != 200 || rec.Body.String() != value || rec.Header().Get("X-Pattern") != "/files/*item" {
				t.Fatalf("path=%q status=%d body=%q", path, rec.Code, rec.Body.String())
			}
		}
	}
	for _, value := range []string{"/start", "end/"} {
		if path, err := r.URL("file", "item", value); err == nil || path != "" {
			t.Fatalf("value=%q path=%q err=%v", value, path, err)
		}
	}
}

func TestRouteOptionOverlappingFreezeDoesNotPublishMetadata(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	mustAddRoute(r, "GET", "/first", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), WithRouteName("first"))
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- r.AddRoute("GET", "/late", http.NotFoundHandler(), func(meta *RouteMeta) {
			close(entered)
			<-release
			meta.Name = "late"
		})
	}()
	<-entered
	r.Freeze()
	close(release)
	if err := <-done; err == nil || len(r.Routes()) != 1 || len(r.NamedRoutes()) != 1 || r.HasRoute("late") {
		t.Fatalf("late registration=%v routes=%v", err, r.Routes())
	}
	if rec := serveRouter(r, "POST", "/first"); rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("old route=%d %v", rec.Code, rec.Header())
	}
	if rec := serveRouter(r, "GET", "/late"); rec.Code != 404 {
		t.Fatalf("late route=%d", rec.Code)
	}
}

func TestFrozenRouterKeepsPrewarmedAllowPolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		r := NewRouter(WithMethodNotAllowed(enabled))
		mustAddRoute(r, "GET", "/fixture", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
		for range 2 {
			for _, method := range []string{"GET", "HEAD"} {
				if rec := serveRouter(r, method, "/fixture"); rec.Code != 204 {
					t.Fatalf("%s status=%d", method, rec.Code)
				}
			}
			w := serveRouter(r, "POST", "/fixture")
			if enabled && (w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD") || !enabled && w.Code != 404 {
				t.Fatalf("policy=%t status=%d headers=%v", enabled, w.Code, w.Header())
			}
			if rec := serveRouter(r, "POST", "/missing"); rec.Code != 404 {
				t.Fatalf("missing status=%d", rec.Code)
			}
			r.Freeze()
			r.SetMethodNotAllowed(!enabled)
		}
	}
}

func TestDuplicateRouteNameLeavesHotDynamicMatchAndMetadata(t *testing.T) {
	r := NewRouter()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, Param(r, "id")) })
	mustAddRoute(r, "GET", "/sample/:id", h, WithRouteName("sample"))
	if got := serveRouter(r, "GET", "/sample/fixed"); got.Body.String() != "fixed" {
		t.Fatalf("initial=%q", got.Body.String())
	}
	if err := r.AddRoute("GET", "/sample/fixed", http.NotFoundHandler(), WithRouteName("sample")); err == nil {
		t.Fatal("duplicate name accepted")
	}
	r.Freeze()
	for _, value := range []string{"fixed", "cold", "fixed"} {
		if got := serveRouter(r, "GET", "/sample/"+value); got.Code != 200 || got.Body.String() != value {
			t.Fatalf("route=%v", got)
		}
	}
	if path, err := r.URL("sample", "id", "value"); path != "/sample/value" || err != nil || len(r.Routes()) != 1 {
		t.Fatalf("URL=%q err=%v routes=%v", path, err, r.Routes())
	}
}

func TestNamedRouteSnapshotsStayOwnedAcrossZeroAndGroups(t *testing.T) {
	var zero Router
	zero.NamedRoutes()["local"] = new(NamedRoute)
	if len(zero.NamedRoutes()) != 0 {
		t.Fatal("zero router retained caller map")
	}
	r := NewRouter()
	group := r.Group("/sample")
	mustAddRoute(group, "GET", "/:id", http.NotFoundHandler(), WithRouteName("sample"))
	names := group.NamedRoutes()
	names["sample"].ParamPos["id"] = 300
	delete(names, "sample")
	if path, err := r.URL("sample", "id", "value"); path != "/sample/value" || err != nil || r.NamedRoutes()["sample"].ParamPos["id"] != 0 {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestNestedWildcardWinsAfterBothMoreSpecificDeadEnds(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	group := r.Group("/prefix")
	mustAddRoute(group, "GET", "/fixed/other", http.NotFoundHandler())
	mustAddRoute(group, "GET", "/:id/shared/other", http.NotFoundHandler())
	mustAddRoute(group, "GET", "/*tail", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta := contract.RequestContextFromContext(r.Context())
		if len(meta.Params) != 1 || meta.RouteName != "wild" || meta.RoutePattern != "/prefix/*tail" {
			t.Errorf("metadata=%+v", meta)
		}
		_, _ = io.WriteString(w, Param(r, "tail"))
	}), WithRouteName("wild"))
	mustAddRoute(group, "POST", "/:id/shared/other", http.NotFoundHandler())
	for range 2 {
		rec := serveRouter(r, "GET", "/prefix/fixed/shared/end")
		if rec.Code != 200 || rec.Body.String() != "fixed/shared/end" {
			t.Fatalf("route=%d body=%q", rec.Code, rec.Body.String())
		}
		rejected := serveRouter(r, "PATCH", "/prefix/fixed/shared/end")
		if rejected.Code != 405 || rejected.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("allow=%d %v", rejected.Code, rejected.Header())
		}
	}
}

func TestAllowMethodsAcrossOverlapWithExplicitHead(t *testing.T) {
	r := NewRouter()
	r.SetMethodNotAllowed(true)
	for _, entry := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/files/item", 200},
		{http.MethodPost, "/files/*rest", 201},
		{http.MethodHead, "/files/:id", 202},
	} {
		mustAddRoute(r, entry.method, entry.path, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(entry.status) }))
	}
	r.Freeze()
	for range 2 {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/files/item", nil))
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD, POST" {
			t.Fatalf("status=%d allow=%v", rec.Code, rec.Header())
		}
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/files/item", nil))
		if rec.Code != 202 {
			t.Fatalf("explicit HEAD=%d", rec.Code)
		}
	}
}

func TestAllowHeaderDetachedAfterPositiveCacheWarms(t *testing.T) {
	r := NewRouter()
	r.SetMethodNotAllowed(true)
	mustAddRoute(r, http.MethodGet, "/item/:id", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	r.Freeze()
	first := httptest.NewRecorder()
	r.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/item/value", nil))
	if first.Code != 405 || first.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("first=%d headers=%v", first.Code, first.Header())
	}
	first.Header().Set("Allow", "fixture")
	warm := httptest.NewRecorder()
	r.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, "/item/value", nil))
	if warm.Code != 204 {
		t.Fatalf("warm status=%d", warm.Code)
	}
	after := httptest.NewRecorder()
	r.ServeHTTP(after, httptest.NewRequest(http.MethodPost, "/item/value", nil))
	if after.Code != 405 || after.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("after=%d headers=%v", after.Code, after.Header())
	}
}

func TestCompleteAnyRouteWinsWhenExplicitMethodDeadEnds(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	g := r.Group("/catalog")
	for _, entry := range []struct {
		method, path, marker string
	}{
		{http.MethodGet, "/item/details", "detail"},
		{MethodAny, "/item", "fallback"},
		{http.MethodPut, "/standalone", "standalone"},
	} {
		if err := g.AddRoute(entry.method, entry.path, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Route", entry.marker)
			_, _ = io.WriteString(w, entry.marker)
		})); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		for _, tc := range []struct {
			method, path, marker, allow string
			status                      int
		}{
			{http.MethodGet, "/item", "fallback", "", 200},
			{http.MethodHead, "/item", "fallback", "", 200},
			{http.MethodGet, "/item/details", "detail", "", 200},
			{http.MethodGet, "/standalone", "", "PUT", 405},
			{http.MethodGet, "/missing", "", "", 404},
		} {
			w := serveRouter(r, tc.method, "/catalog"+tc.path)
			if w.Code != tc.status || w.Header().Get("X-Route") != tc.marker || w.Header().Get("Allow") != tc.allow ||
				(tc.method == http.MethodHead && w.Body.Len() != 0) {
				t.Fatalf("%s %s: status=%d headers=%v body=%q", tc.method, tc.path, w.Code, w.Header(), w.Body)
			}
		}
	}
}

func TestRegistrationReplacesHotAnyAndRefreshesAllow(t *testing.T) {
	r := NewRouter(WithMethodNotAllowed(true))
	makeHandler := func(marker string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Route", contract.RequestContextFromContext(r.Context()).RouteName)
			_, _ = w.Write([]byte(marker))
		})
	}
	mustAddRoute(r, MethodAny, "/item", makeHandler("fallback"))
	mustAddRoute(r, http.MethodPost, "/methods", makeHandler("post"))
	for range 2 {
		if got := serveRouter(r, http.MethodGet, "/item").Body.String(); got != "fallback" {
			t.Fatalf("body=%q", got)
		}
		if got := serveRouter(r, http.MethodGet, "/methods").Header().Get("Allow"); got != "POST" {
			t.Fatalf("allow=%q", got)
		}
	}
	mustAddRoute(r, http.MethodGet, "/item", makeHandler("explicit"), WithRouteName("explicit"))
	mustAddRoute(r, http.MethodPut, "/methods", makeHandler("put"))
	for range 2 {
		got := serveRouter(r, http.MethodGet, "/item")
		if got.Body.String() != "explicit" || got.Header().Get("X-Route") != "explicit" {
			t.Fatalf("response=%v %q", got.Header(), got.Body)
		}
		if got := serveRouter(r, http.MethodGet, "/methods"); got.Code != 405 || got.Header().Get("Allow") != "POST, PUT" {
			t.Fatalf("response=%d %v", got.Code, got.Header())
		}
	}
}
