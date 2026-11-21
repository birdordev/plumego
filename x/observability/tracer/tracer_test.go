package tracer

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/birdordev/plumego/contract"
)

func TestRecordSpanEventOptionCanReadTracer(t *testing.T) {
	config := DefaultTracerConfig()
	config.SamplingRate = 1
	tracer, err := NewTracer(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := tracer.StartTrace(t.Context(), "events")
	if span == nil {
		t.Fatal("expected an active span")
	}

	const events = 8
	var wg sync.WaitGroup
	for range events {
		wg.Go(func() {
			tracer.RecordSpanEvent(span, "sample", func(event *SpanEvent) {
				if tracer.GetActiveSpan(ctx) != span {
					t.Error("active span lookup failed")
				}
				event.Attributes["source"] = "option"
			})
		})
	}
	wg.Wait()

	trace, ok := tracer.collector.GetTrace(span.TraceID)
	if !ok || len(trace.Spans) != 1 {
		t.Fatalf("collected trace = %+v, found = %v", trace, ok)
	}
	for _, got := range [][]SpanEvent{span.Events, trace.Spans[0].Events} {
		if len(got) != events {
			t.Fatalf("event count = %d, want %d", len(got), events)
		}
		for _, event := range got {
			if event.Name != "sample" || event.Attributes["source"] != "option" {
				t.Errorf("unexpected event: %+v", event)
			}
		}
	}
}

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

func TestRecordErrorOptionsCanReadTracer(t *testing.T) {
	config := DefaultTracerConfig()
	config.SamplingRate = 1
	tracer, err := NewTracer(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := tracer.StartTrace(t.Context(), "errors")
	if span == nil {
		t.Fatal("expected an active span")
	}

	const records = 8
	recorded := errors.New("recorded error")
	var wg sync.WaitGroup
	for i := range records {
		wg.Go(func() {
			tracer.RecordError(span, recorded, func(attrs map[string]any) {
				if tracer.GetActiveSpan(ctx) != span {
					t.Error("active span lookup failed")
				}
				attrs[fmt.Sprintf("record.%d", i)] = true
			})
		})
	}
	wg.Wait()

	trace, ok := tracer.collector.GetTrace(span.TraceID)
	if !ok || len(trace.Spans) != 1 {
		t.Fatalf("collected trace = %+v, found = %v", trace, ok)
	}
	for _, got := range []*Span{span, trace.Spans[0]} {
		if got.Status.Code != StatusCodeError || got.Status.Description != recorded.Error() {
			t.Errorf("unexpected status: %+v", got.Status)
		}
		if got.Attributes["error.type"] != fmt.Sprintf("%T", recorded) ||
			got.Attributes["error.message"] != recorded.Error() ||
			got.Attributes["error.stack"] != fmt.Sprintf("%+v", recorded) {
			t.Errorf("unexpected error attributes: %+v", got.Attributes)
		}
		for i := range records {
			if got.Attributes[fmt.Sprintf("record.%d", i)] != true {
				t.Errorf("missing attributes from record %d", i)
			}
		}
	}
}

func TestCollectorFilterReentryAndSnapshotOwnership(t *testing.T) {
	collector := NewSimpleTraceCollector()
	now := time.Now()
	collector.Collect(&Trace{ID: "first", StartTime: now, Attributes: map[string]any{"source": "trace"},
		Spans: []*Span{{Attributes: map[string]any{"source": "span"},
			Events: []SpanEvent{{Attributes: map[string]any{"source": "event"}}}}},
	})
	collector.Collect(&Trace{ID: "second", StartTime: now})
	filtered := make(chan []*Trace, 1)
	go func() {
		filtered <- collector.GetTraces(func(trace *Trace) bool {
			if _, ok := collector.GetTrace(trace.ID); !ok {
				t.Errorf("filter could not read trace %q", trace.ID)
			}
			if trace.ID != "first" {
				return false
			}
			trace.Attributes["source"] = "filter"
			trace.Spans[0].Attributes["source"] = "filter"
			return true
		})
	}()

	select {
	case matches := <-filtered:
		if len(matches) != 1 || matches[0].ID != "first" ||
			matches[0].Attributes["source"] != "filter" || matches[0].Spans[0].Attributes["source"] != "filter" {
			t.Fatalf("filter returned %+v", matches)
		}
		matches[0].Spans[0].Events[0].Attributes["source"] = "caller"
	case <-time.After(5 * time.Second):
		t.Fatal("collector filter could not reenter GetTrace")
	}

	original, ok := collector.GetTrace("first")
	if !ok || original.Attributes["source"] != "trace" ||
		original.Spans[0].Attributes["source"] != "span" || original.Spans[0].Events[0].Attributes["source"] != "event" {
		t.Fatalf("stored trace changed: %+v", original)
	}
}

func TestRecordSpanEventCopiesTopLevelAttributes(t *testing.T) {
	config := DefaultTracerConfig()
	config.SamplingRate = 1
	tr, err := NewTracer(config)
	if err != nil {
		t.Fatal(err)
	}
	_, span := tr.StartTrace(t.Context(), "event trace")
	if span == nil {
		t.Fatal("no span")
	}
	attrs := map[string]any{"count": 3}
	tr.RecordSpanEvent(span, "sample", WithEventAttributes(attrs))
	attrs["count"] = 5
	tr.RecordSpanEvent(span, "empty", WithEventAttributes(nil))
	snapshot, ok := tr.collector.GetTrace(span.TraceID)
	if !ok || len(snapshot.Spans) != 1 || len(snapshot.Spans[0].Events) != 2 {
		t.Fatalf("trace=%+v", snapshot)
	}
	first, second := snapshot.Spans[0].Events[0], snapshot.Spans[0].Events[1]
	if first.Name != "sample" || first.Attributes["count"] != 3 || first.Timestamp.IsZero() || second.Name != "empty" || len(second.Attributes) != 0 {
		t.Errorf("events=%+v and %+v", first, second)
	}
}

func TestTraceStatisticsSeparatesActiveAndCompleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := DefaultTracerConfig()
		config.SamplingRate = 1
		tracer, err := NewTracer(config)
		if err != nil {
			t.Fatal(err)
		}
		_, active := tracer.StartTrace(t.Context(), "active")
		_, completed := tracer.StartTrace(t.Context(), "completed")
		defer tracer.EndSpan(active)
		time.Sleep(2 * time.Second)
		tracer.EndSpan(completed, WithSpanStatus(SpanStatus{Code: StatusCodeError}))

		got := tracer.GetTraceStatistics()
		if got.TotalTraces != 2 || got.ActiveTraces != 1 || got.CompletedTraces != 1 || got.TotalSpans != 2 ||
			got.AverageDuration != 2*time.Second || !got.LastTraceTime.Equal(completed.StartTime) ||
			got.ByStatus[StatusCodeUnset] != 1 || got.ByStatus[StatusCodeError] != 1 {
			t.Errorf("unexpected statistics: %+v", got)
		}
		got.ByStatus[StatusCodeError] = 42
		if again := tracer.GetTraceStatistics(); again.ByStatus[StatusCodeError] != 1 {
			t.Errorf("changing returned status map altered statistics: %+v", again)
		}
	})
}

func TestTraceCloneDetachesLinkAttributes(t *testing.T) {
	original := &Trace{
		Links: []TraceLink{
			{TraceID: "trace-link", Attributes: map[string]any{"kind": "trace"}},
			{TraceID: "without-attributes"},
		},
		Spans: []*Span{{
			Links: []SpanLink{
				{TraceID: "target", SpanID: "span-link", Attributes: map[string]any{"kind": "span"}},
				{SpanID: "without-attributes"},
			},
		}},
	}
	clone := original.Clone()
	clone.Links[0].TraceID = "changed"
	clone.Links[0].Attributes["kind"] = "changed"
	clone.Spans[0].Links[0].SpanID = "changed"
	clone.Spans[0].Links[0].Attributes["kind"] = "changed"
	if link := original.Links[0]; link.TraceID != "trace-link" || link.Attributes["kind"] != "trace" {
		t.Errorf("original trace link changed: %+v", link)
	}
	if link := original.Spans[0].Links[0]; link.SpanID != "span-link" || link.Attributes["kind"] != "span" {
		t.Errorf("original span link changed: %+v", link)
	}
	if clone.Links[1].Attributes != nil || clone.Spans[0].Links[1].Attributes != nil {
		t.Error("Clone allocated attributes for nil maps")
	}
}
