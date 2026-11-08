package client

import (
	"context"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	obstracer "github.com/birdordev/plumego/x/observability/tracer"
)

// CallOption is an opaque caller-owned option passed through interceptors.
type CallOption any

// UnaryInvoker performs a unary RPC call.
type UnaryInvoker func(ctx context.Context, method string, req any, reply any, opts ...CallOption) error

// UnaryInterceptor wraps a UnaryInvoker.
type UnaryInterceptor func(ctx context.Context, method string, req any, reply any, invoker UnaryInvoker, opts ...CallOption) error

// Code identifies a transport status in a dependency-free shape.
type Code string

// CodedError exposes a transport status code for retry and logging decisions.
type CodedError interface {
	RPCCode() Code
}

type TraceStarter interface {
	StartTrace(context.Context, string, ...obstracer.TraceOption) (context.Context, *obstracer.Span)
	EndSpan(*obstracer.Span, ...obstracer.SpanOption)
	RecordError(*obstracer.Span, error, ...obstracer.ErrorOption)
}

func LoggingInterceptor(logger plumelog.StructuredLogger) UnaryInterceptor {
	return func(ctx context.Context, method string, req any, reply any, invoker UnaryInvoker, opts ...CallOption) error {
		start := time.Now()
		err := invoker(ctx, method, req, reply, opts...)
		if logger != nil {
			code := errorCode(err)
			logger.InfoCtx(ctx, "rpc client call", plumelog.Fields{
				"method":      method,
				"duration_ms": time.Since(start).Milliseconds(),
				"status":      string(code),
			})
		}
		return err
	}
}

func RetryInterceptor(maxAttempts int, retryCodes ...Code) UnaryInterceptor {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	retryable := make(map[Code]struct{}, len(retryCodes))
	for _, code := range retryCodes {
		retryable[code] = struct{}{}
	}
	return func(ctx context.Context, method string, req any, reply any, invoker UnaryInvoker, opts ...CallOption) error {
		var err error
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			err = invoker(ctx, method, req, reply, opts...)
			if err == nil {
				return nil
			}
			if attempt == maxAttempts {
				return err
			}
			if _, ok := retryable[errorCode(err)]; !ok {
				return err
			}
		}
		return err
	}
}

func TracingInterceptor(tracer TraceStarter) UnaryInterceptor {
	return func(ctx context.Context, method string, req any, reply any, invoker UnaryInvoker, opts ...CallOption) error {
		var span *obstracer.Span
		if tracer != nil {
			ctx, span = tracer.StartTrace(ctx, method, obstracer.WithSpanKind(obstracer.SpanKindClient))
		}
		err := invoker(ctx, method, req, reply, opts...)
		if tracer != nil {
			if err != nil {
				tracer.RecordError(span, err)
			}
			tracer.EndSpan(span, obstracer.WithSpanStatus(obstracer.SpanStatus{
				Code:        spanStatusCode(err),
				Description: string(errorCode(err)),
			}))
		}
		return err
	}
}

// TraceMetadata returns outgoing trace IDs in a transport-neutral map.
func TraceMetadata(ctx context.Context) map[string]string {
	traceCtx, ok := contract.TraceContextFromContext(ctx)
	if !ok || !traceCtx.Valid() {
		return nil
	}
	return map[string]string{
		"x-trace-id": traceCtx.TraceID,
		"x-span-id":  traceCtx.SpanID,
	}
}

func errorCode(err error) Code {
	if err == nil {
		return "OK"
	}
	if coded, ok := err.(CodedError); ok {
		return coded.RPCCode()
	}
	return "Unknown"
}

func spanStatusCode(err error) obstracer.StatusCode {
	if err != nil {
		return obstracer.StatusCodeError
	}
	return obstracer.StatusCodeOk
}
