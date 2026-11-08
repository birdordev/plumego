package pubsubdebug

import (
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

type Handler struct {
	cfg        PubSubConfig
	defaultPub pubsub.Broker
}

func New(cfg PubSubConfig, fallbackPub pubsub.Broker) *Handler {
	return &Handler{cfg: cfg, defaultPub: fallbackPub}
}

type routeRegistrar interface {
	AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error
}

func (h *Handler) RegisterRoutes(r routeRegistrar) error {
	if !h.cfg.Enabled {
		return nil
	}

	pub := h.cfg.Pub
	if pub == nil {
		pub = h.defaultPub
	}
	path := strings.TrimSpace(h.cfg.Path)
	if path == "" {
		path = "/_debug/pubsub"
	}

	return r.AddRoute(http.MethodGet, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pub == nil {
			_ = contract.WriteError(w, r, pubsubNotConfiguredError())
			return
		}

		type snapshoter interface{ Snapshot() pubsub.MetricsSnapshot }

		if ps, ok := pub.(snapshoter); ok {
			_ = contract.WriteResponse(w, r, http.StatusOK, ps.Snapshot(), nil)
			return
		}

		_ = contract.WriteError(w, r, pubsubSnapshotUnsupportedError())
	}))
}

func pubsubNotConfiguredError() contract.APIError {
	return contract.NewErrorBuilder().
		Type(contract.TypeUnavailable).
		Message("pubsub is not configured").
		Build()
}

func pubsubSnapshotUnsupportedError() contract.APIError {
	return contract.NewErrorBuilder().
		Type(contract.TypeNotImplemented).
		Code(contract.CodeNotImplemented).
		Message("pubsub snapshot not supported by this implementation").
		Build()
}

func (h *Handler) Health() (string, health.HealthStatus) {
	status := health.HealthStatus{
		Status:  health.StatusHealthy,
		Details: map[string]any{"enabled": h.cfg.Enabled},
	}

	if !h.cfg.Enabled {
		status.Status = health.StatusDegraded
		status.Message = "pubsub debug disabled"
	}

	return "pubsub_debug", status
}
