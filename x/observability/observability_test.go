package observability

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	mwtracing "github.com/birdordev/plumego/middleware/tracing"
	"github.com/birdordev/plumego/router"
)

func addRouteHook(r *router.Router) func(method, path string, handler http.Handler) error {
	return func(method, path string, handler http.Handler) error {
		return r.AddRoute(method, path, handler)
	}
}

func TestDefaultMetricsConfig(t *testing.T) {
	cfg := DefaultMetricsConfig()
	if cfg.Enabled {
		t.Fatal("expected metrics disabled by default")
	}
	if cfg.Path != "/metrics" {
		t.Fatalf("expected path /metrics, got %q", cfg.Path)
	}
	if cfg.Namespace != "plumego" {
		t.Fatalf("expected namespace plumego, got %q", cfg.Namespace)
	}
	if cfg.MaxSeries != 10000 {
		t.Fatalf("expected MaxSeries 10000, got %d", cfg.MaxSeries)
	}
}

func TestDefaultTracingConfig(t *testing.T) {
	cfg := DefaultTracingConfig()
	if cfg.Enabled {
		t.Fatal("expected tracing disabled by default")
	}
	if cfg.ServiceName != "plumego" {
		t.Fatalf("expected service name plumego, got %q", cfg.ServiceName)
	}
}

func TestDefaultObservabilityConfig(t *testing.T) {
	cfg := DefaultObservabilityConfig()
	if cfg.Metrics.Enabled || cfg.Tracing.Enabled {
		t.Fatal("expected both disabled by default")
	}
	if cfg.Metrics.Namespace != "plumego" {
		t.Fatalf("expected metrics namespace plumego, got %q", cfg.Metrics.Namespace)
	}
	if cfg.Tracing.ServiceName != "plumego" {
		t.Fatalf("expected tracing service name plumego, got %q", cfg.Tracing.ServiceName)
	}
}

func TestConfigureNilEnsureMutable(t *testing.T) {
	hooks := Hooks{}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true

	err := Configure(hooks, cfg)
	if err == nil {
		t.Fatal("expected error for nil EnsureMutable")
	}
	if err.Error() != "observability hooks missing EnsureMutable" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureEnsureMutableFails(t *testing.T) {
	hooks := Hooks{
		EnsureMutable: func(op, desc string) error {
			return fmt.Errorf("app is already booted")
		},
	}
	cfg := DefaultObservabilityConfig()

	err := Configure(hooks, cfg)
	if err == nil {
		t.Fatal("expected error from EnsureMutable")
	}
	if err.Error() != "app is already booted" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureBothDisabled(t *testing.T) {
	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
	}
	cfg := DefaultObservabilityConfig()

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestConfigureMetricsNilRegisterRoute(t *testing.T) {
	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true

	err := Configure(hooks, cfg)
	if err == nil {
		t.Fatal("expected error for nil RegisterRoute")
	}
	if err.Error() != "observability hooks missing RegisterRoute" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureMetricsWithPrometheus(t *testing.T) {
	r := router.NewRouter()
	var setCollector *PrometheusCollector

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHook(r),
		SetPrometheusCollector: func(c *PrometheusCollector) { setCollector = c },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setCollector == nil {
		t.Fatal("expected collector to be set")
	}
}

func TestConfigureMetricsWithExplicitCollector(t *testing.T) {
	r := router.NewRouter()
	prom := NewPrometheusCollector("test")
	var setCollector *PrometheusCollector

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHook(r),
		SetPrometheusCollector: func(c *PrometheusCollector) { setCollector = c },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.Collector = prom
	exporter, err := NewPrometheusExporter(prom)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Metrics.Exporter = exporter

	err = Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setCollector != prom {
		t.Fatal("expected the explicit collector to be set")
	}
}

func TestConfigureMetricsWithExplicitHandler(t *testing.T) {
	r := router.NewRouter()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
		RegisterRoute: addRouteHook(r),
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.Handler = handler

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureMetricsWithExplicitExporter(t *testing.T) {
	r := router.NewRouter()
	prom := NewPrometheusCollector("test")

	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
		RegisterRoute: addRouteHook(r),
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.Collector = prom
	exporter, err := NewPrometheusExporter(prom)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Metrics.Exporter = exporter

	err = Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureMetricsCollectorFromHooks(t *testing.T) {
	r := router.NewRouter()
	prom := NewPrometheusCollector("hookns")

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHook(r),
		GetPrometheusCollector: func() *PrometheusCollector { return prom },
		SetPrometheusCollector: func(c *PrometheusCollector) {},
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	exporter, err := NewPrometheusExporter(prom)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Metrics.Exporter = exporter

	err = Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureTracingOnly(t *testing.T) {
	var setTracer mwtracing.Tracer

	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
		SetTracer:     func(t mwtracing.Tracer) { setTracer = t },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Tracing.Enabled = true

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setTracer == nil {
		t.Fatal("expected tracer to be set")
	}
}

func TestConfigureTracingWithExplicitTracer(t *testing.T) {
	explicit := &stubTracer{}
	var setTracer mwtracing.Tracer

	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
		SetTracer:     func(t mwtracing.Tracer) { setTracer = t },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Tracing.Enabled = true
	cfg.Tracing.Tracer = explicit

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setTracer != explicit {
		t.Fatal("expected explicit tracer")
	}
}

func TestConfigureTracingFromHooks(t *testing.T) {
	hookTracer := &stubTracer{}
	var setTracer mwtracing.Tracer

	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
		GetTracer:     func() mwtracing.Tracer { return hookTracer },
		SetTracer:     func(t mwtracing.Tracer) { setTracer = t },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Tracing.Enabled = true

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setTracer != hookTracer {
		t.Fatal("expected hook tracer to be used")
	}
}

func TestNormalizeObservabilityPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/metrics", "/metrics"},
		{"metrics", "/metrics"},
		{"  /custom  ", "/custom"},
		{"  custom  ", "/custom"},
		{"/", "/"},
	}
	for _, tt := range tests {
		got := normalizeObservabilityPath(tt.input)
		if got != tt.want {
			t.Errorf("normalizeObservabilityPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConfigureMetricsEmptyPath(t *testing.T) {
	r := router.NewRouter()

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHook(r),
		SetPrometheusCollector: func(c *PrometheusCollector) {},
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.Path = ""

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureMetricsZeroMaxSeries(t *testing.T) {
	r := router.NewRouter()

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHook(r),
		SetPrometheusCollector: func(c *PrometheusCollector) {},
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.MaxSeries = 0

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureTracingWithNilSetTracer(t *testing.T) {
	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Tracing.Enabled = true

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureMetricsWithNilSetCollector(t *testing.T) {
	r := router.NewRouter()

	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
		RegisterRoute: addRouteHook(r),
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true

	err := Configure(hooks, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// stubTracer satisfies mwtracing.Tracer.
type stubTracer struct{}

func (s *stubTracer) Start(ctx context.Context, _ *http.Request) (context.Context, mwtracing.TraceSpan) {
	return ctx, &stubSpan{}
}

type stubSpan struct{}

func (s *stubSpan) End(status, bytes int, traceID string) {}
func (s *stubSpan) TraceID() string                       { return "" }
func (s *stubSpan) SpanID() string                        { return "" }
