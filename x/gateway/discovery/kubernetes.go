package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Kubernetes implements Discovery using the Kubernetes Endpoints API.
//
// It resolves a service name to the ready pod addresses exposed through the
// service's Endpoints object. Registration, deregistration, and health updates
// are not supported — those are managed by Kubernetes itself.
//
// Minimal in-cluster example:
//
//	sd, err := discovery.NewKubernetes(discovery.KubernetesConfig{})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer sd.Close()
//
//	backends, err := sd.Resolve(ctx, "my-service")
//
// Out-of-cluster example:
//
//	sd, err := discovery.NewKubernetes(discovery.KubernetesConfig{
//	    APIServerURL: "https://api.example.com:6443",
//	    BearerToken:  os.Getenv("KUBE_TOKEN"),
//	    Namespace:    "production",
//	})
type Kubernetes struct {
	config KubernetesConfig
	client *http.Client
	stopCh chan struct{}
}

// KubernetesConfig holds Kubernetes-specific configuration.
type KubernetesConfig struct {
	// APIServerURL is the Kubernetes API server address.
	// Defaults to the in-cluster service (https://kubernetes.default.svc).
	APIServerURL string

	// BearerToken is the token used for authentication.
	// When empty, the in-cluster service account token is read from
	// /var/run/secrets/kubernetes.io/serviceaccount/token.
	BearerToken string

	// CAFile is the path to the CA certificate file for TLS verification.
	// When empty, the in-cluster CA bundle
	// (/var/run/secrets/kubernetes.io/serviceaccount/ca.crt) is used.
	CAFile string

	// Namespace restricts discovery to a single namespace.
	// When empty, the in-cluster namespace file is read; defaults to "default".
	Namespace string

	// PortName selects a named port from the endpoint subset.
	// When empty, the first ready address/port pair is used.
	PortName string

	// Timeout for API requests (default: 10s).
	Timeout time.Duration

	// PollInterval controls how often Watch polls for endpoint changes (default: 15s).
	PollInterval time.Duration
}

const (
	inClusterHost      = "https://kubernetes.default.svc"
	inClusterTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	inClusterCAFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	inClusterNSFile    = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// NewKubernetes creates a Kubernetes discovery client.
//
// When running inside a pod, leave APIServerURL, BearerToken, CAFile, and
// Namespace empty to use the in-cluster service account credentials.
func NewKubernetes(config KubernetesConfig) (*Kubernetes, error) {
	if config.APIServerURL == "" {
		config.APIServerURL = inClusterHost
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.PollInterval == 0 {
		config.PollInterval = 15 * time.Second
	}

	if config.BearerToken == "" {
		data, err := os.ReadFile(inClusterTokenFile)
		if err == nil {
			config.BearerToken = string(data)
		}
	}

	if config.Namespace == "" {
		data, err := os.ReadFile(inClusterNSFile)
		if err == nil {
			config.Namespace = string(data)
		}
		if config.Namespace == "" {
			config.Namespace = "default"
		}
	}

	caFile := config.CAFile
	if caFile == "" {
		caFile = inClusterCAFile
	}

	tlsCfg := &tls.Config{}
	if data, err := os.ReadFile(caFile); err == nil {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(data)
		tlsCfg.RootCAs = pool
	}

	transport := &http.Transport{TLSClientConfig: tlsCfg}
	client := &http.Client{
		Timeout:   config.Timeout,
		Transport: transport,
	}

	return &Kubernetes{
		config: config,
		client: client,
		stopCh: make(chan struct{}),
	}, nil
}

// Resolve returns the ready pod addresses for a Kubernetes service.
//
// It queries /api/v1/namespaces/{ns}/endpoints/{service} and returns
// "http://address:port" for every ready address in the endpoint subsets.
func (k *Kubernetes) Resolve(ctx context.Context, serviceName string) ([]string, error) {
	epURL := fmt.Sprintf("%s/api/v1/namespaces/%s/endpoints/%s",
		k.config.APIServerURL,
		url.PathEscape(k.config.Namespace),
		url.PathEscape(serviceName),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, epURL, nil)
	if err != nil {
		return nil, err
	}
	if k.config.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+k.config.BearerToken)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := k.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrServiceNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kubernetes api returned status %d", resp.StatusCode)
	}

	var ep k8sEndpoints
	if err := json.NewDecoder(resp.Body).Decode(&ep); err != nil {
		return nil, err
	}

	backends := k.extractBackends(ep)
	if len(backends) == 0 {
		return nil, ErrNoInstances
	}
	return backends, nil
}

// extractBackends builds backend URLs from the endpoint subsets.
func (k *Kubernetes) extractBackends(ep k8sEndpoints) []string {
	var backends []string
	for _, subset := range ep.Subsets {
		port := k.selectPort(subset.Ports)
		if port == 0 {
			continue
		}
		for _, addr := range subset.Addresses {
			backends = append(backends, fmt.Sprintf("http://%s:%d", addr.IP, port))
		}
	}
	return backends
}

// selectPort returns the port number to use from an endpoint subset.
func (k *Kubernetes) selectPort(ports []k8sEndpointPort) int {
	if len(ports) == 0 {
		return 0
	}
	if k.config.PortName == "" {
		return ports[0].Port
	}
	for _, p := range ports {
		if p.Name == k.config.PortName {
			return p.Port
		}
	}
	return 0
}

// Watch polls the Kubernetes Endpoints API at PollInterval and delivers
// backend-list updates on the returned channel until ctx is cancelled.
func (k *Kubernetes) Watch(ctx context.Context, serviceName string) (<-chan []string, error) {
	ch := make(chan []string, 1)

	// Send initial state immediately.
	backends, err := k.Resolve(ctx, serviceName)
	if err != nil && err != ErrNoInstances {
		close(ch)
		return ch, err
	}
	if len(backends) > 0 {
		ch <- backends
	}

	go func() {
		defer close(ch)
		ticker := time.NewTicker(k.config.PollInterval)
		defer ticker.Stop()
		var last []string
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollCtx, cancel := context.WithTimeout(ctx, k.config.Timeout)
				current, err := k.Resolve(pollCtx, serviceName)
				cancel()
				if err != nil {
					continue
				}
				if !stringSlicesEqual(current, last) {
					last = current
					select {
					case ch <- current:
					default:
					}
				}
			}
		}
	}()

	return ch, nil
}

// Register is not supported; Kubernetes manages endpoints automatically.
func (k *Kubernetes) Register(_ context.Context, _ Instance) error {
	return ErrNotSupported
}

// Deregister is not supported; Kubernetes manages endpoints automatically.
func (k *Kubernetes) Deregister(_ context.Context, _ string) error {
	return ErrNotSupported
}

// Health is not supported; Kubernetes manages readiness probes.
func (k *Kubernetes) Health(_ context.Context, _ string, _ bool) error {
	return ErrNotSupported
}

// Close stops any background goroutines.
func (k *Kubernetes) Close() error {
	select {
	case <-k.stopCh:
	default:
		close(k.stopCh)
	}
	return nil
}

// k8sEndpoints mirrors the relevant fields of a Kubernetes Endpoints object.
type k8sEndpoints struct {
	Subsets []k8sEndpointSubset `json:"subsets"`
}

type k8sEndpointSubset struct {
	Addresses []k8sEndpointAddress `json:"addresses"`
	Ports     []k8sEndpointPort    `json:"ports"`
}

type k8sEndpointAddress struct {
	IP string `json:"ip"`
}

type k8sEndpointPort struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

// stringSlicesEqual reports whether two string slices have the same contents in the same order.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
