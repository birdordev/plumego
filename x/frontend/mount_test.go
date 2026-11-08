package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/birdordev/plumego/router"
)

func TestRegisterFromDir(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>home</html>")
	writeTestFile(t, dir, "_next/static/app.js", "console.log('hello')")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithCacheControl("public, max-age=31536000")); err != nil {
		t.Fatalf("register: %v", err)
	}

	cases := []struct {
		name         string
		method       string
		path         string
		expectStatus int
		expectBody   string
	}{
		{name: "asset", method: http.MethodGet, path: "/_next/static/app.js", expectStatus: http.StatusOK, expectBody: "console.log('hello')"},
		{name: "home", method: http.MethodGet, path: "/", expectStatus: http.StatusOK, expectBody: "<html>home</html>"},
		{name: "fallback", method: http.MethodGet, path: "/missing/page", expectStatus: http.StatusOK, expectBody: "<html>home</html>"},
		{name: "invalid method", method: http.MethodPost, path: "/", expectStatus: http.StatusMethodNotAllowed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != tc.expectStatus {
				t.Fatalf("unexpected status: got %d want %d", rec.Code, tc.expectStatus)
			}

			if tc.expectBody != "" {
				assertBodyContains(t, rec, tc.expectBody)
			}

			if tc.name == "asset" {
				head := rec.Header().Get("Cache-Control")
				if head != "public, max-age=31536000" {
					t.Fatalf("cache control not applied: %q", head)
				}
			}
		})
	}
}

func TestRegisterWithPrefix(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "app shell")
	writeTestFile(t, dir, "assets/logo.png", "png")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithPrefix("/app")); err != nil {
		t.Fatalf("register: %v", err)
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/app/assets/logo.png", nil)
	assetRec := httptest.NewRecorder()
	r.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("asset status: %d", assetRec.Code)
	}

	rootReq := httptest.NewRequest(http.MethodGet, "/app", nil)
	rootRec := httptest.NewRecorder()
	r.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("root status: %d", rootRec.Code)
	}
	assertBodyContains(t, rootRec, "app shell")
}

func TestNewMountFSRegister(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "mount index")
	writeTestFile(t, dir, "assets/app.js", "mount asset")

	mount, err := NewMountFS(http.Dir(dir), WithPrefix("/app"))
	if err != nil {
		t.Fatalf("new mount: %v", err)
	}
	if got := mount.Prefix(); got != "/app" {
		t.Fatalf("prefix: got %q want %q", got, "/app")
	}
	if mount.Handler() == nil {
		t.Fatal("expected mount handler")
	}

	r := router.NewRouter()
	if err := mount.Register(r); err != nil {
		t.Fatalf("register mount: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app/assets/app.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status: got %d want %d", rec.Code, http.StatusOK)
	}
	assertBodyContains(t, rec, "mount asset")
}

func TestNewMountFSAppliesOptionsOnce(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	applied := 0
	_, err := NewMountFS(http.Dir(dir), func(cfg *config) {
		applied++
		cfg.Prefix = "/app"
	})
	if err != nil {
		t.Fatalf("new mount: %v", err)
	}
	if applied != 1 {
		t.Fatalf("option applied %d times, want 1", applied)
	}
}

func TestNewHandlerFS(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "handler index")

	h, err := NewHandlerFS(http.Dir(dir))
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusOK)
	}
	assertBodyContains(t, rec, "handler index")
}

func TestMountRegisterNilRouter(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	mount, err := NewMountFS(http.Dir(dir))
	if err != nil {
		t.Fatalf("new mount: %v", err)
	}
	if err := mount.Register(nil); err == nil {
		t.Fatal("expected error for nil router")
	}
}

func TestMountRegisterPreflightsDuplicateSnapshotRoutes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	if err := r.AddRoute(methodAny, "/*filepath", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err != nil {
		t.Fatalf("seed route: %v", err)
	}

	mount, err := NewMountFromDir(dir)
	if err != nil {
		t.Fatalf("new mount: %v", err)
	}
	err = mount.Register(r)
	assertErrorContains(t, err, "already registered")

	for _, route := range r.Routes() {
		if route.Method == methodAny && route.Path == "/" {
			t.Fatalf("root route was partially registered: %#v", r.Routes())
		}
	}
}

type recordingRegistrar struct {
	routes []routeSpec
}

func (r *recordingRegistrar) AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error {
	r.routes = append(r.routes, routeSpec{method: method, path: path})
	return nil
}

func TestMountRegisterAddRouteOnlyRegistrar(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	mount, err := NewMountFromDir(dir, WithPrefix("/app"))
	if err != nil {
		t.Fatalf("new mount: %v", err)
	}
	registrar := &recordingRegistrar{}
	if err := mount.Register(registrar); err != nil {
		t.Fatalf("register: %v", err)
	}

	want := []routeSpec{
		{method: methodAny, path: "/app/*filepath"},
		{method: methodAny, path: "/app"},
	}
	if len(registrar.routes) != len(want) {
		t.Fatalf("routes = %#v, want %#v", registrar.routes, want)
	}
	for i := range want {
		if registrar.routes[i] != want[i] {
			t.Fatalf("route[%d] = %#v, want %#v", i, registrar.routes[i], want[i])
		}
	}
}

func TestRegisterFromDirMissing(t *testing.T) {
	r := router.NewRouter()
	if err := RegisterFromDir(r, "./does/not/exist"); err == nil {
		t.Fatalf("expected error for missing directory")
	} else if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "no such file") {
		t.Logf("error message: %v", err)
	}
}

func TestRegisterFromDirMissingIndexFailsFast(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "asset.js", "asset")

	r := router.NewRouter()
	err := RegisterFromDir(r, dir)
	assertErrorContains(t, err, "index")
}

func TestRegisterFromDirRelativePathSurvivesChdir(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	parent := t.TempDir()
	dist := filepath.Join(parent, "dist")
	writeTestFile(t, dist, "index.html", "index")
	writeTestFile(t, dist, "asset.js", "asset")
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir parent: %v", err)
	}

	r := router.NewRouter()
	if err := RegisterFromDir(r, "dist", WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	otherDir := t.TempDir()
	if err := os.Chdir(otherDir); err != nil {
		t.Fatalf("chdir other: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/asset.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertBodyContains(t, rec, "asset")
}

// TestRegisterFS_HTTPFileSystem verifies that RegisterFS works with http.Dir.
func TestRegisterFS_HTTPFileSystem(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "embedded home")
	writeTestFile(t, dir, "bundle.js", "console.log('ok')")

	r := router.NewRouter()
	if err := RegisterFS(r, http.Dir(dir)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/bundle.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	assertBodyContains(t, rec, "console.log('ok')")
}

func TestRegisterFSHTTPDirMissingIndexFailsFast(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "bundle.js", "asset")

	r := router.NewRouter()
	err := RegisterFS(r, http.Dir(dir))
	assertErrorContains(t, err, "index")
}

func TestRegisterFSHTTPDirRelativePathSurvivesChdir(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	parent := t.TempDir()
	dist := filepath.Join(parent, "dist")
	writeTestFile(t, dist, "index.html", "index")
	writeTestFile(t, dist, "asset.js", "asset")
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir parent: %v", err)
	}

	r := router.NewRouter()
	if err := RegisterFS(r, http.Dir("dist"), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir other: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/asset.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertBodyContains(t, rec, "asset")
}

func TestRegisterFSCustomFilesystemRemainsLazy(t *testing.T) {
	r := router.NewRouter()
	if err := RegisterFS(r, http.FS(fstest.MapFS{})); err != nil {
		t.Fatalf("register custom fs: %v", err)
	}
}

func TestRegisterFSCustomFilesystemPrecompressedRemainsLazy(t *testing.T) {
	r := router.NewRouter()
	if err := RegisterFS(r, http.FS(fstest.MapFS{}), WithPrecompressed(true)); err != nil {
		t.Fatalf("register custom fs: %v", err)
	}
}

// TestRegisterFS_NestedDirectories verifies that files in nested subdirectories
// are served correctly when using RegisterFS.
func TestRegisterFS_NestedDirectories(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "assets/style.css", "css")

	r := router.NewRouter()
	if err := RegisterFS(r, http.Dir(dir)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/style.css", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("nested asset failed: %d", rec.Code)
	}
	assertBodyContains(t, rec, "css")
}

func TestInvalidPrefixes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	invalidPrefixes := []string{
		"/app/../",
		"/app/./",
	}

	for _, prefix := range invalidPrefixes {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			r := router.NewRouter()
			err := RegisterFromDir(r, dir, WithPrefix(prefix))
			if err == nil {
				t.Fatalf("expected error for prefix %q, got nil", prefix)
			}
		})
	}

	validPrefixes := []string{
		"",
		"app",
		"app/",
		"/app..v2",
	}

	for _, prefix := range validPrefixes {
		t.Run("valid_prefix_"+prefix, func(t *testing.T) {
			r := router.NewRouter()
			err := RegisterFromDir(r, dir, WithPrefix(prefix))
			if err != nil {
				t.Fatalf("prefix %q should work: %v", prefix, err)
			}
		})
	}
}

func TestIndexFileValidation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()

	invalidIndexes := []string{
		"../index.html",
		"index/../index",
		"",
	}

	for _, index := range invalidIndexes {
		t.Run("index_"+index, func(t *testing.T) {
			err := RegisterFromDir(r, dir, WithIndex(index))
			if index == "" {
				if err != nil {
					t.Fatalf("empty index should work: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for index %q, got nil", index)
			}
		})
	}
}

func TestNilFilesystem(t *testing.T) {
	r := router.NewRouter()
	err := RegisterFS(r, nil)
	assertErrorContains(t, err, "filesystem cannot be nil")
}

func TestUnreadableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows due to permission model differences")
	}
	if os.Getuid() == 0 {
		t.Skip("skipping test when running as root")
	}

	dir := t.TempDir()
	subDir := filepath.Join(dir, "unreadable")
	if err := os.MkdirAll(subDir, 0o000); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer func() { _ = os.Chmod(subDir, 0o755) }()

	r := router.NewRouter()
	err := RegisterFromDir(r, subDir)
	assertErrorContains(t, err, "not readable")
}

func TestNonDirectoryPath(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notadir")
	if err := os.WriteFile(filePath, []byte("file"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	r := router.NewRouter()
	err := RegisterFromDir(r, filePath)
	assertErrorContains(t, err, "not a directory")
}

func TestPrefixNormalization(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	tests := []struct {
		input     string
		shouldErr bool
	}{
		{"/", false},
		{"/app", false},
		{"/app/", false},
		{"app", false},
		{"app/", false},
		{"/app/../", true},
		{"/app/./", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r := router.NewRouter()
			err := RegisterFromDir(r, dir, WithPrefix(tt.input))

			if tt.shouldErr {
				if err == nil {
					t.Fatalf("expected error for prefix %q, got nil", tt.input)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for prefix %q: %v", tt.input, err)
				}
			}
		})
	}
}
