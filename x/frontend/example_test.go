package frontend_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing/fstest"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/frontend"
)

// The dir-based examples serve testdata/site, a tiny fixture standing in for
// a real build output directory ("./dist" in a typical app).

// ExampleRegisterFromDir demonstrates basic frontend serving
func ExampleRegisterFromDir() {
	r := router.NewRouter()

	// Serve a Next.js/Vite build output directory
	err := frontend.RegisterFromDir(r, "testdata/site",
		frontend.WithPrefix("/"),
		frontend.WithCacheControl("public, max-age=31536000"),
		frontend.WithIndexCacheControl("no-cache"),
		frontend.WithFallback(true), // Enable SPA routing
	)
	if err != nil {
		panic(err)
	}

	index := doGet(r, "/", "")
	fmt.Println("index status:", index.Code)
	fmt.Println("index cache:", index.Header().Get("Cache-Control"))
	fmt.Println("index body:", index.Body.String())

	asset := doGet(r, "/assets/app.js", "")
	fmt.Println("asset status:", asset.Code)
	fmt.Println("asset cache:", asset.Header().Get("Cache-Control"))

	// Unknown paths without a file extension fall back to the SPA shell.
	spa := doGet(r, "/missing/page", "")
	fmt.Println("spa fallback status:", spa.Code)
	fmt.Println("spa fallback body:", spa.Body.String())

	// Output:
	// index status: 200
	// index cache: no-cache
	// index body: <!doctype html><title>demo</title>
	// asset status: 200
	// asset cache: public, max-age=31536000
	// spa fallback status: 200
	// spa fallback body: <!doctype html><title>demo</title>
}

// ExampleWithPrecompressed demonstrates pre-compressed file serving
func ExampleWithPrecompressed() {
	r := router.NewRouter()

	// Enable serving pre-compressed .gz and .br files
	// When app.js.gz exists and the client accepts gzip, serve that instead
	err := frontend.RegisterFromDir(r, "testdata/site",
		frontend.WithPrecompressed(true), // Enable .gz/.br file serving
		frontend.WithCacheControl("public, max-age=31536000"),
	)
	if err != nil {
		panic(err)
	}

	compressed := doGet(r, "/assets/app.js", "gzip")
	fmt.Println("with gzip:", compressed.Header().Get("Content-Encoding"))

	plain := doGet(r, "/assets/app.js", "")
	fmt.Println("without gzip:", plain.Header().Get("Content-Encoding") == "")
	fmt.Println("body:", plain.Body.String())

	// Output:
	// with gzip: gzip
	// without gzip: true
	// body: console.log("demo");
}

// ExampleWithNotFoundPage demonstrates custom 404 pages
func ExampleWithNotFoundPage() {
	r := router.NewRouter()

	// Serve custom 404 error page
	err := frontend.RegisterFromDir(r, "testdata/site",
		frontend.WithNotFoundPage("404.html"), // Custom 404 page
		frontend.WithFallback(false),          // Disable SPA fallback
	)
	if err != nil {
		panic(err)
	}

	missing := doGet(r, "/missing", "")
	fmt.Println("status:", missing.Code)
	fmt.Println("body:", missing.Body.String())

	// Output:
	// status: 404
	// body: <h1>custom not found</h1>
}

// ExampleWithMIMETypes demonstrates custom MIME type mapping
func ExampleWithMIMETypes() {
	r := router.NewRouter()

	// Set custom MIME types for specific file extensions
	err := frontend.RegisterFromDir(r, "testdata/site",
		frontend.WithMIMETypes(map[string]string{
			".wasm": "application/wasm",                // WebAssembly
			".json": "application/json; charset=utf-8", // JSON with charset
			".xml":  "application/xml; charset=utf-8",  // XML with charset
			".webp": "image/webp",                      // WebP images
		}),
	)
	if err != nil {
		panic(err)
	}

	wasm := doGet(r, "/app.wasm", "")
	fmt.Println("status:", wasm.Code)
	fmt.Println("content-type:", wasm.Header().Get("Content-Type"))

	// Output:
	// status: 200
	// content-type: application/wasm
}

// ExampleRegisterFS demonstrates embedded filesystem serving
func ExampleRegisterFS() {
	r := router.NewRouter()

	// In production, pass http.FS(subFS) from your embed.FS/fs.Sub result.
	// fstest.MapFS keeps this example self-contained.
	assets := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html>")},
	}
	err := frontend.RegisterFS(r, http.FS(assets),
		frontend.WithPrefix("/app"),
		frontend.WithCacheControl("public, max-age=31536000"),
	)
	if err != nil {
		panic(err)
	}

	index := doGet(r, "/app/", "")
	fmt.Println("status:", index.Code)
	fmt.Println("body:", index.Body.String())

	// Output:
	// status: 200
	// body: <!doctype html>
}

// ExampleNewMountFS demonstrates explicit construction before router wiring.
func ExampleNewMountFS() {
	r := router.NewRouter()

	assets := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html>")},
	}
	mount, err := frontend.NewMountFS(http.FS(assets),
		frontend.WithPrefix("/app"),
	)
	if err != nil {
		panic(err)
	}

	if err := mount.Register(r); err != nil {
		panic(err)
	}

	index := doGet(r, "/app/", "")
	fmt.Println("status:", index.Code)
	fmt.Println("body:", index.Body.String())

	// Output:
	// status: 200
	// body: <!doctype html>
}

// ExampleWithPrefix demonstrates mounting at a non-root path
func ExampleWithPrefix() {
	r := router.NewRouter()

	// Mount frontend at /app instead of /
	err := frontend.RegisterFromDir(r, "testdata/site",
		frontend.WithPrefix("/app"), // Access via /app, /app/assets/*, etc.
	)
	if err != nil {
		panic(err)
	}

	mounted := doGet(r, "/app/", "")
	fmt.Println("mounted status:", mounted.Code)
	fmt.Println("mounted body:", mounted.Body.String())

	root := doGet(r, "/", "")
	fmt.Println("root status:", root.Code)

	// Output:
	// mounted status: 200
	// mounted body: <!doctype html><title>demo</title>
	// root status: 404
}

// ExampleWithHeaders demonstrates custom response headers
func ExampleWithHeaders() {
	r := router.NewRouter()

	// Add custom headers to all responses
	err := frontend.RegisterFromDir(r, "testdata/site",
		frontend.WithHeaders(map[string]string{
			"X-Frame-Options":        "DENY",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "strict-origin-when-cross-origin",
		}),
	)
	if err != nil {
		panic(err)
	}

	index := doGet(r, "/", "")
	fmt.Println("status:", index.Code)
	fmt.Println("x-frame-options:", index.Header().Get("X-Frame-Options"))
	fmt.Println("x-content-type-options:", index.Header().Get("X-Content-Type-Options"))
	fmt.Println("referrer-policy:", index.Header().Get("Referrer-Policy"))

	// Output:
	// status: 200
	// x-frame-options: DENY
	// x-content-type-options: nosniff
	// referrer-policy: strict-origin-when-cross-origin
}

// ExampleRegisterFromDir_full demonstrates all options together in a
// production-ready configuration.
func ExampleRegisterFromDir_full() {
	r := router.NewRouter()

	// Production-ready configuration with all features
	err := frontend.RegisterFromDir(r, "testdata/site",
		// Mounting
		frontend.WithPrefix("/"),
		frontend.WithIndex("index.html"),

		// Caching
		frontend.WithCacheControl("public, max-age=31536000, immutable"),
		frontend.WithIndexCacheControl("no-cache, must-revalidate"),

		// Performance
		frontend.WithPrecompressed(true), // Serve .gz/.br files

		// Routing
		frontend.WithFallback(true), // SPA mode

		// Custom pages
		frontend.WithNotFoundPage("404.html"),
		frontend.WithErrorPage("500.html"),

		// MIME types
		frontend.WithMIMETypes(map[string]string{
			".wasm": "application/wasm",
			".json": "application/json; charset=utf-8",
		}),

		// Security headers
		frontend.WithHeaders(map[string]string{
			"X-Frame-Options":        "DENY",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "strict-origin-when-cross-origin",
		}),
	)
	if err != nil {
		panic(err)
	}

	index := doGet(r, "/", "gzip")
	fmt.Println("index status:", index.Code)
	fmt.Println("index cache:", index.Header().Get("Cache-Control"))
	fmt.Println("index x-frame-options:", index.Header().Get("X-Frame-Options"))

	wasm := doGet(r, "/app.wasm", "")
	fmt.Println("wasm content-type:", wasm.Header().Get("Content-Type"))

	// Output:
	// index status: 200
	// index cache: no-cache, must-revalidate
	// index x-frame-options: DENY
	// wasm content-type: application/wasm
}

// doGet issues a GET request against h and returns the recorded response.
func doGet(h http.Handler, path, acceptEncoding string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
