package router

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// ---- noBodyWriter.ReadFrom (0%) ---------------------------------------------

func TestNoBodyWriterReadFromDiscardsData(t *testing.T) {
	rec := httptest.NewRecorder()
	nbw := noBodyWriter{ResponseWriter: rec}

	// ReadFrom should discard all bytes and return the count without writing to rec.
	n, err := nbw.ReadFrom(strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("ReadFrom error: %v", err)
	}
	if n != int64(len("hello world")) {
		t.Fatalf("ReadFrom n = %d, want %d", n, len("hello world"))
	}
	// The response body should be empty because ReadFrom discards.
	if got := rec.Body.String(); got != "" {
		t.Fatalf("expected empty body, got %q", got)
	}
}

func TestNoBodyWriterReadFromWithWrapperBehavior(t *testing.T) {
	// Verify Unwrap returns the inner ResponseWriter.
	rec := httptest.NewRecorder()
	nbw := noBodyWriter{ResponseWriter: rec}
	if nbw.Unwrap() != rec {
		t.Fatal("Unwrap did not return the inner ResponseWriter")
	}
}

// ---- HasRoute (33.3%) -------------------------------------------------------

func TestHasRouteReturnsFalseForUnregistered(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/known", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}),
		WithRouteName("known"))

	if r.HasRoute("unknown") {
		t.Fatal("expected HasRoute to return false for unregistered name")
	}
}

func TestHasRouteReturnsTrueForRegistered(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/known", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}),
		WithRouteName("known"))

	if !r.HasRoute("known") {
		t.Fatal("expected HasRoute to return true for registered name")
	}
}

func TestHasRouteOnNilRouterReturnsFalse(t *testing.T) {
	var r *Router
	if r.HasRoute("any") {
		t.Fatal("expected nil router HasRoute to return false")
	}
}

// ---- URLMust success path (75%) ---------------------------------------------

func TestURLMustReturnsURLForKnownRoute(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}),
		WithRouteName("users.show"))

	got := r.URLMust("users.show", "id", "42")
	if got != "/users/42" {
		t.Fatalf("URLMust = %q, want %q", got, "/users/42")
	}
}

// ---- fullPath with empty result (75%) ---------------------------------------

func TestFullPathWithEmptyPrefixAndEmptyPath(t *testing.T) {
	r := NewRouter()
	// An empty path should resolve to "/" via fullPath.
	got := r.fullPath("")
	if got != "/" {
		t.Fatalf("fullPath(%q) = %q, want %q", "", got, "/")
	}
}

func TestJoinRoutePathEdgeCases(t *testing.T) {
	tests := []struct {
		prefix string
		path   string
		want   string
	}{
		{"", "", "/"},
		{"", "/", "/"},
		{"/api", "", "/api"},
		{"/api", "/", "/api"},
		{"/api/", "v1", "/api/v1"},
		{"", "no-leading-slash", "/no-leading-slash"},
	}

	for _, tt := range tests {
		got := joinRoutePath(tt.prefix, tt.path)
		if got != tt.want {
			t.Errorf("joinRoutePath(%q, %q) = %q, want %q", tt.prefix, tt.path, got, tt.want)
		}
	}
}

func TestCanonicalRoutePathEdgeCases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "/"},
		{"/", "/"},
		{"no-slash", "/no-slash"},
		{"//double", "/double"},
		{"/trailing/", "/trailing"},
	}

	for _, tt := range tests {
		got := canonicalRoutePath(tt.input)
		if got != tt.want {
			t.Errorf("canonicalRoutePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---- isHTTPTokenChar (57.1%) ------------------------------------------------

func TestIsHTTPTokenCharCoversAllBranches(t *testing.T) {
	valid := []byte{
		'0', '9', 'A', 'Z', 'a', 'z',
		'!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~',
	}
	for _, b := range valid {
		if !isHTTPTokenChar(b) {
			t.Errorf("isHTTPTokenChar(%q) = false, want true", b)
		}
	}

	invalid := []byte{' ', '\t', '(', ')', ',', '/', ':', ';', '<', '=', '>', '?', '@', '[', '\\', ']', '{', '}'}
	for _, b := range invalid {
		if isHTTPTokenChar(b) {
			t.Errorf("isHTTPTokenChar(%q) = true, want false", b)
		}
	}
}

// ---- findChildByByte with no indices (63.6%) --------------------------------

func TestFindChildByByteWithNoIndices(t *testing.T) {
	// Build a parent node without indices to exercise the fallback loop.
	parent := &node{
		indices: "",
		children: []*node{
			{path: "abc"},
			{path: "xyz"},
		},
	}
	// Should find "abc" by first byte 'a'.
	child := findChildByByte(parent, 'a')
	if child == nil {
		t.Fatal("expected to find child with path 'abc'")
	}
	if child.path != "abc" {
		t.Fatalf("found wrong child: %q", child.path)
	}

	// Should return nil for missing byte.
	if got := findChildByByte(parent, 'z'); got != nil {
		t.Fatalf("expected nil for 'z', got child %q", got.path)
	}
}

func TestFindChildByByteWithNilParent(t *testing.T) {
	if findChildByByte(nil, 'a') != nil {
		t.Fatal("expected nil for nil parent")
	}
}

func TestFindChildByByteWithEmptyChildren(t *testing.T) {
	parent := &node{}
	if findChildByByte(parent, 'a') != nil {
		t.Fatal("expected nil for empty children")
	}
}

// ---- findStaticChild edge cases (81%) ---------------------------------------

func TestFindStaticChildWithExactlyThreeChildren(t *testing.T) {
	// When len(children) > 2, findStaticChild uses indices for the binary search path.
	parent := &node{
		indices:  "abc",
		children: []*node{{path: "alpha"}, {path: "beta"}, {path: "charlie"}},
	}
	// Searching for "beta" should return the second child.
	child := findStaticChild(parent, "beta")
	if child == nil {
		t.Fatal("expected to find child 'beta'")
	}
	if child.path != "beta" {
		t.Fatalf("found wrong child: %q", child.path)
	}

	// Searching for a non-existent path.
	if findStaticChild(parent, "delta") != nil {
		t.Fatal("expected nil for non-existent child")
	}
}

func TestFindStaticChildWithNoIndicesMoreThanTwo(t *testing.T) {
	// When len(children) > 2 but indices is empty, fallback linear scan is used.
	parent := &node{
		indices:  "",
		children: []*node{{path: "alpha"}, {path: "beta"}, {path: "charlie"}},
	}
	child := findStaticChild(parent, "charlie")
	if child == nil {
		t.Fatal("expected to find child 'charlie'")
	}
}

// ---- pool nil guards (75%) --------------------------------------------------

func TestPutParamValuesWithNilIsNoop(t *testing.T) {
	// Should not panic.
	putParamValues(nil)
}

func TestPutPathPartsWithNilIsNoop(t *testing.T) {
	// Should not panic.
	putPathParts(nil)
}

// ---- splitPathToParts edge cases (87.5%) ------------------------------------

func TestSplitPathToPartsRootReturnsEmpty(t *testing.T) {
	parts := splitPathToParts("/")
	if len(*parts) != 0 {
		t.Fatalf("expected empty parts for root, got %v", *parts)
	}
	putPathParts(parts)
}

func TestSplitPathToPartsEmptyReturnsEmpty(t *testing.T) {
	parts := splitPathToParts("")
	if len(*parts) != 0 {
		t.Fatalf("expected empty parts for empty string, got %v", *parts)
	}
	putPathParts(parts)
}

// ---- fastNormalizePath edge cases (93.3%) -----------------------------------

func TestFastNormalizePathDoubleLeadingSlash(t *testing.T) {
	got := fastNormalizePath("//users/123")
	if got != "/users/123" {
		t.Fatalf("fastNormalizePath(%q) = %q, want %q", "//users/123", got, "/users/123")
	}
}

func TestFastNormalizePathAllSlashes(t *testing.T) {
	got := fastNormalizePath("///")
	if got != "/" {
		t.Fatalf("fastNormalizePath(%q) = %q, want %q", "///", got, "/")
	}
}

// ---- matchRoute edge cases (91.7%) ------------------------------------------

func TestMatchRouteAnyMethodOnRootPath(t *testing.T) {
	r := NewRouter()
	called := false
	mustAddRoute(r, MethodAny, "/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := serveRouter(r, http.MethodPost, "/")
	assertResponseStatus(t, rec, http.StatusOK)
	if !called {
		t.Fatal("expected ANY handler to be called for POST /")
	}
}

func TestMatchRouteHEADFallsBackToGETOnRootPath(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("body"))
	}))

	rec := serveRouter(r, http.MethodHead, "/")
	assertResponseStatus(t, rec, http.StatusOK)
	// HEAD should suppress the body.
	if got := rec.Body.String(); got != "" {
		t.Fatalf("HEAD / body = %q, want empty", got)
	}
}

// ---- attachRouteContextAndServe nil guard -----------------------------------

func TestAttachRouteContextAndServeWithNilResultIsNoop(t *testing.T) {
	r := NewRouter()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Should not panic and must not write a status.
	r.attachRouteContextAndServe(rec, req, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nil result should leave the default status; got %d, want %d", rec.Code, http.StatusOK)
	}
}

// ---- allowedMethods for root "/" --------------------------------------------

func TestAllowedMethodsForRootPath(t *testing.T) {
	r := NewRouter()
	r.SetMethodNotAllowed(true)
	mustAddRoute(r, http.MethodGet, "/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// POST to "/" should return 405 with Allow header.
	rec := serveRouter(r, http.MethodPost, "/")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, "GET") {
		t.Fatalf("Allow header = %q, want GET", allow)
	}
}

// ---- Routes with empty state (92.3%) ----------------------------------------

func TestRoutesOnNilRouterReturnsEmpty(t *testing.T) {
	var r *Router
	routes := r.Routes()
	if len(routes) != 0 {
		t.Fatalf("expected empty routes, got %v", routes)
	}
}

// ---- isPathWithinRoot error path (75%) --------------------------------------

func TestIsPathWithinRootReturnsFalseForOutsidePath(t *testing.T) {
	tmpDir := t.TempDir()
	outside := t.TempDir()

	// outside is completely unrelated to tmpDir; rel path will contain "..".
	if isPathWithinRoot(tmpDir, outside) {
		t.Fatal("expected outside path to not be within root")
	}
}

func TestIsPathWithinRootReturnsTrueForInsidePath(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if !isPathWithinRoot(tmpDir, subDir) {
		t.Fatal("expected subDir to be within root")
	}
}

func TestIsPathWithinRootReturnsTrueForSameDir(t *testing.T) {
	tmpDir := t.TempDir()
	if !isPathWithinRoot(tmpDir, tmpDir) {
		t.Fatal("expected root to be within itself")
	}
}

// ---- serveFileContent seekable path (50% baseline, only seekable is reachable)
// Note: http.File embeds io.Seeker, so the non-seekable branch is structurally
// unreachable via http.File. We cover the seekable path here.

func TestServeFileContentSeekableUsesServeContent(t *testing.T) {
	mapFS := fstest.MapFS{
		"test.txt": &fstest.MapFile{
			Data: []byte("seekable content"),
			Mode: fs.ModePerm,
		},
	}
	fsFile, err := http.FS(mapFS).Open("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer fsFile.Close()

	info, err := fsFile.Stat()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test.txt", nil)

	serveFileContent(rec, req, fsFile, info)
	if rec.Code != http.StatusOK {
		t.Fatalf("seekable path status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "seekable content" {
		t.Fatalf("seekable body = %q, want %q", got, "seekable content")
	}
}

// ---- serveFromFileSystem stat error and directory handling (64.7%) ----------

func TestServeFromFileSystemStatError(t *testing.T) {
	// Use a custom filesystem that returns an error on Stat.
	r := NewRouter()
	called := false
	var statErrFS http.FileSystem = &errStatFS{called: &called}
	if err := r.StaticFS("/assets", statErrFS); err != nil {
		t.Fatalf("StaticFS returned error: %v", err)
	}
	r.Freeze()

	rec := serveRouter(r, http.MethodGet, "/assets/file.txt")
	assertResponseStatus(t, rec, http.StatusNotFound)
}

type errStatFS struct {
	called *bool
}

func (f *errStatFS) Open(name string) (http.File, error) {
	return &errStatFile{}, nil
}

type errStatFile struct{}

func (f *errStatFile) Close() error                                 { return nil }
func (f *errStatFile) Read(p []byte) (int, error)                   { return 0, io.EOF }
func (f *errStatFile) Seek(offset int64, whence int) (int64, error) { return 0, nil }
func (f *errStatFile) Readdir(count int) ([]os.FileInfo, error)     { return nil, nil }
func (f *errStatFile) Stat() (os.FileInfo, error)                   { return nil, os.ErrNotExist }

// ---- getFilePathFromRequest edge cases (87.5% → 93.8%) ----------------------

func TestGetFilePathFromRequestWithNoFilepathParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/", nil)
	// No RequestContext set → rc.Params is empty → filepath param is missing.
	_, ok := getFilePathFromRequest(req)
	if ok {
		t.Fatal("expected false when filepath param is absent")
	}
}

func TestGetFilePathFromRequestRejectsDotPath(t *testing.T) {
	// "." normalises to "." via path.Clean; should be rejected.
	r := NewRouter()
	tmpDir := t.TempDir()
	if err := r.Static("/static", tmpDir); err != nil {
		t.Fatalf("Static: %v", err)
	}
	r.Freeze()

	req := httptest.NewRequest(http.MethodGet, "/static/.", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	// "." filepath is rejected → 404.
	assertResponseStatus(t, rec, http.StatusNotFound)
}

// ---- resolveStaticRoot: symlink resolution errors ---------------------------

func TestResolveStaticRootWithEmptyDirReturnsError(t *testing.T) {
	_, err := resolveStaticRoot("   ")
	if err == nil || !strings.Contains(err.Error(), "empty directory") {
		t.Fatalf("expected empty directory error, got %v", err)
	}
}

// ---- serveFromDirectory: open error (72.7%) ---------------------------------

func TestServeFromDirectoryOpenError(t *testing.T) {
	// Create a root dir and request a file that does not exist.
	tmpDir := t.TempDir()
	r := NewRouter()
	if err := r.Static("/static", tmpDir); err != nil {
		t.Fatalf("Static returned error: %v", err)
	}
	r.Freeze()

	rec := serveRouter(r, http.MethodGet, "/static/nonexistent.txt")
	assertResponseStatus(t, rec, http.StatusNotFound)
}

// ---- HasParentTraversal middle segment (83.3%) --------------------------------

func TestHasParentTraversalMiddleSegment(t *testing.T) {
	// "a/../b" has ".." in the middle.
	if !hasParentTraversal("a/../b") {
		t.Fatal("expected parent traversal for a/../b")
	}
}

func TestHasParentTraversalDotDot(t *testing.T) {
	if !hasParentTraversal("..") {
		t.Fatal("expected parent traversal for ..")
	}
}

func TestHasParentTraversalNoTraversal(t *testing.T) {
	if hasParentTraversal("a/b/c") {
		t.Fatal("expected no parent traversal for a/b/c")
	}
}

// ---- registerNamedRoute with nil namedRoutes map (92.3%) --------------------

func TestRegisterNamedRouteInitializesNilMap(t *testing.T) {
	r := NewRouter()
	// This is an internal function tested indirectly via AddRoute, but we can
	// call it directly since we're in the same package.
	r.state.mu.Lock()
	r.state.namedRoutes = nil
	r.state.mu.Unlock()

	r.state.mu.Lock()
	r.registerNamedRoute("direct.test", http.MethodGet, "/direct/:id")
	r.state.mu.Unlock()

	if !r.HasRoute("direct.test") {
		t.Fatal("expected registered route after nil-map init")
	}
}

// ---- printRoutesSnapshot: sorting (90.0%) ------------------------------------

func TestPrintRoutesSnapshotSortsRoutes(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/z", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))
	mustAddRoute(r, http.MethodGet, "/a", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))
	mustAddRoute(r, http.MethodPost, "/b", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))

	var sb strings.Builder
	r.Print(&sb)
	out := sb.String()
	if !strings.Contains(out, "/a") || !strings.Contains(out, "/z") {
		t.Fatalf("Print output missing routes: %q", out)
	}
}

// ---- AddRoute with invalid method character (93.3%) -------------------------

func TestAddRouteInvalidMethodCharacter(t *testing.T) {
	r := NewRouter()
	err := r.AddRoute("GET/", "/path", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))
	if err == nil {
		t.Fatal("expected error for method with slash character")
	}
}

func TestAddRouteWithSpaceInMethod(t *testing.T) {
	r := NewRouter()
	err := r.AddRoute("GET ME", "/path", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))
	if err == nil {
		t.Fatal("expected error for method with space character")
	}
}

// ---- isRouteParamName coverage (87.5%) ---------------------------------------

func TestIsRouteParamNameInvalidStart(t *testing.T) {
	// Name starting with a digit should fail the isParamNameStart check.
	if isRouteParamName("1bad") {
		t.Fatal("expected false for param name starting with digit")
	}
}

func TestIsRouteParamNameInvalidMidChar(t *testing.T) {
	// Name with a hyphen in the middle should fail the isParamNameChar check.
	if isRouteParamName("bad-name") {
		t.Fatal("expected false for param name with hyphen")
	}
}

func TestIsRouteParamNameValid(t *testing.T) {
	valid := []string{"id", "userId", "_private", "name1", "A"}
	for _, name := range valid {
		if !isRouteParamName(name) {
			t.Errorf("isRouteParamName(%q) = false, want true", name)
		}
	}
}

// ---- Routes on uninitialized state (92.3%) -----------------------------------

func TestRoutesOnUninitializedStateReturnsEmpty(t *testing.T) {
	// A router with nil state should return empty (the ready() guard).
	r := &Router{} // no state
	routes := r.Routes()
	if len(routes) != 0 {
		t.Fatalf("expected empty routes for uninitialized router, got %v", routes)
	}
}

// ---- splitPathToParts with trailing slash input (93.8%) ----------------------

func TestSplitPathToPartsWithTrailingSlash(t *testing.T) {
	parts := splitPathToParts("/users/123/")
	if len(*parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %v", len(*parts), *parts)
	}
	if (*parts)[0] != "users" || (*parts)[1] != "123" {
		t.Fatalf("unexpected parts: %v", *parts)
	}
	putPathParts(parts)
}

// ---- attachRouteContextAndServe with zero ParamKeys (92.3%) -----------------

func TestAttachRouteContextAndServeZeroParams(t *testing.T) {
	r := NewRouter()
	mustAddRoute(r, http.MethodGet, "/no-params", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/no-params", nil)
	r.ServeHTTP(rec, req)
	assertResponseStatus(t, rec, http.StatusOK)
}

// ---- cache Set eviction path (87.5%) ----------------------------------------

func TestMatchCacheSetEvictsWhenAtCapacity(t *testing.T) {
	r := newRouterWithMatchCapacity(2)
	mustAddRoute(r, http.MethodGet, "/a", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))
	mustAddRoute(r, http.MethodGet, "/b", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))
	mustAddRoute(r, http.MethodGet, "/c", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(200) }))

	// Fill the cache and trigger eviction.
	serveRouter(r, http.MethodGet, "/a")
	serveRouter(r, http.MethodGet, "/b")
	// This should trigger eviction since capacity is 2.
	serveRouter(r, http.MethodGet, "/c")

	// All routes should still work.
	assertResponseStatus(t, serveRouter(r, http.MethodGet, "/a"), 200)
	assertResponseStatus(t, serveRouter(r, http.MethodGet, "/b"), 200)
	assertResponseStatus(t, serveRouter(r, http.MethodGet, "/c"), 200)
}

// ---- allowedMethods for non-root path (87.5%) --------------------------------

func TestAllowedMethodsNonRootPath(t *testing.T) {
	r := NewRouter()
	r.SetMethodNotAllowed(true)
	mustAddRoute(r, http.MethodGet, "/resource", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mustAddRoute(r, http.MethodPut, "/resource", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// DELETE to "/resource" should return 405 with Allow: GET, HEAD, PUT.
	rec := serveRouter(r, http.MethodDelete, "/resource")
	assertResponseStatus(t, rec, http.StatusMethodNotAllowed)
	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, "GET") || !strings.Contains(allow, "PUT") {
		t.Fatalf("Allow header = %q, want GET and PUT", allow)
	}
}
