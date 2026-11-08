// Package app wires the with-events scenario dependencies.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/x/messaging"
	"with-events/internal/config"
	"with-events/internal/order"
	orderwebhook "with-events/internal/webhook"
)

// Deps contains optional dependencies supplied by tests or embedders.
type Deps struct {
	Logger plumelog.StructuredLogger
	Bus    *messaging.Broker
}

// App holds application-wide dependencies for the event-driven scenario.
type App struct {
	Core      *core.App
	Cfg       config.Config
	Logger    plumelog.StructuredLogger
	Bus       *messaging.Broker
	Messaging *messaging.Service
	Orders    *order.Handler
	Consumer  *order.OrderConsumer
	Webhooks  *orderwebhook.Sender
}

// New constructs the App with an in-process messaging service.
func New(cfg config.Config, deps Deps) (*App, error) {
	logger := deps.Logger
	if logger == nil {
		logger = plumelog.NewLogger()
	}

	coreCfg := core.DefaultConfig()
	coreCfg.Addr = cfg.Addr
	a := core.New(coreCfg, core.AppDependencies{Logger: logger})
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	if err := a.Use(requestid.Middleware(), recoveryMw); err != nil {
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	bus := deps.Bus
	if bus == nil {
		bus = messaging.NewInProcBroker()
	}
	svc := messaging.New(messaging.Config{
		Bus:        bus,
		Logger:     logger,
		ConsumerID: "with-events",
	})
	publisher := order.NewPublisher(bus, order.NewMemoryIdempotencyStore())
	webhooks := orderwebhook.NewSender(cfg.WebhookTargetURL)
	consumer := order.NewConsumer(bus, order.NewMemoryIdempotencyStore(), webhooks)

	return &App{
		Core:      a,
		Cfg:       cfg,
		Logger:    logger,
		Bus:       bus,
		Messaging: svc,
		Orders:    order.NewHandler(publisher, logger),
		Consumer:  consumer,
		Webhooks:  webhooks,
	}, nil
}

// Start prepares the runtime and blocks while the HTTP server runs.
// ctx should be a signal-aware context created in main; cancelling it
// triggers graceful shutdown of the HTTP server and messaging service.
func (a *App) Start(ctx context.Context) error {
	if a.Messaging != nil {
		if err := a.Messaging.Start(ctx); err != nil {
			return fmt.Errorf("start messaging service: %w", err)
		}
		defer func() {
			_ = a.Messaging.Stop(ctx)
		}()
	}
	if a.Consumer != nil {
		if err := a.Consumer.Start(ctx); err != nil {
			return fmt.Errorf("start order consumer: %w", err)
		}
	}
	if err := a.Core.Prepare(); err != nil {
		return fmt.Errorf("prepare server: %w", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		return fmt.Errorf("get server: %w", err)
	}
	a.Logger.Info("starting server", plumelog.Fields{
		"addr": a.Cfg.Addr,
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
