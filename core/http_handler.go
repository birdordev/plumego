package core

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
)

var (
	errHandlerBuilding      = errors.New("handler preparation is already in progress")
	errHandlerNotConfigured = errors.New("handler not configured")
)

// ensureHandlerPrepared freezes registration before calling middleware builders.
// It reports whether this call published a handler with no registered routes.
func (a *App) ensureHandlerPrepared() (bool, error) {
	if a.handlerFast.Load() != nil {
		return false, nil
	}

	a.mu.Lock()
	if a.handler != nil {
		a.mu.Unlock()
		return false, nil
	}
	if a.handlerBuilding {
		a.mu.Unlock()
		return false, errHandlerBuilding
	}
	chain, r := a.middlewareChain, a.router
	if chain == nil || r == nil {
		a.mu.Unlock()
		return false, errHandlerNotConfigured
	}
	a.freezeRegistration()
	if chain.Len() == 0 {
		a.publishHandlerLocked(r)
		a.mu.Unlock()
		return len(r.Routes()) == 0, nil
	}
	a.handlerBuilding = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.handlerBuilding = false
		a.mu.Unlock()
	}()
	handler := chain.Build(r)
	if handler == nil {
		return false, errHandlerNotConfigured
	}
	warnNoRoutes := len(r.Routes()) == 0

	a.mu.Lock()
	a.publishHandlerLocked(handler)
	a.mu.Unlock()
	return warnNoRoutes, nil
}

func (a *App) publishHandlerLocked(handler http.Handler) {
	a.handler = handler
	a.preparationState = PreparationStateHandlerPrepared
	a.handlerFast.Store(&handlerRef{h: handler})
}

// ensureServerPrepared constructs the backing http.Server for the explicit
// Prepare/Server lifecycle path and reports whether to warn about no routes.
func (a *App) ensureServerPrepared() (bool, error) {
	a.mu.RLock()
	prepared := a.httpServer != nil
	building := a.handlerBuilding && a.handler == nil
	a.mu.RUnlock()
	if prepared {
		return false, nil
	}
	if building {
		return false, wrapCoreError(errHandlerBuilding, operationPrepareServer, nil)
	}

	config, err := a.serverConfigSnapshot()
	if err != nil {
		return false, err
	}
	if err := validateServerConfig(config); err != nil {
		return false, wrapCoreError(err, operationPrepareServer, nil)
	}

	tlsConfig, err := prepareTLSConfig(config.TLS)
	if err != nil {
		return false, wrapCoreError(err, operationPrepareServer, nil)
	}

	warnNoRoutes, err := a.ensureHandlerPrepared()
	if err != nil {
		return false, wrapCoreError(err, operationPrepareServer, nil)
	}
	handler, err := a.preparedHandlerSnapshot()
	if err != nil {
		return false, err
	}
	a.installHTTPServer(config, tlsConfig, handler)
	return warnNoRoutes, nil
}

func (a *App) warnNoRoutes() {
	defer func() { _ = recover() }()
	a.Logger().Warn("no routes registered. Register routes before preparing the application.", nil)
}

func (a *App) serverConfigSnapshot() (AppConfig, error) {
	a.mu.RLock()
	cfg := a.config
	routerConfigured := a.router != nil
	middlewareConfigured := a.middlewareChain != nil
	a.mu.RUnlock()

	if cfg == nil {
		return AppConfig{}, wrapCoreError(fmt.Errorf("app config not configured"), operationPrepareServer, nil)
	}
	if !routerConfigured || !middlewareConfigured {
		return AppConfig{}, wrapCoreError(fmt.Errorf("router or middleware chain not configured"), operationPrepareServer, nil)
	}
	return *cfg, nil
}

func (a *App) preparedHandlerSnapshot() (http.Handler, error) {
	a.mu.RLock()
	handler := a.handler
	a.mu.RUnlock()

	if handler == nil {
		return nil, wrapCoreError(fmt.Errorf("handler not configured"), operationPrepareServer, nil)
	}
	return handler, nil
}

func (a *App) installHTTPServer(config AppConfig, tlsConfig *tls.Config, handler http.Handler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.httpServer != nil {
		return
	}
	a.httpServer = &http.Server{
		Addr:              config.Addr,
		Handler:           handler,
		ReadTimeout:       config.ReadTimeout,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
		MaxHeaderBytes:    config.MaxHeaderBytes,
		TLSConfig:         tlsConfig,
	}
	a.connTracker = newConnectionTracker(a.Logger(), config.DrainInterval)
	a.httpServer.ConnState = a.connTracker.track
	a.preparationState = PreparationStateServerPrepared

	if !config.HTTP2Enabled {
		a.httpServer.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))
	}
}

func prepareTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(cfg.CertFile) == "" || strings.TrimSpace(cfg.KeyFile) == "" {
		return nil, fmt.Errorf("TLS enabled but certificate or key file not provided")
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls certificate: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
	}, nil
}

// ServeHTTP allows App to be used directly with net/http servers.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if warnNoRoutes, _ := a.ensureHandlerPrepared(); warnNoRoutes {
		a.warnNoRoutes()
	}

	if ref := a.handlerFast.Load(); ref != nil {
		ref.h.ServeHTTP(w, r)
		return
	}

	_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeUnavailable).Message("handler not configured").Build())
}
