package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/birdordev/plumego/router"
)

func TestCacheControlBehavior(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "asset.js", "asset")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithCacheControl("max-age=3600")); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []struct {
		name        string
		path        string
		expectCache bool
		cacheValue  string
	}{
		{"index", "/", false, ""},
		{"asset", "/asset.js", true, "max-age=3600"},
		{"missing", "/missing.html", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			cacheHeader := rec.Header().Get("Cache-Control")
			if tt.expectCache {
				if cacheHeader != tt.cacheValue {
					t.Fatalf("cache header: got %q, want %q", cacheHeader, tt.cacheValue)
				}
			} else {
				if cacheHeader != "" {
					t.Fatalf("unexpected cache header: %q", cacheHeader)
				}
			}
		})
	}
}

func TestIndexCacheControl(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "asset.js", "asset")

	r := router.NewRouter()
	if err := RegisterFromDir(
		r,
		dir,
		WithCacheControl("public, max-age=3600"),
		WithIndexCacheControl("no-cache"),
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	r.ServeHTTP(indexRec, indexReq)
	if got := indexRec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index cache header: got %q, want %q", got, "no-cache")
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/asset.js", nil)
	assetRec := httptest.NewRecorder()
	r.ServeHTTP(assetRec, assetReq)
	if got := assetRec.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("asset cache header: got %q, want %q", got, "public, max-age=3600")
	}
}

func TestCacheControlRejectsUnsafeValues(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	tests := []struct {
		name string
		opt  Option
		want string
	}{
		{
			name: "asset cache control",
			opt:  WithCacheControl("public\r\nX-Bad: yes"),
			want: "cache control",
		},
		{
			name: "index cache control",
			opt:  WithIndexCacheControl("no-cache\r\nX-Bad: yes"),
			want: "index cache control",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := router.NewRouter()
			err := RegisterFromDir(r, dir, tt.opt)
			assertErrorContains(t, err, tt.want)
		})
	}
}

func TestFallbackDisabled(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/missing", nil)
	missingRec := httptest.NewRecorder()
	r.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing status: got %d want %d", missingRec.Code, http.StatusNotFound)
	}

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	r.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("root status: got %d want %d", rootRec.Code, http.StatusOK)
	}
}

func TestFallbackOnlyForNavigationRequests(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithFallback(true)); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []struct {
		name         string
		path         string
		accept       string
		expectStatus int
		expectBody   string
	}{
		{
			name:         "extensionless navigation accepts html",
			path:         "/dashboard/settings",
			accept:       "text/html,application/xhtml+xml",
			expectStatus: http.StatusOK,
			expectBody:   "index",
		},
		{
			name:         "extensionless legacy request without accept",
			path:         "/dashboard/settings",
			expectStatus: http.StatusOK,
			expectBody:   "index",
		},
		{
			name:         "extensionless json request does not fallback",
			path:         "/dashboard/settings",
			accept:       "application/json",
			expectStatus: http.StatusNotFound,
		},
		{
			name:         "missing javascript asset does not fallback",
			path:         "/assets/app.js",
			accept:       "*/*",
			expectStatus: http.StatusNotFound,
		},
		{
			name:         "missing sourcemap asset does not fallback",
			path:         "/assets/app.js.map",
			accept:       "*/*",
			expectStatus: http.StatusNotFound,
		},
		{
			name:         "html explicitly refused",
			path:         "/dashboard/settings",
			accept:       "text/html;q=0,application/json",
			expectStatus: http.StatusNotFound,
		},
		{
			name:         "html invalid high q ignored",
			path:         "/dashboard/settings",
			accept:       "text/html;q=1.5,application/json",
			expectStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.expectStatus {
				t.Fatalf("status: got %d want %d body=%q", rec.Code, tt.expectStatus, rec.Body.String())
			}
			if tt.expectBody != "" {
				assertBodyContains(t, rec, tt.expectBody)
			}
			if tt.expectStatus == http.StatusNotFound && strings.Contains(rec.Body.String(), "index") {
				t.Fatalf("unexpected fallback body: %q", rec.Body.String())
			}
		})
	}
}

func TestCustomHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithHeaders(map[string]string{"X-Frontend": "ok"})); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Frontend"); got != "ok" {
		t.Fatalf("header mismatch: got %q", got)
	}
}

func TestCustomHeadersRejectTransportCriticalHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	tests := []string{
		"Cache-Control",
		"Content-Encoding",
		"Content-Length",
		"Content-Type",
		"Transfer-Encoding",
		"Vary",
	}
	for _, header := range tests {
		t.Run(header, func(t *testing.T) {
			r := router.NewRouter()
			err := RegisterFromDir(r, dir, WithHeaders(map[string]string{header: "bad"}))
			assertErrorContains(t, err, header)
		})
	}
}

func TestCustomHeadersRejectUnsafeValues(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	err := RegisterFromDir(r, dir, WithHeaders(map[string]string{"X-Frontend": "ok\r\nX-Bad: yes"}))
	assertErrorContains(t, err, "invalid value")
}

func TestCustomHeadersDoNotMutateCallerMap(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	headers := map[string]string{"X-Frontend": "ok"}
	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithHeaders(headers)); err != nil {
		t.Fatalf("register: %v", err)
	}
	headers["X-Frontend"] = "changed"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Frontend"); got != "ok" {
		t.Fatalf("header mismatch: got %q want %q", got, "ok")
	}
}

func TestCustomPagePathValidation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	tests := []struct {
		name string
		opt  Option
		want string
	}{
		{name: "notfound parent", opt: WithNotFoundPage("../404.html"), want: "not found page"},
		{name: "notfound absolute", opt: WithNotFoundPage("/404.html"), want: "not found page"},
		{name: "notfound backslash", opt: WithNotFoundPage(`errors\404.html`), want: "not found page"},
		{name: "error parent", opt: WithErrorPage("../500.html"), want: "error page"},
		{name: "error absolute", opt: WithErrorPage("/500.html"), want: "error page"},
		{name: "error backslash", opt: WithErrorPage(`errors\500.html`), want: "error page"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := router.NewRouter()
			err := RegisterFromDir(r, dir, tt.opt)
			assertErrorContains(t, err, tt.want)
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir); err != nil {
		t.Fatalf("register: %v", err)
	}

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("method %s: got status %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
				t.Fatalf("method %s: Allow = %q, want %q", method, got, "GET, HEAD")
			}
		})
	}
}

func TestHeadMethod(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "asset.js", "asset")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []string{"/", "/asset.js"}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodHead, path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("HEAD %s: got status %d", path, rec.Code)
			}
			if rec.Body.Len() > 0 {
				t.Fatalf("HEAD %s: unexpected body length %d", path, rec.Body.Len())
			}
		})
	}
}

func TestCustomNotFoundPage(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	writeTestFile(t, dir, "404.html", "<html>Custom 404 Page</html>")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir,
		WithNotFoundPage("404.html"),
		WithFallback(false),
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertBodyContains(t, rec, "Custom 404 Page")
}

// errOnPathFS wraps http.FileSystem and returns os.ErrPermission for a specific
// path, simulating a server-side IO error (not a "file not found" error).
type errOnPathFS struct {
	base    http.FileSystem
	errPath string
}

func (f *errOnPathFS) Open(name string) (http.File, error) {
	if name == f.errPath {
		return nil, os.ErrPermission
	}
	return f.base.Open(name)
}

type statErrorFS struct {
	base     http.FileSystem
	statPath string
}

func (f *statErrorFS) Open(name string) (http.File, error) {
	file, err := f.base.Open(name)
	if err != nil {
		return nil, err
	}
	if name == f.statPath {
		return statErrorFile{File: file}, nil
	}
	return file, nil
}

type statErrorFile struct {
	http.File
}

func (f statErrorFile) Stat() (os.FileInfo, error) {
	return nil, os.ErrPermission
}

// TestCustomErrorPage verifies that when a server IO error occurs and an error
// page is configured, the error page is served with the correct 500 status code.
func TestCustomErrorPage(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	writeTestFile(t, dir, "500.html", "<html>Server Error</html>")

	fs := &errOnPathFS{base: http.Dir(dir), errPath: "bad-file"}

	r := router.NewRouter()
	if err := RegisterFS(r, fs, WithErrorPage("500.html"), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/bad-file", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 status, got: %d", rec.Code)
	}
	assertBodyContains(t, rec, "Server Error")
}

func TestOriginalStatErrorServesErrorPage(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	writeTestFile(t, dir, "bad-file", "unreadable metadata")
	writeTestFile(t, dir, "500.html", "<html>Server Error</html>")

	fs := &statErrorFS{base: http.Dir(dir), statPath: "bad-file"}

	r := router.NewRouter()
	if err := RegisterFS(r, fs, WithErrorPage("500.html"), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/bad-file", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusInternalServerError)
	}
	assertBodyContains(t, rec, "Server Error")
}

func TestIndexServeOpenErrorServesErrorPage(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	writeTestFile(t, dir, "500.html", "<html>Server Error</html>")

	fs := &errOnPathFS{base: http.Dir(dir), errPath: "index.html"}

	r := router.NewRouter()
	if err := RegisterFS(r, fs, WithErrorPage("500.html"), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusInternalServerError)
	}
	assertBodyContains(t, rec, "Server Error")
}

// TestCustomErrorPageFallback verifies that when the custom error page itself
// is missing, serveError falls back to a JSON error response.
func TestCustomErrorPageFallback(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	// 500.html intentionally not created.

	fs := &errOnPathFS{base: http.Dir(dir), errPath: "bad-file"}

	r := router.NewRouter()
	if err := RegisterFS(r, fs, WithErrorPage("500.html"), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/bad-file", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 status, got: %d", rec.Code)
	}
	// Fallback is JSON; it must not contain HTML from the (missing) error page.
	body := rec.Body.String()
	if strings.Contains(body, "<html>") {
		t.Fatalf("should not serve missing error page HTML, got: %q", body)
	}
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("expected JSON error response, got: %q", body)
	}
}

// TestCustomNotFoundPageStatus verifies that a custom 404 page is served with
// the correct 404 status code rather than 200 OK.
func TestCustomNotFoundPageStatus(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	writeTestFile(t, dir, "404.html", "<html>Custom 404 Page</html>")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir,
		WithNotFoundPage("404.html"),
		WithFallback(false),
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 status, got: %d", rec.Code)
	}
	assertBodyContains(t, rec, "Custom 404 Page")
}

func TestCustomNotFoundPageIgnoresConditionalAndRangeHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	writeTestFile(t, dir, "404.html", "<html>Custom 404 Page</html>")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir,
		WithNotFoundPage("404.html"),
		WithFallback(false),
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "range", header: "Range", value: "bytes=0-3"},
		{name: "if modified since", header: "If-Modified-Since", value: "Wed, 21 Oct 2099 07:28:00 GMT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/missing", nil)
			req.Header.Set(tt.header, tt.value)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusNotFound, rec.Body.String())
			}
			assertBodyContains(t, rec, "Custom 404 Page")
		})
	}
}

func TestCustomErrorPageIgnoresConditionalAndRangeHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	writeTestFile(t, dir, "500.html", "<html>Server Error</html>")

	fs := &errOnPathFS{base: http.Dir(dir), errPath: "bad-file"}

	r := router.NewRouter()
	if err := RegisterFS(r, fs, WithErrorPage("500.html"), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "range", header: "Range", value: "bytes=0-3"},
		{name: "if modified since", header: "If-Modified-Since", value: "Wed, 21 Oct 2099 07:28:00 GMT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/bad-file", nil)
			req.Header.Set(tt.header, tt.value)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusInternalServerError, rec.Body.String())
			}
			assertBodyContains(t, rec, "Server Error")
		})
	}
}

func TestCustomPagesDoNotInheritAssetCache(t *testing.T) {
	t.Run("not found page", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "index.html", "<html>home</html>")
		writeTestFile(t, dir, "404.html", "<html>Custom 404 Page</html>")

		r := router.NewRouter()
		if err := RegisterFromDir(r, dir,
			WithCacheControl("public, max-age=31536000, immutable"),
			WithNotFoundPage("404.html"),
			WithFallback(false),
		); err != nil {
			t.Fatalf("register: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/missing", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status: got %d want %d", rec.Code, http.StatusNotFound)
		}
		if got := rec.Header().Get("Cache-Control"); got != "" {
			t.Fatalf("custom 404 cache header: got %q want empty", got)
		}
	})

	t.Run("error page", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, dir, "index.html", "<html>home</html>")
		writeTestFile(t, dir, "500.html", "<html>Server Error</html>")

		fs := &errOnPathFS{base: http.Dir(dir), errPath: "bad-file"}

		r := router.NewRouter()
		if err := RegisterFS(r, fs,
			WithCacheControl("public, max-age=31536000, immutable"),
			WithErrorPage("500.html"),
			WithFallback(false),
		); err != nil {
			t.Fatalf("register: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/bad-file", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status: got %d want %d", rec.Code, http.StatusInternalServerError)
		}
		if got := rec.Header().Get("Cache-Control"); got != "" {
			t.Fatalf("custom error cache header: got %q want empty", got)
		}
	})
}

func TestCustomMIMETypes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.wasm", "wasm binary content")
	writeTestFile(t, dir, "APP.WASM", "uppercase wasm binary content")
	writeTestFile(t, dir, "manifest.json", `{"name":"app"}`)

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithMIMETypes(map[string]string{
		".wasm": "application/wasm",
		".json": "application/json; charset=utf-8",
	})); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []struct {
		path       string
		expectType string
	}{
		{"/app.wasm", "application/wasm"},
		{"/APP.WASM", "application/wasm"},
		{"/manifest.json", "application/json; charset=utf-8"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: %d", rec.Code)
			}

			contentType := rec.Header().Get("Content-Type")
			if !strings.Contains(contentType, tc.expectType) {
				t.Fatalf("Content-Type: got %q, expected to contain %q", contentType, tc.expectType)
			}
		})
	}
}

func TestCustomMIMETypesDoNotObserveCallerMapMutation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.wasm", "wasm binary content")

	mimeTypes := map[string]string{".wasm": "application/wasm"}
	opt := WithMIMETypes(mimeTypes)
	mimeTypes[".wasm"] = "application/octet-stream"

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, opt); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/wasm") {
		t.Fatalf("Content-Type: got %q, want application/wasm", got)
	}
}

func TestCustomMIMETypesRejectUnsafeValues(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	err := RegisterFromDir(r, dir, WithMIMETypes(map[string]string{
		".wasm": "application/wasm\r\nX-Bad: yes",
	}))
	assertErrorContains(t, err, "MIME type")
}

func TestCustomMIMETypesRejectInvalidExtensions(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	tests := []string{
		"",
		".",
		".tar.gz",
		"image/svg+xml",
		`bad\key`,
		"bad key",
		"bad\nkey",
	}

	for _, ext := range tests {
		t.Run(ext, func(t *testing.T) {
			r := router.NewRouter()
			err := RegisterFromDir(r, dir, WithMIMETypes(map[string]string{
				ext: "application/test",
			}))
			assertErrorContains(t, err, "MIME type extension")
		})
	}
}
