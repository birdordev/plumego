package router

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
)

func assertOutputContains(t *testing.T, output string, expected string) {
	t.Helper()

	if !strings.Contains(output, expected) {
		t.Fatalf("output missing %q:\n%s", expected, output)
	}
}

func TestBasicRoutes(t *testing.T) {
	// Reset global router
	r := NewRouter()

	mustAddRoute(r, http.MethodGet, "/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	}))

	mustAddRoute(r, http.MethodPost, "/echo", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("echo"))
	}))

	tests := []struct {
		method   string
		path     string
		expected string
	}{
		{"GET", "/ping", "pong"},
		{"POST", "/echo", "echo"},
		{"GET", "/echo", "404 page not found\n"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		resp := w.Body.String()
		if resp != tt.expected {
			t.Errorf("[%s %s] expected %q, got %q", tt.method, tt.path, tt.expected, resp)
		}
	}
}

func TestParamRoutes(t *testing.T) {
	r := NewRouter()

	mustAddRoute(r, http.MethodGet, "/hello/:name", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := Param(r, "name")
		ctxParams := contract.RequestContextFromContext(r.Context()).Params
		if ctxParams["name"] != name {
			t.Fatalf("context params mismatch: got %s want %s", ctxParams["name"], name)
		}
		w.Write([]byte("Hello " + name))
	}))

	mustAddRoute(r, http.MethodGet, "/users/:id/books/:bookId", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Param(r, "id")
		bookID := Param(r, "bookId")
		ctxParams := contract.RequestContextFromContext(r.Context()).Params
		if ctxParams["id"] != id || ctxParams["bookId"] != bookID {
			t.Fatalf("context params mismatch: %v", ctxParams)
		}
		w.Write([]byte("User " + id + " Book " + bookID))
	}))

	tests := []struct {
		path     string
		expected string
	}{
		{"/hello/Alice", "Hello Alice"},
		{"/hello/Bob", "Hello Bob"},
		{"/users/123/books/456", "User 123 Book 456"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		resp := strings.TrimSpace(w.Body.String())
		if resp != tt.expected {
			t.Errorf("[%s] expected %q, got %q", tt.path, tt.expected, resp)
		}
	}
}

func TestParamsInjectedIntoContext(t *testing.T) {
	r := NewRouter()

	mustAddRoute(r, http.MethodGet, "/hello/:name", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxParams := contract.RequestContextFromContext(r.Context()).Params
		if ctxParams == nil {
			t.Fatalf("expected params in context")
		}

		paramVal := Param(r, "name")

		if ctxParams["name"] != paramVal {
			t.Fatalf("context params mismatch: got %s want %s", ctxParams["name"], paramVal)
		}
		w.Write([]byte(ctxParams["name"]))
	}))

	req := httptest.NewRequest(http.MethodGet, "/hello/Alice", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if got := strings.TrimSpace(w.Body.String()); got != "Alice" {
		t.Fatalf("expected context value to be written, got %q", got)
	}
}

func TestRequestContextHelpers(t *testing.T) {
	r := NewRouter()

	mustAddRoute(r, http.MethodGet, "/hello/:name", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := contract.RequestContextFromContext(r.Context())
		name := Param(r, "name")
		if rc.Params["name"] != name {
			t.Fatalf("request context params mismatch: got %s want %s", rc.Params["name"], name)
		}

		if val := Param(r, "name"); val != name {
			t.Fatalf("Param helper mismatch: got %s want %s", val, name)
		}

		if _, ok := contract.RequestContextFromContext(r.Context()).Params["missing"]; ok {
			t.Fatalf("expected missing parameter to return ok=false")
		}

		w.Write([]byte(rc.Params["name"]))
	}))

	req := httptest.NewRequest(http.MethodGet, "/hello/Carol", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if got := strings.TrimSpace(w.Body.String()); got != "Carol" {
		t.Fatalf("expected Param helper to provide name, got %q", got)
	}
}

func TestContextHandlerRegistration(t *testing.T) {
	r := NewRouter()

	mustAddRoute(r, http.MethodGet, "/ctx/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := contract.RequestContextFromContext(r.Context())
		if rc.Params == nil {
			t.Fatalf("expected RequestContext to be present")
		}

		paramVal := Param(r, "id")

		if rc.Params["id"] != paramVal {
			t.Fatalf("context param mismatch: got %s want %s", rc.Params["id"], paramVal)
		}

		w.Write([]byte(paramVal))
	}))

	req := httptest.NewRequest(http.MethodGet, "/ctx/42", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if got := strings.TrimSpace(w.Body.String()); got != "42" {
		t.Fatalf("expected context handler to write param, got %q", got)
	}
}

func TestAnyRoute(t *testing.T) {
	r := NewRouter()

	mustAddRoute(r, MethodAny, "/any", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("any"))
	}))

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	for _, method := range methods {
		req := httptest.NewRequest(method, "/any", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Body.String() != "any" {
			t.Errorf("[%s /any] expected %q, got %q", method, "any", w.Body.String())
		}
	}
}

func TestAnyRootFallback(t *testing.T) {
	r := NewRouter()

	mustAddRoute(r, http.MethodGet, "/docs", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("docs"))
	}))

	mustAddRoute(r, MethodAny, "/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("home"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := strings.TrimSpace(w.Body.String()); got != "home" {
		t.Fatalf("expected any root to match, got %q", got)
	}
}

func TestPrintRoutes(t *testing.T) {
	// Reset global r
	r := NewRouter()

	mustAddRoute(r, http.MethodGet, "/print1", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	mustAddRoute(r, http.MethodPost, "/print2", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	mustAddRoute(r, MethodAny, "/print3", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	// Read captured output
	var outBuf bytes.Buffer
	r.Print(&outBuf)

	output := outBuf.String()
	assertOutputContains(t, output, "GET    /print1")
	assertOutputContains(t, output, "POST   /print2")
	assertOutputContains(t, output, "/print3")
}

func TestRouteGroup(t *testing.T) {
	r := NewRouter()

	api := r.Group("/api")
	v1 := api.Group("/v1")
	v2 := api.Group("/v2")

	mustAddRoute(v1, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Param(r, "id")
		ctxParams := contract.RequestContextFromContext(r.Context()).Params
		if ctxParams["id"] != id {
			t.Fatalf("expected id in context")
		}
		w.Write([]byte("User " + id))
	}))
	mustAddRoute(v1, http.MethodPost, "/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Create User"))
	}))
	mustAddRoute(v2, http.MethodGet, "/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Users v2"))
	}))

	tests := []struct {
		method   string
		path     string
		expected string
	}{
		{"GET", "/api/v1/users/123", "User 123"},
		{"POST", "/api/v1/users", "Create User"},
		{"GET", "/api/v1/users", "404 page not found\n"},
		{"GET", "/api/v2/users/123", "404 page not found\n"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		resp := w.Body.String()
		if resp != tt.expected {
			t.Errorf("[%s %s] expected %q, got %q", tt.method, tt.path, tt.expected, resp)
		}
	}
}

func TestRouterFreeze(t *testing.T) {
	r := NewRouter()

	mustAddRoute(r, http.MethodGet, "/ping", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r.Freeze()

	err := r.AddRoute("GET", "/panic", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if err == nil {
		t.Errorf("expected error when adding route after freeze")
	}
}

func TestRouteMetadata(t *testing.T) {
	r := NewRouter()

	err := r.AddRoute("GET", "/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), WithRouteName("ping"))
	if err != nil {
		t.Fatalf("add route failed: %v", err)
	}

	routes := r.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	route := routes[0]
	if route.Meta.Name != "ping" {
		t.Fatalf("expected name 'ping', got %q", route.Meta.Name)
	}
}

// --- Enhanced Route Group Tests ---

func TestGroupPrefixNormalization(t *testing.T) {
	tests := []struct {
		name       string
		parent     string
		child      string
		wantPrefix string
	}{
		{"basic", "/api", "/v1", "/api/v1"},
		{"trailing slash parent", "/api/", "/v1", "/api/v1"},
		{"trailing slash child", "/api", "/v1/", "/api/v1"},
		{"both trailing slashes", "/api/", "/v1/", "/api/v1"},
		{"empty parent", "", "/v1", "/v1"},
		{"empty child", "/api", "", "/api"},
		{"both empty", "", "", ""},
		{"root child", "/api", "/", "/api"},
		{"deep nesting", "/a/b", "/c/d", "/a/b/c/d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeGroupPrefix(tt.parent, tt.child)
			if got != tt.wantPrefix {
				t.Errorf("normalizeGroupPrefix(%q, %q) = %q, want %q",
					tt.parent, tt.child, got, tt.wantPrefix)
			}
		})
	}
}

func TestGroupNoPrefixDoubleSlash(t *testing.T) {
	r := NewRouter()

	// Trailing slash on group prefix should not produce double slashes
	api := r.Group("/api/")
	v1 := api.Group("/v1/")

	mustAddRoute(v1, http.MethodGet, "/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Errorf("expected 200/ok, got %d/%q", w.Code, w.Body.String())
	}
}

func TestGroupMissingLeadingSlashPanics(t *testing.T) {
	r := NewRouter()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected group with missing leading slash to panic")
		}
		if !strings.Contains(recovered.(string), "must start with /") {
			t.Fatalf("panic = %v, want must start with /", recovered)
		}
	}()

	_ = r.Group("api")
}

func TestGroupEmptyPrefix(t *testing.T) {
	r := NewRouter()

	// Empty prefix group should behave like root
	g := r.Group("")
	mustAddRoute(g, http.MethodGet, "/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Errorf("expected 200/ok, got %d/%q", w.Code, w.Body.String())
	}
}

func TestDeepNestedGroups(t *testing.T) {
	r := NewRouter()

	// 4 levels of nesting
	api := r.Group("/api")
	v1 := api.Group("/v1")
	users := v1.Group("/users")
	settings := users.Group("/settings")

	mustAddRoute(settings, http.MethodGet, "/theme", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dark"))
	}))

	req := httptest.NewRequest("GET", "/api/v1/users/settings/theme", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "dark" {
		t.Errorf("expected 200/dark, got %d/%q", w.Code, w.Body.String())
	}
}

func TestGroupWithPathParams(t *testing.T) {
	r := NewRouter()

	api := r.Group("/api/v1")
	users := api.Group("/users")
	mustAddRoute(users, http.MethodGet, "/:id/posts/:postID", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Param(r, "id")
		postID := Param(r, "postID")
		w.Write([]byte(id + ":" + postID))
	}))

	req := httptest.NewRequest("GET", "/api/v1/users/42/posts/99", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "42:99" {
		t.Errorf("expected 200/42:99, got %d/%q", w.Code, w.Body.String())
	}
}

func TestGroupRootHandler(t *testing.T) {
	r := NewRouter()

	api := r.Group("/api")
	// Register handler at the group root (empty path)
	mustAddRoute(api, http.MethodGet, "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api-root"))
	}))
	mustAddRoute(api, http.MethodGet, "/info", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api-info"))
	}))

	tests := []struct {
		path     string
		expected string
	}{
		{"/api", "api-root"},
		{"/api/info", "api-info"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Body.String() != tt.expected {
			t.Errorf("[GET %s] expected %q, got %q", tt.path, tt.expected, w.Body.String())
		}
	}
}

func TestGroupMultipleMethods(t *testing.T) {
	r := NewRouter()

	api := r.Group("/api")
	mustAddRoute(api, http.MethodGet, "/items", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("list"))
	}))
	mustAddRoute(api, http.MethodPost, "/items", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("create"))
	}))
	mustAddRoute(api, http.MethodPut, "/items/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("update"))
	}))
	mustAddRoute(api, http.MethodDelete, "/items/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("delete"))
	}))

	tests := []struct {
		method   string
		path     string
		expected string
	}{
		{"GET", "/api/items", "list"},
		{"POST", "/api/items", "create"},
		{"PUT", "/api/items/1", "update"},
		{"DELETE", "/api/items/1", "delete"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Body.String() != tt.expected {
			t.Errorf("[%s %s] expected %q, got %q", tt.method, tt.path, tt.expected, w.Body.String())
		}
	}
}

func TestNestedGroups(t *testing.T) {
	r := NewRouter()

	v1 := r.Group("/api/v1")
	mustAddRoute(v1, http.MethodGet, "/status", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	users := v1.Group("/users")
	mustAddRoute(users, http.MethodGet, "/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Param(r, "id")
		w.Write([]byte("user-" + id))
	}))

	tests := []struct {
		path     string
		expected string
	}{
		{"/api/v1/status", "ok"},
		{"/api/v1/users/5", "user-5"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK || w.Body.String() != tt.expected {
			t.Errorf("[GET %s] expected 200/%q, got %d/%q",
				tt.path, tt.expected, w.Code, w.Body.String())
		}
	}
}

func TestGroupReturnsReusableRouter(t *testing.T) {
	r := NewRouter()

	v1 := r.Group("/api/v1")
	mustAddRoute(v1, http.MethodGet, "/inside", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("inside"))
	}))

	mustAddRoute(v1, http.MethodGet, "/outside", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("outside"))
	}))

	tests := []struct {
		path     string
		expected string
	}{
		{"/api/v1/inside", "inside"},
		{"/api/v1/outside", "outside"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Body.String() != tt.expected {
			t.Errorf("[GET %s] expected %q, got %q", tt.path, tt.expected, w.Body.String())
		}
	}
}
