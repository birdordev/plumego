// Package app wires together the with-ai demo dependencies and manages the
// server lifecycle.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"with-ai/internal/config"
)

// App holds application-wide dependencies.
type App struct {
	Core *core.App
	Cfg  config.Config
}

// New constructs the App with stable-root middleware wired.
func New(cfg config.Config) (*App, error) {
	app := core.New(cfg.Core, core.AppDependencies{Logger: plumelog.NewLogger()})

	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	if err := app.Use(
		requestid.Middleware(),
		recoveryMw,
	); err != nil {
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	return &App{Core: app, Cfg: cfg}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
// Shutdown is driven by ctx so the application owner controls process signals.
func (a *App) Start(ctx context.Context) error {
	a.Core.Logger().Info("starting server", plumelog.Fields{"addr": a.Cfg.Core.Addr})
	return a.Core.RunContext(ctx, 15*time.Second)
}
