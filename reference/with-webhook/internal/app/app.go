// Package app wires the with-webhook demo dependencies.
// Non-canonical: this demo extends the standard-service layout with x/messaging/webhook.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/x/messaging/pubsub"
	"github.com/birdordev/plumego/x/messaging/webhook"
	"with-webhook/internal/config"
)

// App holds application-wide dependencies including the inbound webhook receiver.
type App struct {
	Core    *core.App
	Cfg     config.Config
	Inbound *webhook.Inbound
}

// New constructs the App with an inbound webhook receiver backed by an in-process broker.
func New(cfg config.Config) (*App, error) {
	a := core.New(cfg.Core, core.AppDependencies{Logger: plumelog.NewLogger()})
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: a.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: a.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure access log middleware: %w", err)
	}
	if err := a.Use(
		requestid.Middleware(),
		accesslogMw,
		recoveryMw,
	); err != nil {
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	broker := pubsub.New()
	inbound := webhook.NewInbound(
		webhook.WebhookInConfig{Enabled: true},
		broker,
		a.Logger(),
	)

	return &App{Core: a, Cfg: cfg, Inbound: inbound}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
// Shutdown is driven by ctx so the application owner controls process signals.
func (a *App) Start(ctx context.Context) error {
	a.Core.Logger().Info("starting server", plumelog.Fields{
		"addr": a.Cfg.Core.Addr,
		"tls":  a.Cfg.Core.TLS.Enabled,
	})
	return a.Core.RunContext(ctx, 15*time.Second)
}
