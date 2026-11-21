package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

func TestRetryInterceptorPreservesFinalErrorAndOptions(t *testing.T) {
	first := errors.New("first attempt")
	last := errors.New("last attempt")
	retryable := codedError{code: "Unavailable"}
	failures := []error{fmt.Errorf("first: %w", errors.Join(first, retryable)), fmt.Errorf("last: %w", errors.Join(last, retryable))}
	const method = "/fixture.Service/Read"
	request := new(string)
	reply := new(int)
	opts := []CallOption{"first option", 42}
	calls := 0
	ctx := t.Context()
	err := RetryInterceptor(len(failures), "Unavailable")(ctx, method, request, reply,
		func(gotCtx context.Context, gotMethod string, gotRequest, gotReply any, gotOpts ...CallOption) error {
			if gotCtx != ctx || gotMethod != method || gotRequest != request || gotReply != reply || !slices.Equal(gotOpts, opts) {
				t.Fatalf("attempt %d parameters = %q, %v, %v, %v", calls+1, gotMethod, gotRequest, gotReply, gotOpts)
			}
			calls++
			if calls > len(failures) {
				t.Fatal("attempt limit exceeded")
			}
			return failures[calls-1]
		}, opts...)
	if calls != len(failures) || err != failures[1] || !errors.Is(err, last) || errors.Is(err, first) {
		t.Fatalf("calls = %d, final error = %v", calls, err)
	}
}

func TestTracingInterceptorWithoutTracerPassesThrough(t *testing.T) {
	const method = "/fixture.Service/Read"
	ctx := t.Context()
	request, reply := new(string), new(int)
	opts := []CallOption{"first option", 42}
	sentinel := errors.New("test invocation failure")
	calls := 0
	err := TracingInterceptor(nil)(ctx, method, request, reply,
		func(gotCtx context.Context, gotMethod string, gotRequest, gotReply any, gotOpts ...CallOption) error {
			calls++
			if gotCtx != ctx || gotMethod != method || gotRequest != request || gotReply != reply || !slices.Equal(gotOpts, opts) {
				t.Errorf("invocation parameters = %q, %v, %v, %v", gotMethod, gotRequest, gotReply, gotOpts)
			}
			return sentinel
		}, opts...)
	if calls != 1 || !errors.Is(err, sentinel) {
		t.Errorf("invocation calls = %d, error = %v", calls, err)
	}
}

func TestTraceMetadataOmitsIncompleteCarriers(t *testing.T) {
	tid, sid := "1234567890abcdef1234567890abcdef", "1234567890abcdef"
	for _, tc := range []contract.TraceContext{{}, {TraceID: tid}, {SpanID: sid}} {
		if got := TraceMetadata(contract.WithTraceContext(t.Context(), tc)); got != nil {
			t.Errorf("invalid trace produced metadata: %v", got)
		}
	}
	if got := TraceMetadata(t.Context()); got != nil {
		t.Errorf("missing trace produced metadata: %v", got)
	}
	if got := TraceMetadata(contract.WithTraceContext(t.Context(), contract.TraceContext{TraceID: tid, SpanID: sid})); len(got) != 2 || got["x-trace-id"] != tid || got["x-span-id"] != sid {
		t.Errorf("valid trace metadata=%v", got)
	}
}

func TestRetryInterceptorNormalizesNonpositiveAttempts(t *testing.T) {
	sentinel := &codedError{code: "Unavailable"}
	for _, limit := range []int{0, -2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			retry := RetryInterceptor(limit, "Unavailable")
			calls := 0
			invoke := func(context.Context, string, any, any, ...CallOption) error {
				calls++
				return sentinel
			}
			if err := retry(t.Context(), "Read", nil, nil, invoke); err != sentinel || errorCode(err) != "Unavailable" || calls != 1 {
				t.Errorf("attempts = %d, error = %v", calls, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := retry(ctx, "Read", nil, nil, invoke); !errors.Is(err, context.Canceled) || calls != 1 {
				t.Errorf("pre-canceled call returned %v, total invocations %d", err, calls)
			}
		})
	}
}
