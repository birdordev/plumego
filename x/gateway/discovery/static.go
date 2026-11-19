package discovery

import (
	"context"
	"errors"
	"slices"
	"sync"
)

// Static implements Discovery with static configuration
//
// This is useful for:
//   - Testing
//   - Simple deployments with fixed backends
//   - Development environments
//
// Example:
//
//	sd := NewStatic(map[string][]string{
//		"user-service": {
//			"http://user-1:8080",
//			"http://user-2:8080",
//		},
//		"order-service": {
//			"http://order-1:8080",
//		},
//	})
type Static struct {
	services map[string][]string
	mu       sync.RWMutex
	done     chan struct{}
	closed   bool
	watchers sync.WaitGroup
}

// NewStatic creates a static discovery with its own copy of the service map
// and backend lists.
func NewStatic(services map[string][]string) *Static {
	owned := make(map[string][]string, len(services))
	for name, backends := range services {
		owned[name] = slices.Clone(backends)
	}
	return &Static{services: owned, done: make(chan struct{})}
}

// Resolve returns the list of backend URLs for a service
func (s *Static) Resolve(ctx context.Context, serviceName string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolveLocked(serviceName)
}

func (s *Static) resolveLocked(serviceName string) ([]string, error) {
	backends, exists := s.services[serviceName]
	if !exists {
		return nil, ErrServiceNotFound
	}

	if len(backends) == 0 {
		return nil, ErrNoInstances
	}

	// Return a copy to prevent external modification
	result := make([]string, len(backends))
	copy(result, backends)

	return result, nil
}

var errStaticClosed = errors.New("discovery: static watcher is closed")

// Watch sends the current backends once. The channel closes when ctx is
// canceled or the discovery instance is closed.
func (s *Static) Watch(ctx context.Context, serviceName string) (<-chan []string, error) {
	ch := make(chan []string, 1)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		close(ch)
		return ch, errStaticClosed
	}
	backends, err := s.resolveLocked(serviceName)
	if err != nil {
		close(ch)
		return ch, err
	}

	ch <- backends
	s.watchers.Add(1)
	go func() {
		defer s.watchers.Done()
		defer close(ch)
		select {
		case <-ctx.Done():
		case <-s.done:
		}
	}()

	return ch, nil
}

// Register is not supported for static discovery
func (s *Static) Register(ctx context.Context, instance Instance) error {
	return unsupportedOperation("static register")
}

// Deregister is not supported for static discovery
func (s *Static) Deregister(ctx context.Context, serviceID string) error {
	return unsupportedOperation("static deregister")
}

// Health is not supported for static discovery
func (s *Static) Health(ctx context.Context, serviceID string, healthy bool) error {
	return unsupportedOperation("static health")
}

// Close rejects new watches and waits for existing watch channels to close.
// Resolve and backend edits remain available.
func (s *Static) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.done != nil {
			close(s.done)
		}
	}
	s.mu.Unlock()
	s.watchers.Wait()
	return nil
}

// UpdateService replaces the backends for a service with a copy of backends.
// This is useful for testing or manual updates
func (s *Static) UpdateService(serviceName string, backends []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.services[serviceName] = slices.Clone(backends)
}

// RemoveService removes a service from the registry
func (s *Static) RemoveService(serviceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.services, serviceName)
}

// AddBackend adds a backend to a service
func (s *Static) AddBackend(serviceName, backend string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.services[serviceName]; !exists {
		s.services[serviceName] = []string{}
	}

	s.services[serviceName] = append(s.services[serviceName], backend)
}

// RemoveBackend removes a backend from a service
func (s *Static) RemoveBackend(serviceName, backend string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	backends, exists := s.services[serviceName]
	if !exists {
		return
	}

	for i, b := range backends {
		if b == backend {
			s.services[serviceName] = append(backends[:i], backends[i+1:]...)
			return
		}
	}
}

// Services returns a list of all service names
func (s *Static) Services() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]string, 0, len(s.services))
	for name := range s.services {
		result = append(result, name)
	}

	return result
}
