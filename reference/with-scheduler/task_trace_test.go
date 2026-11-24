package main

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/messaging/mq"
	"github.com/birdordev/plumego/x/observability/tracer"
)

func TestExecutionTraceUsesWorkerDeadlineAndOnlyApprovedLinks(t *testing.T) {
	tracing, err := newSchedulerTracing(false)
	if err != nil {
		t.Fatal(err)
	}
	handlerCtx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	source := traceSource{rand.Text(), "11111111111111111111111111111111", "2222222222222222"}
	task := mq.Task{ID: "run", Meta: map[string]string{
		"authorization": "Bearer credential", "origin_baggage": "secret",
		"replay_request_id": "bad", "replay_trace_id": source.traceID, "replay_span_id": source.spanID,
	}}
	source.attach(task.Meta, "origin")
	originalFinish := tracing.finish
	tracing.finish = func(span *tracer.Span) {
		originalFinish(span)
		if len(span.Links) != 1 || string(span.Links[0].TraceID) != source.traceID ||
			string(span.Links[0].SpanID) != source.spanID || span.Links[0].Attributes["request_id"] != source.requestID {
			t.Errorf("unexpected links %+v", span.Links)
		}
	}
	probe := tracing.handler(func(ctx context.Context, _ mq.Task) error {
		got, ok := ctx.Deadline()
		want, _ := handlerCtx.Deadline()
		trace, _ := contract.TraceContextFromContext(ctx)
		if !ok || got != want || !trace.Valid() || trace.TraceID == source.traceID ||
			len(trace.Baggage) != 0 || contract.RequestIDFromContext(ctx) != source.requestID {
			t.Errorf("invalid worker context deadline=%v trace=%+v", got, trace)
		}
		cancel()
		return ctx.Err()
	})
	if err := probe(handlerCtx, task); !errors.Is(err, context.Canceled) {
		t.Fatalf("worker cancellation not propagated: %v", err)
	}
}
