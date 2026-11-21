package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// writeTestFile creates a file at dir/relPath with the given content,
// creating intermediate directories as needed.
func writeTestFile(t testing.TB, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func assertBodyContains(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()

	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("body = %q, want to contain %q", rec.Body.String(), want)
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want mention of %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want mention of %q", err, want)
	}
}

func TestSPAFallbackVariesByAccept(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "<html>index</html>")
	writeTestFile(t, root, "existing", "asset")

	for _, enabled := range []bool{true, false} {
		h, err := NewHandlerFS(http.Dir(root), WithFallback(enabled))
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			path   string
			accept string
			status int
			vary   []string
		}{
			{"/spa-route", "text/html", http.StatusOK, []string{"Origin", "Accept", "Accept-Encoding"}},
			{"/spa-route", "application/json", http.StatusNotFound, []string{"Origin", "Accept"}},
			{"/missing.js", "text/html", http.StatusNotFound, []string{"Origin"}},
			{"/existing", "application/json", http.StatusOK, []string{"Origin", "Accept-Encoding"}},
		} {
			if !enabled && tc.path == "/spa-route" {
				tc.status, tc.vary = http.StatusNotFound, []string{"Origin"}
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				req := httptest.NewRequest(method, tc.path, nil)
				req.Header.Set("Accept", tc.accept)
				rec := httptest.NewRecorder()
				rec.Header().Set("Vary", "Origin")
				h.ServeHTTP(rec, req)
				if rec.Code != tc.status || !slices.Equal(rec.Header().Values("Vary"), tc.vary) {
					t.Errorf("fallback=%v %s %s %q: status=%d vary=%v, want %d %v", enabled, method, tc.path, tc.accept, rec.Code, rec.Header().Values("Vary"), tc.status, tc.vary)
				}
			}
		}
	}
}

func TestSPAFallbackReadsAllAcceptLines(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "index")
	h, err := NewHandlerFS(http.Dir(root), WithFallback(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		lines  []string
		status int
	}{
		{[]string{"application/json", "text/html"}, 200},
		{[]string{"application/json, text/html"}, 200},
		{[]string{"", "  "}, 200},
		{[]string{"application/json", "text/html;q=0"}, 404},
		{[]string{"text/html;q=NaN"}, 404},
		{[]string{"text/html;q=+Inf"}, 404},
		{[]string{"text/html;q=NaN", "application/xhtml+xml;q=0.5"}, 200},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			req := httptest.NewRequest(method, "/route", nil)
			for _, line := range tc.lines {
				req.Header.Add("Accept", line)
			}
			rec := httptest.NewRecorder()
			rec.Header().Set("Vary", "Origin")
			h.ServeHTTP(rec, req)
			if rec.Code != tc.status || !slices.Contains(rec.Header().Values("Vary"), "Accept") ||
				!slices.Contains(rec.Header().Values("Vary"), "Origin") {
				t.Errorf("%s %q: status=%d Vary=%v", method, tc.lines, rec.Code, rec.Header().Values("Vary"))
			}
		}
	}
}

func TestSPAFallbackHonorsSpecificHTMLRefusal(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "<html>SPA index</html>")
	h, err := NewHandlerFS(http.Dir(root), WithFallback(true))
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name   string
		path   string
		accept []string
		status int
		vary   bool
	}{
		{"exact refusal", "/route", []string{"text/html;q=0, */*;q=1"}, http.StatusNotFound, true},
		{"refusal across lines", "/route", []string{"text/*", "text/html;q=0"}, http.StatusNotFound, true},
		{"refusal across reversed lines", "/route", []string{"text/html;q=0", "text/*"}, http.StatusNotFound, true},
		{"type refusal", "/route", []string{"text/*;q=0, */*"}, http.StatusNotFound, true},
		{"exact acceptance", "/route", []string{"text/*;q=0, text/html;q=0.2"}, http.StatusOK, true},
		{"no accept header", "/route", nil, http.StatusOK, true},
		{"XHTML navigation", "/route", []string{"application/xhtml+xml"}, http.StatusOK, true},
		{"XHTML with explicit HTML refusal", "/route", []string{"application/xhtml+xml, text/html;q=0"}, http.StatusNotFound, true},
		{"missing asset", "/missing.js", []string{"text/html"}, http.StatusNotFound, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				req := httptest.NewRequest(method, tt.path, nil)
				for _, line := range tt.accept {
					req.Header.Add("Accept", line)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != tt.status || slices.Contains(rec.Header().Values("Vary"), "Accept") != tt.vary {
					t.Errorf("%s: status=%d Vary=%v, want status=%d and Vary: Accept=%t", method, rec.Code, rec.Header().Values("Vary"), tt.status, tt.vary)
				}
				wantIndex := method == http.MethodGet && tt.status == http.StatusOK
				if strings.Contains(rec.Body.String(), "SPA index") != wantIndex {
					t.Errorf("%s: body = %q, want SPA index presence=%t", method, rec.Body.String(), wantIndex)
				}
			}
		})
	}
}

func TestMIMEOverridesRejectNormalizedConflicts(t *testing.T) {
	files := http.FS(fstest.MapFS{
		"index.html": {Data: []byte("index")},
		"app.demo":   {Data: []byte("example")},
		"app.other":  {Data: []byte("other")},
	})
	if h, err := NewHandlerFS(files, WithMIMETypes(map[string]string{".Demo": "text/plain", "demo": "application/json"})); err == nil || h != nil {
		t.Fatalf("conflicting options: handler=%v error=%v", h, err)
	}
	h, err := NewHandlerFS(files, WithMIMETypes(map[string]string{".Demo": "text/plain", "demo": "text/plain", "other": "application/json"}))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"/app.demo": "text/plain", "/app.other": "application/json"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Content-Type"); rec.Code != http.StatusOK || got != want {
			t.Errorf("%s status=%d Content-Type=%q, want %q", path, rec.Code, got, want)
		}
	}
}

func TestFrontendHeadersRejectCanonicalConflicts(t *testing.T) {
	fsys := http.FS(fstest.MapFS{"index.html": {Data: []byte("index")}})
	if _, err := NewHandlerFS(fsys, WithHeaders(map[string]string{"X-Custom": "left", "x-custom": "right"})); err == nil {
		t.Fatal("accepted conflicting header values")
	}

	headers := map[string]string{"X-Custom": "value", " x-custom ": " value "}
	option := WithHeaders(headers)
	headers["X-Custom"] = "changed"
	handler, err := NewHandlerFS(fsys, option)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := recorder.Header().Values("X-Custom"); !slices.Equal(got, []string{"value"}) {
		t.Fatalf("X-Custom = %v", got)
	}
}

func TestFrontendPlanRejectsNormalizedConflicts(t *testing.T) {
	fsys := http.FS(fstest.MapFS{"static/app.js": {Data: []byte("original")}, "static/app.js.br": {Data: []byte("br")}})
	variants := map[string]PrecompressedVariants{"static/app.js": {Brotli: true}, "static//app.js": {Gzip: true}}
	if _, err := NewHandlerFS(fsys, WithPrecompressedVariantPlan(variants)); err == nil {
		t.Fatal("accepted conflicting variants")
	}
	variants["static//app.js"] = PrecompressedVariants{Brotli: true}
	for _, plan := range []map[string]PrecompressedVariants{variants, {"static/app.js": {Brotli: true}}} {
		handler, err := NewHandlerFS(fsys, WithPrecompressed(true), WithPrecompressedVariantPlan(plan))
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
		request.Header.Set("Accept-Encoding", "br")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Encoding") != "br" || recorder.Body.String() != "br" {
			t.Fatalf("response = %d %v %q", recorder.Code, recorder.Header(), recorder.Body.String())
		}
	}
}

func TestSPAFallbackPreservesVaryWildcard(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "home")
	h, err := NewHandlerFS(http.Dir(root), WithFallback(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		accept string
		status int
		vary   []string
	}{
		{"application/json", http.StatusNotFound, []string{"*"}},
		{"text/html", http.StatusOK, []string{"*", "Accept-Encoding"}},
	} {
		req := httptest.NewRequest(http.MethodGet, "/route", nil)
		req.Header.Set("Accept", tc.accept)
		rec := httptest.NewRecorder()
		rec.Header().Set("Vary", "*")
		h.ServeHTTP(rec, req)
		if rec.Code != tc.status || !slices.Equal(rec.Header().Values("Vary"), tc.vary) {
			t.Errorf("Accept %q: status=%d Vary=%v", tc.accept, rec.Code, rec.Header().Values("Vary"))
		}
		if tc.status == http.StatusOK && rec.Body.String() != "home" {
			t.Errorf("SPA body=%q", rec.Body.String())
		}
	}
}

func TestStandaloneFrontendHandlerRejectsOutsidePrefix(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "index.html", "home")
	h, err := NewHandlerFS(http.Dir(dir), WithPrefix("/app"), WithFallback(true))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/app": http.StatusOK, "/app/route": http.StatusOK, "/application": http.StatusNotFound, "/outside": http.StatusNotFound} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != want || (out.Body.String() == "home") != (want == http.StatusOK) {
			t.Errorf("%s: response = %d, %q", path, out.Code, out.Body.String())
		}
	}
}

func TestSPAFallbackIgnoresUnrelatedAcceptParameters(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "home")
	h, err := NewHandlerFS(http.Dir(root), WithFallback(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		accept string
		status int
	}{
		{"text/html; level=1; q=0.8; foo=bar", http.StatusOK},
		{"text/html; level=1; q=0; foo=bar, text/*; q=1", http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, "/client-route", nil)
		req.Header.Set("Accept", tc.accept)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != tc.status || (out.Body.String() == "home") != (tc.status == http.StatusOK) ||
			!slices.Contains(out.Header().Values("Vary"), "Accept") {
			t.Errorf("Accept %q: status=%d, body=%q, Vary=%v", tc.accept, out.Code, out.Body.String(), out.Header().Values("Vary"))
		}
	}
}

func TestSPAFallbackSkipsEmptyMediaType(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "index.html", "home")
	h, err := NewHandlerFS(http.Dir(root), WithFallback(true))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		accept string
		status int
	}{
		{";q=1", http.StatusNotFound},
		{";q=1, text/html;q=0.5", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, "/client-route", nil)
		req.Header.Set("Accept", tc.accept)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != tc.status || !slices.Contains(out.Header().Values("Vary"), "Accept") ||
			(out.Body.String() == "home") != (tc.status == http.StatusOK) {
			t.Errorf("Accept %q: status=%d, body=%q, Vary=%v", tc.accept, out.Code, out.Body.String(), out.Header().Values("Vary"))
		}
	}
}
