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
