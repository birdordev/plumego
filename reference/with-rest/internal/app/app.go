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
	"github.com/birdordev/plumego/x/rest"
	"with-rest/internal/config"
	"with-rest/internal/domain/user"
	"with-rest/internal/handler"
	"with-rest/internal/validation/playground"
)

// App holds application-wide dependencies.
type App struct {
	Core  *core.App
	Cfg   config.Config
	Users *rest.DBResourceController[user.User]
	Items *handler.CreateItem
}

// New constructs the App with all dependencies wired explicitly.
// The middleware stack mirrors standard-service so x/rest integrations
// run with the full production-oriented middleware baseline.
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

	// Middleware order matches standard-service (outermost to innermost):
	//   requestid  → stamps correlation ID before any logging or error handling
	//   security   → security headers on all responses
	//   cors       → CORS preflight and headers; set APP_CORS_ALLOWED_ORIGINS to restrict
	//               cross-origin access to known domains in production (see env.example)
	//   recovery   → converts panics to 500 responses
	//   accesslog  → logs all requests including 413; outer to bodylimit so rejections are logged
	//   bodylimit  → rejects oversized bodies with 413
	//   httpmetrics→ noop collector; swap for observability.NewPrometheusCollector in production
	//   timeout    → per-request wall-clock limit; innermost so only handler time is counted
	if err := app.Use(
		requestid.Middleware(),
		securityMw,
		cors.Middleware(corsOpts),
		recoveryMw,
		accesslogMw,
		bodylimit.Middleware(bodylimit.Config{
			MaxBytes: 1 << 20, // 1 MiB default; make configurable via AppConfig if needed
			Logger:   app.Logger(),
		}),
		httpmetrics.Middleware(metrics.NewNoopCollector()),
		timeout.Middleware(timeout.Config{Timeout: 30 * time.Second}),
	); err != nil {
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	spec := rest.DefaultResourceSpec("users").WithPrefix("/api/users")
	users := rest.NewDBResource[user.User](spec, user.NewRepository())

	return &App{
		Core:  app,
		Cfg:   cfg,
		Users: users,
		Items: handler.NewCreateItem(playground.NewValidator(), app.Logger()),
	}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
// Shutdown is driven by ctx so the application owner controls process signals.
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
	})
	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		shutdownErr <- a.Core.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server stopped: %w", err)
	}
	if err := <-shutdownErr; err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	return nil
}
