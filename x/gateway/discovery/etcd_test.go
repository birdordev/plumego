package discovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestEtcdOwnsEndpointOrder(t *testing.T) {
	first := Instance{ID: "first", Name: "sample", Address: "127.0.0.1", Port: 10101, Healthy: true}
	second := Instance{ID: "second", Name: "sample", Address: "127.0.0.1", Port: 20202, Healthy: true}
	serve := func(instance Instance) *httptest.Server {
		body := etcdRangeBody([]Instance{instance})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		}))
		t.Cleanup(server.Close)
		return server
	}
	primary, fallback := serve(first), serve(second)

	endpoints := []string{primary.URL, fallback.URL}
	discovery, err := NewEtcd(endpoints, EtcdConfig{})
	if err != nil {
		t.Fatal(err)
	}
	endpoints[0], endpoints[1] = endpoints[1], endpoints[0]

	assertResolution := func(want string) {
		t.Helper()
		got, err := discovery.Resolve(t.Context(), first.Name)
		if err != nil || len(got) != 1 || got[0] != want {
			t.Fatalf("Resolve = %v, %v, want [%s]", got, err, want)
		}
	}
	assertResolution(first.URL())
	primary.Close()
	assertResolution(second.URL())
}

func TestEtcdCloseStopsActiveWatch(t *testing.T) {
	testPollingCloseStopsActiveWatch(t, "etcd")
}

type trackedEtcdBody struct {
	io.Reader
	closed bool
}

func (b *trackedEtcdBody) Close() error {
	b.closed = true
	return nil
}

func TestEtcdHealthClosesResponsesBeforeNextOperation(t *testing.T) {
	e, err := NewEtcd([]string{"http://first.invalid", "http://second.invalid"}, EtcdConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	instance := Instance{ID: "one", Name: "fixture", Healthy: true}
	for _, tc := range []struct {
		name    string
		wantErr error
	}{
		{name: "write back"},
		{name: "missing", wantErr: ErrServiceNotFound},
		{name: "canceled", wantErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first := &trackedEtcdBody{Reader: strings.NewReader("")}
			rangeBody := etcdRangeBody([]Instance{instance})
			if tc.wantErr == ErrServiceNotFound {
				rangeBody = etcdRangeBody(nil)
			}
			second := &trackedEtcdBody{Reader: bytes.NewReader(rangeBody)}
			writes := 0
			e.client.Transport = consulTestRoundTripper(func(r *http.Request) (*http.Response, error) {
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				switch r.URL.Path {
				case "/v3/kv/range":
					if r.URL.Host == "first.invalid" {
						if tc.wantErr == context.Canceled {
							cancel()
						}
						return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: first}, nil
					}
					if !first.closed {
						t.Error("first range body still open during failover")
					}
					return &http.Response{StatusCode: http.StatusOK, Body: second}, nil
				case "/v3/kv/put":
					writes++
					if !second.closed {
						t.Error("range body still open during write back")
					}
					var request struct{ Value string }
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Fatal(err)
					}
					payload, err := base64.StdEncoding.DecodeString(request.Value)
					if err != nil {
						t.Fatal(err)
					}
					var updated Instance
					if err := json.Unmarshal(payload, &updated); err != nil || updated.Healthy || updated.ID != instance.ID {
						t.Errorf("updated instance = %+v, %v", updated, err)
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
				default:
					t.Fatalf("unexpected request %s", r.URL)
					return nil, nil
				}
			})
			if err := e.Health(ctx, "fixture/one", false); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Health = %v, want %v", err, tc.wantErr)
			}
			wantWrites := 0
			if tc.wantErr == nil {
				wantWrites = 1
			}
			if !first.closed || (tc.wantErr != context.Canceled && !second.closed) || writes != wantWrites {
				t.Errorf("bodies closed = %t, %t, writes = %d", first.closed, second.closed, writes)
			}
		})
	}
}
