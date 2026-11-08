package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/router"
)

// --- newBackendCircuitBreaker defaults ---

func TestNewBackendCircuitBreaker_NilConfig_UsesDefaults(t *testing.T) {
	cb := newBackendCircuitBreaker("http://backend:8080", nil)
	if cb == nil {
		t.Fatal("expected non-nil circuit breaker")
	}
	// Newly created CB must be in closed state.
	stats := cb.Stats()
	if stats.State.String() == "open" {
		t.Error("new circuit breaker should not start open")
	}
}

func TestNewBackendCircuitBreaker_ExplicitConfig(t *testing.T) {
	cfg := &CircuitBreakerConfig{
		FailureThreshold: 0.8,
		SuccessThreshold: 5,
		MinRequests:      20,
	}
	cb := newBackendCircuitBreaker("http://backend:8080", cfg)
	if cb == nil {
		t.Fatal("expected non-nil circuit breaker")
	}
}

func TestNewBackendCircuitBreaker_TripAndReset(t *testing.T) {
	cb := newBackendCircuitBreaker("http://b:1", nil)
	cb.Trip()
	if cb.State().String() == "closed" {
		t.Error("circuit breaker should be open after Trip()")
	}
	cb.Reset()
	if cb.State().String() != "closed" {
		t.Errorf("circuit breaker should be closed after Reset(), got %s", cb.State())
	}
}

// --- entrypoints ---

func TestNewGateway_ReturnsProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := NewGateway(GatewayConfig{Targets: []string{srv.URL}})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	if p == nil {
		t.Fatal("NewGateway returned nil")
	}
}

func TestNewGateway_InvalidConfigReturnsError(t *testing.T) {
	proxy, err := NewGateway(GatewayConfig{})
	if err == nil {
		t.Fatal("NewGateway returned nil error")
	}
	if proxy != nil {
		t.Fatal("NewGateway returned proxy for invalid config")
	}
}

func TestNewGatewayBackendPool_OK(t *testing.T) {
	pool, err := NewGatewayBackendPool([]string{"http://a:8080", "http://b:8080"})
	if err != nil {
		t.Fatalf("NewGatewayBackendPool: %v", err)
	}
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
}

func TestNewGatewayBackendPool_InvalidURL(t *testing.T) {
	_, err := NewGatewayBackendPool([]string{"://bad-url"})
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
}

func TestNewGatewayProtocolRegistry_NotNil(t *testing.T) {
	reg := NewGatewayProtocolRegistry()
	if reg == nil {
		t.Fatal("NewGatewayProtocolRegistry returned nil")
	}
}

func TestRegisterRoute_OK(t *testing.T) {
	r := router.NewRouter()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if err := RegisterRoute(r, "/proxy", handler); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	// Verify the route is reachable.
	r.Freeze()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("route status = %d, want 200", w.Code)
	}
}

func TestRegisterRoute_InvalidArgsReturnErrors(t *testing.T) {
	r := router.NewRouter()

	tests := []struct {
		name    string
		router  *router.Router
		path    string
		handler http.Handler
		want    error
	}{
		{
			name:    "nil router",
			router:  nil,
			path:    "/p",
			handler: http.NotFoundHandler(),
			want:    errRegisterNilRouter,
		},
		{
			name:    "empty path",
			router:  r,
			path:    "",
			handler: http.NotFoundHandler(),
			want:    errRegisterEmptyPath,
		},
		{
			name:    "blank path",
			router:  r,
			path:    " \t ",
			handler: http.NotFoundHandler(),
			want:    errRegisterEmptyPath,
		},
		{
			name:    "nil handler",
			router:  r,
			path:    "/p",
			handler: nil,
			want:    errRegisterNilHandler,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RegisterRoute(tt.router, tt.path, tt.handler)
			if !errors.Is(err, tt.want) {
				t.Fatalf("RegisterRoute error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRegisterProxy_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := router.NewRouter()
	proxy, err := RegisterProxy(r, "/svc", GatewayConfig{Targets: []string{srv.URL}})
	if err != nil {
		t.Fatalf("RegisterProxy: %v", err)
	}
	if proxy == nil {
		t.Fatal("RegisterProxy returned nil proxy")
	}
}

func TestRegisterProxy_InvalidConfigReturnsError(t *testing.T) {
	r := router.NewRouter()
	proxy, err := RegisterProxy(r, "/svc", GatewayConfig{})
	if err == nil {
		t.Fatal("RegisterProxy returned nil error")
	}
	if proxy != nil {
		t.Fatal("RegisterProxy returned proxy for invalid config")
	}
}

func TestRegisterProxy_InvalidArgsReturnErrors(t *testing.T) {
	r := router.NewRouter()

	tests := []struct {
		name   string
		router *router.Router
		path   string
		want   error
	}{
		{
			name:   "nil router",
			router: nil,
			path:   "/svc",
			want:   errRegisterNilRouter,
		},
		{
			name:   "empty path",
			router: r,
			path:   "",
			want:   errRegisterEmptyPath,
		},
		{
			name:   "blank path",
			router: r,
			path:   " \t ",
			want:   errRegisterEmptyPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy, err := RegisterProxy(tt.router, tt.path, GatewayConfig{})
			if !errors.Is(err, tt.want) {
				t.Fatalf("RegisterProxy error = %v, want %v", err, tt.want)
			}
			if proxy != nil {
				t.Fatal("RegisterProxy returned proxy for invalid args")
			}
		})
	}
}
