package client

import (
	"context"
	"errors"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/observability/tracer"
)

type invocationTrace struct {
	span     *tracer.Span
	started  int
	ended    int
	recorded error
}

func (tr *invocationTrace) StartTrace(ctx context.Context, _ string, _ ...tracer.TraceOption) (context.Context, *tracer.Span) {
	tr.started++
	tr.span = new(tracer.Span)
	return contract.WithTraceContext(ctx, contract.TraceContext{TraceID: "1234567890abcdef1234567890abcdef", SpanID: "1234567890abcdef"}), tr.span
}

func (tr *invocationTrace) EndSpan(span *tracer.Span, opts ...tracer.SpanOption) {
	tr.ended++
	for _, opt := range opts {
		opt(span)
	}
}

func (tr *invocationTrace) RecordError(_ *tracer.Span, err error, _ ...tracer.ErrorOption) {
	tr.recorded = err
}

func TestTracingInterceptorEndsSpanOnPanic(t *testing.T) {
	sentinel := errors.New("test call error")
	for _, tt := range []struct {
		name   string
		err    error
		panics bool
	}{
		{name: "success"},
		{name: "error", err: sentinel},
		{name: "panic", panics: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tr := new(invocationTrace)
			invoke := func() (recovered any, err error) {
				defer func() { recovered = recover() }()
				err = TracingInterceptor(tr)(t.Context(), "GetItem", nil, nil, func(ctx context.Context, _ string, _, _ any, _ ...CallOption) error {
					if meta := TraceMetadata(ctx); len(meta) != 2 {
						t.Errorf("trace metadata = %v", meta)
					}
					if tt.panics {
						panic(sentinel)
					}
					return tt.err
				})
				return nil, err
			}
			recovered, err := invoke()
			if tr.started != 1 || tr.ended != 1 || err != tt.err || tr.recorded != tt.err || (recovered == sentinel) != tt.panics {
				t.Fatalf("start=%d end=%d error=%v recorded=%v panic=%v", tr.started, tr.ended, err, tr.recorded, recovered)
			}
			wantStatus := tracer.StatusCodeOk
			if tt.err != nil || tt.panics {
				wantStatus = tracer.StatusCodeError
			}
			if tr.span.Status.Code != wantStatus {
				t.Errorf("span status = %+v, want %v", tr.span.Status, wantStatus)
			}
			if tt.panics && tr.span.Status.Description == sentinel.Error() {
				t.Error("panic payload included in span status")
			}
		})
	}
}
