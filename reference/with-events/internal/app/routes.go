package app

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
)

type placeholderResponse struct {
	Status string `json:"status"`
	Group  string `json:"group"`
}

// RegisterRoutes wires placeholder route groups for later event-flow cards.
func (a *App) RegisterRoutes() error {
	logger := a.Core.Logger()
	reg := newRouteReg(a.Core)
	reg.post("/orders", http.HandlerFunc(a.Orders.Create))
	reg.get("/orders/:id", http.HandlerFunc(a.Orders.Get))
	reg.post("/scheduler/retry", placeholder("scheduler", logger))
	reg.post("/webhook/send", placeholder("webhook", logger))
	return reg.err
}

func placeholder(group string, logger plumelog.StructuredLogger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := contract.WriteResponse(w, r, http.StatusOK, placeholderResponse{
			Status: "placeholder",
			Group:  group,
		}, nil); err != nil && logger != nil {
			logger.Warn("write response failed", plumelog.Fields{"error": err.Error()})
		}
	}
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

func (r *routeReg) get(path string, h http.Handler)  { r.record(r.adder.Get(path, h)) }
func (r *routeReg) post(path string, h http.Handler) { r.record(r.adder.Post(path, h)) }
func (r *routeReg) record(err error) {
	if r.err == nil {
		r.err = err
	}
}
