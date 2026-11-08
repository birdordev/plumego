package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/router"
)

func BenchmarkServeAsset(b *testing.B) {
	dir := b.TempDir()
	writeTestFile(b, dir, "index.html", "index")
	writeTestFile(b, dir, "assets/app.js", "console.log('asset')")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithCacheControl("public, max-age=31536000")); err != nil {
		b.Fatalf("register: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status: got %d want %d", rec.Code, http.StatusOK)
		}
	}
}

func BenchmarkServePrecompressedAsset(b *testing.B) {
	dir := b.TempDir()
	writeTestFile(b, dir, "index.html", "index")
	writeTestFile(b, dir, "assets/app.js", "console.log('asset')")
	writeTestFile(b, dir, "assets/app.js.br", "compressed asset")

	r := router.NewRouter()
	if err := RegisterFromDir(r, dir, WithPrecompressed(true), WithCacheControl("public, max-age=31536000")); err != nil {
		b.Fatalf("register: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status: got %d want %d", rec.Code, http.StatusOK)
		}
		if rec.Header().Get("Content-Encoding") != "br" {
			b.Fatalf("Content-Encoding: got %q want br", rec.Header().Get("Content-Encoding"))
		}
	}
}
