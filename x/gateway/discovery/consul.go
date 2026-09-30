package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Consul implements Discovery using HashiCorp Consul
//
// Example:
//
//	sd, err := discovery.NewConsul("localhost:8500", discovery.ConsulConfig{
//		Datacenter: "dc1",
//		Token:      "secret-token",
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer sd.Close()
//
//	backends, err := sd.Resolve(ctx, "user-service")
type Consul struct {
	address  string
	config   ConsulConfig
	client   *http.Client
	watchers map[string]*consulWatcher
	mu       sync.RWMutex
}

// ConsulConfig holds Consul-specific configuration
type ConsulConfig struct {
	// Datacenter to query (default: "dc1")
	Datacenter string

	// Token for ACL authentication
	Token string

	// Namespace for Consul Enterprise namespace filtering (sets the ?ns= query parameter)
	Namespace string

	// WaitTime for long polling (default: 30s)
	WaitTime time.Duration

	// Timeout for HTTP requests (default: 60s)
	Timeout time.Duration

	// IncludeUnhealthy includes unhealthy instances in results.
	// Default: false (only healthy instances are returned).
	IncludeUnhealthy bool

	// Tag filters services by tag
	Tag string

	// Scheme is the URL scheme for the Consul agent (default: "http")
	Scheme string
}

// NewConsul creates a new Consul discovery client
func NewConsul(address string, config ConsulConfig) (*Consul, error) {
	if config.Datacenter == "" {
		config.Datacenter = "dc1"
	}
	if config.WaitTime == 0 {
		config.WaitTime = 30 * time.Second
	}
	if config.Timeout == 0 {
		config.Timeout = 60 * time.Second
	}
	if config.Scheme == "" {
		config.Scheme = "http"
	}

	return &Consul{
		address:  address,
		config:   config,
		client:   &http.Client{Timeout: config.Timeout},
		watchers: make(map[string]*consulWatcher),
	}, nil
}

// baseURL returns the base URL for Consul API requests
func (c *Consul) baseURL() string {
	return c.config.Scheme + "://" + c.address
}

// doRequest executes an HTTP request with ACL token and Content-Type headers applied
func (c *Consul) doRequest(ctx context.Context, method, reqURL string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.config.Token != "" {
		req.Header.Set("X-Consul-Token", c.config.Token)
	}
	return c.client.Do(req)
}

// Resolve returns the list of backend URLs for a service
func (c *Consul) Resolve(ctx context.Context, serviceName string) ([]string, error) {
	params := url.Values{}
	params.Set("dc", c.config.Datacenter)
	if !c.config.IncludeUnhealthy {
		params.Set("passing", "true")
	}
	if c.config.Tag != "" {
		params.Set("tag", c.config.Tag)
	}
	if c.config.Namespace != "" {
		params.Set("ns", c.config.Namespace)
	}

	queryURL := c.baseURL() + "/v1/health/service/" + url.PathEscape(serviceName) + "?" + params.Encode()

	resp, err := c.doRequest(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrServiceNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("consul returned status %d", resp.StatusCode)
	}

	var entries []consulServiceEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, ErrNoInstances
	}

	backends := make([]string, 0, len(entries))
	for _, entry := range entries {
		backends = append(backends, fmt.Sprintf("http://%s:%d", entry.Service.Address, entry.Service.Port))
	}
	return backends, nil
}

// Watch returns a channel for backend updates
func (c *Consul) Watch(ctx context.Context, serviceName string) (<-chan []string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if watcher, exists := c.watchers[serviceName]; exists {
		return watcher.subscribe(ctx), nil
	}

	watcher := newConsulWatcher(c, serviceName)
	c.watchers[serviceName] = watcher
	go watcher.watch()

	return watcher.subscribe(ctx), nil
}

// Register registers a service instance
func (c *Consul) Register(ctx context.Context, instance Instance) error {
	if err := validateRegistrationInstance(instance); err != nil {
		return err
	}

	reg := consulRegistration{
		ID:      instance.ID,
		Name:    instance.Name,
		Address: instance.Address,
		Port:    instance.Port,
		Tags:    instance.Tags,
		Meta:    instance.Metadata,
	}

	body, err := json.Marshal(reg)
	if err != nil {
		return err
	}

	resp, err := c.doRequest(ctx, http.MethodPut,
		c.baseURL()+"/v1/agent/service/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("consul returned status %d", resp.StatusCode)
	}
	return nil
}

// Deregister removes a service instance
func (c *Consul) Deregister(ctx context.Context, serviceID string) error {
	resp, err := c.doRequest(ctx, http.MethodPut,
		c.baseURL()+"/v1/agent/service/deregister/"+url.PathEscape(serviceID), nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("consul returned status %d", resp.StatusCode)
	}
	return nil
}

// Health updates the health status of an instance
// Consul TTL-based health checks require registering with a TTL check first;
// use the Consul agent API directly for TTL heartbeats.
func (c *Consul) Health(ctx context.Context, serviceID string, healthy bool) error {
	return ErrNotSupported
}

// Close closes the Consul client and stops all watchers
func (c *Consul) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, watcher := range c.watchers {
		watcher.stop()
	}
	c.watchers = make(map[string]*consulWatcher)
	return nil
}

// consulServiceEntry represents a Consul health service entry
type consulServiceEntry struct {
	Service consulService `json:"Service"`
}

// consulService holds the address and port from a Consul service entry
type consulService struct {
	Address string `json:"Address"`
	Port    int    `json:"Port"`
}

// consulRegistration represents a Consul service registration payload
type consulRegistration struct {
	ID      string            `json:"ID"`
	Name    string            `json:"Name"`
	Address string            `json:"Address"`
	Port    int               `json:"Port"`
	Tags    []string          `json:"Tags,omitempty"`
	Meta    map[string]string `json:"Meta,omitempty"`
}

// consulWatcher watches a service for changes and fans out updates to subscribers
type consulWatcher struct {
	consul      *Consul
	serviceName string
	subscribers []chan []string
	stopChan    chan struct{}
	stopOnce    sync.Once
	mu          sync.RWMutex
}

// newConsulWatcher creates a new watcher for the given service
func newConsulWatcher(consul *Consul, serviceName string) *consulWatcher {
	return &consulWatcher{
		consul:      consul,
		serviceName: serviceName,
		subscribers: make([]chan []string, 0),
		stopChan:    make(chan struct{}),
	}
}

// subscribe returns a channel that receives backend updates.
// The channel is closed when ctx is cancelled.
func (w *consulWatcher) subscribe(ctx context.Context) <-chan []string {
	ch := make(chan []string, 1)

	w.mu.Lock()
	w.subscribers = append(w.subscribers, ch)
	w.mu.Unlock()

	go func() {
		<-ctx.Done()
		w.mu.Lock()
		defer w.mu.Unlock()
		for i, sub := range w.subscribers {
			if sub == ch {
				w.subscribers = append(w.subscribers[:i], w.subscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}()

	return ch
}

// watch polls Consul at WaitTime intervals and notifies all subscribers on change
func (w *consulWatcher) watch() {
	ticker := time.NewTicker(w.consul.config.WaitTime)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), w.consul.config.Timeout)
			backends, err := w.consul.Resolve(ctx, w.serviceName)
			cancel()

			if err == nil && len(backends) > 0 {
				w.notify(backends)
			}

		case <-w.stopChan:
			return
		}
	}
}

// notify sends the current backend list to all subscribers (non-blocking)
func (w *consulWatcher) notify(backends []string) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	for _, ch := range w.subscribers {
		select {
		case ch <- backends:
		default:
			// Skip if subscriber channel is full
		}
	}
}

// stop stops the watcher. Safe to call multiple times.
func (w *consulWatcher) stop() {
	w.stopOnce.Do(func() {
		close(w.stopChan)
	})
}
