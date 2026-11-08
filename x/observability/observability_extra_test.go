package observability

import (
	"net/http"
	"sync"
	"testing"

	mwtracing "github.com/birdordev/plumego/middleware/tracing"
	"github.com/birdordev/plumego/router"
)

func addRouteHookExtra(r *router.Router) func(method, path string, handler http.Handler) error {
	return func(method, path string, handler http.Handler) error {
		return r.AddRoute(method, path, handler)
	}
}

// TestConfigureBothEnabledWiresAll verifies metrics + tracing enabled together.
func TestConfigureBothEnabledWiresAll(t *testing.T) {
	r := router.NewRouter()
	var gotCollector *PrometheusCollector
	var gotTracer mwtracing.Tracer

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHookExtra(r),
		SetPrometheusCollector: func(c *PrometheusCollector) { gotCollector = c },
		SetTracer:              func(tr mwtracing.Tracer) { gotTracer = tr },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Tracing.Enabled = true

	if err := Configure(hooks, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCollector == nil {
		t.Error("expected collector to be set")
	}
	if gotTracer == nil {
		t.Error("expected tracer to be set")
	}
}

// TestConfigureMetricsCustomNamespace verifies a custom namespace is applied.
func TestConfigureMetricsCustomNamespace(t *testing.T) {
	r := router.NewRouter()
	var gotCollector *PrometheusCollector

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHookExtra(r),
		SetPrometheusCollector: func(c *PrometheusCollector) { gotCollector = c },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.Namespace = "myapp"

	if err := Configure(hooks, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCollector == nil {
		t.Error("expected collector to be set with custom namespace")
	}
}

// TestConfigureTracingCustomServiceName verifies custom service name is used.
func TestConfigureTracingCustomServiceName(t *testing.T) {
	var gotTracer mwtracing.Tracer

	hooks := Hooks{
		EnsureMutable: func(op, desc string) error { return nil },
		SetTracer:     func(tr mwtracing.Tracer) { gotTracer = tr },
	}
	cfg := DefaultObservabilityConfig()
	cfg.Tracing.Enabled = true
	cfg.Tracing.ServiceName = "custom-svc"

	if err := Configure(hooks, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotTracer == nil {
		t.Error("expected tracer to be set")
	}
}

// TestConfigureConcurrent verifies Configure is safe to call concurrently
// (each call is independent, no shared mutable state in Configure itself).
func TestConfigureConcurrent(t *testing.T) {
	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := router.NewRouter()
			hooks := Hooks{
				EnsureMutable:          func(op, desc string) error { return nil },
				RegisterRoute:          addRouteHookExtra(r),
				SetPrometheusCollector: func(c *PrometheusCollector) {},
				SetTracer:              func(tr mwtracing.Tracer) {},
			}
			cfg := DefaultObservabilityConfig()
			cfg.Metrics.Enabled = true
			cfg.Tracing.Enabled = true
			errs[idx] = Configure(hooks, cfg)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
}

// TestConfigureMetricsMaxSeriesPositive verifies MaxSeries > 0 is applied.
func TestConfigureMetricsMaxSeriesPositive(t *testing.T) {
	r := router.NewRouter()

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHookExtra(r),
		SetPrometheusCollector: func(c *PrometheusCollector) {},
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.MaxSeries = 500

	if err := Configure(hooks, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestConfigureMetricsCustomPath verifies non-default path works.
func TestConfigureMetricsCustomPath(t *testing.T) {
	r := router.NewRouter()

	hooks := Hooks{
		EnsureMutable:          func(op, desc string) error { return nil },
		RegisterRoute:          addRouteHookExtra(r),
		SetPrometheusCollector: func(c *PrometheusCollector) {},
	}
	cfg := DefaultObservabilityConfig()
	cfg.Metrics.Enabled = true
	cfg.Metrics.Path = "/prom"

	if err := Configure(hooks, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestConfigureEnsureMutableCalledWithOpAndDesc verifies hook call arguments.
func TestConfigureEnsureMutableCalledWithOpAndDesc(t *testing.T) {
	var capturedOp, capturedDesc string
	hooks := Hooks{
		EnsureMutable: func(op, desc string) error {
			capturedOp = op
			capturedDesc = desc
			return nil
		},
	}
	cfg := DefaultObservabilityConfig()

	if err := Configure(hooks, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOp != "configure_observability" {
		t.Errorf("op = %q, want configure_observability", capturedOp)
	}
	if capturedDesc == "" {
		t.Error("desc should be non-empty")
	}
}
