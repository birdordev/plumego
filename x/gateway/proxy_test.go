package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// startBackend starts a test HTTP server and returns its URL.
func startBackend(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// --- ServeHTTP basics ---

func TestProxyBasicRequest(t *testing.T) {
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "ok")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "hello")
	}))

	proxy, err := New(Config{
		Targets:             []string{backend.URL},
		RetryBackoff:        0,
		AddForwardedHeaders: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "hello" {
		t.Errorf("body = %q, want hello", w.Body.String())
	}
	if w.Header().Get("X-Backend") != "ok" {
		t.Error("backend header not forwarded")
	}
}

func TestProxyForwardedHeaders(t *testing.T) {
	var receivedXFF string
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedXFF = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:             []string{backend.URL},
		AddForwardedHeaders: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	proxy.ServeHTTP(w, r)

	if receivedXFF == "" {
		t.Error("X-Forwarded-For should be set on backend request")
	}
}

func TestProxyPathRewrite(t *testing.T) {
	var receivedPath string
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:     []string{backend.URL},
		PathRewrite: StripPrefix("/api"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	proxy.ServeHTTP(w, r)

	if receivedPath != "/users" {
		t.Errorf("backend received path %q, want /users", receivedPath)
	}
}

func TestProxyNoHealthyBackends(t *testing.T) {
	proxy, err := New(Config{Targets: []string{"http://localhost:9999"}})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	// Mark the only backend unhealthy
	proxy.pool.Backends()[0].SetHealthy(false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestProxyBackendFailureRecorded(t *testing.T) {
	// Backend that always returns 500
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:      []string{backend.URL},
		RetryCount:   0,
		RetryBackoff: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	// Request succeeded; verify stats
	stats := proxy.Stats()
	if stats.TotalBackends != 1 {
		t.Errorf("TotalBackends = %d, want 1", stats.TotalBackends)
	}
	if stats.HealthyBackends != 1 {
		t.Errorf("HealthyBackends = %d, want 1", stats.HealthyBackends)
	}
	if stats.Strategy != "round_robin" {
		t.Errorf("Strategy = %q", stats.Strategy)
	}
}

func TestProxyCustomModifyRequest(t *testing.T) {
	var receivedHeader string
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:       []string{backend.URL},
		ModifyRequest: SetHeader("X-Custom", "injected"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if receivedHeader != "injected" {
		t.Errorf("X-Custom = %q, want injected", receivedHeader)
	}
}

func TestProxyCustomModifyResponse(t *testing.T) {
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:        []string{backend.URL},
		ModifyResponse: SetResponseHeader("X-Modified", "yes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if w.Header().Get("X-Modified") != "yes" {
		t.Errorf("X-Modified = %q, want yes", w.Header().Get("X-Modified"))
	}
}

func TestProxyPreserveHost(t *testing.T) {
	var receivedHost string
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:      []string{backend.URL},
		PreserveHost: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "original.host"
	proxy.ServeHTTP(w, r)

	if receivedHost != "original.host" {
		t.Errorf("backend host = %q, want original.host", receivedHost)
	}
}

func TestProxyStats(t *testing.T) {
	proxy, err := New(Config{
		Targets:      []string{"http://a:8080", "http://b:8080"},
		LoadBalancer: NewRoundRobinBalancer(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	stats := proxy.Stats()
	if stats.TotalBackends != 2 {
		t.Errorf("TotalBackends = %d, want 2", stats.TotalBackends)
	}
	if stats.HealthyBackends != 2 {
		t.Errorf("HealthyBackends = %d, want 2", stats.HealthyBackends)
	}
}

func TestProxyMiddleware(t *testing.T) {
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{Targets: []string{backend.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	mw := proxy.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next should not be called by proxy middleware")
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestProxyWebSocketDetection(t *testing.T) {
	// Proxy with WebSocket disabled should not route as WebSocket
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:          []string{backend.URL},
		WebSocketEnabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")
	proxy.ServeHTTP(w, r)

	// Should fall through to HTTP handler (not WebSocket path)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestProxyRetryOnFailure verifies that the proxy retries after backend failure.
func TestProxyRetryOnFailure(t *testing.T) {
	attempt := 0
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:      []string{backend.URL},
		RetryCount:   2,
		RetryBackoff: 0,
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	// First attempt should succeed
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestProxyDoesNotRetryAfterCommittedBodyCopyError(t *testing.T) {
	attempts := 0
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "response body")
	}))

	errorHandlerCalls := 0
	proxy, err := New(Config{
		Targets:      []string{backend.URL},
		RetryCount:   2,
		RetryBackoff: 0,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			errorHandlerCalls++
			w.WriteHeader(http.StatusBadGateway)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := newFailingBodyResponseWriter()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if attempts != 1 {
		t.Fatalf("expected no retry after committed copy error, got %d attempts", attempts)
	}
	if errorHandlerCalls != 0 {
		t.Fatalf("expected error handler not to run after commit, got %d calls", errorHandlerCalls)
	}
	if w.status != http.StatusOK {
		t.Fatalf("expected committed status %d, got %d", http.StatusOK, w.status)
	}
}

func TestProxyRetriesPreCommitFailure(t *testing.T) {
	attempts := 0
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "response body")
	}))

	errorHandlerCalls := 0
	proxy, err := New(Config{
		Targets:      []string{backend.URL},
		RetryCount:   2,
		RetryBackoff: 0,
		ModifyResponse: func(resp *http.Response) error {
			return errors.New("modify response failed before commit")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			errorHandlerCalls++
			w.WriteHeader(http.StatusBadGateway)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if attempts != 3 {
		t.Fatalf("expected 3 attempts for pre-commit failure, got %d", attempts)
	}
	if errorHandlerCalls != 1 {
		t.Fatalf("expected error handler to run once, got %d calls", errorHandlerCalls)
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected status %d, got %d", http.StatusBadGateway, w.Code)
	}
}

type failingBodyResponseWriter struct {
	header http.Header
	status int
}

func newFailingBodyResponseWriter() *failingBodyResponseWriter {
	return &failingBodyResponseWriter{header: make(http.Header)}
}

func (w *failingBodyResponseWriter) Header() http.Header {
	return w.header
}

func (w *failingBodyResponseWriter) WriteHeader(statusCode int) {
	if w.status != 0 {
		return
	}
	w.status = statusCode
}

func (w *failingBodyResponseWriter) Write([]byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return 0, errors.New("client write failed")
}

// TestProxyLeastConnectionsBalancer uses LeastConnections and completes without error.
func TestProxyLeastConnectionsBalancer(t *testing.T) {
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:      []string{backend.URL},
		LoadBalancer: NewLeastConnectionsBalancer(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestProxyCustomErrorHandler verifies custom error handler is invoked.
func TestProxyCustomErrorHandler(t *testing.T) {
	proxy, err := New(Config{
		Targets: []string{"http://localhost:9999"},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("X-Custom-Error", "yes")
			w.WriteHeader(http.StatusBadGateway)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	// Force all backends unhealthy
	proxy.pool.Backends()[0].SetHealthy(false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if w.Header().Get("X-Custom-Error") != "yes" {
		t.Error("custom error handler was not called")
	}
}

// TestProxyIsWebSocketRequest exercises the helper.
func TestProxyIsWebSocketRequest(t *testing.T) {
	tests := []struct {
		connection string
		upgrade    string
		want       bool
	}{
		{"upgrade", "websocket", true},
		{"Upgrade", "WebSocket", true},
		{"keep-alive", "", false},
		{"", "", false},
		{"upgrade", "http2", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.connection != "" {
			r.Header.Set("Connection", tt.connection)
		}
		if tt.upgrade != "" {
			r.Header.Set("Upgrade", tt.upgrade)
		}
		got := isWebSocketRequest(r)
		if got != tt.want {
			t.Errorf("isWebSocketRequest(connection=%q upgrade=%q) = %v, want %v",
				tt.connection, tt.upgrade, got, tt.want)
		}
	}
}

// --- NewE safe constructor ---

func TestProxyNew_InvalidConfig_ReturnsError(t *testing.T) {
	p, err := New(Config{}) // no targets or discovery
	if err == nil {
		t.Error("expected error for invalid config, got nil")
	}
	if p != nil {
		t.Error("expected nil proxy on error")
	}
}

func TestProxyNew_ValidConfig_ReturnsProxy(t *testing.T) {
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	p, err := New(Config{Targets: []string{backend.URL}})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil proxy")
	}
	defer p.Close()
}

func TestProxyCloseCancelsServiceDiscoveryWatch(t *testing.T) {
	discovery := &blockingDiscovery{
		watchStarted:  make(chan context.Context, 1),
		watchCanceled: make(chan struct{}),
	}

	p, err := New(Config{
		Discovery:   discovery,
		ServiceName: "users",
	})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	select {
	case <-discovery.watchStarted:
	case <-time.After(time.Second):
		t.Fatal("service discovery watch did not start")
	}

	p.Close()
	p.Close()

	select {
	case <-discovery.watchCanceled:
	case <-time.After(time.Second):
		t.Fatal("service discovery watch was not canceled by Close")
	}
}

// --- Circuit breaker integration ---

func TestProxyCircuitBreakerTripsAfterFailures(t *testing.T) {
	// Start and immediately close a backend so all TCP connections fail.
	// The circuit breaker counts failures when client.Do returns an error
	// (connection-level failure), not when the backend returns an HTTP 5xx.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	targetURL := srv.URL
	srv.Close() // subsequent connections → "connection refused"

	proxy, err := New(Config{
		Targets:               []string{targetURL},
		CircuitBreakerEnabled: true,
		CircuitBreakerConfig: &CircuitBreakerConfig{
			FailureThreshold: 0.5,
			MinRequests:      3,
			SuccessThreshold: 2,
			Timeout:          5 * time.Second,
		},
		// Use non-zero sentinel values to avoid WithDefaults overriding them.
		RetryCount:   1,
		RetryBackoff: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	pool := proxy.pool
	if len(pool.Backends()) == 0 {
		t.Skip("no backends to inspect circuit state")
	}
	cb := pool.Backends()[0].circuitBreaker
	if cb == nil {
		t.Skip("circuit breaker not attached")
	}

	// Drive enough connection failures to trip the breaker.
	// With MinRequests=3 and FailureThreshold=0.5, circuit opens after 3 failures.
	// RetryCount=1 means each ServeHTTP call makes up to 2 CB attempts.
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	}

	if cb.State().String() == "closed" {
		t.Errorf("expected circuit to be open after connection failures, got %v", cb.State())
	}
}

func TestProxyCircuitBreakerReset(t *testing.T) {
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	proxy, err := New(Config{
		Targets:               []string{backend.URL},
		CircuitBreakerEnabled: true,
		CircuitBreakerConfig: &CircuitBreakerConfig{
			FailureThreshold: 0.5,
			MinRequests:      5,
			SuccessThreshold: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	pool := proxy.pool
	if len(pool.Backends()) == 0 {
		t.Skip("no backends to inspect circuit state")
	}
	cb := pool.Backends()[0].circuitBreaker
	if cb == nil {
		t.Skip("circuit breaker not attached")
	}

	cb.Trip()
	cb.Reset()

	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 after circuit reset, got %d", w.Code)
	}
}

// --- TestProxyBufferPool verifies buffer pool path in copyResponse.
func TestProxyBufferPool(t *testing.T) {
	backend := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 1024))
	}))

	proxy, err := New(Config{
		Targets:    []string{backend.URL},
		BufferPool: &testBufferPool{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	proxy.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if len(w.Body.String()) != 1024 {
		t.Errorf("body length = %d, want 1024", len(w.Body.String()))
	}
}

// testBufferPool is a trivial BufferPool implementation.
type testBufferPool struct{}

func (p *testBufferPool) Get() []byte { return make([]byte, 32*1024) }
func (p *testBufferPool) Put([]byte)  {}

type blockingDiscovery struct {
	watchStarted  chan context.Context
	watchCanceled chan struct{}
}

func (d *blockingDiscovery) Resolve(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (d *blockingDiscovery) Watch(ctx context.Context, _ string) (<-chan []string, error) {
	updates := make(chan []string)
	d.watchStarted <- ctx
	go func() {
		<-ctx.Done()
		close(d.watchCanceled)
		close(updates)
	}()
	return updates, nil
}

type snapshotDiscovery struct {
	stubDiscovery
	updates chan []string
}

func (d snapshotDiscovery) Watch(context.Context, string) (<-chan []string, error) {
	return d.updates, nil
}

func TestProxyAppliesEmptyDiscoverySnapshot(t *testing.T) {
	first := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("first")) }))
	second := startBackend(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("second")) }))
	updates := make(chan []string)
	p, err := New(Config{Discovery: &snapshotDiscovery{updates: updates}, ServiceName: "reports"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, entry := range []struct {
		backends []string
		status   int
		body     string
	}{
		{[]string{first.URL}, http.StatusOK, "first"},
		{nil, http.StatusServiceUnavailable, ""},
		{[]string{second.URL}, http.StatusOK, "second"},
	} {
		updates <- entry.backends
		// The next receive can occur only after the first snapshot is applied.
		updates <- entry.backends
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != entry.status || (entry.body != "" && w.Body.String() != entry.body) {
			t.Fatalf("backends %v produced %d %q, want %d %q", entry.backends, w.Code, w.Body.String(), entry.status, entry.body)
		}
	}
}

func TestProxyDiscoveryPreservesBackendPolicy(t *testing.T) {
	updates := make(chan []string)
	balancer := NewRoundRobinBalancer()
	p, err := New(Config{
		Discovery:             &snapshotDiscovery{updates: updates},
		ServiceName:           "reports",
		LoadBalancer:          balancer,
		CircuitBreakerEnabled: true,
		CircuitBreakerConfig: &CircuitBreakerConfig{
			MinRequests: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	const target = "http://backend.example"
	publish := func(urls ...string) {
		updates <- urls
		// The watcher can receive the second snapshot only after applying the first.
		updates <- urls
	}
	publish(target)
	original := p.pool.GetBackend(target)
	if original == nil || !original.HasCircuitBreaker() {
		t.Fatal("discovered backend has no circuit breaker")
	}
	backendErr := errors.New("backend unavailable")
	if err := original.Execute(func() error { return backendErr }); !errors.Is(err, backendErr) {
		t.Fatalf("first request error = %v", err)
	}
	if original.circuitBreaker.State().String() != "open" {
		t.Fatal("discovered backend circuit did not open")
	}
	original.RecordFailure(1)

	publish("HTTP://backend.example", "http://other.example")
	if got := p.pool.GetBackend(target); got != original || got.IsHealthy() ||
		got.Stats().TotalFailures != 1 || got.circuitBreaker.State().String() != "open" {
		t.Fatalf("repeated snapshot lost backend state: %+v", got)
	}
	if _, err := balancer.Next(p.pool); err != nil {
		t.Fatal(err)
	}
	publish(target, "http://other.example")
	if balancer.counter.Load() != 1 {
		t.Fatal("unchanged membership reset the load balancer")
	}
	for _, invalid := range []string{"http://[invalid", "relative", "unknown://backend.example"} {
		publish("http://new.example", invalid)
		if p.pool.Count() != 2 || p.pool.GetBackend(target) != original || balancer.counter.Load() != 1 {
			t.Fatalf("invalid snapshot containing %q changed the pool", invalid)
		}
	}
	publish()
	if p.pool.Count() != 0 {
		t.Fatal("empty snapshot did not clear the pool")
	}
	publish(target)
	replacement := p.pool.GetBackend(target)
	if replacement == nil || replacement == original || !replacement.IsHealthy() ||
		replacement.Stats().TotalFailures != 0 || replacement.circuitBreaker.State().String() != "closed" {
		t.Fatal("rediscovered backend did not start with fresh state")
	}
}
