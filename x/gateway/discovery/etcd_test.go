package discovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// etcdRangeBody builds the JSON body for a successful etcd range response.
func etcdRangeBody(instances []Instance) []byte {
	kvs := make([]etcdKV, len(instances))
	for i, inst := range instances {
		v, _ := json.Marshal(inst)
		kvs[i] = etcdKV{
			Key:   base64.StdEncoding.EncodeToString([]byte("/services/" + inst.Name + "/" + inst.ID)),
			Value: base64.StdEncoding.EncodeToString(v),
		}
	}
	b, _ := json.Marshal(etcdRangeResponse{Kvs: kvs})
	return b
}

// newTestEtcd builds an Etcd client pointed at a test HTTP server.
func newTestEtcd(t *testing.T, handler http.Handler) *Etcd {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	e, err := NewEtcd([]string{srv.URL}, EtcdConfig{
		Prefix:       "/services",
		Timeout:      2 * time.Second,
		PollInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEtcd: %v", err)
	}
	return e
}

func TestEtcd_NewEtcd_NoEndpoints(t *testing.T) {
	_, err := NewEtcd(nil, EtcdConfig{})
	if err == nil {
		t.Fatal("expected error for empty endpoints")
	}
}

func TestEtcd_Resolve_OK(t *testing.T) {
	instances := []Instance{
		{ID: "i1", Name: "svc", Address: "10.0.0.1", Port: 8080, Healthy: true},
		{ID: "i2", Name: "svc", Address: "10.0.0.2", Port: 8080, Healthy: true},
	}
	body := etcdRangeBody(instances)

	e := newTestEtcd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	backends, err := e.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("expected 2 backends, got %d: %v", len(backends), backends)
	}
}

func TestEtcd_Resolve_UnhealthyFiltered(t *testing.T) {
	instances := []Instance{
		{ID: "i1", Name: "svc", Address: "10.0.0.1", Port: 8080, Healthy: false},
		{ID: "i2", Name: "svc", Address: "10.0.0.2", Port: 8080, Healthy: true},
	}
	body := etcdRangeBody(instances)

	e := newTestEtcd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	backends, err := e.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(backends) != 1 {
		t.Errorf("expected 1 healthy backend, got %d", len(backends))
	}
}

func TestEtcd_Resolve_NoInstances(t *testing.T) {
	body, _ := json.Marshal(etcdRangeResponse{Kvs: nil})
	e := newTestEtcd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	_, err := e.Resolve(t.Context(), "svc")
	if err != ErrNoInstances {
		t.Errorf("expected ErrNoInstances, got %v", err)
	}
}

func TestEtcd_Resolve_AllUnhealthy(t *testing.T) {
	instances := []Instance{
		{ID: "i1", Name: "svc", Address: "10.0.0.1", Port: 8080, Healthy: false},
	}
	body := etcdRangeBody(instances)
	e := newTestEtcd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	_, err := e.Resolve(t.Context(), "svc")
	if err != ErrNoInstances {
		t.Errorf("expected ErrNoInstances for all-unhealthy, got %v", err)
	}
}

func TestEtcd_Resolve_ServerError(t *testing.T) {
	e := newTestEtcd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, err := e.Resolve(t.Context(), "svc")
	if err == nil {
		t.Error("expected error for server 500, got nil")
	}
}

func TestEtcd_Register_OK(t *testing.T) {
	var received map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	e, _ := NewEtcd([]string{srv.URL}, EtcdConfig{Prefix: "/s", Timeout: 2 * time.Second})
	inst := Instance{ID: "i1", Name: "svc", Address: "10.0.0.1", Port: 8080, Healthy: true}
	if err := e.Register(t.Context(), inst); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestEtcd_Register_MissingID(t *testing.T) {
	e := newTestEtcd(t, http.NotFoundHandler())
	err := e.Register(t.Context(), Instance{Name: "svc"})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Register() error = %v, want ErrInvalidConfig", err)
	}
}

func TestEtcd_Register_MissingName(t *testing.T) {
	e := newTestEtcd(t, http.NotFoundHandler())
	err := e.Register(t.Context(), Instance{ID: "i1"})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Register() error = %v, want ErrInvalidConfig", err)
	}
}

func TestEtcd_Deregister_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	e, _ := NewEtcd([]string{srv.URL}, EtcdConfig{Prefix: "/s", Timeout: 2 * time.Second})
	if err := e.Deregister(t.Context(), "svc/i1"); err != nil {
		t.Errorf("Deregister: %v", err)
	}
}

func TestEtcd_Deregister_InvalidID(t *testing.T) {
	e := newTestEtcd(t, http.NotFoundHandler())
	if err := e.Deregister(t.Context(), "no-slash"); err == nil {
		t.Error("expected error for invalid serviceID format")
	}
}

func TestEtcd_Watch_ReceivesInitialState(t *testing.T) {
	instances := []Instance{
		{ID: "i1", Name: "svc", Address: "10.0.0.1", Port: 8080, Healthy: true},
	}
	body := etcdRangeBody(instances)

	e := newTestEtcd(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	ch, err := e.Watch(ctx, "svc")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	select {
	case backends := <-ch:
		if len(backends) != 1 {
			t.Errorf("Watch initial state: expected 1 backend, got %d", len(backends))
		}
	case <-ctx.Done():
		t.Fatal("Watch timed out")
	}
}

func TestEtcd_Close(t *testing.T) {
	e := newTestEtcd(t, http.NotFoundHandler())
	if err := e.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestEtcd_ImplementsDiscovery(t *testing.T) {
	var _ Discovery = (*Etcd)(nil)
}

func TestPrefixRangeEnd(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/services/svc/", "/services/svc0"},
		{"a", "b"},
		{"é", "ê"},
		{"a\xff", "b"},
		{"\xff\xff", "\x00"},
		{"", "\x00"},
	}
	for _, tc := range cases {
		got := prefixRangeEnd(tc.input)
		if got != tc.want {
			t.Errorf("prefixRangeEnd(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStringSlicesEqual(t *testing.T) {
	if !stringSlicesEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Error("equal slices reported as unequal")
	}
	if stringSlicesEqual([]string{"a"}, []string{"b"}) {
		t.Error("unequal slices reported as equal")
	}
	if stringSlicesEqual([]string{"a"}, []string{"a", "b"}) {
		t.Error("different-length slices reported as equal")
	}
}
