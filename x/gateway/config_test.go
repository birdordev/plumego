package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- Config.Validate ---

func TestConfigValidateNoTargetsNoDiscovery(t *testing.T) {
	cfg := &Config{}
	err := cfg.Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigValidateBothTargetsAndDiscovery(t *testing.T) {
	cfg := &Config{
		Targets:   []string{"http://a:8080"},
		Discovery: &stubDiscovery{},
	}
	err := cfg.Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigValidateDiscoveryWithoutServiceName(t *testing.T) {
	cfg := &Config{
		Discovery: &stubDiscovery{},
	}
	err := cfg.Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigValidateStaticTargetsOK(t *testing.T) {
	cfg := &Config{Targets: []string{"http://a:8080"}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigValidateDiscoveryWithServiceNameOK(t *testing.T) {
	cfg := &Config{
		Discovery:   &stubDiscovery{},
		ServiceName: "my-service",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Config.WithDefaults ---

func TestConfigWithDefaultsLoadBalancer(t *testing.T) {
	cfg := &Config{Targets: []string{"http://a:8080"}}
	out := cfg.WithDefaults()
	if out.LoadBalancer == nil {
		t.Fatal("LoadBalancer should default to round-robin")
	}
	if out.LoadBalancer.Name() != "round_robin" {
		t.Errorf("LoadBalancer.Name = %q", out.LoadBalancer.Name())
	}
}

func TestConfigWithDefaultsPreservesExplicitLoadBalancer(t *testing.T) {
	cfg := &Config{
		Targets:      []string{"http://a:8080"},
		LoadBalancer: NewRandomBalancer(),
	}
	out := cfg.WithDefaults()
	if out.LoadBalancer.Name() != "random" {
		t.Errorf("LoadBalancer should be preserved, got %q", out.LoadBalancer.Name())
	}
}

func TestConfigWithDefaultsTimeout(t *testing.T) {
	cfg := &Config{Targets: []string{"http://a:8080"}}
	out := cfg.WithDefaults()
	if out.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", out.Timeout)
	}
}

func TestConfigWithDefaultsPreservesExplicitTimeout(t *testing.T) {
	cfg := &Config{
		Targets: []string{"http://a:8080"},
		Timeout: 5 * time.Second,
	}
	out := cfg.WithDefaults()
	if out.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", out.Timeout)
	}
}

func TestConfigWithDefaultsRetryCount(t *testing.T) {
	cfg := &Config{Targets: []string{"http://a:8080"}}
	out := cfg.WithDefaults()
	if out.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", out.RetryCount)
	}
}

func TestConfigWithDefaultsFailureThreshold(t *testing.T) {
	cfg := &Config{Targets: []string{"http://a:8080"}}
	out := cfg.WithDefaults()
	if out.FailureThreshold != 3 {
		t.Errorf("FailureThreshold = %d, want 3", out.FailureThreshold)
	}
}

func TestConfigWithDefaultsRetryBackoff(t *testing.T) {
	cfg := &Config{Targets: []string{"http://a:8080"}}
	out := cfg.WithDefaults()
	if out.RetryBackoff != 100*time.Millisecond {
		t.Errorf("RetryBackoff = %v, want 100ms", out.RetryBackoff)
	}
}

func TestConfigWithDefaultsForwardedHeaders(t *testing.T) {
	cfg := &Config{Targets: []string{"http://a:8080"}}
	out := cfg.WithDefaults()
	if !out.AddForwardedHeaders {
		t.Error("AddForwardedHeaders should default to true")
	}
	if !out.RemoveHopByHop {
		t.Error("RemoveHopByHop should default to true")
	}
}

func TestConfigWithDefaultsTransport(t *testing.T) {
	cfg := &Config{Targets: []string{"http://a:8080"}}
	out := cfg.WithDefaults()
	if out.Transport == nil {
		t.Fatal("Transport should be set by default")
	}
	if out.Transport.MaxIdleConns != 100 {
		t.Errorf("Transport.MaxIdleConns = %d, want 100", out.Transport.MaxIdleConns)
	}
}

// --- HealthCheckConfig.WithDefaults ---

func TestHealthCheckConfigWithDefaults(t *testing.T) {
	cfg := &HealthCheckConfig{}
	out := cfg.WithDefaults()

	if out.Interval != 10*time.Second {
		t.Errorf("Interval = %v, want 10s", out.Interval)
	}
	if out.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", out.Timeout)
	}
	if out.Path != "/health" {
		t.Errorf("Path = %q, want /health", out.Path)
	}
	if out.Method != "GET" {
		t.Errorf("Method = %q, want GET", out.Method)
	}
	if out.ExpectedStatus != 200 {
		t.Errorf("ExpectedStatus = %d, want 200", out.ExpectedStatus)
	}
}

func TestHealthCheckConfigWithDefaultsPreservesExplicit(t *testing.T) {
	cfg := &HealthCheckConfig{
		Path:           "/ping",
		Method:         "HEAD",
		ExpectedStatus: 204,
		Interval:       5 * time.Second,
		Timeout:        2 * time.Second,
	}
	out := cfg.WithDefaults()
	if out.Path != "/ping" {
		t.Errorf("Path = %q, want /ping", out.Path)
	}
	if out.Method != "HEAD" {
		t.Errorf("Method = %q", out.Method)
	}
	if out.ExpectedStatus != 204 {
		t.Errorf("ExpectedStatus = %d", out.ExpectedStatus)
	}
	if out.Interval != 5*time.Second {
		t.Errorf("Interval = %v", out.Interval)
	}
}

// --- DefaultTransportConfig ---

func TestDefaultTransportConfig(t *testing.T) {
	cfg := DefaultTransportConfig()
	if cfg.MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns = %d", cfg.MaxIdleConns)
	}
	if cfg.MaxIdleConnsPerHost != 10 {
		t.Errorf("MaxIdleConnsPerHost = %d", cfg.MaxIdleConnsPerHost)
	}
	if cfg.IdleConnTimeout != 90*time.Second {
		t.Errorf("IdleConnTimeout = %v", cfg.IdleConnTimeout)
	}
	if cfg.DisableKeepAlives {
		t.Error("DisableKeepAlives should be false by default")
	}
	if cfg.DisableCompression {
		t.Error("DisableCompression should be false by default")
	}
}

// stubDiscovery satisfies ServiceDiscovery for validation-only tests.
type stubDiscovery struct{}

func (s *stubDiscovery) Resolve(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (s *stubDiscovery) Watch(_ context.Context, _ string) (<-chan []string, error) {
	return nil, nil
}

func TestProxyRejectsNegativeTimeout(t *testing.T) {
	for _, tc := range []struct{ timeout, want time.Duration }{{-time.Second, 0}, {0, 30 * time.Second}, {time.Second, time.Second}} {
		cfg := Config{Targets: []string{"http://example.invalid"}, Timeout: tc.timeout, WebSocketEnabled: true}
		p, err := New(cfg)
		if tc.timeout < 0 {
			if p != nil || !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("negative timeout: New=%v, %v", p, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		if p.config.Timeout != tc.want {
			t.Fatalf("timeout=%v, want %v", p.config.Timeout, tc.want)
		}
	}
}
