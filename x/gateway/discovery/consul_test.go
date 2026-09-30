package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestConsulServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func TestNewConsul(t *testing.T) {
	c, err := NewConsul("localhost:8500", ConsulConfig{})
	if err != nil {
		t.Fatalf("NewConsul() error = %v", err)
	}
	if c == nil {
		t.Fatal("NewConsul() returned nil")
	}
	if c.address != "localhost:8500" {
		t.Errorf("address = %q, want %q", c.address, "localhost:8500")
	}
}

func TestNewConsul_Defaults(t *testing.T) {
	c, err := NewConsul("localhost:8500", ConsulConfig{})
	if err != nil {
		t.Fatalf("NewConsul() error = %v", err)
	}

	if c.config.Datacenter != "dc1" {
		t.Errorf("Datacenter = %q, want %q", c.config.Datacenter, "dc1")
	}
	if c.config.WaitTime != 30*time.Second {
		t.Errorf("WaitTime = %v, want %v", c.config.WaitTime, 30*time.Second)
	}
	if c.config.Timeout != 60*time.Second {
		t.Errorf("Timeout = %v, want %v", c.config.Timeout, 60*time.Second)
	}
	if c.config.Scheme != "http" {
		t.Errorf("Scheme = %q, want %q", c.config.Scheme, "http")
	}
	// Default: only healthy instances are returned (IncludeUnhealthy = false)
	if c.config.IncludeUnhealthy {
		t.Error("IncludeUnhealthy = true, want false (healthy only by default)")
	}
}

func TestNewConsul_CustomConfig(t *testing.T) {
	c, err := NewConsul("consul.example.com:8500", ConsulConfig{
		Datacenter:       "us-east-1",
		Token:            "secret",
		Namespace:        "production",
		WaitTime:         10 * time.Second,
		Timeout:          30 * time.Second,
		Tag:              "primary",
		Scheme:           "https",
		IncludeUnhealthy: true,
	})
	if err != nil {
		t.Fatalf("NewConsul() error = %v", err)
	}

	if c.config.Datacenter != "us-east-1" {
		t.Errorf("Datacenter = %q, want %q", c.config.Datacenter, "us-east-1")
	}
	if c.config.Token != "secret" {
		t.Errorf("Token = %q, want %q", c.config.Token, "secret")
	}
	if c.config.Namespace != "production" {
		t.Errorf("Namespace = %q, want %q", c.config.Namespace, "production")
	}
	if c.config.WaitTime != 10*time.Second {
		t.Errorf("WaitTime = %v, want %v", c.config.WaitTime, 10*time.Second)
	}
	if c.config.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want %v", c.config.Timeout, 30*time.Second)
	}
	if c.config.Tag != "primary" {
		t.Errorf("Tag = %q, want %q", c.config.Tag, "primary")
	}
	if !c.config.IncludeUnhealthy {
		t.Error("IncludeUnhealthy = false, want true")
	}
}

func TestNewConsul_WatchersInitialized(t *testing.T) {
	c, err := NewConsul("localhost:8500", ConsulConfig{})
	if err != nil {
		t.Fatalf("NewConsul() error = %v", err)
	}
	if c.watchers == nil {
		t.Error("watchers is nil, want initialized map")
	}
	if len(c.watchers) != 0 {
		t.Errorf("watchers len = %d, want 0", len(c.watchers))
	}
}

func TestConsul_Resolve_Success(t *testing.T) {
	entries := []consulServiceEntry{
		{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		{Service: consulService{Address: "10.0.0.2", Port: 8080}},
	}

	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v1/health/service/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	backends, err := c.Resolve(t.Context(), "my-service")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("Resolve() returned %d backends, want 2", len(backends))
	}
	if backends[0] != "http://10.0.0.1:8080" {
		t.Errorf("backends[0] = %q, want %q", backends[0], "http://10.0.0.1:8080")
	}
	if backends[1] != "http://10.0.0.2:8080" {
		t.Errorf("backends[1] = %q, want %q", backends[1], "http://10.0.0.2:8080")
	}
}

func TestConsul_Resolve_ServiceName(t *testing.T) {
	var receivedPath string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})
	_, _ = c.Resolve(t.Context(), "user-service")

	expected := "/v1/health/service/user-service"
	if receivedPath != expected {
		t.Errorf("path = %q, want %q", receivedPath, expected)
	}
}

func TestConsul_Resolve_QueryParams(t *testing.T) {
	var receivedQuery string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{
		Tag: "primary",
	})

	_, _ = c.Resolve(t.Context(), "svc")

	assertQueryContains(t, receivedQuery, "dc=dc1", "passing=true", "tag=primary")
}

func TestConsul_Resolve_IncludeUnhealthy(t *testing.T) {
	var receivedQuery string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{IncludeUnhealthy: true})

	_, _ = c.Resolve(t.Context(), "svc")

	assertQueryOmits(t, receivedQuery, "passing=true")
}

func assertQueryContains(t *testing.T, query string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(query, want) {
			t.Errorf("query %q missing %s", query, want)
		}
	}
}

func assertQueryOmits(t *testing.T, query string, forbidden ...string) {
	t.Helper()

	for _, fragment := range forbidden {
		if strings.Contains(query, fragment) {
			t.Errorf("query %q must not contain %s", query, fragment)
		}
	}
}

func TestConsul_Resolve_Namespace(t *testing.T) {
	var receivedNS string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedNS = r.URL.Query().Get("ns")
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{Namespace: "production"})

	_, _ = c.Resolve(t.Context(), "svc")

	if receivedNS != "production" {
		t.Errorf("ns = %q, want %q", receivedNS, "production")
	}
}

func TestConsul_Resolve_NoNamespace(t *testing.T) {
	var receivedNS string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedNS = r.URL.Query().Get("ns")
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	_, _ = c.Resolve(t.Context(), "svc")

	if receivedNS != "" {
		t.Errorf("ns = %q, want empty (no namespace filter)", receivedNS)
	}
}

func TestConsul_Resolve_ACLToken(t *testing.T) {
	var receivedToken string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("X-Consul-Token")
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{Token: "my-secret-token"})

	_, _ = c.Resolve(t.Context(), "svc")

	if receivedToken != "my-secret-token" {
		t.Errorf("token = %q, want %q", receivedToken, "my-secret-token")
	}
}

func TestConsul_Resolve_NoToken(t *testing.T) {
	var receivedToken string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("X-Consul-Token")
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	_, _ = c.Resolve(t.Context(), "svc")

	if receivedToken != "" {
		t.Errorf("token = %q, want empty", receivedToken)
	}
}

func TestConsul_Resolve_NotFound(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	_, err := c.Resolve(t.Context(), "missing")
	if err != ErrServiceNotFound {
		t.Errorf("Resolve() error = %v, want ErrServiceNotFound", err)
	}
}

func TestConsul_Resolve_ServerError(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	_, err := c.Resolve(t.Context(), "svc")
	if err == nil {
		t.Fatal("Resolve() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want to contain '500'", err.Error())
	}
}

func TestConsul_Resolve_EmptyResponse(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]consulServiceEntry{})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	_, err := c.Resolve(t.Context(), "svc")
	if err != ErrNoInstances {
		t.Errorf("Resolve() error = %v, want ErrNoInstances", err)
	}
}

func TestConsul_Resolve_InvalidJSON(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	_, err := c.Resolve(t.Context(), "svc")
	if err == nil {
		t.Fatal("Resolve() error = nil, want JSON parse error")
	}
}

func TestConsul_Resolve_ContextCancelled(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		json.NewEncoder(w).Encode([]consulServiceEntry{})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	_, err := c.Resolve(ctx, "svc")
	if err == nil {
		t.Fatal("Resolve() error = nil, want context error")
	}
}

func TestConsul_Resolve_MultipleServices(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		svcName := parts[len(parts)-1]

		var entries []consulServiceEntry
		switch svcName {
		case "svc-a":
			entries = []consulServiceEntry{
				{Service: consulService{Address: "10.0.0.1", Port: 8080}},
			}
		case "svc-b":
			entries = []consulServiceEntry{
				{Service: consulService{Address: "10.0.0.2", Port: 9090}},
				{Service: consulService{Address: "10.0.0.3", Port: 9090}},
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	backendsA, err := c.Resolve(t.Context(), "svc-a")
	if err != nil {
		t.Fatalf("Resolve(svc-a) error = %v", err)
	}
	if len(backendsA) != 1 {
		t.Errorf("svc-a backends = %d, want 1", len(backendsA))
	}

	backendsB, err := c.Resolve(t.Context(), "svc-b")
	if err != nil {
		t.Fatalf("Resolve(svc-b) error = %v", err)
	}
	if len(backendsB) != 2 {
		t.Errorf("svc-b backends = %d, want 2", len(backendsB))
	}

	_, err = c.Resolve(t.Context(), "svc-c")
	if err != ErrServiceNotFound {
		t.Errorf("Resolve(svc-c) error = %v, want ErrServiceNotFound", err)
	}
}

func TestConsul_Register_Success(t *testing.T) {
	var receivedBody consulRegistration
	var receivedMethod string
	var receivedPath string
	var receivedContentType string
	var receivedToken string

	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedContentType = r.Header.Get("Content-Type")
		receivedToken = r.Header.Get("X-Consul-Token")
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{Token: "reg-token"})

	inst := Instance{
		ID:       "svc-1",
		Name:     "my-service",
		Address:  "10.0.0.5",
		Port:     8080,
		Tags:     []string{"v2", "primary"},
		Metadata: map[string]string{"version": "2.0"},
	}

	err := c.Register(t.Context(), inst)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if receivedMethod != "PUT" {
		t.Errorf("method = %q, want PUT", receivedMethod)
	}
	if receivedPath != "/v1/agent/service/register" {
		t.Errorf("path = %q, want /v1/agent/service/register", receivedPath)
	}
	if receivedContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", receivedContentType)
	}
	if receivedToken != "reg-token" {
		t.Errorf("token = %q, want reg-token", receivedToken)
	}
	if receivedBody.ID != "svc-1" {
		t.Errorf("body.ID = %q, want %q", receivedBody.ID, "svc-1")
	}
	if receivedBody.Name != "my-service" {
		t.Errorf("body.Name = %q, want %q", receivedBody.Name, "my-service")
	}
	if receivedBody.Address != "10.0.0.5" {
		t.Errorf("body.Address = %q, want %q", receivedBody.Address, "10.0.0.5")
	}
	if receivedBody.Port != 8080 {
		t.Errorf("body.Port = %d, want %d", receivedBody.Port, 8080)
	}
	if len(receivedBody.Tags) != 2 {
		t.Errorf("body.Tags len = %d, want 2", len(receivedBody.Tags))
	}
	if receivedBody.Meta["version"] != "2.0" {
		t.Errorf("body.Meta[version] = %q, want %q", receivedBody.Meta["version"], "2.0")
	}
}

func TestConsul_Register_ServerError(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	err := c.Register(t.Context(), Instance{ID: "svc-1", Name: "svc"})
	if err == nil {
		t.Fatal("Register() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want to contain '500'", err.Error())
	}
}

func TestConsul_Register_MissingID(t *testing.T) {
	c, err := NewConsul("127.0.0.1:1", ConsulConfig{})
	if err != nil {
		t.Fatalf("NewConsul() error = %v", err)
	}

	err = c.Register(t.Context(), Instance{Name: "svc"})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Register() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConsul_Register_MissingName(t *testing.T) {
	c, err := NewConsul("127.0.0.1:1", ConsulConfig{})
	if err != nil {
		t.Fatalf("NewConsul() error = %v", err)
	}

	err = c.Register(t.Context(), Instance{ID: "svc-1"})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Register() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConsul_Register_NoToken(t *testing.T) {
	var receivedToken string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("X-Consul-Token")
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	c.Register(t.Context(), Instance{ID: "svc-1", Name: "svc"})

	if receivedToken != "" {
		t.Errorf("token = %q, want empty", receivedToken)
	}
}

func TestConsul_Deregister_Success(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	var receivedToken string

	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedToken = r.Header.Get("X-Consul-Token")
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{Token: "dereg-token"})

	err := c.Deregister(t.Context(), "svc-1")
	if err != nil {
		t.Fatalf("Deregister() error = %v", err)
	}
	if receivedMethod != "PUT" {
		t.Errorf("method = %q, want PUT", receivedMethod)
	}
	if receivedPath != "/v1/agent/service/deregister/svc-1" {
		t.Errorf("path = %q, want /v1/agent/service/deregister/svc-1", receivedPath)
	}
	if receivedToken != "dereg-token" {
		t.Errorf("token = %q, want dereg-token", receivedToken)
	}
}

func TestConsul_Deregister_ServerError(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	err := c.Deregister(t.Context(), "svc-1")
	if err == nil {
		t.Fatal("Deregister() error = nil, want error")
	}
}

func TestConsul_Health_NotSupported(t *testing.T) {
	c, _ := NewConsul("localhost:8500", ConsulConfig{})
	err := c.Health(t.Context(), "svc-1", true)
	if err != ErrNotSupported {
		t.Errorf("Health() = %v, want ErrNotSupported", err)
	}
}

func TestConsul_Close(t *testing.T) {
	c, _ := NewConsul("localhost:8500", ConsulConfig{})
	err := c.Close()
	if err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestConsul_Close_ClearsWatchers(t *testing.T) {
	entries := []consulServiceEntry{
		{Service: consulService{Address: "10.0.0.1", Port: 8080}},
	}
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{WaitTime: 100 * time.Millisecond})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, _ = c.Watch(ctx, "svc-a")
	_, _ = c.Watch(ctx, "svc-b")

	c.mu.RLock()
	count := len(c.watchers)
	c.mu.RUnlock()

	if count != 2 {
		t.Fatalf("watchers count = %d, want 2", count)
	}

	c.Close()

	c.mu.RLock()
	count = len(c.watchers)
	c.mu.RUnlock()

	if count != 0 {
		t.Errorf("watchers after Close() = %d, want 0", count)
	}
}

func TestConsul_Watch_CreatesWatcher(t *testing.T) {
	entries := []consulServiceEntry{
		{Service: consulService{Address: "10.0.0.1", Port: 8080}},
	}
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{WaitTime: 100 * time.Millisecond})
	defer c.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := c.Watch(ctx, "svc")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if ch == nil {
		t.Fatal("Watch() returned nil channel")
	}

	c.mu.RLock()
	_, exists := c.watchers["svc"]
	c.mu.RUnlock()

	if !exists {
		t.Error("watcher not created for 'svc'")
	}
}

func TestConsul_Watch_ReusesWatcher(t *testing.T) {
	entries := []consulServiceEntry{
		{Service: consulService{Address: "10.0.0.1", Port: 8080}},
	}
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{WaitTime: 100 * time.Millisecond})
	defer c.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, _ = c.Watch(ctx, "svc")
	_, _ = c.Watch(ctx, "svc")

	c.mu.RLock()
	count := len(c.watchers)
	c.mu.RUnlock()

	if count != 1 {
		t.Errorf("watchers count = %d, want 1 (should reuse)", count)
	}
}

func TestConsul_Watch_ReceivesUpdates(t *testing.T) {
	entries := []consulServiceEntry{
		{Service: consulService{Address: "10.0.0.1", Port: 8080}},
	}
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{WaitTime: 50 * time.Millisecond})
	defer c.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := c.Watch(ctx, "svc")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}

	select {
	case backends := <-ch:
		if len(backends) != 1 {
			t.Errorf("got %d backends, want 1", len(backends))
		}
		if backends[0] != "http://10.0.0.1:8080" {
			t.Errorf("backend = %q, want %q", backends[0], "http://10.0.0.1:8080")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Watch() timed out waiting for update")
	}
}

func TestConsul_Watch_ContextCancel(t *testing.T) {
	entries := []consulServiceEntry{
		{Service: consulService{Address: "10.0.0.1", Port: 8080}},
	}
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{WaitTime: 50 * time.Millisecond})
	defer c.Close()

	ctx, cancel := context.WithCancel(t.Context())

	ch, err := c.Watch(ctx, "svc")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}

	cancel()

	// Channel should eventually close
	select {
	case _, ok := <-ch:
		if ok {
			// May receive one more update before close, drain again
			select {
			case _, ok2 := <-ch:
				if ok2 {
					// Might get one more buffered; try once more
					timer := time.NewTimer(500 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-ch:
					case <-timer.C:
					}
				}
			case <-time.After(500 * time.Millisecond):
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed after context cancel")
	}
}

func TestConsul_Resolve_ConcurrentAccess(t *testing.T) {
	entries := []consulServiceEntry{
		{Service: consulService{Address: "10.0.0.1", Port: 8080}},
	}
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			svc := fmt.Sprintf("svc-%d", n%5)
			_, _ = c.Resolve(t.Context(), svc)
		}(i)
	}
	wg.Wait()
}

func TestConsul_Resolve_Datacenter(t *testing.T) {
	var receivedDC string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedDC = r.URL.Query().Get("dc")
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{Datacenter: "us-west-2"})

	_, _ = c.Resolve(t.Context(), "svc")

	if receivedDC != "us-west-2" {
		t.Errorf("dc = %q, want %q", receivedDC, "us-west-2")
	}
}

func TestConsul_Resolve_NoTag(t *testing.T) {
	var receivedTag string
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		receivedTag = r.URL.Query().Get("tag")
		json.NewEncoder(w).Encode([]consulServiceEntry{
			{Service: consulService{Address: "10.0.0.1", Port: 8080}},
		})
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	_, _ = c.Resolve(t.Context(), "svc")

	if receivedTag != "" {
		t.Errorf("tag = %q, want empty (no tag filter)", receivedTag)
	}
}

func TestConsul_Register_ContextCancelled(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := c.Register(ctx, Instance{ID: "svc-1", Name: "svc"})
	if err == nil {
		t.Fatal("Register() error = nil, want context error")
	}
}

func TestConsul_Deregister_ContextCancelled(t *testing.T) {
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := c.Deregister(ctx, "svc-1")
	if err == nil {
		t.Fatal("Deregister() error = nil, want context error")
	}
}

func TestConsul_Resolve_ConnectionRefused(t *testing.T) {
	c, _ := NewConsul("127.0.0.1:1", ConsulConfig{Timeout: 100 * time.Millisecond})

	_, err := c.Resolve(t.Context(), "svc")
	if err == nil {
		t.Fatal("Resolve() error = nil, want connection error")
	}
}

func TestConsul_Watch_MultipleSubscribers(t *testing.T) {
	entries := []consulServiceEntry{
		{Service: consulService{Address: "10.0.0.1", Port: 8080}},
	}
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{WaitTime: 50 * time.Millisecond})
	defer c.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch1, _ := c.Watch(ctx, "svc")
	ch2, _ := c.Watch(ctx, "svc")

	// Both subscribers should receive updates
	received := 0
	timeout := time.After(3 * time.Second)

	for received < 2 {
		select {
		case <-ch1:
			received++
		case <-ch2:
			received++
		case <-timeout:
			t.Fatalf("timed out, only received %d/2 updates", received)
		}
	}
}

func TestConsul_Resolve_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    error
	}{
		{"404", http.StatusNotFound, ErrServiceNotFound},
		{"403", http.StatusForbidden, nil},  // generic error
		{"502", http.StatusBadGateway, nil}, // generic error
		{"503", http.StatusServiceUnavailable, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			})
			defer server.Close()

			addr := strings.TrimPrefix(server.URL, "http://")
			c, _ := NewConsul(addr, ConsulConfig{})

			_, err := c.Resolve(t.Context(), "svc")
			if err == nil {
				t.Fatal("Resolve() error = nil, want error")
			}
			if tt.wantErr != nil && err != tt.wantErr {
				t.Errorf("Resolve() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && !strings.Contains(err.Error(), fmt.Sprintf("%d", tt.statusCode)) {
				t.Errorf("error = %q, want to contain '%d'", err.Error(), tt.statusCode)
			}
		})
	}
}

func TestConsul_Register_NilTagsAndMeta(t *testing.T) {
	var receivedBody []byte
	server := newTestConsulServer(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	c, _ := NewConsul(addr, ConsulConfig{})

	// Instance with nil Tags and Metadata
	inst := Instance{
		ID:      "svc-1",
		Name:    "svc",
		Address: "10.0.0.1",
		Port:    8080,
	}

	err := c.Register(t.Context(), inst)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// The registration body must decode cleanly and carry no tags/meta.
	var reg consulRegistration
	if err := json.Unmarshal(receivedBody, &reg); err != nil {
		t.Fatalf("decode registration body: %v", err)
	}
	if reg.Tags != nil {
		t.Errorf("Tags = %v, want nil", reg.Tags)
	}
	if reg.Meta != nil {
		t.Errorf("Meta = %v, want nil", reg.Meta)
	}
}

func TestConsulConfig_Defaults(t *testing.T) {
	// Verify ConsulConfig zero value before NewConsul applies defaults
	cfg := ConsulConfig{}
	if cfg.Datacenter != "" {
		t.Errorf("default Datacenter = %q, want empty", cfg.Datacenter)
	}
	if cfg.WaitTime != 0 {
		t.Errorf("default WaitTime = %v, want 0", cfg.WaitTime)
	}
	if cfg.Timeout != 0 {
		t.Errorf("default Timeout = %v, want 0", cfg.Timeout)
	}
	if cfg.Scheme != "" {
		t.Errorf("default Scheme = %q, want empty", cfg.Scheme)
	}
	if cfg.IncludeUnhealthy {
		t.Error("default IncludeUnhealthy = true, want false")
	}
}
