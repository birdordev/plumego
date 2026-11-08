package observability

import (
	"fmt"
	"net/http"
	"strings"

	mwtracing "github.com/birdordev/plumego/middleware/tracing"
)

// MetricsConfig configures the built-in metrics endpoint and collector wiring.
type MetricsConfig struct {
	Enabled   bool
	Path      string
	Namespace string
	MaxSeries int
	Collector *PrometheusCollector
	Exporter  Exporter
	Handler   http.Handler
}

// TracingConfig configures the built-in tracing hook.
type TracingConfig struct {
	Enabled     bool
	ServiceName string
	Tracer      mwtracing.Tracer
}

// ObservabilityConfig combines metrics and tracing settings.
type ObservabilityConfig struct {
	Metrics MetricsConfig
	Tracing TracingConfig
}

// DefaultMetricsConfig returns baseline metrics defaults.
func DefaultMetricsConfig() MetricsConfig {
	return MetricsConfig{
		Enabled:   false,
		Path:      "/metrics",
		Namespace: "plumego",
		MaxSeries: 10000,
	}
}

// DefaultTracingConfig returns baseline tracing defaults.
func DefaultTracingConfig() TracingConfig {
	return TracingConfig{
		Enabled:     false,
		ServiceName: "plumego",
	}
}

// DefaultObservabilityConfig returns baseline observability defaults.
func DefaultObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		Metrics: DefaultMetricsConfig(),
		Tracing: DefaultTracingConfig(),
	}
}

// Configure wires built-in metrics and tracing with structured logging.
func Configure(hooks Hooks, cfg ObservabilityConfig) error {
	if hooks.EnsureMutable == nil {
		return fmt.Errorf("observability hooks missing EnsureMutable")
	}
	if err := hooks.EnsureMutable("configure_observability", "configure observability"); err != nil {
		return err
	}

	if cfg.Metrics.Enabled {
		if err := configureMetrics(hooks, cfg.Metrics); err != nil {
			return err
		}
	}

	if cfg.Tracing.Enabled {
		if err := configureTracing(hooks, cfg.Tracing); err != nil {
			return err
		}
	}

	return nil
}

func configureMetrics(hooks Hooks, cfg MetricsConfig) error {
	if hooks.RegisterRoute == nil {
		return fmt.Errorf("observability hooks missing RegisterRoute")
	}

	path := normalizeObservabilityPath(cfg.Path)
	if path == "" {
		path = "/metrics"
	}

	collector := cfg.Collector
	if collector == nil && hooks.GetPrometheusCollector != nil {
		collector = hooks.GetPrometheusCollector()
	}

	exporter := cfg.Exporter
	handler := cfg.Handler

	if collector == nil && exporter == nil && handler == nil {
		prom := NewPrometheusCollector(cfg.Namespace)
		if cfg.MaxSeries > 0 {
			prom.WithMaxMemory(cfg.MaxSeries)
		}
		collector = prom
		var err error
		exporter, err = NewPrometheusExporter(prom)
		if err != nil {
			return err
		}
	}

	if handler == nil && exporter == nil && collector != nil {
		var err error
		exporter, err = NewPrometheusExporter(collector)
		if err != nil {
			return err
		}
	}

	if handler == nil && exporter != nil {
		handler = exporter.Handler()
	}

	if err := hooks.RegisterRoute(http.MethodGet, path, handler); err != nil {
		return err
	}

	if collector != nil {
		if hooks.SetPrometheusCollector != nil {
			hooks.SetPrometheusCollector(collector)
		}
	}
	return nil
}

func configureTracing(hooks Hooks, cfg TracingConfig) error {
	tracer := cfg.Tracer
	if tracer == nil && hooks.GetTracer != nil {
		tracer = hooks.GetTracer()
	}
	if tracer == nil {
		tracer = NewOpenTelemetryTracer(cfg.ServiceName)
	}

	if hooks.SetTracer != nil {
		hooks.SetTracer(tracer)
	}
	return nil
}

func normalizeObservabilityPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

// Hooks provide access to the app wiring points needed by Configure.
type Hooks struct {
	EnsureMutable          func(op, desc string) error
	RegisterRoute          func(method, path string, handler http.Handler) error
	GetPrometheusCollector func() *PrometheusCollector
	SetPrometheusCollector func(*PrometheusCollector)
	GetTracer              func() mwtracing.Tracer
	SetTracer              func(mwtracing.Tracer)
}
