package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/log"
)

// Run prepares the application and starts listening for HTTP requests.
// It blocks until the server stops and returns nil on a clean shutdown via Shutdown.
// For explicit lifecycle control use Prepare and Server directly.
func (a *App) Run() error {
	if err := a.Prepare(); err != nil {
		return err
	}
	server, err := a.Server()
	if err != nil {
		return err
	}
	a.mu.RLock()
	tlsEnabled := a.config.TLS.Enabled
	a.mu.RUnlock()
	if tlsEnabled {
		// Empty strings: certificates are already loaded into server.TLSConfig by Prepare;
		// the stdlib skips LoadX509KeyPair when configHasCert && certFile == "" && keyFile == "".
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	return normalizeRunError(err)
}

func normalizeRunError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Prepare freezes routing/middleware state and constructs the HTTP server.
func (a *App) Prepare() error {
	return a.ensureServerPrepared()
}

// Server returns the prepared HTTP server instance.
func (a *App) Server() (*http.Server, error) {
	a.mu.RLock()
	server := a.httpServer
	a.mu.RUnlock()

	if server == nil {
		return nil, wrapCoreError(fmt.Errorf("server not prepared"), operationGetServer, nil)
	}
	return server, nil
}

// Shutdown gracefully stops the prepared HTTP server.
func (a *App) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.RLock()
	httpServer := a.httpServer
	connTracker := a.connTracker
	a.mu.RUnlock()

	if httpServer == nil {
		return wrapCoreError(fmt.Errorf("server not prepared"), operationShutdownApp, nil)
	}
	if connTracker != nil {
		connTracker.startDrain(ctx)
	}

	var shutdownErr error
	if err := httpServer.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
		a.Logger().Error("Server shutdown error", log.Fields{"error": err})
		shutdownErr = wrapCoreError(err, operationShutdownApp, nil)
	}

	return shutdownErr
}

type connectionTracker struct {
	open         atomic.Int64         // Number of open connections
	drainStarted atomic.Bool          // Whether drain logging has been started
	logger       log.StructuredLogger // Logger for connection tracking
	interval     time.Duration        // Interval at which to log open connection counts
}

func newConnectionTracker(logger log.StructuredLogger, interval time.Duration) *connectionTracker {
	if interval <= 0 {
		interval = defaultDrainInterval
	}
	return &connectionTracker{logger: logger, interval: interval}
}

func (t *connectionTracker) track(_ net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		t.open.Add(1)
	case http.StateHijacked, http.StateClosed:
		t.decrementOpen()
	}
}

func (t *connectionTracker) decrementOpen() {
	if t.open.Load() <= 0 {
		return
	}
	if v := t.open.Add(-1); v < 0 {
		t.open.CompareAndSwap(v, 0)
	}
}

func (t *connectionTracker) startDrain(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	if !t.drainStarted.CompareAndSwap(false, true) {
		return false
	}
	go t.drain(ctx)
	return true
}

func (t *connectionTracker) drain(ctx context.Context) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		if t.open.Load() <= 0 {
			return
		}
		select {
		case <-ctx.Done():
			if t.open.Load() > 0 {
				t.drainStarted.Store(false)
			}
			return
		case <-ticker.C:
			if t.logger != nil {
				t.logger.Info("draining open connections", log.Fields{"open_connections": t.open.Load()})
			}
		}
	}
}
