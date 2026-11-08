package pubsubdebug

import (
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

// Configure registers a snapshot endpoint when enabled.
func Configure(hooks Hooks) {
	if hooks.EnsureMutable == nil || hooks.ConfigSnapshot == nil || hooks.RegisterRoute == nil {
		return
	}

	if err := hooks.EnsureMutable("configure_pubsub", "configure pubsub"); err != nil {
		if hooks.LogError != nil {
			hooks.LogError("ConfigurePubSub failed", err)
		}
		return
	}

	cfg := hooks.ConfigSnapshot()
	if !cfg.Enabled {
		return
	}

	pub := cfg.Pub
	if pub == nil && hooks.DefaultPubSub != nil {
		pub = hooks.DefaultPubSub()
	}
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		path = "/_debug/pubsub"
	}

	_ = hooks.RegisterRoute(http.MethodGet, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// Hooks provide the minimal integration points for Configure.
type Hooks struct {
	EnsureMutable  func(op, desc string) error
	LogError       func(msg string, err error)
	ConfigSnapshot func() PubSubConfig
	DefaultPubSub  func() pubsub.Broker
	RegisterRoute  func(method, path string, handler http.Handler) error
}
