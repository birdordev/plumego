package core

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/router"
)

// handlerRef wraps http.Handler for atomic.Pointer; the type parameter must be concrete, not an interface.
type handlerRef struct{ h http.Handler }

// App is the Plumego HTTP application kernel.
type App struct {
	// Core components (immutable after construction)
	config          *AppConfig           // Application configuration
	router          *router.Router       // HTTP router
	middlewareChain *middleware.Chain    // Middleware pipeline for all routes
	logger          log.StructuredLogger // Logger instance

	// Runtime state (protected by mutex)
	mu                 sync.RWMutex
	preparationState   PreparationState // Last successfully published phase
	registrationFrozen bool
	handlerBuilding    bool

	// Server components
	httpServer  *http.Server               // HTTP server instance
	connTracker *connectionTracker         // Open HTTP connection tracker
	handler     http.Handler               // Combined handler with middleware applied; guarded by mu
	handlerFast atomic.Pointer[handlerRef] // Published once after successful assembly
}

// New creates an App from a value-copied config and explicit dependencies.
func New(cfg AppConfig, dependencies AppDependencies) *App {
	config := cfg
	app := &App{
		config:           &config,
		router:           router.NewRouter(),
		middlewareChain:  middleware.NewChain(),
		logger:           resolveLogger(dependencies),
		preparationState: PreparationStateMutable,
	}

	app.router.SetMethodNotAllowed(config.Router.MethodNotAllowed)

	return app
}

// Logger returns the configured application logger or a discard fallback.
func (a *App) Logger() log.StructuredLogger {
	if a.logger == nil {
		return resolveLogger(AppDependencies{})
	}
	return a.logger
}

// PreparationState returns the app's current kernel preparation phase.
func (a *App) PreparationState() PreparationState {
	a.mu.RLock()
	state := a.preparationState
	a.mu.RUnlock()
	return state
}

// Routes returns the owned route table snapshot.
func (a *App) Routes() []router.RouteInfo {
	r := a.ensureRouter()
	if r == nil {
		return nil
	}
	return r.Routes()
}
