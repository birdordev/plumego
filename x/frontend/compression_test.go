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
		expectVary     bool
	}{
		{
			name:           "brotli preferred",
			path:           "/app.js",
			acceptEncoding: "br, gzip, deflate",
			expectBody:     "brotli content",
			expectEncoding: "br",
			expectVary:     true,
		},
		{
			name:           "gzip when br not accepted",
			path:           "/app.js",
			acceptEncoding: "gzip, deflate",
			expectBody:     "gzipped content",
			expectEncoding: "gzip",
			expectVary:     true,
		},
		{
			name:           "original when no encoding accepted",
			path:           "/app.js",
			acceptEncoding: "",
			expectBody:     "console.log('original')",
			expectEncoding: "",
			expectVary:     true,
		},
		{
			name:           "gzip when brotli q zero",
			path:           "/app.js",
			acceptEncoding: "br;q=0, gzip;q=1",
			expectBody:     "gzipped content",
			expectEncoding: "gzip",
			expectVary:     true,
		},
		{
			name:           "gzip when gzip quality higher",
			path:           "/app.js",
			acceptEncoding: "br;q=0.1, gzip;q=1",
			expectBody:     "gzipped content",
			expectEncoding: "gzip",
			expectVary:     true,
		},
		{
			name:           "brotli wins equal quality",
			path:           "/app.js",
			acceptEncoding: "br;q=0.8, gzip;q=0.8",
			expectBody:     "brotli content",
			expectEncoding: "br",
			expectVary:     true,
		},
		{
			name:           "original when encodings q zero",
			path:           "/app.js",
			acceptEncoding: "br;q=0, gzip;q=0",
			expectBody:     "console.log('original')",
			expectEncoding: "",
			expectVary:     true,
		},
		{
			name:           "only gzip available",
			path:           "/style.css",
			acceptEncoding: "br, gzip",
			expectBody:     "gzipped css",
			expectEncoding: "gzip",
			expectVary:     true,
		},
		{
			name:           "no precompressed version",
			path:           "/index.html",
			acceptEncoding: "gzip, br",
			expectBody:     "<html>index</html>",
			expectEncoding: "",
			expectVary:     false,
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

			hasVary := rec.Header().Get("Vary") != ""
			if hasVary != tc.expectVary {
				t.Fatalf("Vary header: got %v want %v", hasVary, tc.expectVary)
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
	if got := rec.Header().Get("Vary"); got != "" {
		t.Fatalf("Vary: got %q want empty", got)
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

func TestCustomFSLazyPrecompressedVariantProbing(t *testing.T) {
	tests := []struct {
		name       string
		writeBR    bool
		expectVary bool
		wantOpens  []string
	}{
		{
			name:       "br variant found",
			writeBR:    true,
			expectVary: true,
			wantOpens:  []string{"app.js", "app.js.br"},
		},
		{
			name:       "no variants found",
			expectVary: false,
			wantOpens:  []string{"app.js", "app.js.br", "app.js.gz"},
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
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d want %d body=%q", rec.Code, http.StatusOK, rec.Body.String())
			}
			assertBodyContains(t, rec, "original")
			if got := rec.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding: got %q want empty", got)
			}
			hasVary := rec.Header().Get("Vary") != ""
			if hasVary != tt.expectVary {
				t.Fatalf("Vary present: got %v want %v", hasVary, tt.expectVary)
			}
			if !slices.Equal(fsys.opened, tt.wantOpens) {
				t.Fatalf("opened paths: got %#v want %#v", fsys.opened, tt.wantOpens)
			}
		})
	}
}

func TestCustomFSPrecompressedVariantPlanAvoidsLazyVaryProbes(t *testing.T) {
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
	if got := rec.Header().Get("Vary"); got != "" {
		t.Fatalf("Vary: got %q want empty", got)
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

// TestAcceptsToken verifies the token-level Accept-Encoding parser.
func TestAcceptsToken(t *testing.T) {
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
			got := acceptsToken(req, tt.token)
			if got != tt.want {
				t.Fatalf("acceptsToken(%q, %q) = %v, want %v", tt.header, tt.token, got, tt.want)
			}
		})
	}
}
