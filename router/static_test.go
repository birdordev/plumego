package router

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/birdordev/plumego/contract"
)

// helper: create a temporary file with given content
func createTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	fullPath := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	return fullPath
}

func TestStatic(t *testing.T) {
	tmpDir := t.TempDir()
	// create sample static files
	createTempFile(t, tmpDir, "js/app.js", "console.log('hello');")
	createTempFile(t, tmpDir, "css/style.css", "body{color:red;}")
	createTempFile(t, tmpDir, "safe..name.txt", "safe")

	r := NewRouter()
	if err := r.Static("/static", tmpDir); err != nil {
		t.Fatalf("Static returned error: %v", err)
	}
	r.Freeze()

	// test existing file
	w := serveRouter(r, http.MethodGet, "/static/js/app.js")
	assertResponseStatus(t, w, http.StatusOK)
	assertResponseBody(t, w, "console.log('hello');")

	w = serveRouter(r, http.MethodGet, "/static/safe..name.txt")
	assertResponseStatus(t, w, http.StatusOK)
	assertResponseBody(t, w, "safe")

	// test non-existing file
	w = serveRouter(r, http.MethodGet, "/static/img/logo.png")
	assertResponseStatus(t, w, http.StatusNotFound)
}

func TestStaticNormalizesTrailingSlashPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	createTempFile(t, tmpDir, "js/app.js", "console.log('hello');")

	r := NewRouter()
	if err := r.Static("/static/", tmpDir); err != nil {
		t.Fatalf("Static returned error: %v", err)
	}
	r.Freeze()

	w := serveRouter(r, http.MethodGet, "/static/js/app.js")
	assertResponseStatus(t, w, http.StatusOK)
	assertResponseBody(t, w, "console.log('hello');")

	routes := r.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Path != "/static/*filepath" {
		t.Fatalf("expected normalized static route path %q, got %q", "/static/*filepath", routes[0].Path)
	}
}

func TestStaticRootPrefixRegistersSingleSlashWildcard(t *testing.T) {
	tmpDir := t.TempDir()
	createTempFile(t, tmpDir, "app.js", "console.log('root');")

	r := NewRouter()
	if err := r.Static("/", tmpDir); err != nil {
		t.Fatalf("Static returned error: %v", err)
	}

	routes := r.Routes()
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Path != "/*filepath" {
		t.Fatalf("root static route path = %q, want %q", routes[0].Path, "/*filepath")
	}

	w := serveRouter(r, http.MethodGet, "/app.js")
	assertResponseStatus(t, w, http.StatusOK)
	assertResponseBody(t, w, "console.log('root');")
}

func TestNormalizeStaticPrefix(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{name: "empty", prefix: "", want: "/"},
		{name: "spaces", prefix: "   ", want: "/"},
		{name: "root", prefix: "/", want: "/"},
		{name: "root_repeated", prefix: "///", want: "/"},
		{name: "relative", prefix: "static", want: "/static"},
		{name: "relative_trailing", prefix: "static/", want: "/static"},
		{name: "absolute", prefix: "/static", want: "/static"},
		{name: "absolute_trailing", prefix: "/static/", want: "/static"},
		{name: "nested_trailing", prefix: "/assets/public///", want: "/assets/public"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeStaticPrefix(tt.prefix); got != tt.want {
				t.Fatalf("normalizeStaticPrefix(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestStaticRejectsMissingDirectory(t *testing.T) {
	r := NewRouter()
	err := r.Static("/static", filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatalf("expected missing static directory registration to fail")
	}
}

func TestStaticRejectsFileRoot(t *testing.T) {
	tmpDir := t.TempDir()
	file := createTempFile(t, tmpDir, "not-dir.txt", "content")

	r := NewRouter()
	err := r.Static("/static", file)
	if err == nil {
		t.Fatalf("expected file static root registration to fail")
	}
}

func TestStaticPrefixValidationPrecedesDirectoryResolution(t *testing.T) {
	r := NewRouter()
	err := r.Static("/bad//prefix", filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "empty path segment") {
		t.Fatalf("invalid Static prefix error = %v, want empty path segment", err)
	}
	if err != nil && strings.Contains(err.Error(), "stat directory") {
		t.Fatalf("invalid Static prefix resolved filesystem before route validation: %v", err)
	}
}

func TestStaticLifecyclePrecedesDirectoryResolution(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "missing")

	r := NewRouter()
	r.Freeze()
	err := r.Static("/static", missingDir)
	if err == nil || !strings.Contains(err.Error(), "router is frozen") {
		t.Fatalf("frozen Static error = %v, want frozen", err)
	}

	var nilRouter *Router
	err = nilRouter.Static("/static", missingDir)
	if err == nil || !strings.Contains(err.Error(), "router is not initialized") {
		t.Fatalf("nil Static error = %v, want not initialized", err)
	}
}

func TestStaticRejectsDirectoryRequest(t *testing.T) {
	tmpDir := t.TempDir()
	createTempFile(t, tmpDir, "dir/file.txt", "nested")

	r := NewRouter()
	if err := r.Static("/static", tmpDir); err != nil {
		t.Fatalf("Static returned error: %v", err)
	}
	r.Freeze()

	w := serveRouter(r, http.MethodGet, "/static/dir")
	assertResponseStatus(t, w, http.StatusNotFound)

	w = serveRouter(r, http.MethodGet, "/static/dir/file.txt")
	assertResponseStatus(t, w, http.StatusOK)
	assertResponseBody(t, w, "nested")
}

func TestStaticRejectsSymlinkEscape(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := createTempFile(t, outsideDir, "secret.txt", "secret")

	if err := os.Symlink(outsideFile, filepath.Join(tmpDir, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := NewRouter()
	if err := r.Static("/static", tmpDir); err != nil {
		t.Fatalf("Static returned error: %v", err)
	}
	r.Freeze()

	w := serveRouter(r, http.MethodGet, "/static/link.txt")
	assertResponseStatus(t, w, http.StatusNotFound)
}

func TestStaticServesSymlinkInsideRoot(t *testing.T) {
	tmpDir := t.TempDir()
	createTempFile(t, tmpDir, "files/readme.txt", "readme")
	if err := os.Symlink(filepath.Join(tmpDir, "files/readme.txt"), filepath.Join(tmpDir, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := NewRouter()
	if err := r.Static("/static", tmpDir); err != nil {
		t.Fatalf("Static returned error: %v", err)
	}
	r.Freeze()

	w := serveRouter(r, http.MethodGet, "/static/link.txt")
	assertResponseStatus(t, w, http.StatusOK)
	assertResponseBody(t, w, "readme")
}

func TestStaticFS(t *testing.T) {
	r := NewRouter()
	if err := r.StaticFS("/assets", http.FS(fstest.MapFS{
		"app.js": &fstest.MapFile{
			Data: []byte("console.log('embedded');"),
			Mode: fs.ModePerm,
		},
	})); err != nil {
		t.Fatalf("StaticFS returned error: %v", err)
	}
	r.Freeze()

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assertResponseStatus(t, w, http.StatusOK)
	assertResponseBody(t, w, "console.log('embedded');")
}

type staticOpenResult struct {
	file http.File
	err  error
}

func (s staticOpenResult) Open(string) (http.File, error) {
	return s.file, s.err
}

type closeCountingStaticFile struct {
	http.File
	closes  int
	statErr error
}

func (f *closeCountingStaticFile) Stat() (fs.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.File.Stat()
}

func (f *closeCountingStaticFile) Close() error {
	f.closes++
	return f.File.Close()
}

func TestStaticFSClosesFilesFromInjectedOpen(t *testing.T) {
	for _, tc := range []struct {
		name     string
		withFile bool
		err      error
		statErr  error
		status   int
	}{
		{"partial open", true, errors.New("open failed"), nil, http.StatusNotFound},
		{"missing file", false, fs.ErrNotExist, nil, http.StatusNotFound},
		{"stat fails", true, nil, errors.New("stat failed"), http.StatusNotFound},
		{"success", true, nil, nil, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := staticOpenResult{err: tc.err}
			var tracked *closeCountingStaticFile
			if tc.withFile {
				file, err := http.FS(fstest.MapFS{"note.txt": {Data: []byte("hello")}}).Open("note.txt")
				if err != nil {
					t.Fatal(err)
				}
				tracked = &closeCountingStaticFile{File: file, statErr: tc.statErr}
				defer func() {
					if tracked.closes == 0 {
						_ = tracked.Close()
					}
				}()
				source.file = tracked
			}
			r := NewRouter()
			if err := r.StaticFS("/assets", source); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/note.txt", nil))
			if w.Code != tc.status || (tc.status == http.StatusOK && w.Body.String() != "hello") {
				t.Fatalf("status=%d body=%q", w.Code, w.Body)
			}
			if tracked != nil && tracked.closes != 1 {
				t.Fatalf("closed %d times, want once", tracked.closes)
			}
		})
	}
}

func TestStaticFSHandlesEmptyOpenResult(t *testing.T) {
	r := NewRouter()
	if err := r.StaticFS("/assets", staticOpenResult{}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for attempt := range 2 {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/assets/note.txt", nil))
			if w.Code != http.StatusNotFound || (method == http.MethodHead && w.Body.Len() != 0) {
				t.Errorf("%s attempt %d: status=%d body=%q", method, attempt, w.Code, w.Body)
			}
		}
	}
}

func TestStaticFSRejectsNonRegularFiles(t *testing.T) {
	r := NewRouter()
	source := new(staticOpenResult)
	if err := r.StaticFS("/assets", source); err != nil {
		t.Fatal(err)
	}
	files := http.FS(fstest.MapFS{
		"special.txt": {Data: []byte("sample"), Mode: fs.ModeNamedPipe | 0o644},
	})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for attempt := range 2 {
			file, err := files.Open("special.txt")
			if err != nil {
				t.Fatal(err)
			}
			tracked := &closeCountingStaticFile{File: file}
			source.file = tracked
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/assets/special.txt", nil))
			if w.Code != http.StatusNotFound || (method == http.MethodHead && w.Body.Len() != 0) || tracked.closes != 1 {
				t.Errorf("%s attempt %d: status=%d body=%q closes=%d", method, attempt, w.Code, w.Body, tracked.closes)
			}
		}
	}
}

type staticFileWithoutInfo struct{ *closeCountingStaticFile }

func (staticFileWithoutInfo) Stat() (fs.FileInfo, error) { return nil, nil }

func TestStaticFSRejectsMissingFileInfo(t *testing.T) {
	r := NewRouter()
	source := new(staticOpenResult)
	if err := r.StaticFS("/assets", source); err != nil {
		t.Fatal(err)
	}
	files := http.FS(fstest.MapFS{"sample.txt": {Data: []byte("sample")}})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for attempt := range 2 {
			file, err := files.Open("sample.txt")
			if err != nil {
				t.Fatal(err)
			}
			tracked := &closeCountingStaticFile{File: file}
			source.file = staticFileWithoutInfo{tracked}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/assets/sample.txt", nil))
			if w.Code != http.StatusNotFound || (method == http.MethodHead && w.Body.Len() != 0) || tracked.closes != 1 {
				t.Errorf("%s attempt %d: status=%d body=%q closes=%d", method, attempt, w.Code, w.Body, tracked.closes)
			}
		}
	}
}

func TestStaticFSRejectsDirectoryRequest(t *testing.T) {
	r := NewRouter()
	if err := r.StaticFS("/assets", http.FS(fstest.MapFS{
		"dir": &fstest.MapFile{
			Mode: fs.ModeDir,
		},
		"dir/app.js": &fstest.MapFile{
			Data: []byte("console.log('nested');"),
			Mode: fs.ModePerm,
		},
	})); err != nil {
		t.Fatalf("StaticFS returned error: %v", err)
	}
	r.Freeze()

	w := serveRouter(r, http.MethodGet, "/assets/dir")
	assertResponseStatus(t, w, http.StatusNotFound)

	w = serveRouter(r, http.MethodGet, "/assets/dir/app.js")
	assertResponseStatus(t, w, http.StatusOK)
	assertResponseBody(t, w, "console.log('nested');")
}

func TestStaticFSRejectsNilFileSystem(t *testing.T) {
	r := NewRouter()

	err := r.StaticFS("/assets", nil)
	if err == nil {
		t.Fatalf("expected nil filesystem registration to fail")
	}
}

func TestStaticFSPrefixValidationPrecedesNilFileSystemValidation(t *testing.T) {
	r := NewRouter()
	err := r.StaticFS("/bad//prefix", nil)
	if err == nil || !strings.Contains(err.Error(), "empty path segment") {
		t.Fatalf("invalid StaticFS prefix error = %v, want empty path segment", err)
	}
	if err != nil && strings.Contains(err.Error(), "nil file system") {
		t.Fatalf("invalid StaticFS prefix checked nil file system before route validation: %v", err)
	}
}

func TestStaticFSLifecyclePrecedesNilFileSystemValidation(t *testing.T) {
	r := NewRouter()
	r.Freeze()
	err := r.StaticFS("/assets", nil)
	if err == nil || !strings.Contains(err.Error(), "router is frozen") {
		t.Fatalf("frozen StaticFS error = %v, want frozen", err)
	}

	var nilRouter *Router
	err = nilRouter.StaticFS("/assets", nil)
	if err == nil || !strings.Contains(err.Error(), "router is not initialized") {
		t.Fatalf("nil StaticFS error = %v, want not initialized", err)
	}
}

func TestGetFilePathFromRequestRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantOK  bool
		wantVal string
	}{
		{name: "valid nested path", path: "css/site.css", wantOK: true, wantVal: "css/site.css"},
		{name: "empty path", path: "", wantOK: false},
		{name: "null byte", path: "css/site.css\x00", wantOK: false},
		{name: "traversal", path: "../secret.txt", wantOK: false},
		{name: "nested traversal", path: "css/../secret.txt", wantOK: false},
		{name: "absolute path", path: "/etc/passwd", wantOK: false},
		{name: "backslash traversal", path: "..\\secret.txt", wantOK: false},
		{name: "dot segment cleaned", path: "css/./site.css", wantOK: true, wantVal: "css/site.css"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			ctx := contract.WithRequestContext(req.Context(), contract.RequestContext{
				Params: map[string]string{"filepath": tt.path},
			})
			req = req.WithContext(ctx)

			got, ok := getFilePathFromRequest(req)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.wantVal {
				t.Fatalf("path = %q, want %q", got, tt.wantVal)
			}
		})
	}
}

type sequentialFS struct {
	fs.FS
	reads *int
}

type sequentialFile struct {
	fs.File
	reads *int
}

func (f sequentialFile) Read(dst []byte) (int, error) {
	(*f.reads)++
	return f.File.Read(dst)
}

func (f sequentialFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return sequentialFile{File: file, reads: f.reads}, nil
}

func TestStaticFSSequentialAndSeekable(t *testing.T) {
	const body = `{"a":1}`
	files := fstest.MapFS{"note.json": {Data: []byte(body)}}
	for _, tc := range []struct {
		name              string
		sequential        bool
		method, byteRange string
		status            int
		body, length      string
		contentType       string
	}{
		{"sequential GET", true, http.MethodGet, "", 200, body, "7", ""},
		{"sequential HEAD", true, http.MethodHead, "", 200, "", "7", ""},
		{"sequential explicit type", true, http.MethodHead, "", 200, "", "7", "application/example"},
		{"sequential Range", true, http.MethodGet, "bytes=1-2", 200, body, "7", ""},
		{"seekable Range", false, http.MethodGet, "bytes=1-2", 206, `"a`, "2", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRouter()
			var reads int
			var source fs.FS = files
			if tc.sequential {
				source = sequentialFS{FS: files, reads: &reads}
			}
			if err := r.StaticFS("/assets", http.FS(source)); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tc.method, "/assets/note.json", nil)
			req.Header.Set("Range", tc.byteRange)
			dst := httptest.NewRecorder()
			wantType := "application/json"
			if tc.contentType != "" {
				dst.Header().Set("Content-Type", tc.contentType)
				wantType = tc.contentType
			}
			r.ServeHTTP(dst, req)
			if dst.Code != tc.status || dst.Body.String() != tc.body || dst.Result().Header.Get("Content-Length") != tc.length {
				t.Fatalf("status=%d headers=%v body=%q", dst.Code, dst.Result().Header, dst.Body)
			}
			if got := dst.Result().Header.Get("Content-Type"); got != wantType {
				t.Fatalf("Content-Type = %q, want %q", got, wantType)
			}
			if tc.method == http.MethodHead && reads != 0 {
				t.Fatalf("HEAD read file contents %d times", reads)
			}
		})
	}
}

func TestStaticFSSequentialConditionalRequestContract(t *testing.T) {
	const body = "example"
	modified := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	files := fstest.MapFS{"sample.txt": {Data: []byte(body), ModTime: modified}}
	for _, tc := range []struct {
		name       string
		sequential bool
		status     int
	}{
		{"seekable", false, http.StatusNotModified},
		{"sequential", true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads int
			var source fs.FS = files
			if tc.sequential {
				source = sequentialFS{FS: files, reads: &reads}
			}
			r := NewRouter()
			if err := r.StaticFS("/assets", http.FS(source)); err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				req := httptest.NewRequest(method, "/assets/sample.txt", nil)
				req.Header.Set("If-Modified-Since", modified.Format(http.TimeFormat))
				rec := httptest.NewRecorder()
				before := reads
				r.ServeHTTP(rec, req)
				wantBody := ""
				if tc.sequential && method == http.MethodGet {
					wantBody = body
				}
				if rec.Code != tc.status || rec.Body.String() != wantBody || (method == http.MethodHead && reads != before) {
					t.Fatalf("%s: status=%d body=%q reads=%d", method, rec.Code, rec.Body.String(), reads-before)
				}
			}
		})
	}
}

type interruptedStaticRead struct {
	*closeCountingStaticFile
	prefix string
}

func (f *interruptedStaticRead) Seek(int64, int) (int64, error) {
	return 0, errors.New("not seekable")
}

func (f *interruptedStaticRead) Read(dst []byte) (int, error) {
	return copy(dst, f.prefix), errors.New("interrupted read")
}

func TestStaticSequentialReadErrorDoesNotAppendAnotherResponse(t *testing.T) {
	for _, prefix := range []string{"", "par"} {
		file, err := http.FS(fstest.MapFS{"file.txt": {Data: []byte("partial")}}).Open("file.txt")
		if err != nil {
			t.Fatal(err)
		}
		fixture := &interruptedStaticRead{closeCountingStaticFile: &closeCountingStaticFile{File: file}, prefix: prefix}
		r := NewRouter()
		if err := r.StaticFS("/files", staticOpenResult{file: fixture}); err != nil {
			t.Fatal(err)
		}
		w := serveRouter(r, http.MethodGet, "/files/file.txt")
		if w.Code != http.StatusOK || w.Body.String() != prefix || fixture.closes != 1 {
			t.Fatalf("status=%d body=%q closes=%d", w.Code, w.Body.String(), fixture.closes)
		}
	}
}

func TestStaticFSEmptySeekableFileHasZeroLength(t *testing.T) {
	r := NewRouter()
	files := fstest.MapFS{"empty.txt": {}}
	if err := r.StaticFS("/assets", http.FS(files)); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", "application/example")
		r.ServeHTTP(w, httptest.NewRequest(method, "/assets/empty.txt", nil))
		if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "0" ||
			w.Header().Get("Content-Type") != "application/example" {
			t.Fatalf("%s: status=%d headers=%v body=%q", method, w.Code, w.Header(), w.Body)
		}
	}
}

func TestStaticRootDisappearingReturnsFixedNotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewRouter()
	if err := r.Static("/files", dir); err != nil {
		t.Fatal(err)
	}
	reqPath := "/files/visible.txt"
	if w := serveRouter(r, http.MethodGet, reqPath); w.Code != http.StatusOK || w.Body.String() != "fixture" {
		t.Fatalf("initial asset: %d %q", w.Code, w.Body)
	}
	if err := os.Remove(filepath.Join(dir, "visible.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if w := serveRouter(r, http.MethodGet, reqPath); w.Code != http.StatusNotFound || w.Body.String() != "404 page not found\n" {
		t.Fatalf("unavailable root: %d %q", w.Code, w.Body)
	}
}

type rewindFailingFile struct {
	*closeCountingStaticFile
}

func (*rewindFailingFile) Seek(_ int64, origin int) (int64, error) {
	if origin == io.SeekEnd {
		return 8, nil
	}
	return 0, errors.New("fixture rewind failure")
}

func TestStaticRewindFailureStopsBeforeServingContent(t *testing.T) {
	file, err := http.FS(fstest.MapFS{"file.txt": {Data: []byte("fixture!")}}).Open("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rewindFailingFile{&closeCountingStaticFile{File: file}}
	r := NewRouter()
	if err := r.StaticFS("/files", staticOpenResult{file: fixture}); err != nil {
		t.Fatal(err)
	}
	w := serveRouter(r, http.MethodGet, "/files/file.txt")
	if w.Code != http.StatusInternalServerError || w.Body.String() != "cannot rewind static file\n" || fixture.closes != 1 {
		t.Fatalf("status=%d body=%q closes=%d", w.Code, w.Body, fixture.closes)
	}
}

type waitingFixtureFS struct {
	http.FileSystem
	entered, release chan struct{}
	file             *closeCountingStaticFile
}

func (f waitingFixtureFS) Open(string) (http.File, error) {
	close(f.entered)
	<-f.release
	return f.file, nil
}

func TestStaticFileSystemOwnsAnOpenThatOutlivesCancellation(t *testing.T) {
	file, err := http.FS(fstest.MapFS{"file.txt": {Data: []byte("fixture")}}).Open("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	f := waitingFixtureFS{entered: make(chan struct{}), release: make(chan struct{}), file: &closeCountingStaticFile{File: file}}
	r := NewRouter()
	if err := r.StaticFS("/fs", f); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/fs/file.txt", nil).WithContext(ctx))
	}()
	<-f.entered
	cancel()
	close(f.release)
	<-done
	if f.file.closes != 1 || w.Body.String() != "fixture" {
		t.Fatalf("closes=%d status=%d body=%q", f.file.closes, w.Code, w.Body)
	}
}

func TestEmptyRegularStaticFileMatchesServeContentRangesAndConditions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	router := NewRouter()
	if err := router.Static("/files", dir); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, variant := range []string{"plain", "range", "modified"} {
			req := httptest.NewRequest(method, "/files/empty.txt", nil)
			switch variant {
			case "range":
				req.Header.Set("Range", "bytes=0-0")
			case "modified":
				req.Header.Set("If-Modified-Since", stamp.UTC().Format(http.TimeFormat))
			}
			dst, reference := httptest.NewRecorder(), httptest.NewRecorder()
			router.ServeHTTP(dst, req)
			http.ServeContent(reference, req, "empty.txt", stamp, strings.NewReader(""))
			if dst.Code != reference.Code || dst.Body.String() != reference.Body.String() ||
				dst.Header().Get("Content-Length") != reference.Header().Get("Content-Length") {
				t.Fatalf("%s %s got=%d %v %q want=%d %v %q", method, variant, dst.Code, dst.Header(), dst.Body.String(), reference.Code, reference.Header(), reference.Body.String())
			}
		}
	}
}

func TestStaticIfRangeDateParity(t *testing.T) {
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	dir := t.TempDir()
	file := createTempFile(t, dir, "fixture.txt", "abcdef")
	if err := os.Chtimes(file, at, at); err != nil {
		t.Fatal(err)
	}
	r := NewRouter()
	if err := r.Static("/disk", dir); err != nil {
		t.Fatal(err)
	}
	if err := r.StaticFS("/fs", http.FS(fstest.MapFS{"fixture.txt": {Data: []byte("abcdef"), ModTime: at}})); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"/disk", "/fs"} {
		for _, tc := range []struct {
			date               time.Time
			code               int
			body, contentRange string
		}{
			{at, 206, "bc", "bytes 1-2/6"},
			{at.Add(-time.Second), 200, "abcdef", ""},
		} {
			req := httptest.NewRequest(http.MethodGet, prefix+"/fixture.txt", nil)
			req.Header.Set("Range", "bytes=1-2")
			req.Header.Set("If-Range", tc.date.Format(http.TimeFormat))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.code || rec.Body.String() != tc.body || rec.Header().Get("Content-Range") != tc.contentRange {
				t.Fatalf("prefix=%v status=%d body=%q headers=%v", prefix, rec.Code, rec.Body.String(), rec.Header())
			}
		}
	}
}

func TestStaticFSStatErrorClosesEachHeadAndGetFile(t *testing.T) {
	source := new(staticOpenResult)
	r := NewRouter()
	if err := r.StaticFS("/files", source); err != nil {
		t.Fatal(err)
	}
	files := http.FS(fstest.MapFS{"sample.txt": {Data: []byte("sample")}})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		f, err := files.Open("sample.txt")
		if err != nil {
			t.Fatal(err)
		}
		tracked := &closeCountingStaticFile{File: f, statErr: errors.New("stat failed")}
		source.file = tracked
		w := serveRouter(r, method, "/files/sample.txt")
		if w.Code != http.StatusNotFound || tracked.closes != 1 || (method == http.MethodHead && w.Body.Len() != 0) {
			t.Fatalf("%s: status=%d closes=%d body=%q", method, w.Code, tracked.closes, w.Body)
		}
	}
}

func TestExtensionlessStaticFileSniffsContentTypeForHeadAndGet(t *testing.T) {
	dir := t.TempDir()
	const data = "plain fixture\n"
	if err := os.WriteFile(filepath.Join(dir, "sample"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewRouter()
	if err := r.Static("/assets", dir); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r)
	defer srv.Close()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodHead} {
		r, err := http.NewRequestWithContext(t.Context(), method, srv.URL+"/assets/sample", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		want := data
		if method == http.MethodHead {
			want = ""
		}
		if err != nil || resp.StatusCode != http.StatusOK || resp.ContentLength != int64(len(data)) || resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" || string(body) != want {
			t.Fatalf("%s status=%d type=%q size=%d body=%q err=%v", method, resp.StatusCode, resp.Header.Get("Content-Type"), resp.ContentLength, body, err)
		}
	}
}
