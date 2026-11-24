package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/messaging/mq"
	"github.com/birdordev/plumego/x/observability/tracer"
)

type traceSource struct {
	requestID, traceID, spanID string
}

func (s traceSource) valid() bool {
	return len(s.requestID) == 26 && runCursorID.MatchString(s.requestID) &&
		(contract.TraceContext{TraceID: s.traceID, SpanID: s.spanID}).Valid()
}

func sourceFromRequest(ctx context.Context) traceSource {
	identity, ok := ctx.Value(auditRequestKey{}).(auditIdentity)
	if !ok {
		return traceSource{}
	}
	traceID, spanID := contract.TraceIDsFromContext(ctx)
	source := traceSource{identity.requestID, traceID, spanID}
	if !source.valid() {
		return traceSource{}
	}
	return source
}

func sourceFromTask(meta map[string]string, key string) traceSource {
	return traceSource{meta[key+"_request_id"], meta[key+"_trace_id"], meta[key+"_span_id"]}
}

func (s traceSource) attach(meta map[string]string, key string) {
	if s.valid() {
		meta[key+"_request_id"] = s.requestID
		meta[key+"_trace_id"] = s.traceID
		meta[key+"_span_id"] = s.spanID
	}
}

type schedulerTracing struct {
	tracer *tracer.Tracer
	finish func(*tracer.Span)
}

func newSchedulerTracing(writeLog bool) (*schedulerTracing, error) {
	cfg := tracer.DefaultTracerConfig()
	cfg.SamplingRate = 0
	tr, err := tracer.NewTracer(cfg)
	if err != nil {
		return nil, err
	}
	return &schedulerTracing{tracer: tr, finish: func(span *tracer.Span) {
		tr.EndSpan(span)
		if writeLog {
			if record, err := json.Marshal(span); err == nil {
				log.Printf("scheduler span %s", record)
			}
		}
	}}, nil
}

func (t *schedulerTracing) handler(next mq.TaskHandler) mq.TaskHandler {
	return func(ctx context.Context, task mq.Task) error {
		source := sourceFromTask(task.Meta, "origin")
		if source.valid() {
			ctx = contract.WithRequestID(ctx, source.requestID)
		}
		ctx, span := t.tracer.StartTrace(ctx, "scheduler.run", tracer.WithSpanKind(tracer.SpanKindConsumer), func(_ *tracer.Trace, span *tracer.Span) {
			span.Attributes["run_id"] = task.ID
			span.Attributes["claim_attempt"] = task.Attempts
			span.Attributes["execution_attempt"] = task.ExecutionAttempts
			linkSource := func(source traceSource, kind string) {
				if source.valid() {
					span.Links = append(span.Links, tracer.SpanLink{
						TraceID: tracer.TraceID(source.traceID), SpanID: tracer.SpanID(source.spanID),
						Attributes: map[string]any{"request_id": source.requestID, "kind": kind},
					})
				}
			}
			linkSource(source, "origin")
			linkSource(sourceFromTask(task.Meta, "replay"), "replay")
		})
		defer t.finish(span)
		return next(ctx, task)
	}
}
