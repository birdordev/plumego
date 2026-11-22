package app

import (
	"github.com/birdordev/plumego/x/rest"
	"net/http"

	"with-rest/internal/handler"
)

// RegisterRoutes wires all HTTP routes for the with-rest demo.
func (a *App) RegisterRoutes() error {
	logger := a.Core.Logger()
	h := handler.HealthHandler{
		ServiceName: "with-rest",
		Logger:      logger,
	}
	reg := newRouteReg(a.Core)
	reg.get("/healthz", http.HandlerFunc(h.Live))
	reg.get("/readyz", http.HandlerFunc(h.Ready))
	reg.get("/api/hello", handler.HelloHandler{Logger: logger})
	reg.post("/api/items", a.Items)
	if reg.err != nil {
		return reg.err
	}
	return rest.RegisterResource(a.Core, a.Users.Spec.Prefix, a.Users, rest.ResourceOperations{})
}

// routeAdder is the minimal interface shared by *core.App and *core.RouteGroup.
type routeAdder interface {
	Get(path string, h http.Handler) error
	Post(path string, h http.Handler) error
	Put(path string, h http.Handler) error
	Delete(path string, h http.Handler) error
}

// routeReg wraps a routeAdder and records the first registration error.
// This lets the route table be written one route per line without per-call
// error checks; inspect reg.err once after all registrations.
type routeReg struct {
	adder routeAdder
	err   error
}

func newRouteReg(adder routeAdder) *routeReg { return &routeReg{adder: adder} }

func (r *routeReg) get(path string, h http.Handler)    { r.record(r.adder.Get(path, h)) }
func (r *routeReg) post(path string, h http.Handler)   { r.record(r.adder.Post(path, h)) }
func (r *routeReg) put(path string, h http.Handler)    { r.record(r.adder.Put(path, h)) }
func (r *routeReg) delete(path string, h http.Handler) { r.record(r.adder.Delete(path, h)) }
func (r *routeReg) record(err error) {
	if r.err == nil {
		r.err = err
	}
}
