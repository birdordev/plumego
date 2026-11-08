// Package app wires together the application dependencies and manages the
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
	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/bodylimit"
	"github.com/birdordev/plumego/middleware/cors"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/middleware/securityheaders"
	"github.com/birdordev/plumego/middleware/timeout"
	"standard-service/internal/config"
)

// App holds application-wide dependencies.
type App struct {
	Core *core.App
	Cfg  config.Config
}

// New constructs the App with explicit stable-root wiring only.
func New(cfg config.Config) (*App, error) {
	app := core.New(cfg.Core, core.AppDependencies{Logger: plumelog.NewLogger()})
	securityMw, err := securityheaders.Middleware(securityheaders.Config{})
	if err != nil {
		return nil, fmt.Errorf("configure security headers middleware: %w", err)
	}
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: app.Logger()})
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

	// Middleware order — outermost to innermost (first registered runs first on inbound requests):
	//   requestid  → stamps correlation ID before any logging or error handling
	//   security   → security headers (X-Frame-Options, X-Content-Type-Options, …) on all responses
	//   cors       → CORS preflight and headers; set APP_CORS_ALLOWED_ORIGINS to restrict
	//               cross-origin access to known domains in production (see env.example)
	//   recovery   → converts panics to 500 responses; inside cors/security so headers still apply
	//   accesslog  → logs every request/response; after recovery so panics appear as 500
	//   bodylimit  → rejects oversized bodies with 413; after accesslog so the 413 is logged
	//   [abuseguard]→ optional: add middleware/abuseguard.NewAbuseGuard here (after bodylimit so
	//               oversized bodies are rejected before a rate-limit token is consumed)
	//               See middleware/abuseguard.AbuseGuardConfig for token-bucket defaults.
	//   httpmetrics→ measures handler latency and status; swap NewNoopCollector for
	//               observability.NewPrometheusCollector (from x/observability) in production,
	//               then register GET /metrics with observability.NewPrometheusExporter.
	//               See reference/with-ops for a complete wiring example.
	//   timeout    → per-request wall-clock limit; innermost so only handler time is counted
	if err := app.Use(
		requestid.Middleware(),
		securityMw,
		cors.Middleware(corsOpts),
		recoveryMw,
		accesslogMw,
		bodylimit.Middleware(bodylimit.Config{
			MaxBytes: cfg.App.MaxBodyBytes,
			Logger:   app.Logger(),
		}),
		httpmetrics.Middleware(metrics.NewNoopCollector()),
		timeoutMw,
	); err != nil {
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	// Warn about insecure defaults to make the risk observable.
	if cfg.App.WriteKey == "" {
		app.Logger().Warn("write guard disabled: POST/PUT/PATCH/DELETE /api/v1/items are publicly writable; set APP_WRITE_KEY in production", plumelog.Fields{})
	}
	if len(cfg.App.CORSAllowedOrigins) == 0 {
		app.Logger().Warn("CORS allows all origins (*); set APP_CORS_ALLOWED_ORIGINS in production", plumelog.Fields{})
	}

	return &App{
		Core: app,
		Cfg:  cfg,
	}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
// When ctx is canceled, it triggers a graceful shutdown.
func (a *App) Start(ctx context.Context) error {
	if err := a.Core.Prepare(); err != nil {
		return fmt.Errorf("prepare server: %w", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		return fmt.Errorf("get server: %w", err)
	}

	a.Core.Logger().Info("starting server", plumelog.Fields{
		"addr": a.Cfg.Core.Addr,
		"tls":  a.Cfg.Core.TLS.Enabled,
	})

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		// Allow up to 15 s for in-flight requests to complete before forcing close.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		shutdownErr <- a.Core.Shutdown(shutdownCtx)
	}()

	var serveErr error
	if a.Cfg.Core.TLS.Enabled {
		// Empty cert/key paths are valid here: core.Prepare() already loaded
		// cfg.Core.TLS.CertFile and cfg.Core.TLS.KeyFile into srv.TLSConfig.
		// Both paths are validated at startup by config.Validate before reaching here.
		serveErr = srv.ListenAndServeTLS("", "")
	} else {
		serveErr = srv.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("server stopped: %w", serveErr)
	}
	// Always drain the shutdown channel so the goroutine is not leaked and
	// shutdown errors are not silently discarded.
	if err := <-shutdownErr; err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	return nil
}
