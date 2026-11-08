// Package app wires the with-websocket demo dependencies.
// Non-canonical: this demo extends the standard-service layout with x/websocket.
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
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	"github.com/birdordev/plumego/x/websocket"
	"with-websocket/internal/config"
	"with-websocket/internal/message"
)

// App holds application-wide dependencies including the WebSocket server.
type App struct {
	Core *core.App
	Cfg  config.Config
	WS   *websocket.Server
}

// New constructs the App with a WebSocket server.
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
		recoveryMw,
		accesslogMw,
	); err != nil {
		return nil, fmt.Errorf("register middleware: %w", err)
	}

	wsCfg := websocket.DefaultWebSocketConfig()
	wsCfg.Secret = []byte(cfg.WSSecret)
	wsCfg.AllowUnauthenticated = true // demo only; set false and provide a JWT to enable auth
	wsCfg.AllowAllOrigins = true      // demo only; restrict to specific origins in production
	wsCfg.OnMessage = echoHandler

	ws, err := websocket.New(wsCfg)
	if err != nil {
		return nil, fmt.Errorf("create websocket server: %w", err)
	}

	return &App{Core: a, Cfg: cfg, WS: ws}, nil
}

// echoHandler is the OnMessage callback wired at startup.
//
// Text frames: if the payload decodes as a JSON Event with type "ping", the
// handler responds with {"type":"pong"}. All other text frames are echoed
// verbatim so plain-text clients still work.
// Binary frames: always echoed verbatim.
func echoHandler(c *websocket.Conn, msg websocket.Message) error {
	if msg.Op == websocket.OpcodeText {
		ev, err := message.Decode(msg.Data)
		if err == nil && ev.Type == "ping" {
			return c.WriteJSON(message.Event{Type: "pong"})
		}
	}
	return c.WriteMessage(msg.Op, msg.Data)
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
		"tls":  a.Cfg.Core.TLS.Enabled,
	})
	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := a.WS.Shutdown(shutdownCtx); err != nil {
			shutdownErr <- fmt.Errorf("shutdown websocket: %w", err)
			return
		}
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
