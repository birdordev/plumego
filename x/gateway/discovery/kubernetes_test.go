package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// k8sEndpointFixture builds a minimal Kubernetes Endpoints JSON body.
func k8sEndpointFixture(addresses []string, port int, portName string) []byte {
	subsets := []k8sEndpointSubset{}
	if len(addresses) > 0 {
		addrs := make([]k8sEndpointAddress, len(addresses))
		for i, a := range addresses {
			addrs[i] = k8sEndpointAddress{IP: a}
		}
		subsets = append(subsets, k8sEndpointSubset{
			Addresses: addrs,
			Ports:     []k8sEndpointPort{{Name: portName, Port: port}},
		})
	}
	b, _ := json.Marshal(k8sEndpoints{Subsets: subsets})
	return b
}

func newTestKubernetes(t *testing.T, handler http.Handler) *Kubernetes {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	k, err := NewKubernetes(KubernetesConfig{
		APIServerURL: srv.URL,
		Namespace:    "test-ns",
		Timeout:      2 * time.Second,
		PollInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewKubernetes: %v", err)
	}
	return k
}

func TestKubernetes_Resolve_OK(t *testing.T) {
	body := k8sEndpointFixture([]string{"10.0.0.1", "10.0.0.2"}, 8080, "http")
	k := newTestKubernetes(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	backends, err := k.Resolve(t.Context(), "my-service")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(backends))
	}
	want := map[string]bool{"http://10.0.0.1:8080": true, "http://10.0.0.2:8080": true}
	for _, b := range backends {
		if !want[b] {
			t.Errorf("unexpected backend %q", b)
		}
	}
}

func TestKubernetes_Resolve_NotFound(t *testing.T) {
	k := newTestKubernetes(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	_, err := k.Resolve(t.Context(), "missing-service")
	if err != ErrServiceNotFound {
		t.Errorf("expected ErrServiceNotFound, got %v", err)
	}
}

func TestKubernetes_Resolve_NoInstances(t *testing.T) {
	body := k8sEndpointFixture(nil, 0, "")
	k := newTestKubernetes(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	_, err := k.Resolve(t.Context(), "empty-service")
	if err != ErrNoInstances {
		t.Errorf("expected ErrNoInstances, got %v", err)
	}
}

func TestKubernetes_Resolve_PortName(t *testing.T) {
	// Two ports; only "grpc" (9090) should be selected.
	fixture := k8sEndpoints{
		Subsets: []k8sEndpointSubset{{
			Addresses: []k8sEndpointAddress{{IP: "10.1.2.3"}},
			Ports: []k8sEndpointPort{
				{Name: "http", Port: 8080},
				{Name: "grpc", Port: 9090},
			},
		}},
	}
	body, _ := json.Marshal(fixture)

	k := newTestKubernetes(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	k.config.PortName = "grpc"

	backends, err := k.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(backends) != 1 || backends[0] != "http://10.1.2.3:9090" {
		t.Errorf("backends = %v, want [http://10.1.2.3:9090]", backends)
	}
}

func TestKubernetes_Resolve_UnknownPortName(t *testing.T) {
	fixture := k8sEndpoints{
		Subsets: []k8sEndpointSubset{{
			Addresses: []k8sEndpointAddress{{IP: "10.1.2.3"}},
			Ports:     []k8sEndpointPort{{Name: "http", Port: 8080}},
		}},
	}
	body, _ := json.Marshal(fixture)

	k := newTestKubernetes(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	k.config.PortName = "nonexistent"

	_, err := k.Resolve(t.Context(), "svc")
	if err != ErrNoInstances {
		t.Errorf("expected ErrNoInstances for unknown port name, got %v", err)
	}
}

func TestKubernetes_Register_NotSupported(t *testing.T) {
	k := newTestKubernetes(t, http.NotFoundHandler())
	if err := k.Register(t.Context(), Instance{}); err != ErrNotSupported {
		t.Errorf("Register = %v, want ErrNotSupported", err)
	}
}

func TestKubernetes_Deregister_NotSupported(t *testing.T) {
	k := newTestKubernetes(t, http.NotFoundHandler())
	if err := k.Deregister(t.Context(), "id"); err != ErrNotSupported {
		t.Errorf("Deregister = %v, want ErrNotSupported", err)
	}
}

func TestKubernetes_Health_NotSupported(t *testing.T) {
	k := newTestKubernetes(t, http.NotFoundHandler())
	if err := k.Health(t.Context(), "id", true); err != ErrNotSupported {
		t.Errorf("Health = %v, want ErrNotSupported", err)
	}
}

func TestKubernetes_Watch_ReceivesInitialState(t *testing.T) {
	body := k8sEndpointFixture([]string{"10.0.0.5"}, 7070, "")
	k := newTestKubernetes(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	ch, err := k.Watch(ctx, "svc")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	select {
	case backends := <-ch:
		if len(backends) != 1 || backends[0] != "http://10.0.0.5:7070" {
			t.Errorf("Watch initial = %v", backends)
		}
	case <-ctx.Done():
		t.Fatal("Watch timed out waiting for initial state")
	}
}

func TestKubernetes_Close(t *testing.T) {
	k := newTestKubernetes(t, http.NotFoundHandler())
	if err := k.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Second close must not panic.
	if err := k.Close(); err != nil {
		t.Errorf("Close (2nd): %v", err)
	}
}

func TestKubernetes_ImplementsDiscovery(t *testing.T) {
	var _ Discovery = (*Kubernetes)(nil)
}

func TestDiscoveryFormatsIPv6Backend(t *testing.T) {
	want := []string{"http://[2001:db8::1]:8080", "http://192.0.2.1:8080", "http://example.test:8080"}
	t.Run("consul", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("[{\"Service\":{\"Address\":\"2001:db8::1\",\"Port\":8080}},{\"Service\":{\"Address\":\"192.0.2.1\",\"Port\":8080}},{\"Service\":{\"Address\":\"example.test\",\"Port\":8080}}]"))
		}))
		defer s.Close()
		c, err := NewConsul(s.Listener.Addr().String(), ConsulConfig{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Resolve(t.Context(), "example")
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Resolve = %v, %v; want %v", got, err, want)
		}
	})
	t.Run("kubernetes", func(t *testing.T) {
		body := k8sEndpointFixture([]string{"2001:db8::1", "192.0.2.1", "example.test"}, 8080, "http")
		k := newTestKubernetes(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		}))
		k.config.PortName = "http"
		got, err := k.Resolve(t.Context(), "example")
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Resolve = %v, %v; want %v", got, err, want)
		}
	})
}
