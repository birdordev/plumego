// Package discovery provides service discovery interfaces and implementations
//
// This package enables dynamic backend discovery for the proxy middleware,
// supporting multiple backend types including:
//   - Static configuration
//   - Consul (HashiCorp)
//   - Kubernetes (Endpoints API)
//   - etcd (v3 HTTP gateway)
//
// Example usage:
//
//	import (
//		"github.com/birdordev/plumego/x/gateway/discovery"
//		"github.com/birdordev/plumego/middleware/proxy"
//	)
//
//	// Static discovery
//	sd := discovery.NewStatic(map[string][]string{
//		"user-service": {"http://user-1:8080", "http://user-2:8080"},
//		"order-service": {"http://order-1:8080"},
//	})
//
//	// Consul discovery
//	consulSD := discovery.NewConsul("localhost:8500")
//
//	// Use with proxy
//	proxy.New(proxy.Config{
//		ServiceName: "user-service",
//		Discovery: sd,
//	})
package discovery

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrServiceNotFound is returned when a service is not found
	ErrServiceNotFound = errors.New("discovery: service not found")

	// ErrNoInstances is returned when a service has no instances
	ErrNoInstances = errors.New("discovery: no instances available")

	// ErrInvalidConfig is returned when configuration is invalid
	ErrInvalidConfig = errors.New("discovery: invalid configuration")
)

// Discovery defines the interface for service discovery
type Discovery interface {
	// Resolve returns the list of backend URLs for a service
	// Returns ErrServiceNotFound if service doesn't exist
	// Returns ErrNoInstances if service has no healthy instances
	Resolve(ctx context.Context, serviceName string) ([]string, error)

	// Watch returns a channel that receives backend updates
	// The channel is closed when the context is cancelled
	// Returns ErrNotSupported if watching is not implemented
	Watch(ctx context.Context, serviceName string) (<-chan []string, error)

	// Register registers a service instance
	// Returns ErrNotSupported if registration is not implemented
	Register(ctx context.Context, instance Instance) error

	// Deregister removes a service instance
	// Returns ErrNotSupported if deregistration is not implemented
	Deregister(ctx context.Context, serviceID string) error

	// Health updates the health status of an instance
	// Returns ErrNotSupported if health updates are not implemented
	Health(ctx context.Context, serviceID string, healthy bool) error

	// Close closes the discovery client and cleans up resources
	Close() error
}

// Instance represents a service instance
type Instance struct {
	// ID is the unique instance identifier
	ID string

	// Name is the service name
	Name string

	// Address is the instance address (IP or hostname)
	Address string

	// Port is the service port
	Port int

	// Scheme is the URL scheme (http, https, ws, wss)
	// Default: http
	Scheme string

	// Metadata is arbitrary metadata associated with the instance
	Metadata map[string]string

	// Tags are instance tags for filtering
	Tags []string

	// Weight is the load balancing weight
	// Default: 1
	Weight int

	// Healthy indicates if the instance is healthy
	Healthy bool
}

func validateRegistrationInstance(instance Instance) error {
	if instance.ID == "" {
		return fmt.Errorf("%w: instance ID required", ErrInvalidConfig)
	}
	if instance.Name == "" {
		return fmt.Errorf("%w: instance Name required", ErrInvalidConfig)
	}
	return nil
}

// URL returns the full URL for the instance
func (i *Instance) URL() string {
	scheme := i.Scheme
	if scheme == "" {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, i.Address, i.Port)
}

// HealthChecker defines an optional interface for health checking
type HealthChecker interface {
	// CheckHealth performs a health check on the instance
	CheckHealth(ctx context.Context, instance Instance) error
}

// Watcher defines an optional interface for watching service changes
type Watcher interface {
	// Start starts watching for service changes
	Start(ctx context.Context) error

	// Stop stops watching
	Stop() error

	// Updates returns a channel that receives service updates
	Updates() <-chan ServiceUpdate
}

// ServiceUpdate represents a service update event
type ServiceUpdate struct {
	// ServiceName is the name of the service
	ServiceName string

	// Instances is the new list of instances
	Instances []Instance

	// Error is set if there was an error fetching instances
	Error error
}
