package observability

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/contract"
	mwtracing "github.com/birdordev/plumego/middleware/tracing"
)

// Span captures finalized tracing data for inspection or export.
type Span struct {
	Name          string            `json:"name"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Status        string            `json:"status"`
	StatusMessage string            `json:"status_message,omitempty"`
	Duration      time.Duration     `json:"duration,omitempty"`
	Timestamp     time.Time         `json:"timestamp"`
	ParentSpanID  string            `json:"parent_span_id,omitempty"`
	SpanID        string            `json:"span_id"`
	TraceID       string            `json:"trace_id"`
}

type spanHandle struct {
	tracer    *OpenTelemetryTracer
	name      string
	attrs     map[string]string
	startTime time.Time
	spanID    string
	traceID   string
	parentID  string
}

// OpenTelemetryTracer is the generic tracing implementation owned by x/observability.
type OpenTelemetryTracer struct {
	name string

	mu           sync.RWMutex
	spans        []Span
	maxSpans     int
	droppedSpans int
	spanCounter  uint64
	traceCounter uint64
}

const defaultMaxSpans = 10000

func NewOpenTelemetryTracer(name string) *OpenTelemetryTracer {
	if name == "" {
		name = "github.com/birdordev/plumego/x/observability"
	}
	return &OpenTelemetryTracer{
		name:     name,
		maxSpans: defaultMaxSpans,
	}
}

func (t *OpenTelemetryTracer) WithMaxSpans(max int) *OpenTelemetryTracer {
	t.mu.Lock()
	defer t.mu.Unlock()

	if max <= 0 {
		t.maxSpans = 0
		return t
	}

	t.maxSpans = max
	if len(t.spans) > max {
		t.droppedSpans += len(t.spans) - max
		t.spans = t.spans[len(t.spans)-max:]
	}

	return t
}

func (t *OpenTelemetryTracer) Start(ctx context.Context, r *http.Request) (context.Context, mwtracing.TraceSpan) {
	spanID := t.generateSpanID()
	parent, hasParent := contract.TraceContextFromContext(ctx)

	inboundTraceID := r.Header.Get("X-Trace-ID")
	traceID := ""
	if traceContextHasTraceID(inboundTraceID) {
		traceID = inboundTraceID
	}
	if traceID == "" && hasParent && parent.Valid() {
		traceID = string(parent.TraceID)
	}
	if traceID == "" {
		traceID = t.generateTraceID()
	}

	parentID := ""
	if hasParent && parent.Valid() {
		parentID = string(parent.SpanID)
	}

	handle := &spanHandle{
		tracer:    t,
		name:      "http.request",
		startTime: time.Now(),
		spanID:    spanID,
		traceID:   traceID,
		parentID:  parentID,
		attrs: map[string]string{
			"http.method":     r.Method,
			"http.route":      r.URL.Path,
			"http.user_agent": r.UserAgent(),
			"http.scheme":     "http",
			"net.peer.name":   r.Host,
			"net.transport":   "tcp",
			"service.name":    t.name,
		},
	}
	if parentID != "" {
		handle.attrs["parent.span_id"] = parentID
	}
	if inboundTraceID != "" {
		handle.attrs["parent.trace_id"] = inboundTraceID
	}

	ctx = contract.WithTraceContext(ctx, contract.TraceContext{
		TraceID: traceID,
		SpanID:  spanID,
	})

	return ctx, handle
}

func (t *OpenTelemetryTracer) Spans() []Span {
	t.mu.RLock()
	defer t.mu.RUnlock()

	spans := make([]Span, len(t.spans))
	copy(spans, t.spans)
	return spans
}

type SpanStats struct {
	TotalSpans      int
	ErrorSpans      int
	TotalDuration   time.Duration
	AverageDuration time.Duration
	MaxRetention    int
	DroppedSpans    int
}

func (t *OpenTelemetryTracer) GetSpanStats() SpanStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var stats SpanStats
	stats.TotalSpans = len(t.spans)
	stats.MaxRetention = t.maxSpans
	stats.DroppedSpans = t.droppedSpans

	for _, span := range t.spans {
		if span.Status == "ERROR" {
			stats.ErrorSpans++
		}
		stats.TotalDuration += span.Duration
	}

	if stats.TotalSpans > 0 {
		stats.AverageDuration = stats.TotalDuration / time.Duration(stats.TotalSpans)
	}

	return stats
}

func (t *OpenTelemetryTracer) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.spans = t.spans[:0]
	t.droppedSpans = 0
}

func (t *OpenTelemetryTracer) record(span Span) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.maxSpans > 0 && len(t.spans) >= t.maxSpans {
		t.spans = t.spans[1:]
		t.droppedSpans++
	}

	t.spans = append(t.spans, span)
}

func (s *spanHandle) End(status, bytes int, requestID string) {
	duration := time.Since(s.startTime)

	attrs := make(map[string]string, len(s.attrs)+4)
	for k, v := range s.attrs {
		attrs[k] = v
	}

	attrs["http.status_code"] = strconv.Itoa(status)
	attrs["http.response_content_length"] = strconv.Itoa(bytes)
	attrs["duration_ms"] = strconv.FormatInt(duration.Milliseconds(), 10)
	attrs["http.status_text"] = http.StatusText(status)
	attrs["plumego.trace_id"] = s.traceID
	if requestID != "" {
		attrs["request_id"] = requestID
	}

	spanStatus := "OK"
	statusMsg := ""
	if status >= 500 {
		spanStatus = "ERROR"
		statusMsg = fmt.Sprintf("Server error: %d", status)
	} else if status >= 400 {
		spanStatus = "ERROR"
		statusMsg = fmt.Sprintf("Client error: %d", status)
	}

	s.tracer.record(Span{
		Name:          s.name,
		Attributes:    attrs,
		Status:        spanStatus,
		StatusMessage: statusMsg,
		Duration:      duration,
		Timestamp:     s.startTime,
		SpanID:        s.spanID,
		TraceID:       s.traceID,
		ParentSpanID:  s.parentID,
	})
}

func (s *spanHandle) TraceID() string {
	return s.traceID
}

func (s *spanHandle) SpanID() string {
	return s.spanID
}

func traceContextHasTraceID(traceID string) bool {
	return (contract.TraceContext{TraceID: traceID}).HasTraceID()
}

func (t *OpenTelemetryTracer) generateSpanID() string {
	n := atomic.AddUint64(&t.spanCounter, 1)
	id := uint64(time.Now().UnixNano()) ^ n
	if id == 0 {
		id = 1
	}
	return fmt.Sprintf("%016x", id)
}

func (t *OpenTelemetryTracer) generateTraceID() string {
	n := atomic.AddUint64(&t.traceCounter, 1)
	return fmt.Sprintf("%016x%016x", uint64(time.Now().UnixNano()), n)
}
