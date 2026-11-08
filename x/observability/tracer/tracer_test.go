package tracer

import (
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

func TestRandomIDGenerator(t *testing.T) {
	gen := NewRandomIDGenerator()

	id1 := gen.GenerateTraceID()
	id2 := gen.GenerateTraceID()
	if id1 == id2 {
		t.Fatalf("expected different trace IDs")
	}
	if len(id1) != TraceIDLength {
		t.Fatalf("expected trace ID length %d, got %d", TraceIDLength, len(id1))
	}

	sid1 := gen.GenerateSpanID()
	sid2 := gen.GenerateSpanID()
	if sid1 == sid2 {
		t.Fatalf("expected different span IDs")
	}
	if len(sid1) != SpanIDLength {
		t.Fatalf("expected span ID length %d, got %d", SpanIDLength, len(sid1))
	}
}

func TestTracer(t *testing.T) {
	tracer, err := NewTracer(DefaultTracerConfig())
	if err != nil {
		t.Fatal(err)
	}

	ctx, span := tracer.StartTrace(t.Context(), "test-operation")

	if span == nil {
		t.Fatalf("expected span to be created")
	}
	if span.Name != "test-operation" {
		t.Fatalf("expected span name to be test-operation")
	}
	if span.TraceID == "" {
		t.Fatalf("expected trace ID to be set")
	}
	if span.ID == "" {
		t.Fatalf("expected span ID to be set")
	}

	traceCtx, ok := contract.TraceContextFromContext(ctx)
	if !ok {
		t.Fatalf("expected trace context in context")
	}
	if traceCtx.TraceID != string(span.TraceID) {
		t.Fatalf("expected trace context to match span trace ID")
	}
	if traceCtx.SpanID != string(span.ID) {
		t.Fatalf("expected trace context to match span ID")
	}

	time.Sleep(time.Millisecond)
	tracer.EndSpan(span)

	if span.EndTime == nil {
		t.Fatalf("expected end time to be set")
	}
	if span.Duration <= 0 {
		t.Fatalf("expected positive duration")
	}
}

func TestTracerWithOptions(t *testing.T) {
	tracer, err := NewTracer(DefaultTracerConfig())
	if err != nil {
		t.Fatal(err)
	}
	attrs := map[string]any{"service.name": "test-service"}

	ctx, span := tracer.StartTrace(t.Context(), "test-operation",
		WithTraceAttributes(attrs),
		WithSpanKind(SpanKindClient),
	)

	if span.Kind != SpanKindClient {
		t.Fatalf("expected span kind to be client")
	}
	if _, ok := contract.TraceContextFromContext(ctx); !ok {
		t.Fatalf("expected trace context")
	}
}

func TestStartChildSpan(t *testing.T) {
	tracer, err := NewTracer(DefaultTracerConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, parentSpan := tracer.StartTrace(t.Context(), "parent-operation")
	ctx, childSpan := tracer.StartChildSpan(t.Context(), parentSpan, "child-operation")

	if childSpan == nil {
		t.Fatalf("expected child span to be created")
	}
	if childSpan.TraceID != parentSpan.TraceID {
		t.Fatalf("expected child span to have same trace ID as parent")
	}
	if childSpan.ParentSpanID == nil || *childSpan.ParentSpanID != parentSpan.ID {
		t.Fatalf("expected child span to reference parent span ID")
	}

	traceCtx, ok := contract.TraceContextFromContext(ctx)
	if !ok {
		t.Fatalf("expected trace context in context")
	}
	if traceCtx.SpanID != string(childSpan.ID) {
		t.Fatalf("expected context to contain child span ID")
	}
	if traceCtx.ParentSpanID == nil || *traceCtx.ParentSpanID != string(parentSpan.ID) {
		t.Fatalf("expected context to contain parent span ID")
	}
}

func TestStartChildSpanWithNilParent(t *testing.T) {
	tracer, err := NewTracer(DefaultTracerConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := tracer.StartChildSpan(t.Context(), nil, "standalone-operation")

	if span == nil {
		t.Fatalf("expected span to be created")
	}
	if span.ParentSpanID != nil {
		t.Fatalf("expected no parent span ID for standalone operation")
	}
	if tc, _ := contract.TraceContextFromContext(ctx); tc.ParentSpanID != nil {
		t.Fatalf("expected no parent span ID in context")
	}
}

func TestTracerMaxSpansPerTrace(t *testing.T) {
	config := DefaultTracerConfig()
	config.SamplingRate = 1.0
	config.MaxSpansPerTrace = 1

	tracer, err := NewTracer(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, root := tracer.StartTrace(t.Context(), "root")
	_, child := tracer.StartChildSpan(ctx, root, "child")
	tracer.EndSpan(child)
	tracer.EndSpan(root)

	trace, exists := tracer.collector.GetTrace(root.TraceID)
	if !exists {
		t.Fatalf("expected trace to be collected")
	}
	if len(trace.Spans) != 1 {
		t.Fatalf("expected 1 span collected, got %d", len(trace.Spans))
	}
}

func TestDefaultTracerConfig(t *testing.T) {
	cfg := DefaultTracerConfig()
	if cfg.ServiceName != "plumego" {
		t.Fatalf("expected service name plumego, got %s", cfg.ServiceName)
	}
	if cfg.SamplingRate != 0.1 {
		t.Fatalf("expected sampling rate 0.1, got %f", cfg.SamplingRate)
	}
	if cfg.MaxSpansPerTrace != 1000 {
		t.Fatalf("expected max spans 1000, got %d", cfg.MaxSpansPerTrace)
	}
}

func TestNewProbabilitySamplerValidation(t *testing.T) {
	for _, probability := range []float64{-0.01, 1.01} {
		sampler, err := NewProbabilitySampler(probability)
		if err == nil {
			t.Fatalf("NewProbabilitySampler(%f) error = nil", probability)
		}
		if sampler != nil {
			t.Fatalf("NewProbabilitySampler(%f) sampler = %#v, want nil", probability, sampler)
		}
	}

	sampler, err := NewProbabilitySampler(0.5)
	if err != nil {
		t.Fatalf("NewProbabilitySampler valid config: %v", err)
	}
	if sampler == nil {
		t.Fatal("NewProbabilitySampler valid config returned nil sampler")
	}
}

func TestNewTracerValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  TracerConfig
		want error
	}{
		{
			name: "invalid sampling rate",
			cfg: func() TracerConfig {
				cfg := DefaultTracerConfig()
				cfg.SamplingRate = 2
				return cfg
			}(),
			want: ErrInvalidSamplingRate,
		},
		{
			name: "negative max spans",
			cfg: func() TracerConfig {
				cfg := DefaultTracerConfig()
				cfg.MaxSpansPerTrace = -1
				return cfg
			}(),
			want: ErrNegativeMaxSpansPerTrace,
		},
		{
			name: "negative max trace age",
			cfg: func() TracerConfig {
				cfg := DefaultTracerConfig()
				cfg.MaxTraceAge = -time.Second
				return cfg
			}(),
			want: ErrNegativeMaxTraceAge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracer, err := NewTracer(tt.cfg)
			if err != tt.want {
				t.Fatalf("NewTracer error = %v, want %v", err, tt.want)
			}
			if tracer != nil {
				t.Fatalf("NewTracer tracer = %#v, want nil", tracer)
			}
		})
	}
}

func TestNewTracerValidConfig(t *testing.T) {
	tracer, err := NewTracer(DefaultTracerConfig())
	if err != nil {
		t.Fatalf("NewTracer valid config: %v", err)
	}
	if tracer == nil {
		t.Fatal("NewTracer valid config returned nil tracer")
	}
}
