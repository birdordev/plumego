package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
