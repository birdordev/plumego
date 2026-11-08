package app

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/health"
	"standard-service/internal/domain/item"
	"standard-service/internal/handler"
)

// RegisterRoutes wires all HTTP routes for the reference application.
// One method, one path, one handler per line — no scanning, no annotations.
// Routes that share a common path prefix are registered through a RouteGroup
// so the prefix is declared once and not repeated on every line.
func (a *App) RegisterRoutes() error {
	// HealthHandler receives one ComponentChecker per dependency so /readyz reflects
	// the real readiness of each backing resource. storeChecker adapts the in-memory
	// item store; it always passes because MemoryStore is process-local — its role
	// here is to demonstrate the wiring pattern. In production replace it with real
	// dependency probes passed in the same slice:
	//
	//   db    := &dbChecker{pool: pool}     // Check() calls pool.PingContext(ctx)
	//   cache := &cacheChecker{c: redis}    // Check() calls redis.Ping(ctx).Err()
	//   handler.HealthHandler{Checkers: []health.ComponentChecker{db, cache}}
	health := handler.HealthHandler{
		ServiceName: a.Cfg.App.ServiceName,
		Logger:      a.Core.Logger(),
		Checkers:    []health.ComponentChecker{storeChecker{}},
	}
	// ItemHandler demonstrates the three-layer architecture: handler → service → repository.
	// handler/items.go declares the ItemService interface; routes.go wires
	// item.ItemService as the concrete implementation. ItemService wraps
	// item.NewMemoryStore() here; replace NewMemoryStore() with a database-backed
	// item.Repository for production. Both item.ItemService and item.MemoryStore
	// satisfy handler.ItemService via structural typing, so handler tests can inject
	// either without changing handler code.
	items := handler.ItemHandler{
		Service: item.NewItemService(item.NewMemoryStore()),
		Logger:  a.Core.Logger(),
	}

	// RequireWriteKey is a per-route middleware that gates mutating operations.
	// When APP_WRITE_KEY is empty (the default) the guard is a no-op, so the
	// service works out of the box without configuration.
	// This demonstrates the per-route middleware wrapping pattern: only the
	// handlers that mutate state are wrapped; read-only routes are unaffected.
	writeGuard := handler.RequireWriteKey(a.Cfg.App.WriteKey, a.Core.Logger())

	// APIHandler carries Logger, ServiceName, and Version via constructor injection
	// rather than reading from package-level variables. ServiceName is injected from
	// config (not hardcoded) so every response reflects the configured identity.
	api := handler.APIHandler{
		Logger:      a.Core.Logger(),
		ServiceName: a.Cfg.App.ServiceName,
		Version:     a.Cfg.App.Version,
	}

	// Top-level routes registered directly on the app.
	root := newRouteReg(a.Core)
	root.get("/", http.HandlerFunc(api.Root))
	root.get("/healthz", http.HandlerFunc(health.Live))
	root.get("/readyz", http.HandlerFunc(health.Ready))
	root.get("/api/hello", http.HandlerFunc(api.Hello))
	root.get("/api/info", http.HandlerFunc(api.Info))
	if root.err != nil {
		return root.err
	}

	// Versioned API — all routes under /api/v1 share this group prefix.
	// Query-param binding: GET   /api/v1/greet?name=Alice → 200
	//                      GET   /api/v1/greet            → 400 TypeRequired
	// Collection:          GET   /api/v1/items                                                     → 200 {items:[…],total:N,…}
	//                      POST  /api/v1/items {"name":"…","description":"…"}                      → 201 item (guarded)
	// Member:              GET   /api/v1/items/:id                                                 → 200 item or 404
	//                      PUT   /api/v1/items/:id {"name":"…","description":"…"}                 → 200 item or 404 (guarded, replaces all fields)
	//                      PATCH /api/v1/items/:id {"name":"…"}                                   → 200 item or 404 (guarded, updates only non-empty fields)
	//                      DELETE /api/v1/items/:id                                               → 204      or 404 (guarded)
	v1 := newRouteReg(a.Core.Group("/api/v1"))
	v1.get("/greet", http.HandlerFunc(api.Greet))
	v1.get("/items", http.HandlerFunc(items.List))
	v1.post("/items", writeGuard(http.HandlerFunc(items.Create)))
	v1.get("/items/:id", http.HandlerFunc(items.GetByID))
	v1.put("/items/:id", writeGuard(http.HandlerFunc(items.Update)))
	v1.patch("/items/:id", writeGuard(http.HandlerFunc(items.Patch)))
	v1.delete("/items/:id", writeGuard(http.HandlerFunc(items.Delete)))
	return v1.err
}

// storeChecker is a synthetic readiness probe for the in-memory item store.
// MemoryStore is always reachable (it is process-local memory), so this checker
// always passes — its purpose is to demonstrate the ComponentChecker wiring
// pattern. Replace it with real dependency probes as described in RegisterRoutes.
type storeChecker struct{}

func (storeChecker) Name() string                  { return "item_store" }
func (storeChecker) Check(_ context.Context) error { return nil }

// routeAdder is the minimal interface shared by *core.App and *core.RouteGroup,
// allowing newRouteReg to work with both without type-switching.
type routeAdder interface {
	Get(path string, h http.Handler) error
	Post(path string, h http.Handler) error
	Put(path string, h http.Handler) error
	Patch(path string, h http.Handler) error
	Delete(path string, h http.Handler) error
}

// routeReg wraps a routeAdder and accumulates the first registration error.
// Style guide §5 requires registration errors to be returned to the caller
// (not logged and swallowed); this wrapper fulfills that requirement while
// keeping the route table visually flat — one method + path + handler per line
// with a single reg.err check at the end instead of a per-line if-block.
// Only the first error is retained: route conflicts are programming mistakes
// that surface one at a time; fix the first one to unblock the rest.
type routeReg struct {
	adder routeAdder
	err   error
}

func newRouteReg(adder routeAdder) *routeReg { return &routeReg{adder: adder} }

func (r *routeReg) get(path string, h http.Handler)    { r.record(r.adder.Get(path, h)) }
func (r *routeReg) post(path string, h http.Handler)   { r.record(r.adder.Post(path, h)) }
func (r *routeReg) put(path string, h http.Handler)    { r.record(r.adder.Put(path, h)) }
func (r *routeReg) patch(path string, h http.Handler)  { r.record(r.adder.Patch(path, h)) }
func (r *routeReg) delete(path string, h http.Handler) { r.record(r.adder.Delete(path, h)) }

// record stores the first non-nil error; subsequent errors are dropped.
func (r *routeReg) record(err error) {
	if r.err == nil {
		r.err = err
	}
}
