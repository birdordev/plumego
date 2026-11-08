// Package app wires the with-gateway demo dependencies.
// Non-canonical: this demo extends the standard-service layout with x/gateway.
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
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/middleware/securityheaders"
	"github.com/birdordev/plumego/middleware/timeout"
	"github.com/birdordev/plumego/x/gateway"
	"with-gateway/internal/config"
)

// App holds application-wide dependencies including the reverse proxy.
type App struct {
	Core  *core.App
	Cfg   config.Config
	Proxy *gateway.GatewayProxy
}

// New constructs the App with a gateway reverse proxy.
// Middleware order matches standard-service so the gateway runs with the same
// production-oriented baseline. CORS is intentionally omitted — the gateway
// proxies CORS headers from the backend rather than imposing its own policy.
func New(cfg config.Config) (*App, error) {
	a := core.New(cfg.Core, core.AppDependencies{Logger: plumelog.NewLogger()})

	securityMw, err := securityheaders.Middleware(securityheaders.Config{})
	if err != nil {
		return nil, fmt.Errorf("configure security headers middleware: %w", err)
	}
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: a.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: a.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure access log middleware: %w", err)
	}

	// Middleware order — outermost to innermost:
	//   requestid → stamps correlation ID
	//   security  → security headers on all responses
	//   recovery  → converts panics to 500
	//   accesslog → logs all requests; outer to bodylimit so 413s are logged
	//   bodylimit → protects the backend from oversized request bodies
	//   timeout   → per-request wall-clock limit; innermost
	if err := a.Use(
		requestid.Middleware(),
		securityMw,
		recoveryMw,
		accesslogMw,
		bodylimit.Middleware(bodylimit.Config{
			MaxBytes: 1 << 20, // 1 MiB
			Logger:   a.Logger(),
		}),
		timeout.Middleware(timeout.Config{Timeout: 30 * time.Second}),
	); err != nil {
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	proxy, err := gateway.NewGateway(gateway.GatewayConfig{
		Targets: []string{cfg.GatewayBackend},
	})
	if err != nil {
		return nil, fmt.Errorf("configure gateway proxy: %w", err)
	}

	return &App{Core: a, Cfg: cfg, Proxy: proxy}, nil
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
		"addr":    a.Cfg.Core.Addr,
		"backend": a.Cfg.GatewayBackend,
		"tls":     a.Cfg.Core.TLS.Enabled,
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
