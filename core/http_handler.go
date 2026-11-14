package core

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
)

// ensureHandlerPrepared freezes registration before calling middleware builders.
// A failed build releases the preparation lock without publishing a handler.
func (a *App) ensureHandlerPrepared() {
	if a.handlerFast.Load() != nil {
		return
	}

	a.handlerPrepareMu.Lock()
	defer a.handlerPrepareMu.Unlock()
	if a.handlerFast.Load() != nil {
		return
	}

	a.mu.Lock()
	chain, r := a.middlewareChain, a.router
	if chain == nil || r == nil {
		a.mu.Unlock()
		return
	}
	a.freezeRegistration()
	a.mu.Unlock()

	if len(r.Routes()) == 0 {
		a.Logger().Warn("no routes registered: all requests will receive 404; call app.Get/Post/… before Prepare", nil)
	}
	handler := chain.Build(r)
	if handler == nil {
		return
	}

	a.mu.Lock()
	a.handler = handler
	a.preparationState = PreparationStateHandlerPrepared
	a.handlerFast.Store(&handlerRef{h: handler})
	a.mu.Unlock()
}

// ensureServerPrepared constructs the backing http.Server for the explicit
// Prepare/Server lifecycle path.
func (a *App) ensureServerPrepared() error {
	a.serverPrepareMu.Lock()
	defer a.serverPrepareMu.Unlock()

	a.mu.RLock()
	prepared := a.httpServer != nil
	a.mu.RUnlock()
	if prepared {
		return nil
	}

	config, err := a.serverConfigSnapshot()
	if err != nil {
		return err
	}
	if err := validateServerConfig(config); err != nil {
		return wrapCoreError(err, operationPrepareServer, nil)
	}

	tlsConfig, err := prepareTLSConfig(config.TLS)
	if err != nil {
		return wrapCoreError(err, operationPrepareServer, nil)
	}

	a.ensureHandlerPrepared()
	handler, err := a.preparedHandlerSnapshot()
	if err != nil {
		return err
	}
	a.installHTTPServer(config, tlsConfig, handler)
	return nil
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
	a.ensureHandlerPrepared()

	if ref := a.handlerFast.Load(); ref != nil {
		ref.h.ServeHTTP(w, r)
		return
	}

	_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(contract.TypeUnavailable).Message("handler not configured").Build())
}
