package discovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Etcd implements Discovery using the etcd v3 HTTP gateway (gRPC-gateway).
//
// It stores service instances as JSON-encoded Instance values under a
// configurable key prefix. Each instance is stored at:
//
//	{prefix}/{serviceName}/{instanceID}
//
// Example:
//
//	sd, err := discovery.NewEtcd("http://localhost:2379", discovery.EtcdConfig{
//	    Prefix: "/services",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer sd.Close()
//
//	backends, err := sd.Resolve(ctx, "my-service")
type Etcd struct {
	endpoints []string
	config    EtcdConfig
	client    *http.Client
}

// EtcdConfig holds etcd-specific configuration.
type EtcdConfig struct {
	// Prefix is the key prefix under which services are stored.
	// Default: "/services".
	Prefix string

	// Username and Password for etcd basic authentication.
	Username string
	Password string

	// Timeout for HTTP requests (default: 10s).
	Timeout time.Duration

	// PollInterval controls how often Watch polls for changes (default: 15s).
	PollInterval time.Duration
}

// NewEtcd creates an etcd discovery client.
//
// endpoints is a list of etcd endpoint URLs, e.g. ["http://etcd1:2379", "http://etcd2:2379"].
// The client tries each endpoint in order and uses the first that succeeds.
func NewEtcd(endpoints []string, config EtcdConfig) (*Etcd, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("%w: at least one endpoint required", ErrInvalidConfig)
	}
	if config.Prefix == "" {
		config.Prefix = "/services"
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.PollInterval == 0 {
		config.PollInterval = 15 * time.Second
	}

	return &Etcd{
		endpoints: endpoints,
		config:    config,
		client:    &http.Client{Timeout: config.Timeout},
	}, nil
}

// serviceKey returns the etcd key prefix for a service.
func (e *Etcd) serviceKey(serviceName string) string {
	return e.config.Prefix + "/" + serviceName + "/"
}

// instanceKey returns the full etcd key for a service instance.
func (e *Etcd) instanceKey(serviceName, instanceID string) string {
	return e.serviceKey(serviceName) + instanceID
}

// Resolve returns all registered healthy instances for a service.
func (e *Etcd) Resolve(ctx context.Context, serviceName string) ([]string, error) {
	instances, err := e.fetchInstances(ctx, serviceName)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, ErrNoInstances
	}

	backends := make([]string, 0, len(instances))
	for _, inst := range instances {
		if inst.Healthy {
			backends = append(backends, inst.URL())
		}
	}
	if len(backends) == 0 {
		return nil, ErrNoInstances
	}
	return backends, nil
}

// fetchInstances retrieves all Instance values stored under the service key prefix.
func (e *Etcd) fetchInstances(ctx context.Context, serviceName string) ([]Instance, error) {
	prefix := e.serviceKey(serviceName)

	var lastErr error
	for _, endpoint := range e.endpoints {
		kvs, err := e.rangeGet(ctx, endpoint, prefix)
		if err != nil {
			lastErr = err
			continue
		}
		if len(kvs) == 0 {
			return nil, nil
		}
		instances := make([]Instance, 0, len(kvs))
		for _, v := range kvs {
			var inst Instance
			if err := json.Unmarshal(v, &inst); err != nil {
				continue
			}
			instances = append(instances, inst)
		}
		return instances, nil
	}
	return nil, lastErr
}

// rangeGet fetches all keys with the given prefix using the etcd v3 HTTP gateway.
//
// Endpoint format: POST {endpoint}/v3/kv/range
// It uses a prefix range: key = prefix, range_end = prefix+1 (byte increment).
func (e *Etcd) rangeGet(ctx context.Context, endpoint, prefix string) ([][]byte, error) {
	rangeEnd := prefixRangeEnd(prefix)

	keyB64 := base64.StdEncoding.EncodeToString([]byte(prefix))
	rangeEndB64 := base64.StdEncoding.EncodeToString([]byte(rangeEnd))
	body := fmt.Sprintf(`{"key":%q,"range_end":%q}`, keyB64, rangeEndB64)

	resp, err := e.doRequest(ctx, endpoint+"/v3/kv/range", body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("etcd returned status %d", resp.StatusCode)
	}

	var result etcdRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	values := make([][]byte, 0, len(result.Kvs))
	for _, kv := range result.Kvs {
		decoded, err := base64.StdEncoding.DecodeString(kv.Value)
		if err != nil {
			continue
		}
		values = append(values, decoded)
	}
	return values, nil
}

// doRequest sends an HTTP POST to the etcd HTTP gateway with optional basic auth.
func (e *Etcd) doRequest(ctx context.Context, reqURL, jsonBody string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL,
		strings.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.config.Username != "" {
		req.SetBasicAuth(e.config.Username, e.config.Password)
	}
	return e.client.Do(req)
}

// Register stores an Instance under its service key prefix.
func (e *Etcd) Register(ctx context.Context, instance Instance) error {
	if err := validateRegistrationInstance(instance); err != nil {
		return err
	}

	key := e.instanceKey(instance.Name, instance.ID)
	value, err := json.Marshal(instance)
	if err != nil {
		return err
	}

	keyB64 := base64.StdEncoding.EncodeToString([]byte(key))
	valueB64 := base64.StdEncoding.EncodeToString(value)
	body := fmt.Sprintf(`{"key":%q,"value":%q}`, keyB64, valueB64)

	var lastErr error
	for _, endpoint := range e.endpoints {
		resp, err := e.doRequest(ctx, endpoint+"/v3/kv/put", body)
		if err != nil {
			lastErr = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("etcd returned status %d", resp.StatusCode)
			continue
		}
		return nil
	}
	return lastErr
}

// Deregister removes an instance by deleting its key from etcd.
//
// serviceID must be in the form "serviceName/instanceID".
func (e *Etcd) Deregister(ctx context.Context, serviceID string) error {
	parts := strings.SplitN(serviceID, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("%w: serviceID must be 'serviceName/instanceID'", ErrInvalidConfig)
	}
	key := e.instanceKey(parts[0], parts[1])
	keyB64 := base64.StdEncoding.EncodeToString([]byte(key))
	body := fmt.Sprintf(`{"key":%q}`, keyB64)

	var lastErr error
	for _, endpoint := range e.endpoints {
		resp, err := e.doRequest(ctx, endpoint+"/v3/kv/deleterange", body)
		if err != nil {
			lastErr = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("etcd returned status %d", resp.StatusCode)
			continue
		}
		return nil
	}
	return lastErr
}

// Health updates the Healthy field of an instance stored in etcd.
//
// serviceID must be in the form "serviceName/instanceID".
func (e *Etcd) Health(ctx context.Context, serviceID string, healthy bool) error {
	parts := strings.SplitN(serviceID, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("%w: serviceID must be 'serviceName/instanceID'", ErrInvalidConfig)
	}
	serviceName, instanceID := parts[0], parts[1]
	key := e.instanceKey(serviceName, instanceID)

	// Fetch current value.
	keyB64 := base64.StdEncoding.EncodeToString([]byte(key))
	var lastErr error
	for _, endpoint := range e.endpoints {
		body := fmt.Sprintf(`{"key":%q}`, keyB64)
		resp, err := e.doRequest(ctx, endpoint+"/v3/kv/range", body)
		if err != nil {
			lastErr = err
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("etcd returned status %d", resp.StatusCode)
			continue
		}
		var result etcdRangeResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			lastErr = err
			continue
		}
		if len(result.Kvs) == 0 {
			return ErrServiceNotFound
		}
		decoded, err := base64.StdEncoding.DecodeString(result.Kvs[0].Value)
		if err != nil {
			return err
		}
		var inst Instance
		if err := json.Unmarshal(decoded, &inst); err != nil {
			return err
		}
		inst.Healthy = healthy
		return e.Register(ctx, inst)
	}
	return lastErr
}

// Watch polls etcd at PollInterval and sends backend-list updates on the
// returned channel until ctx is cancelled.
func (e *Etcd) Watch(ctx context.Context, serviceName string) (<-chan []string, error) {
	ch := make(chan []string, 1)

	backends, err := e.Resolve(ctx, serviceName)
	if err != nil && err != ErrNoInstances {
		close(ch)
		return ch, err
	}
	if len(backends) > 0 {
		ch <- backends
	}

	go func() {
		defer close(ch)
		ticker := time.NewTicker(e.config.PollInterval)
		defer ticker.Stop()
		var last []string
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollCtx, cancel := context.WithTimeout(ctx, e.config.Timeout)
				current, err := e.Resolve(pollCtx, serviceName)
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

// Close is a no-op; the etcd client holds no persistent connections.
func (e *Etcd) Close() error {
	return nil
}

// prefixRangeEnd returns the lexicographic end key for a prefix range scan.
// It increments the last byte of the prefix that can be incremented.
func prefixRangeEnd(prefix string) string {
	b := []byte(url.QueryEscape(prefix))
	// Use the raw prefix bytes for the range calculation.
	raw := []byte(prefix)
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] < 0xff {
			end := make([]byte, i+1)
			copy(end, raw)
			end[i]++
			return string(end)
		}
	}
	// All bytes are 0xff — use the empty key to mean "no upper bound".
	_ = b
	return "\x00"
}

// etcdRangeResponse mirrors the JSON shape returned by the etcd v3 range endpoint.
type etcdRangeResponse struct {
	Kvs []etcdKV `json:"kvs"`
}

// etcdKV holds the base64-encoded key and value from an etcd range response.
// The etcd HTTP gateway returns values as base64 strings in JSON.
type etcdKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
