// Package plumego is the convenience entry point for the plumego HTTP toolkit.
//
// New returns an [*core.App] assembled from the default configuration, making
// it the minimal import for a new service:
//
//	app := plumego.New()
//	app.Get("/ping", http.HandlerFunc(pingHandler))
//	log.Fatal(http.ListenAndServe(":8080", app))
//
// All routing, middleware, and lifecycle methods are on [*core.App].
// For a non-default address, use [NewWithConfig]:
//
//	cfg := plumego.DefaultConfig()
//	cfg.Addr = ":9090"
//	app := plumego.NewWithConfig(cfg)
//
// For production wiring with explicit logger injection, use [core.New] directly.
package plumego

import (
	"net/http"

	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/router"
)

// AppConfig is an alias for [core.AppConfig]. Use [DefaultConfig] as the
// starting point and override only the fields your service requires.
type AppConfig = core.AppConfig

// AppDependencies is an alias for [core.AppDependencies].
type AppDependencies = core.AppDependencies

// DefaultConfig returns the canonical baseline application configuration:
// address ":8080", 30 s read/write timeouts, 5 s header timeout, 1 MiB max
// header bytes, and HTTP/2 enabled.
func DefaultConfig() AppConfig {
	return core.DefaultConfig()
}

// New returns an [*core.App] assembled from [DefaultConfig] with no injected
// dependencies. It is the minimal entry point for a plumego service.
//
// For a non-default address, timeouts, or TLS, use [NewWithConfig].
// For explicit logger injection, use [core.New].
func New() *core.App {
	return core.New(core.DefaultConfig(), core.AppDependencies{})
}

// NewWithConfig returns an [*core.App] assembled from the provided
// configuration with no injected dependencies.
//
// Start from [DefaultConfig] and override only the fields your service
// requires:
//
//	cfg := plumego.DefaultConfig()
//	cfg.Addr = ":9090"
//	app := plumego.NewWithConfig(cfg)
func NewWithConfig(cfg AppConfig) *core.App {
	return core.New(cfg, core.AppDependencies{})
}

// Param extracts the named path parameter from the matched route.
// It returns an empty string when the parameter is absent.
//
//	app.Get("/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	    id := plumego.Param(r, "id")
//	    ...
//	}))
func Param(r *http.Request, name string) string {
	return router.Param(r, name)
}
