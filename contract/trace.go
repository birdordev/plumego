package contract

import (
	"context"
	"maps"
)

const (
	traceFlagsSampled uint8 = 0x01
	traceIDLength     int   = 32
	spanIDLength      int   = 16
)

// TraceContext is the minimal trace/span metadata carrier stored in
// context.Context. Full tracing infrastructure (Tracer, Span, Collector,
// Sampler) lives in x/observability/tracer.
type TraceContext struct {
	TraceID      string  `json:"trace_id"`
	SpanID       string  `json:"span_id"`
	ParentSpanID *string `json:"parent_span_id,omitempty"`
	// Baggage carries W3C baggage key-value pairs.
	// Extraction from HTTP headers and injection into outgoing requests is not
	// implemented in this package; use x/observability for full propagation support.
	Baggage map[string]string `json:"baggage,omitempty"`
	Flags   uint8             `json:"flags"`
}

// IsSampled reports whether the W3C sampled flag is set in Flags.
func (tc TraceContext) IsSampled() bool {
	return tc.Flags&traceFlagsSampled != 0
}

// HasTraceID reports whether TraceID is present and valid.
func (tc TraceContext) HasTraceID() bool {
	return validHexID(tc.TraceID, traceIDLength)
}

// HasSpanID reports whether SpanID is present and valid.
func (tc TraceContext) HasSpanID() bool {
	return validHexID(tc.SpanID, spanIDLength)
}

// Valid reports whether TraceContext carries both required W3C identifiers.
// Callers that read a TraceContext from context must check Valid before
// treating the carrier as a usable propagation context.
func (tc TraceContext) Valid() bool {
	return tc.HasTraceID() && tc.HasSpanID()
}

type traceContextKey struct{}

// WithTraceContext adds trace context to a context.
// It stores a defensive copy and does not validate, extract, or inject trace
// propagation headers; owning observability packages handle propagation policy.
func WithTraceContext(ctx context.Context, traceContext TraceContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	traceContext = cloneTraceContext(traceContext)
	return context.WithValue(ctx, traceContextKey{}, &traceContext)
}

// TraceContextFromContext retrieves trace context from a context.
// The second return value reports whether a TraceContext was stored.
// The returned carrier may be invalid or span-only; callers must use Valid when
// they require a complete propagation context.
func TraceContextFromContext(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}
	if v := ctx.Value(traceContextKey{}); v != nil {
		if tc, ok := v.(*TraceContext); ok {
			return cloneTraceContext(*tc), true
		}
	}
	return TraceContext{}, false
}

func cloneTraceContext(traceContext TraceContext) TraceContext {
	if traceContext.ParentSpanID != nil {
		parentSpanID := *traceContext.ParentSpanID
		traceContext.ParentSpanID = &parentSpanID
	}
	traceContext.Baggage = maps.Clone(traceContext.Baggage)
	return traceContext
}

func validHexID(id string, length int) bool {
	if len(id) != length {
		return false
	}
	nonzero := false
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c == '0':
			continue
		case '1' <= c && c <= '9', 'a' <= c && c <= 'f', 'A' <= c && c <= 'F':
			nonzero = true
		default:
			return false
		}
	}
	return nonzero
}
