package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/birdordev/plumego/router"
)

func TestPrecompressedFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.js", "console.log('original')")
	writeTestFile(t, dir, "app.js.gz", "gzipped content")
	writeTestFile(t, dir, "app.js.br", "brotli content")
	writeTestFile(t, dir, "style.css", "body { color: red; }")
	writeTestFile(t, dir, "style.css.gz", "gzipped css")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithPrecompressed(true)); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []struct {
		name           string
		path           string
		acceptEncoding string
		expectBody     string
		expectEncoding string
	}{
		{
			name:           "brotli preferred",
			path:           "/app.js",
			acceptEncoding: "br, gzip, deflate",
			expectBody:     "brotli content",
			expectEncoding: "br",
		},
		{
			name:           "gzip when br not accepted",
			path:           "/app.js",
			acceptEncoding: "gzip, deflate",
			expectBody:     "gzipped content",
			expectEncoding: "gzip",
		},
		{
			name:           "original when no encoding accepted",
			path:           "/app.js",
			acceptEncoding: "",
			expectBody:     "console.log('original')",
			expectEncoding: "",
		},
		{
			name:           "gzip when brotli q zero",
			path:           "/app.js",
			acceptEncoding: "br;q=0, gzip;q=1",
			expectBody:     "gzipped content",
			expectEncoding: "gzip",
		},
		{
			name:           "gzip when gzip quality higher",
			path:           "/app.js",
			acceptEncoding: "br;q=0.1, gzip;q=1",
			expectBody:     "gzipped content",
			expectEncoding: "gzip",
		},
		{
			name:           "invalid brotli weight leaves wildcard preference",
			path:           "/app.js",
			acceptEncoding: "br;q=NaN, *;q=0.8, gzip;q=0.2",
			expectBody:     "brotli content",
			expectEncoding: "br",
		},
		{
			name:           "brotli wins equal quality",
			path:           "/app.js",
			acceptEncoding: "br;q=0.8, gzip;q=0.8",
			expectBody:     "brotli content",
			expectEncoding: "br",
		},
		{
			name:           "original when encodings q zero",
			path:           "/app.js",
			acceptEncoding: "br;q=0, gzip;q=0",
			expectBody:     "console.log('original')",
			expectEncoding: "",
		},
		{
			name:           "only gzip available",
			path:           "/style.css",
			acceptEncoding: "br, gzip",
			expectBody:     "gzipped css",
			expectEncoding: "gzip",
		},
		{
			name:           "no precompressed version",
			path:           "/index.html",
			acceptEncoding: "gzip, br",
			expectBody:     "<html>index</html>",
			expectEncoding: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			}
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d want %d", rec.Code, http.StatusOK)
			}

			assertBodyContains(t, rec, tc.expectBody)

			gotEncoding := rec.Header().Get("Content-Encoding")
			if gotEncoding != tc.expectEncoding {
				t.Fatalf("Content-Encoding: got %q want %q", gotEncoding, tc.expectEncoding)
			}

			if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
				t.Fatalf("Vary = %q, want Accept-Encoding", got)
			}
		})
	}
}

func TestPrecompressedVariantScanErrorFailsFast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping test on Windows due to permission model differences")
	}
	if os.Getuid() == 0 {
		t.Skip("skipping test when running as root")
	}

	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(blocked, 0o000); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer func() { _ = os.Chmod(blocked, 0o755) }()

	r := router.NewRouter()
	err := RegisterFromDir(r, dir, WithPrecompressed(true))
	assertErrorContains(t, err, "scan precompressed variant")
}

func TestPrecompressedDisabled(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.js", "original")
	writeTestFile(t, dir, "app.js.gz", "gzipped")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithPrecompressed(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if rec.Body.String() != "original" {
		t.Fatalf("should serve original file when precompressed disabled, got: %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("should not set Content-Encoding when precompressed disabled")
	}
}

func TestPrecompressedRequiresOriginalAsset(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "orphan.js.br", "orphan compressed")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithPrecompressed(true), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/orphan.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("unexpected Content-Encoding: %q", rec.Header().Get("Content-Encoding"))
	}
	if strings.Contains(rec.Body.String(), "orphan compressed") {
		t.Fatalf("served orphan precompressed body: %q", rec.Body.String())
	}
}

func TestDirectoryPrecompressedRequiresCurrentOriginalAsset(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.js", "original")
	writeTestFile(t, dir, "app.js.br", "compressed")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithPrecompressed(true), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "app.js")); err != nil {
		t.Fatalf("remove original: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding: got %q want empty", got)
	}
	if strings.Contains(rec.Body.String(), "compressed") {
		t.Fatalf("served compressed variant without current original: %q", rec.Body.String())
	}
}

func TestDirectoryPrecompressedPlanIsImmutable(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.js", "original")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithPrecompressed(true)); err != nil {
		t.Fatalf("register: %v", err)
	}
	writeTestFile(t, dir, "app.js.br", "late compressed")

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusOK)
	}
	assertBodyContains(t, rec, "original")
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding: got %q want empty", got)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary: got %q want Accept-Encoding", got)
	}
}

func TestRegisterFSHTTPDirPrecompressedUsesDirectoryPlan(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.js", "original")

	r := router.NewRouter()
	if err := RegisterFS(r, http.Dir(dir), WithPrecompressed(true)); err != nil {
		t.Fatalf("register: %v", err)
	}
	writeTestFile(t, dir, "app.js.br", "late compressed")

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusOK)
	}
	assertBodyContains(t, rec, "original")
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding: got %q want empty", got)
	}
}

func TestIdentityEncodingRefusal(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.js", "original")
	writeTestFile(t, dir, "style.css", "style")
	writeTestFile(t, dir, "style.css.gz", "compressed style")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithPrecompressed(true), WithFallback(false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	tests := []struct {
		name           string
		path           string
		acceptEncoding string
		expectStatus   int
		expectEncoding string
		expectBody     string
	}{
		{
			name:           "identity refused without variant",
			path:           "/app.js",
			acceptEncoding: "identity;q=0",
			expectStatus:   http.StatusNotAcceptable,
			expectBody:     "acceptable content encoding not available",
		},
		{
			name:           "wildcard zero refuses identity",
			path:           "/app.js",
			acceptEncoding: "*;q=0",
			expectStatus:   http.StatusNotAcceptable,
			expectBody:     "acceptable content encoding not available",
		},
		{
			name:           "compressed variant satisfies identity refusal",
			path:           "/style.css",
			acceptEncoding: "gzip;q=1, identity;q=0",
			expectStatus:   http.StatusOK,
			expectEncoding: "gzip",
			expectBody:     "compressed style",
		},
		{
			name:           "invalid high q token ignored",
			path:           "/app.js",
			acceptEncoding: "br;q=1.5",
			expectStatus:   http.StatusOK,
			expectBody:     "original",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.expectStatus {
				t.Fatalf("status: got %d want %d body=%q", rec.Code, tt.expectStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Encoding"); got != tt.expectEncoding {
				t.Fatalf("Content-Encoding: got %q want %q", got, tt.expectEncoding)
			}
			assertBodyContains(t, rec, tt.expectBody)
		})
	}
}

func TestMIMETypesWithPrecompressed(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.wasm", "wasm original")
	writeTestFile(t, dir, "app.wasm.br", "wasm compressed")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir,
		WithPrecompressed(true),
		WithMIMETypes(map[string]string{
			".wasm": "application/wasm",
		}),
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	assertBodyContains(t, rec, "wasm compressed")
	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/wasm") {
		t.Fatalf("Content-Type should be set for original file type, got: %q", contentType)
	}
	if rec.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("should set Content-Encoding")
	}
}

func TestCustomFSOnlyProbesAcceptedPrecompressedVariants(t *testing.T) {
	tests := []struct {
		name      string
		writeBR   bool
		wantOpens []string
	}{
		{
			name:      "br variant found",
			writeBR:   true,
			wantOpens: []string{"app.js", "app.js.br"},
		},
		{
			name:      "no variants found",
			wantOpens: []string{"app.js", "app.js.br"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, dir, "index.html", "index")
			writeTestFile(t, dir, "app.js", "original")
			if tt.writeBR {
				writeTestFile(t, dir, "app.js.br", "compressed")
			}

			fsys := &recordingFS{base: http.Dir(dir)}
			r := router.NewRouter()
			if err := RegisterFS(r, fsys, WithPrecompressed(true), WithFallback(false)); err != nil {
				t.Fatalf("register: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
			req.Header.Set("Accept-Encoding", "br")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
			}
			if tt.writeBR {
				assertBodyContains(t, rec, "compressed")
			} else {
				assertBodyContains(t, rec, "original")
			}
			if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
				t.Fatalf("Vary = %q, want Accept-Encoding", got)
			}
			if !slices.Equal(fsys.opened, tt.wantOpens) {
				t.Fatalf("opened paths: got %#v want %#v", fsys.opened, tt.wantOpens)
			}
		})
	}
}

func TestCustomFSPrecompressedVariantPlanServesIdentityWithoutProbing(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "app.js", "original")
	writeTestFile(t, dir, "app.js.br", "compressed")

	fsys := &recordingFS{base: http.Dir(dir)}
	r := router.NewRouter()
	if err := RegisterFS(r, fsys,
		WithPrecompressed(true),
		WithFallback(false),
		WithPrecompressedVariantPlan(map[string]PrecompressedVariants{
			"app.js": {Brotli: true},
		}),
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertBodyContains(t, rec, "original")
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding: got %q want empty", got)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary: got %q want Accept-Encoding", got)
	}
	if want := []string{"app.js"}; !slices.Equal(fsys.opened, want) {
		t.Fatalf("opened paths: got %#v want %#v", fsys.opened, want)
	}
}

func TestCustomFSPrecompressedVariantPlanServesAcceptedVariantBeforeOriginal(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "app.js", "original")
	writeTestFile(t, dir, "app.js.br", "compressed")

	fsys := &recordingFS{base: http.Dir(dir)}
	r := router.NewRouter()
	if err := RegisterFS(r, fsys,
		WithPrecompressed(true),
		WithFallback(false),
		WithPrecompressedVariantPlan(map[string]PrecompressedVariants{
			"app.js": {Brotli: true},
		}),
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertBodyContains(t, rec, "compressed")
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding: got %q want br", got)
	}
	if want := []string{"app.js.br"}; !slices.Equal(fsys.opened, want) {
		t.Fatalf("opened paths: got %#v want %#v", fsys.opened, want)
	}
}

func TestCustomFSLazyPrecompressedStatErrorFallsBackToOriginal(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "app.js", "original")
	writeTestFile(t, dir, "app.js.br", "compressed")

	var misses []PrecompressedVariantMiss
	fsys := &statErrorFS{base: http.Dir(dir), statPath: "app.js.br"}
	r := router.NewRouter()
	if err := RegisterFS(r, fsys,
		WithPrecompressed(true),
		WithFallback(false),
		WithPrecompressedVariantMissHandler(func(miss PrecompressedVariantMiss) {
			misses = append(misses, miss)
		}),
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertBodyContains(t, rec, "original")
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding: got %q want empty", got)
	}
	if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("Vary: got %q want Accept-Encoding", got)
	}
	if len(misses) != 1 {
		t.Fatalf("misses: got %#v want one event", misses)
	}
	want := PrecompressedVariantMiss{
		Path:        "app.js",
		VariantPath: "app.js.br",
		Encoding:    "br",
		Operation:   "stat",
	}
	if misses[0] != want {
		t.Fatalf("miss event: got %#v want %#v", misses[0], want)
	}
}

func TestPlannedPrecompressedOpenMissReportsDowngrade(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "app.js", "original")
	writeTestFile(t, dir, "app.js.br", "compressed")

	var misses []PrecompressedVariantMiss
	r := router.NewRouter()
	if err := RegisterFromDir(r, dir,
		WithPrecompressed(true),
		WithFallback(false),
		WithPrecompressedVariantMissHandler(func(miss PrecompressedVariantMiss) {
			misses = append(misses, miss)
		}),
	); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "app.js.br")); err != nil {
		t.Fatalf("remove variant: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertBodyContains(t, rec, "original")
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding: got %q want empty", got)
	}
	if len(misses) != 1 {
		t.Fatalf("misses: got %#v want one event", misses)
	}
	want := PrecompressedVariantMiss{
		Path:        "app.js",
		VariantPath: "app.js.br",
		Encoding:    "br",
		Operation:   "open",
	}
	if misses[0] != want {
		t.Fatalf("miss event: got %#v want %#v", misses[0], want)
	}
}

func TestPrecompressedVariantPlanRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")

	tests := []string{
		"",
		"../app.js",
		"/app.js",
		`assets\app.js`,
	}

	for _, filePath := range tests {
		t.Run(filePath, func(t *testing.T) {
			r := router.NewRouter()
			err := RegisterFS(r, &recordingFS{base: http.Dir(dir)},
				WithPrecompressed(true),
				WithPrecompressedVariantPlan(map[string]PrecompressedVariants{
					filePath: {Brotli: true},
				}),
			)
			assertErrorContains(t, err, "precompressed variant path")
		})
	}
}

func TestAcceptedEncodingQuality(t *testing.T) {
	tests := []struct {
		header string
		token  string
		want   bool
	}{
		{"br, gzip, deflate", "br", true},
		{"gzip, deflate", "br", false},
		{"gzip, deflate", "gzip", true},
		{"GZIP", "gzip", true},               // case-insensitive
		{"gzip;q=0.9, br;q=1.0", "br", true}, // positive quality accepts token
		{"br;q=0, gzip;q=1", "br", false},    // zero quality rejects token
		{"br;q=0, gzip;q=1", "gzip", true},
		{"br;q=bogus, gzip;q=1", "br", false},
		{"br;q=bogus, gzip;q=1", "gzip", true},
		{"br;q=1.5, gzip;q=1", "br", false},
		{"br;q=-0.1, gzip;q=1", "br", false},
		{"*;q=0.5", "br", true},          // wildcard
		{"br;q=0, *;q=0.5", "br", false}, // explicit token overrides wildcard
		{"", "gzip", false},              // empty header
		{"brotli", "br", false},          // no substring matching
	}

	for _, tt := range tests {
		t.Run(tt.header+"/"+tt.token, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Accept-Encoding", tt.header)
			}
			got := acceptedEncodings(req).quality(tt.token) > 0
			if got != tt.want {
				t.Fatalf("positive quality for %q in %q = %v, want %v", tt.token, tt.header, got, tt.want)
			}
		})
	}
}

func TestIdentityOnlyAssetVariesByAcceptEncoding(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "<html>index</html>")
	writeTestFile(t, dir, "app.js", "original")
	mount, err := NewMountFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, encoding string
		status           int
	}{
		{http.MethodGet, "", http.StatusOK},
		{http.MethodHead, "", http.StatusOK},
		{http.MethodGet, "identity;q=0", http.StatusNotAcceptable},
	} {
		req := httptest.NewRequest(tc.method, "/app.js", nil)
		req.Header.Set("Accept-Encoding", tc.encoding)
		rec := httptest.NewRecorder()
		rec.Header().Set("Vary", "Origin")
		mount.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.status || !slices.Equal(rec.Header().Values("Vary"), []string{"Origin", "Accept-Encoding"}) {
			t.Errorf("%s with %q: status %d, Vary %v", tc.method, tc.encoding, rec.Code, rec.Header().Values("Vary"))
		}
	}
}

func TestPrecompressedEncodingFieldLines(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "index")
	writeTestFile(t, dir, "app.js", "identity")
	writeTestFile(t, dir, "app.js.br", "brotli fixture")
	writeTestFile(t, dir, "app.js.gz", "gzip fixture")
	handler, err := NewHandlerFS(http.Dir(dir), WithPrecompressed(true))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		fields   []string
		status   int
		encoding string
	}{
		{[]string{"gzip;q=0.2, br;q=0.9"}, http.StatusOK, "br"},
		{[]string{"gzip;q=0.2", "br;q=0.9"}, http.StatusOK, "br"},
		{[]string{"", "gzip"}, http.StatusOK, "gzip"},
		{[]string{"gzip;q=0, identity;q=0"}, http.StatusNotAcceptable, ""},
		{[]string{"gzip;q=0", "identity;q=0"}, http.StatusNotAcceptable, ""},
		{[]string{"br;q=0.1", "gzip;q=0.5", "br;q=0.9"}, http.StatusOK, "br"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			req := httptest.NewRequest(method, "/app.js", nil)
			for _, field := range tc.fields {
				req.Header.Add("Accept-Encoding", field)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.status || w.Header().Get("Content-Encoding") != tc.encoding ||
				!slices.Contains(w.Header().Values("Vary"), "Accept-Encoding") {
				t.Errorf("%s %q: status=%d headers=%v, want %d and encoding %q", method, tc.fields, w.Code, w.Header(), tc.status, tc.encoding)
			}
		}
	}
}
