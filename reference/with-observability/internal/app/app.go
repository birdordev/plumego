// Package app wires together the with-observability dependencies and manages the
// server lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/bodylimit"
	"github.com/birdordev/plumego/middleware/cors"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/middleware/securityheaders"
	"github.com/birdordev/plumego/middleware/timeout"
	mwtracing "github.com/birdordev/plumego/middleware/tracing"
	"github.com/birdordev/plumego/x/observability"
	"with-observability/internal/config"
)

// App holds application-wide dependencies including the observability components
// that are threaded into handlers and the /metrics route.
type App struct {
	Core      *core.App
	Cfg       config.Config
	Collector *observability.PrometheusCollector
	Tracer    *observability.OpenTelemetryTracer
}

// New constructs the App with a real PrometheusCollector and OpenTelemetryTracer
// wired into the middleware stack. The middleware order matches standard-service
// with tracing inserted after requestid so spans carry the correlation ID.
//
// Middleware order — outermost to innermost:
//
//	requestid   → stamps correlation ID before any logging or error handling
//	security    → security headers on all responses
//	cors        → CORS preflight and headers
//	recovery    → converts panics to 500 responses
//	accesslog   → logs every request/response
//	bodylimit   → rejects oversized bodies with 413
//	httpmetrics → records request count and latency in the PrometheusCollector
//	tracing     → records distributed trace spans in the OpenTelemetryTracer
//	timeout     → per-request wall-clock limit; innermost so only handler time is counted
func New(cfg config.Config) (*App, error) {
	coreApp := core.New(cfg.Core, core.AppDependencies{Logger: plumelog.NewLogger()})

	collector := observability.NewPrometheusCollector(cfg.App.MetricsNamespace).
		WithMaxMemory(cfg.App.MetricsMaxSeries)

	tracer := observability.NewOpenTelemetryTracer(cfg.App.ServiceName)

	securityMw, err := securityheaders.Middleware(securityheaders.Config{})
	if err != nil {
		return nil, fmt.Errorf("configure security headers middleware: %w", err)
	}
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: coreApp.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: coreApp.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure access log middleware: %w", err)
	}
	timeoutMw := timeout.Middleware(timeout.Config{Timeout: 30 * time.Second})
	// Build CORS options from config. When CORSAllowedOrigins is set, use strict
	// defaults that restrict cross-origin access to the enumerated origins. When
	// empty (the default), CORSOptions{} allows all origins ("*") — safe for local
	// development; always set APP_CORS_ALLOWED_ORIGINS in production.
	var corsOpts cors.CORSOptions
	if len(cfg.App.CORSAllowedOrigins) > 0 {
		strictOpts, err := cors.StrictDefaultOptions(cfg.App.CORSAllowedOrigins...)
		if err != nil {
			return nil, fmt.Errorf("configure CORS middleware: %w", err)
		}
		corsOpts = strictOpts
	}

	if err := coreApp.Use(
		requestid.Middleware(),
		securityMw,
		cors.Middleware(corsOpts),
		recoveryMw,
		accesslogMw,
		bodylimit.Middleware(bodylimit.Config{
			MaxBytes: cfg.App.MaxBodyBytes,
			Logger:   coreApp.Logger(),
		}),
		// httpmetrics records each request into the PrometheusCollector so the
		// /metrics endpoint returns real counters and latency data immediately.
		httpmetrics.Middleware(collector),
		// tracing.Middleware creates an OpenTelemetry span per request.
		// Spans are queryable at GET /api/v1/spans for development inspection.
		// In production, replace OpenTelemetryTracer with an OTLP exporter.
		mwtracing.Middleware(tracer),
		timeoutMw,
	); err != nil {
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	return &App{
		Core:      coreApp,
		Cfg:       cfg,
		Collector: collector,
		Tracer:    tracer,
	}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
func (a *App) Start(ctx context.Context) error {
	if err := a.Core.Prepare(); err != nil {
		return fmt.Errorf("prepare server: %w", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		return fmt.Errorf("get server: %w", err)
	}

	a.Core.Logger().Info("starting server", plumelog.Fields{
		"addr":              a.Cfg.Core.Addr,
		"metrics_namespace": a.Cfg.App.MetricsNamespace,
		"tracing":           "enabled",
	})

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		shutdownErr <- a.Core.Shutdown(shutdownCtx)
	}()

	var serveErr error
	if a.Cfg.Core.TLS.Enabled {
		serveErr = srv.ListenAndServeTLS("", "")
	} else {
		serveErr = srv.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("server stopped: %w", serveErr)
	}
	if err := <-shutdownErr; err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	return nil
}
