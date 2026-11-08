package core

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
)

// registerRoute is the single implementation for all route registration.
func (a *App) registerRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error {
	params := map[string]any{"method": method, "path": path}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := validateMutableState(a.preparationState, operationAddRoute, "register route", params); err != nil {
		return err
	}
	if handler == nil {
		return wrapCoreError(contract.ErrHandlerNil, operationAddRoute, params)
	}

	r := a.router
	if r == nil {
		return wrapCoreError(fmt.Errorf("router not configured"), operationAddRoute, nil)
	}

	return r.AddRoute(method, path, handler, opts...)
}

// AddRoute registers a route and returns explicit errors for invalid registration.
// This is useful in strict boot wiring where route registration failures must be surfaced.
func (a *App) AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error {
	return a.registerRoute(method, path, handler, opts...)
}

// URL resolves a named route against the owned app router.
// It returns an error when the route is unknown, a required parameter is
// missing or empty, or the app has no router configured.
func (a *App) URL(name string, params ...string) (string, error) {
	r := a.ensureRouter()
	if r == nil {
		return "", errors.New("router not configured")
	}
	return r.URL(name, params...)
}

// Get registers a GET route with the given handler.
func (a *App) Get(path string, handler http.Handler) error {
	return a.registerRoute(http.MethodGet, path, handler)
}

// Post registers a POST route with the given handler.
func (a *App) Post(path string, handler http.Handler) error {
	return a.registerRoute(http.MethodPost, path, handler)
}

// Put registers a PUT route with the given handler.
func (a *App) Put(path string, handler http.Handler) error {
	return a.registerRoute(http.MethodPut, path, handler)
}

// Delete registers a DELETE route with the given handler.
func (a *App) Delete(path string, handler http.Handler) error {
	return a.registerRoute(http.MethodDelete, path, handler)
}

// Patch registers a PATCH route with the given handler.
func (a *App) Patch(path string, handler http.Handler) error {
	return a.registerRoute(http.MethodPatch, path, handler)
}

// Any registers a route for any HTTP method with the given handler.
func (a *App) Any(path string, handler http.Handler, opts ...router.RouteOption) error {
	return a.registerRoute(router.MethodAny, path, handler, opts...)
}

// Group returns a RouteGroup with the given path prefix.
// All routes registered on the group share the prefix without repeating it.
// Group itself does not register any routes; call Get/Post/Delete/etc. on the
// returned group to register individual routes.
//
// Group panics when prefix does not begin with "/". This mirrors the behaviour of
// [regexp.MustCompile]: Group is intended for startup wiring where the prefix is a
// compile-time constant; a bad value is always a bug, not a runtime condition.
// All dynamic errors (duplicate routes, frozen app) are still returned as errors
// from the route registration methods on the returned group.
//
// Example:
//
//	v1 := app.Group("/api/v1")
//	if err := v1.Get("/items", listHandler); err != nil { return err }
//	if err := v1.Post("/items", createHandler); err != nil { return err }
//	if err := v1.Get("/items/:id", getHandler); err != nil { return err }
//	if err := v1.Delete("/items/:id", deleteHandler); err != nil { return err }
func (a *App) Group(prefix string) *RouteGroup {
	return &RouteGroup{app: a, r: a.ensureRouter().Group(prefix)}
}

// RouteGroup represents a set of routes sharing a common path prefix.
// Create one with App.Group; use its methods to register routes without
// repeating the prefix on every call.
type RouteGroup struct {
	app *App
	r   *router.Router
}

// Group returns a nested RouteGroup with an additional path prefix appended.
func (g *RouteGroup) Group(prefix string) *RouteGroup {
	return &RouteGroup{app: g.app, r: g.r.Group(prefix)}
}

// Get registers a GET route on this group.
func (g *RouteGroup) Get(path string, handler http.Handler) error {
	return g.addRoute(http.MethodGet, path, handler)
}

// Post registers a POST route on this group.
func (g *RouteGroup) Post(path string, handler http.Handler) error {
	return g.addRoute(http.MethodPost, path, handler)
}

// Put registers a PUT route on this group.
func (g *RouteGroup) Put(path string, handler http.Handler) error {
	return g.addRoute(http.MethodPut, path, handler)
}

// Delete registers a DELETE route on this group.
func (g *RouteGroup) Delete(path string, handler http.Handler) error {
	return g.addRoute(http.MethodDelete, path, handler)
}

// Patch registers a PATCH route on this group.
func (g *RouteGroup) Patch(path string, handler http.Handler) error {
	return g.addRoute(http.MethodPatch, path, handler)
}

// AddRoute registers a route with an explicit method and optional route metadata.
func (g *RouteGroup) AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error {
	return g.addRoute(method, path, handler, opts...)
}

// addRoute is the single implementation for RouteGroup route registration.
// It mirrors the preparation-state and nil-handler validation from App.registerRoute
// while directing registrations into the group's prefixed router rather than the
// App's root router.
func (g *RouteGroup) addRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error {
	params := map[string]any{"method": method, "path": path}

	g.app.mu.Lock()
	defer g.app.mu.Unlock()

	if err := validateMutableState(g.app.preparationState, operationAddRoute, "register route", params); err != nil {
		return err
	}
	if handler == nil {
		return wrapCoreError(contract.ErrHandlerNil, operationAddRoute, params)
	}
	if g.r == nil {
		return wrapCoreError(fmt.Errorf("router not configured"), operationAddRoute, nil)
	}
	return g.r.AddRoute(method, path, handler, opts...)
}
