// Package app wires the production reference service.
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
	"github.com/birdordev/plumego/middleware/abuseguard"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/bodylimit"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/middleware/securityheaders"
	"github.com/birdordev/plumego/middleware/timeout"
	"github.com/birdordev/plumego/middleware/tracing"
	"production-service/internal/config"
	"production-service/internal/domain/tenant"
)

// App holds application-wide dependencies.
type App struct {
	Core      *core.App
	Cfg       config.Config
	Metrics   *metrics.BaseMetricsCollector
	Profiles  *tenant.Store
	RateLimit *abuseguard.AbuseGuardMiddleware
}

// New constructs the production reference with explicit middleware wiring.
func New(cfg config.Config) (*App, error) {
	logger := plumelog.NewLogger()
	collector := metrics.NewBaseMetricsCollector()
	profiles, err := tenant.NewStore(cfg.App.ProfileStorePath)
	if err != nil {
		return nil, fmt.Errorf("load profile store: %w", err)
	}
	app := core.New(cfg.Core, core.AppDependencies{Logger: logger})
	rateLimitGuard := abuseguard.NewAbuseGuard(abuseguard.AbuseGuardConfig{
		Rate:     cfg.App.RateLimit,
		Capacity: cfg.App.RateBurst,
		Logger:   app.Logger(),
	})
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
	if err != nil {
		rateLimitGuard.Stop()
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: app.Logger()})
	if err != nil {
		rateLimitGuard.Stop()
		return nil, fmt.Errorf("configure access log middleware: %w", err)
	}
	securityMw, err := securityheaders.Middleware(securityheaders.Config{})
	if err != nil {
		rateLimitGuard.Stop()
		return nil, fmt.Errorf("configure security middleware: %w", err)
	}

	// Middleware order — outermost to innermost (first registered runs first on inbound requests):
	//   requestid    → stamps correlation ID before any logging or error handling
	//   security     → sets security headers (X-Frame-Options, …) on all responses, including
	//                  timed-out, rate-limited, and panicked ones
	//   recovery     → converts panics to 500; inside security so headers are already set
	//   accesslog    → logs all requests including 413 (body too large) and 429 (rate limited);
	//                  must be outer to bodylimit and ratelimit to capture those rejections
	//   bodylimit    → rejects oversized bodies with 413; after accesslog so the 413 is logged
	//   abuseguard   → rejects abusive clients with 429; after accesslog so the 429 is logged
	//   tracing      → records distributed trace spans; after rejection decisions
	//   httpmetrics  → records request count and latency including rejected requests
	//   timeout      → per-request wall-clock limit; innermost so only handler time is counted
	if err := app.Use(
		requestid.Middleware(),
		securityMw,
		recoveryMw,
		accesslogMw,
		bodylimit.Middleware(bodylimit.Config{MaxBytes: cfg.App.BodyLimitBytes, Logger: app.Logger()}),
		rateLimitGuard.Middleware(),
		tracing.Middleware(noopTracer{}),
		httpmetrics.Middleware(collector),
		timeout.Middleware(timeout.Config{Timeout: cfg.App.RequestTimeout}),
	); err != nil {
		rateLimitGuard.Stop()
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	return &App{
		Core:      app,
		Cfg:       cfg,
		Metrics:   collector,
		Profiles:  profiles,
		RateLimit: rateLimitGuard,
	}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
// When ctx is canceled, it triggers a graceful shutdown.
func (a *App) Start(ctx context.Context) error {
	if a.RateLimit != nil {
		defer a.RateLimit.Stop()
	}

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
		// Empty cert/key paths rely on core.Server() having loaded
		// cfg.Core.TLS.CertFile and cfg.Core.TLS.KeyFile into srv.TLSConfig.
		// In most deployments TLS is terminated by the proxy; set those fields
		// in config.go before enabling self-terminating TLS.
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

type noopTracer struct{}

func (noopTracer) Start(ctx context.Context, r *http.Request) (context.Context, tracing.TraceSpan) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) End(status, bytes int, requestID string) {}
func (noopSpan) TraceID() string                         { return "" }
func (noopSpan) SpanID() string                          { return "" }
