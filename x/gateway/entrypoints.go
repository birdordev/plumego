package gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/gateway/protocol"
)

const methodAny = "ANY"

var (
	errRegisterNilRouter  = errors.New("gateway register: router cannot be nil")
	errRegisterEmptyPath  = errors.New("gateway register: path cannot be empty")
	errRegisterNilHandler = errors.New("gateway register: handler cannot be nil")
)

// GatewayConfig is the canonical app-facing proxy configuration type.
type GatewayConfig = Config

// GatewayProxy is the canonical app-facing reverse proxy handler.
type GatewayProxy = Proxy

// GatewayBackend is an addressable backend endpoint.
type GatewayBackend = Backend

// GatewayBackendPool manages gateway backends.
type GatewayBackendPool = BackendPool

// GatewayServiceDiscovery resolves dynamic backends.
type GatewayServiceDiscovery = ServiceDiscovery

// GatewayTransportConfig configures outbound HTTP transport pooling.
type GatewayTransportConfig = TransportConfig

// GatewayTransportPool manages outbound HTTP transports.
type GatewayTransportPool = TransportPool

// GatewayPathRewriteFunc rewrites request paths before proxying.
type GatewayPathRewriteFunc = PathRewriteFunc

// GatewayProtocolAdapter adapts HTTP requests to another protocol.
type GatewayProtocolAdapter = protocol.ProtocolAdapter

// GatewayProtocolRegistry manages protocol adapters.
type GatewayProtocolRegistry = protocol.Registry

// GatewayHTTPRequest is the transport-neutral request shape for protocol adapters.
type GatewayHTTPRequest = protocol.HTTPRequest

// GatewayProtocolRequest is a protocol-specific request.
type GatewayProtocolRequest = protocol.Request

// GatewayProtocolResponse is a protocol-specific response.
type GatewayProtocolResponse = protocol.Response

// GatewayProtocolResponseWriter writes adapter responses.
type GatewayProtocolResponseWriter = protocol.ResponseWriter

// NewGateway constructs the canonical reverse proxy handler for app-facing code
// and returns configuration errors.
func NewGateway(cfg GatewayConfig) (*GatewayProxy, error) {
	return New(cfg)
}

// NewGatewayBackendPool constructs a backend pool from backend URLs.
func NewGatewayBackendPool(urls []string) (*GatewayBackendPool, error) {
	return NewBackendPool(urls)
}

// NewGatewayProtocolRegistry creates a protocol adapter registry.
func NewGatewayProtocolRegistry() *GatewayProtocolRegistry {
	return protocol.NewRegistry()
}

// RegisterRoute binds a gateway handler to a path using explicit ANY semantics.
func RegisterRoute(r *router.Router, path string, handler http.Handler) error {
	if r == nil {
		return errRegisterNilRouter
	}
	if strings.TrimSpace(path) == "" {
		return errRegisterEmptyPath
	}
	if handler == nil {
		return errRegisterNilHandler
	}
	return r.AddRoute(methodAny, path, handler)
}

// RegisterProxy constructs a gateway proxy and binds it to a path.
func RegisterProxy(r *router.Router, path string, cfg GatewayConfig) (*GatewayProxy, error) {
	if r == nil {
		return nil, errRegisterNilRouter
	}
	if strings.TrimSpace(path) == "" {
		return nil, errRegisterEmptyPath
	}
	proxy, err := NewGateway(cfg)
	if err != nil {
		return nil, err
	}
	if err := RegisterRoute(r, path, proxy); err != nil {
		return nil, err
	}
	return proxy, nil
}
