package router

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spcent/plumego/contract"
	"github.com/spcent/plumego/internal/testutil/wait"
)

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
	if err == nil {
		t.Fatalf("expected nil handler registration to fail")
	}
	if !strings.Contains(err.Error(), "nil handler") {
		t.Fatalf("expected nil handler error, got %v", err)
	}
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
		w.Header().Set("X-Write-N", strconv.Itoa(n))
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
		if got := rec.Header().Get("X-Write-N"); got != "4" {
			t.Fatalf("expected suppressed write to report 4 bytes, got %q", got)
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
		w.Header().Set("X-Copied-N", strconv.FormatInt(n, 10))
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
		if got := rec.Header().Get("X-Copied-N"); got != "11" {
			t.Fatalf("request %d: expected copied count 11, got %q", i, got)
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
		w.Header().Set("X-Write-N", strconv.Itoa(n))
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
		if got := rec.Header().Get("X-Write-N"); got != "4" {
			t.Fatalf("expected suppressed write to report 4 bytes, got %q", got)
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
