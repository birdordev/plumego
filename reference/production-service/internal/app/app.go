// Package app wires the production reference service.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/middleware"
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

	components *core.LifecycleGroup
	probe      chan struct{}
	ready      atomic.Bool
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
	clientIP, err := trustedClientIP(cfg.App.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}
	rateLimitGuard := abuseguard.NewAbuseGuard(abuseguard.AbuseGuardConfig{
		Rate:     cfg.App.RateLimit,
		Capacity: cfg.App.RateBurst,
		KeyFunc:  clientIP,
		Logger:   app.Logger(),
	})
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
	if err != nil {
		rateLimitGuard.Stop()
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	accesslogMw, err := accesslog.MiddlewareWithClientIP(accesslog.Config{Logger: app.Logger()}, clientIP)
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
	//   httpmetrics  → records final status, including early rejections and recovered panics
	//   recovery     → converts panics to 500; inside security so headers are already set
	//   accesslog    → logs all requests including 413 (body too large) and 429 (rate limited);
	//                  must be outer to bodylimit and ratelimit to capture those rejections
	//   bodylimit    → rejects oversized bodies with 413; after accesslog so the 413 is logged
	//   abuseguard   → rejects abusive clients with 429; after accesslog so the 429 is logged
	//   tracing      → records distributed trace spans; after rejection decisions
	//   timeout      → per-request wall-clock limit; innermost so only handler time is counted
	if err := app.Use(
		requestid.Middleware(),
		securityMw,
		httpmetrics.Middleware(collector),
		recoveryMw,
		accesslogMw,
		bodylimit.Middleware(bodylimit.Config{MaxBytes: cfg.App.BodyLimitBytes, Logger: app.Logger()}),
		rateLimitGuard.Middleware(),
		tracing.Middleware(noopTracer{}),
		timeout.Middleware(timeout.Config{Timeout: cfg.App.RequestTimeout}),
	); err != nil {
		rateLimitGuard.Stop()
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	components := core.NewLifecycleGroup().Add("rate_limiter", core.Component{OnStop: func(context.Context) error {
		rateLimitGuard.Stop()
		return nil
	}})
	return &App{
		Core:       app,
		Cfg:        cfg,
		Metrics:    collector,
		Profiles:   profiles,
		RateLimit:  rateLimitGuard,
		components: components,
		probe:      make(chan struct{}, 1),
	}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
// When ctx is canceled, it triggers a graceful shutdown.
func (a *App) Start(ctx context.Context) error {
	return a.start(ctx, func(ctx context.Context, address string) (net.Listener, error) {
		return new(net.ListenConfig).Listen(ctx, "tcp", address)
	})
}

func (a *App) start(ctx context.Context, listen func(context.Context, string) (net.Listener, error)) (result error) {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := a.components.Stop(cleanupCtx); err != nil {
			result = errors.Join(result, fmt.Errorf("stop components: %w", err))
		}
	}()
	if err := a.components.Start(ctx); err != nil {
		return fmt.Errorf("start components: %w", err)
	}

	listener, err := listen(ctx, srv.Addr)
	if err != nil {
		return fmt.Errorf("bind server: %w", err)
	}
	shutdownErr := make(chan error, 1)
	listenerDone := make(chan struct{})
	a.ready.Store(true)
	go func() {
		select {
		case <-ctx.Done():
		case <-listenerDone:
		}
		a.ready.Store(false)
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		err := a.Core.Shutdown(shutdownCtx)
		if err != nil {
			err = errors.Join(err, srv.Close())
		}
		shutdownErr <- err
	}()

	a.Core.Logger().Info("starting server", plumelog.Fields{"addr": srv.Addr, "tls": a.Cfg.Core.TLS.Enabled})
	var serveErr error
	if a.Cfg.Core.TLS.Enabled {
		serveErr = srv.ServeTLS(listener, "", "")
	} else {
		serveErr = srv.Serve(listener)
	}
	a.ready.Store(false)
	close(listenerDone)
	drainErr := <-shutdownErr
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, drainErr)
}

var errRuntimeNotReady = errors.New("runtime is not ready")

type noopTracer struct{}

func (noopTracer) Start(ctx context.Context, r *http.Request) (context.Context, tracing.TraceSpan) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) End(status, bytes int, requestID string) {}
func (noopSpan) TraceID() string                         { return "" }
func (noopSpan) SpanID() string                          { return "" }

func trustedClientIP(configured string) (func(*http.Request) string, error) {
	var prefixes []netip.Prefix
	if strings.TrimSpace(configured) != "" {
		for _, value := range strings.Split(configured, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			prefixes = append(prefixes, prefix)
		}
	}
	return middleware.TrustedProxyClientIP(prefixes...)
}
