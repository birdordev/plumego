package app

import (
	"net/http"
	"testing/fstest"

	"github.com/birdordev/plumego/x/frontend"
	"with-frontend/internal/handler"
)

// RegisterRoutes wires JSON API routes and mounts the static frontend.
//
// API routes are registered first so they take precedence over the frontend
// catch-all. The frontend mount uses an embedded in-memory filesystem here so
// the demo compiles and runs without a real build directory. In a real service,
// replace fstest.MapFS with your framework's build output (embed.FS or a dir).
func (a *App) RegisterRoutes() error {
	api := handler.APIHandler{Logger: a.Core.Logger()}
	if err := a.Core.Get(a.Cfg.APIPrefix+"/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}

	// Embed a minimal set of assets so the demo is self-contained.
	// In production: use frontend.RegisterFromDir(a.Core, "./dist", ...) or
	// frontend.RegisterFS with your embedded embed.FS for reproducible builds.
	assets := fstest.MapFS{
		"index.html": {Data: []byte(`<!doctype html><html><body><h1>with-frontend</h1></body></html>`)},
		"app.js":     {Data: []byte(`console.log("with-frontend loaded");`)},
	}
	return frontend.RegisterFS(a.Core, http.FS(assets),
		frontend.WithPrefix(a.Cfg.UIPrefix),
		frontend.WithFallback(a.Cfg.SPAFallback),
		frontend.WithCacheControl("public, max-age=31536000, immutable"),
		frontend.WithIndexCacheControl("no-cache, must-revalidate"),
	)
}
