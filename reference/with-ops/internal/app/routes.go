package app

import (
	"context"
	"net/http"
	"time"

	"github.com/birdordev/plumego/x/observability/ops"
	"with-ops/internal/handler"
)

// RegisterRoutes wires all HTTP routes for the with-ops demo.
// Stable app routes come first; then the ops handler registers its own paths.
func (a *App) RegisterRoutes() error {
	h := handler.New(a.Cfg, a.Collector, a.Core.Logger())

	root := newRouteReg(a.Core)
	root.get("/", http.HandlerFunc(h.Root))
	root.get("/metrics", http.HandlerFunc(h.Metrics))
	if root.err != nil {
		return root.err
	}

	// ops.Handler accepts a RouteRegistrar so we pass *core.App directly —
	// no router exposure needed.
	opsHandler := ops.New(ops.Options{
		Enabled:  a.Cfg.OpsEnabled,
		BasePath: a.Cfg.OpsBasePath,
		Auth: ops.AuthConfig{
			Token: a.Cfg.OpsToken,
		},
		Hooks: ops.Hooks{
			QueueStats: func(ctx context.Context, queue string) (ops.QueueStats, error) {
				return ops.QueueStats{Queue: queue, Queued: 0, UpdatedAt: time.Now().UTC()}, nil
			},
		},
		Logger: a.Core.Logger(),
	})
	return opsHandler.RegisterRoutes(a.Core)
}

type routeAdder interface {
	Get(path string, h http.Handler) error
}

type routeReg struct {
	adder routeAdder
	err   error
}

func newRouteReg(adder routeAdder) *routeReg { return &routeReg{adder: adder} }

func (r *routeReg) get(path string, h http.Handler) { r.record(r.adder.Get(path, h)) }
func (r *routeReg) record(err error) {
	if r.err == nil {
		r.err = err
	}
}
