package file

import (
	"strings"
	"testing"
	"time"

	storefile "github.com/birdordev/plumego/store/file"
)

// --- isPathSafe ---

func TestIsPathSafe_Safe(t *testing.T) {
	safePaths := []string{
		"uploads/image.jpg",
		"a/b/c.txt",
		"file.txt",
	}
	for _, p := range safePaths {
		if !isPathSafe(p) {
			t.Errorf("isPathSafe(%q) = false, want true", p)
		}
	}
}

func TestIsPathSafe_Traversal(t *testing.T) {
	unsafe := []string{
		"../etc/passwd",
		"a/../../b",
		"uploads/../secret",
	}
	for _, p := range unsafe {
		if isPathSafe(p) {
			t.Errorf("isPathSafe(%q) = true, want false (path traversal)", p)
		}
	}
}

func TestIsPathSafe_AbsolutePath(t *testing.T) {
	if isPathSafe("/etc/passwd") {
		t.Error("isPathSafe(/etc/passwd) = true, want false (absolute path)")
	}
}

// --- mimeToExt ---

func TestMimeToExt_KnownTypes(t *testing.T) {
	cases := []struct {
		mime string
		ext  string
	}{
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
		{"image/svg+xml", ".svg"},
		{"application/pdf", ".pdf"},
		{"text/plain", ".txt"},
		{"text/html", ".html"},
		{"text/css", ".css"},
		{"text/javascript", ".js"},
		{"application/json", ".json"},
		{"application/xml", ".xml"},
		{"application/zip", ".zip"},
	}
	for _, tc := range cases {
		if got := mimeToExt(tc.mime); got != tc.ext {
			t.Errorf("mimeToExt(%q) = %q, want %q", tc.mime, got, tc.ext)
		}
	}
}

func TestMimeToExt_Unknown(t *testing.T) {
	if got := mimeToExt("application/octet-stream"); got != "" {
		t.Errorf("mimeToExt(unknown) = %q, want empty", got)
	}
}

// --- extToMime ---

func TestExtToMime_KnownExtensions(t *testing.T) {
	cases := []struct {
		ext  string
		mime string
	}{
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".png", "image/png"},
		{".gif", "image/gif"},
		{".webp", "image/webp"},
		{".svg", "image/svg+xml"},
		{".pdf", "application/pdf"},
		{".txt", "text/plain"},
		{".html", "text/html"},
		{".css", "text/css"},
		{".js", "text/javascript"},
		{".json", "application/json"},
		{".xml", "application/xml"},
		{".zip", "application/zip"},
	}
	for _, tc := range cases {
		if got := extToMime(tc.ext); got != tc.mime {
			t.Errorf("extToMime(%q) = %q, want %q", tc.ext, got, tc.mime)
		}
	}
}

func TestExtToMime_WithoutLeadingDot(t *testing.T) {
	if got := extToMime("jpg"); got != "image/jpeg" {
		t.Errorf("extToMime(jpg) = %q, want image/jpeg", got)
	}
}

func TestExtToMime_CaseInsensitive(t *testing.T) {
	if got := extToMime(".JPG"); got != "image/jpeg" {
		t.Errorf("extToMime(.JPG) = %q, want image/jpeg", got)
	}
}

func TestExtToMime_Unknown(t *testing.T) {
	if got := extToMime(".xyz"); got != "application/octet-stream" {
		t.Errorf("extToMime(.xyz) = %q, want application/octet-stream", got)
	}
}

// round-trip: mimeToExt → extToMime for image types
func TestMimeExtRoundTrip(t *testing.T) {
	mimes := []string{
		"image/jpeg", "image/png", "image/gif", "application/pdf",
		"text/plain", "application/json", "application/zip",
	}
	for _, mime := range mimes {
		ext := mimeToExt(mime)
		if ext == "" {
			t.Errorf("mimeToExt(%q) returned empty", mime)
			continue
		}
		if got := extToMime(ext); !strings.HasPrefix(got, strings.Split(mime, "/")[0]) {
			t.Errorf("extToMime(mimeToExt(%q)) = %q, expected mime family match", mime, got)
		}
	}
}

func TestFileCloneOwnsSupportedMutableFields(t *testing.T) {
	accessed, deleted := time.Unix(1, 0), time.Unix(2, 0)
	payload := []byte{1, 2}
	nested := map[string]any{"payload": payload}
	original := File{
		File: storefile.File{
			Metadata:     map[string]any{"nested": nested},
			LastAccessAt: &accessed,
			DeletedAt:    &deleted,
		},
		TenantID: "tenant", UploadedBy: "owner", Width: 640, Height: 480, ThumbnailPath: "thumbnail",
	}
	cloned := original.Clone()
	if cloned.TenantID != original.TenantID || cloned.UploadedBy != original.UploadedBy ||
		cloned.Width != original.Width || cloned.Height != original.Height || cloned.ThumbnailPath != original.ThumbnailPath {
		t.Errorf("extension fields changed in clone: %+v", cloned)
	}
	*cloned.LastAccessAt = time.Unix(10, 0)
	*cloned.DeletedAt = time.Unix(20, 0)
	clonedNested := cloned.Metadata["nested"].(map[string]any)
	clonedNested["payload"].([]byte)[0] = 9
	clonedNested["new"] = true
	cloned.Metadata["added"] = true

	if accessed.Unix() != 1 || deleted.Unix() != 2 || payload[0] != 1 || len(nested) != 1 || len(original.Metadata) != 1 {
		t.Errorf("clone mutation changed the original: %+v", original)
	}
}
