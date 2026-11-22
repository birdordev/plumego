package app

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"testing/fstest"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/frontend"
	"with-frontend/internal/handler"
)

// RegisterRoutes 把 JSON API 和静态前端挂载到独立的路由范围。
func (a *App) RegisterRoutes() error {
	apiPrefix := path.Clean(a.Cfg.APIPrefix)
	uiPrefix := path.Clean(a.Cfg.UIPrefix)
	if !strings.HasPrefix(apiPrefix, "/") || apiPrefix == "/" {
		return fmt.Errorf("API_PREFIX must be an absolute path other than /")
	}
	if uiPrefix == apiPrefix || strings.HasPrefix(uiPrefix, apiPrefix+"/") {
		return fmt.Errorf("UI_PREFIX cannot be inside API_PREFIX")
	}

	api := handler.APIHandler{Logger: a.Core.Logger()}
	apiRouter := router.NewRouter(router.WithMethodNotAllowed(true), router.WithNotFoundHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeNotFound).Build())
		}),
	))
	if err := apiRouter.AddRoute(http.MethodGet, apiPrefix+"/status", http.HandlerFunc(api.Status)); err != nil {
		return err
	}
	apiRouter.Freeze()
	if err := a.Core.Any(apiPrefix, apiRouter); err != nil {
		return err
	}
	if err := a.Core.Any(apiPrefix+"/*apiPath", apiRouter); err != nil {
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
