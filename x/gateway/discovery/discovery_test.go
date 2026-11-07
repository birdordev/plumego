package discovery

import (
	"testing"
)

// Compile-time interface checks
var (
	_ Discovery = (*Static)(nil)
	_ Discovery = (*Consul)(nil)
	_ Discovery = (*Kubernetes)(nil)
	_ Discovery = (*Etcd)(nil)
)

func TestInstance_URL_DefaultScheme(t *testing.T) {
	inst := Instance{Address: "localhost", Port: 8080}
	got := inst.URL()
	want := "http://localhost:8080"
	if got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

func TestInstance_URL_CustomScheme(t *testing.T) {
	inst := Instance{Address: "example.com", Port: 443, Scheme: "https"}
	got := inst.URL()
	want := "https://example.com:443"
	if got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

func TestInstance_URL_Schemes(t *testing.T) {
	tests := []struct {
		name   string
		scheme string
		port   int
		want   string
	}{
		{"empty defaults to http", "", 80, "http://host:80"},
		{"http", "http", 80, "http://host:80"},
		{"https", "https", 443, "https://host:443"},
		{"ws", "ws", 80, "ws://host:80"},
		{"wss", "wss", 443, "wss://host:443"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := Instance{Address: "host", Port: tt.port, Scheme: tt.scheme}
			got := inst.URL()
			if got != tt.want {
				t.Errorf("URL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServiceUpdate_Fields(t *testing.T) {
	su := ServiceUpdate{
		ServiceName: "my-service",
		Instances:   []Instance{{ID: "1", Name: "my-service"}},
		Error:       nil,
	}
	if su.ServiceName != "my-service" {
		t.Errorf("ServiceName = %q, want %q", su.ServiceName, "my-service")
	}
	if len(su.Instances) != 1 {
		t.Errorf("Instances len = %d, want 1", len(su.Instances))
	}
	if su.Error != nil {
		t.Errorf("Error = %v, want nil", su.Error)
	}
}

func TestInstance_Fields(t *testing.T) {
	inst := Instance{
		ID:       "inst-1",
		Name:     "svc",
		Address:  "10.0.0.1",
		Port:     9090,
		Scheme:   "https",
		Metadata: map[string]string{"env": "prod"},
		Tags:     []string{"primary", "v2"},
		Weight:   5,
		Healthy:  true,
	}

	if inst.ID != "inst-1" {
		t.Errorf("ID = %q, want %q", inst.ID, "inst-1")
	}
	if inst.Name != "svc" {
		t.Errorf("Name = %q, want %q", inst.Name, "svc")
	}
	if inst.Address != "10.0.0.1" {
		t.Errorf("Address = %q, want %q", inst.Address, "10.0.0.1")
	}
	if inst.Port != 9090 {
		t.Errorf("Port = %d, want %d", inst.Port, 9090)
	}
	if inst.Scheme != "https" {
		t.Errorf("Scheme = %q, want %q", inst.Scheme, "https")
	}
	if inst.Metadata["env"] != "prod" {
		t.Errorf("Metadata[env] = %q, want %q", inst.Metadata["env"], "prod")
	}
	if len(inst.Tags) != 2 || inst.Tags[0] != "primary" || inst.Tags[1] != "v2" {
		t.Errorf("Tags = %v, want [primary v2]", inst.Tags)
	}
	if inst.Weight != 5 {
		t.Errorf("Weight = %d, want %d", inst.Weight, 5)
	}
	if !inst.Healthy {
		t.Error("Healthy = false, want true")
	}
}

func TestErrorVariables(t *testing.T) {
	tests := []struct {
		name string
		err  error
		msg  string
	}{
		{"ErrServiceNotFound", ErrServiceNotFound, "discovery: service not found"},
		{"ErrNoInstances", ErrNoInstances, "discovery: no instances available"},
		{"ErrInvalidConfig", ErrInvalidConfig, "discovery: invalid configuration"},
		{"ErrNotSupported", ErrNotSupported, "discovery: operation not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatal("error is nil")
			}
			if tt.err.Error() != tt.msg {
				t.Errorf("Error() = %q, want %q", tt.err.Error(), tt.msg)
			}
		})
	}
}

// Static must satisfy the Discovery interface; checked at compile time.
var _ Discovery = (*Static)(nil)

func TestDiscoveryInterface_StaticImplements(t *testing.T) {
	var d Discovery = NewStatic(nil)

	ctx := t.Context()

	// All unsupported operations return ErrNotSupported
	if err := d.Register(ctx, Instance{}); err != ErrNotSupported {
		t.Errorf("Register() = %v, want ErrNotSupported", err)
	}
	if err := d.Deregister(ctx, "id"); err != ErrNotSupported {
		t.Errorf("Deregister() = %v, want ErrNotSupported", err)
	}
	if err := d.Health(ctx, "id", true); err != ErrNotSupported {
		t.Errorf("Health() = %v, want ErrNotSupported", err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}
