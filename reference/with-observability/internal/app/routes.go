package app

import (
	"fmt"
	"net/http"

	"github.com/birdordev/plumego/x/observability"
	"with-observability/internal/handler"
)

// RegisterRoutes wires all HTTP routes for the with-observability reference app.
//
// Route map:
//
//	GET /           → service identity
//	GET /healthz    → liveness probe (always 200 while process is alive)
//	GET /readyz     → readiness probe (probes registered ComponentCheckers)
//	GET /api/hello  → service discovery
//	GET /metrics    → Prometheus text exposition (scrape this with Prometheus)
//
// Observability inspection (remove in production):
//
//	GET /api/v1/stats           → JSON summary of PrometheusCollector counters
//	GET /api/v1/collector-stats → collector stats via metrics.StatsReader interface
//	GET /api/v1/spans           → OpenTelemetry spans collected in-process
//	GET /api/v1/spans?limit=N   → most recent N spans
func (a *App) RegisterRoutes() error {
	health := handler.HealthHandler{
		ServiceName: a.Cfg.App.ServiceName,
		Logger:      a.Core.Logger(),
	}
	api := handler.APIHandler{
		Logger:      a.Core.Logger(),
		ServiceName: a.Cfg.App.ServiceName,
		Version:     a.Cfg.App.Version,
	}
	obs := handler.ObservabilityHandler{
		Logger:    a.Core.Logger(),
		Collector: a.Collector,
		Tracer:    a.Tracer,
	}
	mh := handler.MetricsHandler{
		Logger:   a.Core.Logger(),
		Observer: a.Collector,
	}

	// Wire the Prometheus exporter to GET /metrics.
	// The exporter reads a live snapshot from the collector on every request so
	// the output is always current without any buffering or caching.
	exporter, err := observability.NewPrometheusExporter(a.Collector)
	if err != nil {
		return fmt.Errorf("create prometheus exporter: %w", err)
	}

	root := newRouteReg(a.Core)
	root.get("/", http.HandlerFunc(api.Root))
	root.get("/healthz", http.HandlerFunc(health.Live))
	root.get("/readyz", http.HandlerFunc(health.Ready))
	root.get("/api/hello", http.HandlerFunc(api.Hello))
	root.get("/metrics", exporter.Handler())
	if root.err != nil {
		return root.err
	}

	v1 := newRouteReg(a.Core.Group("/api/v1"))
	v1.get("/stats", http.HandlerFunc(obs.Stats))
	v1.get("/collector-stats", http.HandlerFunc(mh.CollectorStats))
	v1.get("/spans", http.HandlerFunc(obs.Spans))
	return v1.err
}

// routeAdder is the minimal interface shared by *core.App and *core.RouteGroup.
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
