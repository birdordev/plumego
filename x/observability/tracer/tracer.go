// Package tracer provides a distributed tracing subsystem: Tracer, Span, Trace,
// TraceCollector, Sampler, and tracing ID types. The contract package only
// carries string trace metadata through context.Context.
package tracer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/contract"
)

// TraceID represents a unique identifier for tracing context.
type TraceID string

// SpanID represents a unique identifier for a span.
type SpanID string

const (
	// TraceIDLength is the expected length of a trace ID in hexadecimal format.
	TraceIDLength = 32
	// SpanIDLength is the expected length of a span ID in hexadecimal format.
	SpanIDLength = 16

	traceFlagsSampled uint8 = 0x01
)

var (
	ErrInvalidSamplingRate      = errors.New("tracer: sampling rate must be between 0 and 1")
	ErrNegativeMaxSpansPerTrace = errors.New("tracer: max spans per trace cannot be negative")
	ErrNegativeMaxTraceAge      = errors.New("tracer: max trace age cannot be negative")
)

// Trace represents a distributed trace containing multiple spans.
type Trace struct {
	ID         TraceID        `json:"trace_id"`
	RootSpanID SpanID         `json:"root_span_id"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    *time.Time     `json:"end_time,omitempty"`
	Spans      []*Span        `json:"spans"`
	Attributes map[string]any `json:"attributes"`
	Links      []TraceLink    `json:"links,omitempty"`
}

// Clone returns a deep copy of the Trace.
func (t *Trace) Clone() *Trace {
	if t == nil {
		return nil
	}
	trace := &Trace{
		ID:         t.ID,
		RootSpanID: t.RootSpanID,
		StartTime:  t.StartTime,
		EndTime:    cloneTimePtr(t.EndTime),
		Attributes: cloneAttributes(t.Attributes),
	}
	if len(t.Spans) > 0 {
		trace.Spans = make([]*Span, len(t.Spans))
		for i, span := range t.Spans {
			trace.Spans[i] = span.Clone()
		}
	}
	if len(t.Links) > 0 {
		trace.Links = make([]TraceLink, len(t.Links))
		for i, link := range t.Links {
			trace.Links[i] = link.Clone()
		}
	}
	return trace
}

// Span represents a single operation in a distributed trace.
type Span struct {
	ID           SpanID         `json:"span_id"`
	ParentSpanID *SpanID        `json:"parent_span_id,omitempty"`
	TraceID      TraceID        `json:"trace_id"`
	Name         string         `json:"name"`
	Kind         SpanKind       `json:"kind"`
	StartTime    time.Time      `json:"start_time"`
	EndTime      *time.Time     `json:"end_time,omitempty"`
	Duration     time.Duration  `json:"duration,omitempty"`
	Status       SpanStatus     `json:"status"`
	Attributes   map[string]any `json:"attributes"`
	Events       []SpanEvent    `json:"events,omitempty"`
	Links        []SpanLink     `json:"links,omitempty"`
}

// Clone returns a deep copy of the Span.
func (s *Span) Clone() *Span {
	if s == nil {
		return nil
	}
	span := &Span{
		ID:           s.ID,
		ParentSpanID: cloneSpanIDPtr(s.ParentSpanID),
		TraceID:      s.TraceID,
		Name:         s.Name,
		Kind:         s.Kind,
		StartTime:    s.StartTime,
		EndTime:      cloneTimePtr(s.EndTime),
		Duration:     s.Duration,
		Status:       s.Status,
		Attributes:   cloneAttributes(s.Attributes),
	}
	if len(s.Events) > 0 {
		span.Events = make([]SpanEvent, len(s.Events))
		for i, event := range s.Events {
			span.Events[i] = event.Clone()
		}
	}
	if len(s.Links) > 0 {
		span.Links = make([]SpanLink, len(s.Links))
		for i, link := range s.Links {
			span.Links[i] = link.Clone()
		}
	}
	return span
}

// SpanKind represents the kind of span.
type SpanKind string

const (
	SpanKindInternal SpanKind = "internal"
	SpanKindServer   SpanKind = "server"
	SpanKindClient   SpanKind = "client"
	SpanKindProducer SpanKind = "producer"
	SpanKindConsumer SpanKind = "consumer"
)

// SpanStatus represents the status of a span.
type SpanStatus struct {
	Code        StatusCode `json:"code"`
	Description string     `json:"description,omitempty"`
}

// StatusCode represents the outcome of a span.
type StatusCode int32

const (
	StatusCodeUnset StatusCode = 0
	StatusCodeOk    StatusCode = 1
	StatusCodeError StatusCode = 2
)

// SpanEvent represents an event that occurred during a span.
type SpanEvent struct {
	Name       string         `json:"name"`
	Timestamp  time.Time      `json:"timestamp"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Clone returns a deep copy of the SpanEvent.
func (e SpanEvent) Clone() SpanEvent {
	return SpanEvent{
		Name:       e.Name,
		Timestamp:  e.Timestamp,
		Attributes: cloneAttributes(e.Attributes),
	}
}

// SpanLink represents a link to another span.
type SpanLink struct {
	TraceID    TraceID        `json:"trace_id"`
	SpanID     SpanID         `json:"span_id"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Clone returns a deep copy of the SpanLink.
func (l SpanLink) Clone() SpanLink {
	return SpanLink{
		TraceID:    l.TraceID,
		SpanID:     l.SpanID,
		Attributes: cloneAttributes(l.Attributes),
	}
}

// TraceLink represents a link between traces.
type TraceLink struct {
	TraceID    TraceID        `json:"trace_id"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Clone returns a deep copy of the TraceLink.
func (l TraceLink) Clone() TraceLink {
	return TraceLink{
		TraceID:    l.TraceID,
		Attributes: cloneAttributes(l.Attributes),
	}
}

// IDGenerator generates unique IDs for traces and spans.
type IDGenerator interface {
	GenerateTraceID() TraceID
	GenerateSpanID() SpanID
}

// RandomIDGenerator generates random IDs using crypto/rand.
type RandomIDGenerator struct{}

// NewRandomIDGenerator creates a new random ID generator.
func NewRandomIDGenerator() *RandomIDGenerator {
	return &RandomIDGenerator{}
}

// GenerateTraceID generates a new trace ID.
func (g *RandomIDGenerator) GenerateTraceID() TraceID {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return TraceID(hex.EncodeToString(id[:]))
}

// GenerateSpanID generates a new span ID.
func (g *RandomIDGenerator) GenerateSpanID() SpanID {
	var id [8]byte
	_, _ = rand.Read(id[:])
	return SpanID(hex.EncodeToString(id[:]))
}

// TraceCollector collects and stores traces.
type TraceCollector interface {
	Collect(trace *Trace)
	GetTrace(traceID TraceID) (*Trace, bool)
	GetTraces(filter TraceFilter) []*Trace
}

// TraceFilter is a predicate for filtering traces.
type TraceFilter func(trace *Trace) bool

// SimpleTraceCollector is an in-memory trace collector with optional age-based pruning.
type SimpleTraceCollector struct {
	mu            sync.RWMutex
	traces        map[TraceID]*Trace
	maxAge        time.Duration
	lastPruneTime time.Time
}

// NewSimpleTraceCollector creates a new simple trace collector.
func NewSimpleTraceCollector() *SimpleTraceCollector {
	return &SimpleTraceCollector{
		traces: make(map[TraceID]*Trace),
	}
}

// SetMaxAge configures how long completed traces are retained.
func (c *SimpleTraceCollector) SetMaxAge(maxAge time.Duration) {
	c.mu.Lock()
	c.maxAge = maxAge
	c.lastPruneTime = time.Time{}
	c.pruneLocked(time.Now())
	c.mu.Unlock()
}

// Collect stores a trace.
func (c *SimpleTraceCollector) Collect(trace *Trace) {
	if trace == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxAge > 0 {
		now := time.Now()
		cutoff := now.Add(-c.maxAge)
		ts := trace.StartTime
		if trace.EndTime != nil {
			ts = *trace.EndTime
		}
		if ts.Before(cutoff) {
			return
		}
	}
	c.traces[trace.ID] = trace.Clone()
	c.pruneLocked(time.Now())
}

// GetTrace retrieves a trace by ID.
func (c *SimpleTraceCollector) GetTrace(traceID TraceID) (*Trace, bool) {
	c.mu.Lock()
	c.pruneLocked(time.Now())
	trace, exists := c.traces[traceID]
	c.mu.Unlock()
	if !exists {
		return nil, false
	}
	return trace.Clone(), true
}

// GetTraces retrieves traces matching the filter.
func (c *SimpleTraceCollector) GetTraces(filter TraceFilter) []*Trace {
	c.mu.Lock()
	c.pruneLocked(time.Now())
	traces := make([]*Trace, 0, len(c.traces))
	for _, trace := range c.traces {
		traces = append(traces, trace.Clone())
	}
	c.mu.Unlock()
	if filter == nil {
		return traces
	}
	var result []*Trace
	for _, trace := range traces {
		if filter(trace) {
			result = append(result, trace)
		}
	}
	return result
}

func (c *SimpleTraceCollector) pruneLocked(now time.Time) {
	if c.maxAge <= 0 {
		return
	}
	pruneInterval := c.maxAge / 10
	if pruneInterval < 10*time.Second {
		pruneInterval = 10 * time.Second
	}
	if pruneInterval > time.Minute {
		pruneInterval = time.Minute
	}
	if now.Sub(c.lastPruneTime) < pruneInterval {
		return
	}
	c.lastPruneTime = now
	cutoff := now.Add(-c.maxAge)
	for id, trace := range c.traces {
		if trace == nil {
			delete(c.traces, id)
			continue
		}
		ts := trace.StartTime
		if trace.EndTime != nil {
			ts = *trace.EndTime
		}
		if ts.Before(cutoff) {
			delete(c.traces, id)
		}
	}
}

// Sampler determines whether a trace should be sampled.
type Sampler interface {
	ShouldSample(traceID TraceID) bool
}

// ProbabilitySampler samples traces based on a probability between 0 and 1.
type ProbabilitySampler struct {
	probability float64
}

// NewProbabilitySampler creates a probability sampler and returns an error for
// out-of-range sampling rates.
func NewProbabilitySampler(probability float64) (*ProbabilitySampler, error) {
	if probability < 0 || probability > 1 {
		return nil, ErrInvalidSamplingRate
	}
	return &ProbabilitySampler{probability: probability}, nil
}

// ShouldSample returns true if the trace should be sampled.
func (s *ProbabilitySampler) ShouldSample(traceID TraceID) bool {
	if len(traceID) == 0 {
		return false
	}
	var hash uint64
	for _, b := range traceID {
		hash = hash*31 + uint64(b)
	}
	return float64(hash%100) < s.probability*100.0
}

// TracerConfig holds configuration for the Tracer.
type TracerConfig struct {
	ServiceName      string
	ServiceVersion   string
	Environment      string
	SamplingRate     float64
	MaxSpansPerTrace int
	MaxTraceAge      time.Duration
}

// DefaultTracerConfig returns default tracer configuration.
func DefaultTracerConfig() TracerConfig {
	return TracerConfig{
		ServiceName:      "plumego",
		ServiceVersion:   "1.0.0",
		Environment:      "production",
		SamplingRate:     0.1,
		MaxSpansPerTrace: 1000,
		MaxTraceAge:      24 * time.Hour,
	}
}

// Tracer manages trace creation and span lifecycle.
// TraceContext storage lives in contract as a string transport carrier.
type Tracer struct {
	generator   IDGenerator
	collector   TraceCollector
	sampler     Sampler
	config      TracerConfig
	mu          sync.RWMutex
	activeSpans map[SpanID]*Span
}

// NewTracer creates a tracer and returns validation errors for invalid config.
func NewTracer(config TracerConfig) (*Tracer, error) {
	sampler, err := NewProbabilitySampler(config.SamplingRate)
	if err != nil {
		return nil, err
	}
	if config.MaxSpansPerTrace < 0 {
		return nil, ErrNegativeMaxSpansPerTrace
	}
	if config.MaxTraceAge < 0 {
		return nil, ErrNegativeMaxTraceAge
	}
	return newTracer(config, sampler), nil
}

func newTracer(config TracerConfig, sampler Sampler) *Tracer {
	t := &Tracer{
		generator:   NewRandomIDGenerator(),
		collector:   NewSimpleTraceCollector(),
		sampler:     sampler,
		config:      config,
		activeSpans: make(map[SpanID]*Span),
	}
	if c, ok := t.collector.(*SimpleTraceCollector); ok {
		c.SetMaxAge(config.MaxTraceAge)
	}
	return t
}

// StartTrace starts a new trace and stores a TraceContext in the returned context.
func (t *Tracer) StartTrace(ctx context.Context, name string, options ...TraceOption) (context.Context, *Span) {
	traceID := t.generator.GenerateTraceID()
	spanID := t.generator.GenerateSpanID()

	trace := &Trace{
		ID:         traceID,
		RootSpanID: spanID,
		StartTime:  time.Now(),
		Spans:      make([]*Span, 0),
		Attributes: make(map[string]any),
	}

	span := &Span{
		ID:         spanID,
		TraceID:    traceID,
		Name:       name,
		Kind:       SpanKindServer,
		StartTime:  time.Now(),
		Status:     SpanStatus{Code: StatusCodeUnset},
		Attributes: make(map[string]any),
	}

	for _, option := range options {
		option(trace, span)
	}

	spanCtx := contract.TraceContext{
		TraceID: string(traceID),
		SpanID:  string(spanID),
		Baggage: make(map[string]string),
	}
	if t.sampler.ShouldSample(traceID) {
		spanCtx.Flags |= traceFlagsSampled
		if t.canAddSpan(trace) {
			trace.Spans = append(trace.Spans, span)
			t.collector.Collect(trace)
		}
	}

	ctx = contract.WithTraceContext(ctx, spanCtx)

	t.mu.Lock()
	t.activeSpans[spanID] = span
	t.mu.Unlock()

	return ctx, span
}

// StartChildSpan starts a child span. If parentSpan is nil, starts a new trace.
func (t *Tracer) StartChildSpan(ctx context.Context, parentSpan *Span, name string, options ...TraceOption) (context.Context, *Span) {
	if parentSpan == nil {
		return t.StartTrace(ctx, name, options...)
	}

	spanID := t.generator.GenerateSpanID()

	span := &Span{
		ID:           spanID,
		ParentSpanID: &parentSpan.ID,
		TraceID:      parentSpan.TraceID,
		Name:         name,
		Kind:         SpanKindInternal,
		StartTime:    time.Now(),
		Status:       SpanStatus{Code: StatusCodeUnset},
		Attributes:   make(map[string]any),
	}

	for _, option := range options {
		option(nil, span)
	}

	parentSpanID := string(parentSpan.ID)
	spanCtx := contract.TraceContext{
		TraceID:      string(parentSpan.TraceID),
		SpanID:       string(spanID),
		ParentSpanID: &parentSpanID,
		Baggage:      make(map[string]string),
	}
	if t.sampler.ShouldSample(parentSpan.TraceID) {
		spanCtx.Flags |= traceFlagsSampled
	}

	ctx = contract.WithTraceContext(ctx, spanCtx)

	t.mu.Lock()
	t.activeSpans[spanID] = span
	t.mu.Unlock()

	return ctx, span
}

// EndSpan ends the given span and updates the collected trace.
func (t *Tracer) EndSpan(span *Span, options ...SpanOption) {
	if span == nil {
		return
	}
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	span.EndTime = &now
	span.Duration = span.EndTime.Sub(span.StartTime)

	for _, option := range options {
		option(span)
	}

	delete(t.activeSpans, span.ID)

	if trace, exists := t.collector.GetTrace(span.TraceID); exists {
		trace = mergeSpanIntoTrace(trace, span, t.config.MaxSpansPerTrace)
		if span.ID == trace.RootSpanID {
			endTime := now
			trace.EndTime = &endTime
		}
		t.collector.Collect(trace)
	}
}

// GetActiveSpan returns the active span from the context.
func (t *Tracer) GetActiveSpan(ctx context.Context) *Span {
	spanCtx, ok := contract.TraceContextFromContext(ctx)
	if !ok || !spanCtx.HasSpanID() {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.activeSpans[SpanID(spanCtx.SpanID)]
}

// RecordSpanEvent records a named event on the given span.
func (t *Tracer) RecordSpanEvent(span *Span, name string, options ...EventOption) {
	if span == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	event := SpanEvent{
		Name:       name,
		Timestamp:  time.Now(),
		Attributes: make(map[string]any),
	}
	for _, option := range options {
		option(&event)
	}
	span.Events = append(span.Events, event)
	t.updateCollectedSpan(span)
}

// RecordError records an error on the given span.
func (t *Tracer) RecordError(span *Span, err error, options ...ErrorOption) {
	if span == nil || err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	span.Status.Code = StatusCodeError
	span.Status.Description = err.Error()

	errorAttrs := map[string]any{
		"error.type":    fmt.Sprintf("%T", err),
		"error.message": err.Error(),
		"error.stack":   fmt.Sprintf("%+v", err),
	}
	for _, option := range options {
		option(errorAttrs)
	}
	for k, v := range errorAttrs {
		span.Attributes[k] = v
	}
	t.updateCollectedSpan(span)
}

// TraceStatistics provides aggregate statistics about collected traces.
type TraceStatistics struct {
	TotalTraces     int64                `json:"total_traces"`
	ActiveTraces    int64                `json:"active_traces"`
	CompletedTraces int64                `json:"completed_traces"`
	ByStatus        map[StatusCode]int64 `json:"by_status"`
	AverageDuration time.Duration        `json:"average_duration"`
	TotalSpans      int64                `json:"total_spans"`
	LastTraceTime   time.Time            `json:"last_trace_time"`
}

// GetTraceStatistics returns statistics about collected traces.
func (t *Tracer) GetTraceStatistics() TraceStatistics {
	stats := TraceStatistics{ByStatus: make(map[StatusCode]int64)}
	traces := t.collector.GetTraces(func(*Trace) bool { return true })

	var totalDuration time.Duration
	var completedCount int64

	for _, trace := range traces {
		stats.TotalTraces++
		stats.TotalSpans += int64(len(trace.Spans))

		if trace.EndTime != nil {
			completedCount++
			totalDuration += trace.EndTime.Sub(trace.StartTime)
			stats.CompletedTraces++
			if trace.StartTime.After(stats.LastTraceTime) {
				stats.LastTraceTime = trace.StartTime
			}
		} else {
			stats.ActiveTraces++
		}

		for _, span := range trace.Spans {
			stats.ByStatus[span.Status.Code]++
		}
	}

	if completedCount > 0 {
		stats.AverageDuration = totalDuration / time.Duration(completedCount)
	}
	return stats
}

// TraceOption is a function that configures a trace and its root span.
type TraceOption func(trace *Trace, span *Span)

// WithTraceAttributes sets attributes on the trace.
func WithTraceAttributes(attrs map[string]any) TraceOption {
	return func(trace *Trace, span *Span) {
		if trace == nil {
			return
		}
		for k, v := range attrs {
			trace.Attributes[k] = v
		}
	}
}

// WithSpanKind sets the span kind.
func WithSpanKind(kind SpanKind) TraceOption {
	return func(trace *Trace, span *Span) {
		span.Kind = kind
	}
}

// SpanOption is a function that configures a span.
type SpanOption func(span *Span)

// WithSpanAttributes sets attributes on the span.
func WithSpanAttributes(attrs map[string]any) SpanOption {
	return func(span *Span) {
		for k, v := range attrs {
			span.Attributes[k] = v
		}
	}
}

// WithSpanStatus sets the span status.
func WithSpanStatus(status SpanStatus) SpanOption {
	return func(span *Span) {
		span.Status = status
	}
}

// EventOption is a function that configures a span event.
type EventOption func(event *SpanEvent)

// WithEventAttributes sets attributes on the event.
func WithEventAttributes(attrs map[string]any) EventOption {
	return func(event *SpanEvent) {
		for k, v := range attrs {
			event.Attributes[k] = v
		}
	}
}

// ErrorOption is a function that adds attributes to an error recording.
type ErrorOption func(attrs map[string]any)

// WithErrorAttributes adds attributes to an error recording.
func WithErrorAttributes(attrs map[string]any) ErrorOption {
	return func(errorAttrs map[string]any) {
		for k, v := range attrs {
			errorAttrs[k] = v
		}
	}
}

// --- internal helpers ---

func (t *Tracer) updateCollectedSpan(span *Span) {
	if span == nil {
		return
	}
	trace, exists := t.collector.GetTrace(span.TraceID)
	if !exists {
		return
	}
	trace = mergeSpanIntoTrace(trace, span, t.config.MaxSpansPerTrace)
	t.collector.Collect(trace)
}

func (t *Tracer) canAddSpan(trace *Trace) bool {
	if trace == nil {
		return false
	}
	if t.config.MaxSpansPerTrace <= 0 {
		return true
	}
	return len(trace.Spans) < t.config.MaxSpansPerTrace
}

func mergeSpanIntoTrace(trace *Trace, span *Span, maxSpans int) *Trace {
	if trace == nil || span == nil {
		return trace
	}
	spanIndex := -1
	for i, s := range trace.Spans {
		if s.ID == span.ID {
			spanIndex = i
			break
		}
	}
	if spanIndex >= 0 {
		newTrace := &Trace{
			ID:         trace.ID,
			RootSpanID: trace.RootSpanID,
			StartTime:  trace.StartTime,
			EndTime:    trace.EndTime,
			Attributes: trace.Attributes,
			Links:      trace.Links,
			Spans:      make([]*Span, len(trace.Spans)),
		}
		copy(newTrace.Spans, trace.Spans)
		newTrace.Spans[spanIndex] = span.Clone()
		return newTrace
	}
	if maxSpans > 0 && len(trace.Spans) >= maxSpans {
		return trace
	}
	newTrace := &Trace{
		ID:         trace.ID,
		RootSpanID: trace.RootSpanID,
		StartTime:  trace.StartTime,
		EndTime:    trace.EndTime,
		Attributes: trace.Attributes,
		Links:      trace.Links,
		Spans:      make([]*Span, len(trace.Spans)+1),
	}
	copy(newTrace.Spans, trace.Spans)
	newTrace.Spans[len(trace.Spans)] = span.Clone()
	return newTrace
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	val := *value
	return &val
}

func cloneSpanIDPtr(value *SpanID) *SpanID {
	if value == nil {
		return nil
	}
	val := *value
	return &val
}

func cloneAttributes(attrs map[string]any) map[string]any {
	if attrs == nil {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}
