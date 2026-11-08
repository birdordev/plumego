package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/birdordev/plumego/router"
)

// Test security and edge cases
func TestSecurityPathTraversal(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "secret/secret.txt", "secret data")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir); err != nil {
		t.Fatalf("register: %v", err)
	}

	traversalPaths := []string{
		"/../secret/secret.txt",
		"/../../secret.txt",
		"/../../../etc/passwd",
		"/%2e%2e%2fsecret%2fsecret.txt",
		"/%2e%2e/",
	}

	for _, path := range traversalPaths {
		t.Run("traversal_"+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "secret data") {
				t.Fatalf("path traversal succeeded: %s", path)
			}
		})
	}
}

func TestDirectorySymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping symlink test on Windows")
	}

	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, root, "index.html", "index")
	writeTestFile(t, outside, "secret.txt", "outside secret")

	linkPath := filepath.Join(root, "linked")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := router.NewRouter()
	if err := RegisterFromDir(r, root, WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/linked/secret.txt", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "outside secret") {
		t.Fatalf("symlink escape served outside file: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestRegisterFSHTTPDirSymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping symlink test on Windows")
	}

	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, root, "index.html", "index")
	writeTestFile(t, outside, "secret.txt", "outside secret")

	linkPath := filepath.Join(root, "linked")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := router.NewRouter()
	if err := RegisterFS(r, http.Dir(root), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/linked/secret.txt", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "outside secret") {
		t.Fatalf("symlink escape served outside file: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestDottedFilenamesAreNotTraversal(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "assets/app..v1.2.3.js", "dotted asset")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/app..v1.2.3.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("dotted file status: got %d want %d", rec.Code, http.StatusOK)
	}
	assertBodyContains(t, rec, "dotted asset")
}

func TestBackslashPathRejected(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "secret.txt", "secret data")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, `/..\secret.txt`, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "secret data") {
		t.Fatalf("backslash traversal served file: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestUnsafePathsDoNotFallbackToIndex(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithFallback(true)); err != nil {
		t.Fatalf("register: %v", err)
	}

	paths := []string{
		"/%2e%2e/secret.txt",
		`/..\secret.txt`,
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("unsafe path status: got %d want %d body=%q", rec.Code, http.StatusNotFound, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "index") {
				t.Fatalf("unsafe path returned index body: %q", rec.Body.String())
			}
		})
	}
}

func TestSpecialCharactersInFilenames(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"index.html":            "index",
		"file with spaces.html": "spaces",
		"file-with-dash.html":   "dash",
		"file_with_underscore":  "underscore",
		"file.multiple.dots.js": "dots",
	}

	for filename, content := range files {
		writeTestFile(t, dir, filename, content)
	}

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir); err != nil {
		t.Fatalf("register: %v", err)
	}

	for filename, content := range files {
		t.Run(filename, func(t *testing.T) {
			path := "/" + filename
			if strings.Contains(filename, " ") {
				path = "/file%20with%20spaces.html"
			}

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("failed to serve %s: %d", filename, rec.Code)
			}
			assertBodyContains(t, rec, content)
		})
	}
}

func TestDirectoryTraversalWithSubdirs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "assets/js/app.js", "app code")
	writeTestFile(t, dir, "index.html", "index")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/js/app.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("nested path failed: %d", rec.Code)
	}
	assertBodyContains(t, rec, "app code")

	req = httptest.NewRequest(http.MethodGet, "/assets/js/", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("directory access failed: %d", rec.Code)
	}
}

type recordingFS struct {
	base   http.FileSystem
	opened []string
}

func (f *recordingFS) Open(name string) (http.File, error) {
	f.opened = append(f.opened, name)
	return f.base.Open(name)
}

func TestRegisterFSRejectsUnsafePathBeforeBackendOpen(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	fsys := &recordingFS{base: http.Dir(dir)}
	r := router.NewRouter()
	if err := RegisterFS(r, fsys, WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/%2e%2e/secret.txt", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusNotFound)
	}
	if len(fsys.opened) != 0 {
		t.Fatalf("backend opened paths = %#v, want none", fsys.opened)
	}
}

func TestLocalDirFSRootBoundary(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeTestFile(t, root, "asset.txt", "safe")
	writeTestFile(t, outside, "asset.txt", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if err := os.Symlink("asset.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	fsys := localDirFS(root)
	h := &handler{fs: fsys}
	if h.localFileExists("escape/asset.txt") || !h.localFileExists("asset.txt") {
		t.Fatal("precompressed original check did not enforce the root boundary")
	}
	for _, name := range []string{"../asset.txt", "escape/asset.txt", "/asset.txt", `escape\asset.txt`} {
		if f, err := fsys.Open(name); err == nil {
			_ = f.Close()
			t.Fatalf("opened file outside the root boundary: %q", name)
		}
	}
	f, err := fsys.Open("alias.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}
