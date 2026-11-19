package client

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	plumelog "github.com/birdordev/plumego/log"
	obstracer "github.com/birdordev/plumego/x/observability/tracer"
)

func TestPoolDialCachesConnection(t *testing.T) {
	var calls atomic.Int64
	pool := New(func(context.Context, string, ...DialOption) (Conn, error) {
		calls.Add(1)
		return &fakeConn{}, nil
	})
	defer pool.Close()

	first, err := pool.Dial(t.Context(), "rpc://service")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	second, err := pool.Dial(t.Context(), "rpc://service")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if first != second {
		t.Fatal("Dial() returned different connections for the same target")
	}
	if calls.Load() != 1 {
		t.Fatalf("dial calls = %d, want 1", calls.Load())
	}
}

func TestPoolDialAfterCloseReturnsError(t *testing.T) {
	pool := New(func(context.Context, string, ...DialOption) (Conn, error) {
		return &fakeConn{}, nil
	})
	if err := pool.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := pool.Dial(t.Context(), "rpc://closed"); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("Dial() error = %v, want %v", err, ErrPoolClosed)
	}
}

func TestPoolDialWithoutDialerReturnsError(t *testing.T) {
	if _, err := New(nil).Dial(t.Context(), "rpc://missing"); !errors.Is(err, ErrNoDialer) {
		t.Fatalf("Dial() error = %v, want %v", err, ErrNoDialer)
	}
}

func TestLoggingInterceptorLogsMethod(t *testing.T) {
	logger := &recordingLogger{}
	interceptor := LoggingInterceptor(logger)
	err := interceptor(t.Context(), "/plumego.rpc.Test/Ping", nil, nil, func(context.Context, string, any, any, ...CallOption) error {
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error = %v", err)
	}
	if logger.method.Load() != "/plumego.rpc.Test/Ping" {
		t.Fatalf("logged method = %q", logger.method.Load())
	}
}

func TestRetryInterceptorRetriesRetryableCode(t *testing.T) {
	var calls atomic.Int64
	interceptor := RetryInterceptor(3, "Unavailable")
	err := interceptor(t.Context(), "/plumego.rpc.Test/Ping", nil, nil, func(context.Context, string, any, any, ...CallOption) error {
		if calls.Add(1) < 3 {
			return codedError{code: "Unavailable"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error = %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestRetryInterceptorStopsOnCancellation(t *testing.T) {
	retryable := codedError{code: "Unavailable"}
	terminal := errors.New("terminal error")
	for _, tc := range []struct {
		name          string
		cancelBefore  bool
		invocationErr error
		wantErr       error
		wantCalls     int
	}{
		{"before invocation", true, retryable, context.Canceled, 0},
		{"before retry", false, retryable, context.Canceled, 1},
		{"terminal result", false, terminal, terminal, 1},
		{"successful result", false, nil, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			calls := 0
			invoke := RetryInterceptor(3, "Unavailable")
			err := invoke(ctx, "/test/Ping", nil, nil, func(context.Context, string, any, any, ...CallOption) error {
				calls++
				cancel()
				return tc.invocationErr
			})
			if calls != tc.wantCalls || !errors.Is(err, tc.wantErr) {
				t.Errorf("calls = %d, err = %v, want %d and %v", calls, err, tc.wantCalls, tc.wantErr)
			}
		})
	}
}

type fakeConn struct {
	closed atomic.Bool
}

func (c *fakeConn) Close() error {
	c.closed.Store(true)
	return nil
}

type codedError struct {
	code Code
}

func (e codedError) Error() string {
	return string(e.code)
}

func (e codedError) RPCCode() Code {
	return e.code
}

type recordingLogger struct {
	method atomic.Value
	status atomic.Value
}

func (l *recordingLogger) WithFields(plumelog.Fields) plumelog.StructuredLogger { return l }
func (l *recordingLogger) Debug(string, ...plumelog.Fields)                     {}
func (l *recordingLogger) Warn(string, ...plumelog.Fields)                      {}
func (l *recordingLogger) Error(string, ...plumelog.Fields)                     {}
func (l *recordingLogger) Fatal(string, ...plumelog.Fields)                     {}
func (l *recordingLogger) DebugCtx(context.Context, string, ...plumelog.Fields) {}
func (l *recordingLogger) WarnCtx(context.Context, string, ...plumelog.Fields)  {}
func (l *recordingLogger) ErrorCtx(context.Context, string, ...plumelog.Fields) {}
func (l *recordingLogger) FatalCtx(context.Context, string, ...plumelog.Fields) {}

func (l *recordingLogger) Info(_ string, fields ...plumelog.Fields) {
	l.storeFields(fields...)
}

func (l *recordingLogger) InfoCtx(_ context.Context, _ string, fields ...plumelog.Fields) {
	l.storeFields(fields...)
}

func (l *recordingLogger) storeFields(fields ...plumelog.Fields) {
	for _, fieldSet := range fields {
		if method, ok := fieldSet["method"].(string); ok {
			l.method.Store(method)
		}
		if status, ok := fieldSet["status"].(string); ok {
			l.status.Store(status)
		}
	}
}

type blockingPoolConn struct {
	closing chan struct{}
	release chan struct{}
}

func (c *blockingPoolConn) Close() error {
	close(c.closing)
	<-c.release
	return nil
}

func TestPoolClosesDiscardedDialsOutsideLock(t *testing.T) {
	for _, closePool := range []bool{false, true} {
		name := "existing winner"
		if closePool {
			name = "pool closed"
		}
		t.Run(name, func(t *testing.T) {
			discarded := &blockingPoolConn{closing: make(chan struct{}), release: make(chan struct{})}
			winner := &fakeConn{}
			dialStarted := make(chan struct{})
			allowFirstDial := make(chan struct{})
			var count atomic.Int32
			p := New(func(context.Context, string, ...DialOption) (Conn, error) {
				if count.Add(1) == 1 {
					close(dialStarted)
					<-allowFirstDial
					return discarded, nil
				}
				return winner, nil
			})
			type result struct {
				conn Conn
				err  error
			}
			first := make(chan result, 1)
			go func() { c, e := p.Dial(t.Context(), "same"); first <- result{c, e} }()
			<-dialStarted
			if closePool {
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			} else if c, err := p.Dial(t.Context(), "same"); err != nil || c != winner {
				t.Fatalf("winner = %v, %v", c, err)
			}
			close(allowFirstDial)
			defer close(discarded.release)
			<-discarded.closing
			probe := make(chan result, 1)
			go func() { c, e := p.Dial(t.Context(), "other"); probe <- result{c, e} }()
			select {
			case r := <-probe:
				if closePool && !errors.Is(r.err, ErrPoolClosed) || !closePool && r.err != nil {
					t.Fatalf("pool probe = %v, %v", r.conn, r.err)
				}
			case <-time.After(time.Second):
				t.Fatal("discarded connection Close held pool lock")
			}
			discarded.release <- struct{}{}
			r := <-first
			if closePool && (r.conn != nil || !errors.Is(r.err, ErrPoolClosed)) || !closePool && (r.conn != winner || r.err != nil) {
				t.Fatalf("discarded dial result = %v, %v", r.conn, r.err)
			}
		})
	}
}

func TestPoolDoesNotCacheNilDial(t *testing.T) {
	calls := 0
	conn := &fakeConn{}
	p := New(func(context.Context, string, ...DialOption) (Conn, error) {
		calls++
		if calls == 1 {
			return nil, nil
		}
		return conn, nil
	})
	if _, err := p.Dial(t.Context(), "target"); err == nil {
		t.Fatal("nil connection was accepted")
	}
	if got, err := p.Dial(t.Context(), "target"); err != nil || got != conn {
		t.Fatalf("second dial = %v, %v", got, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

type recordingTraceStarter struct {
	status obstracer.SpanStatus
}

func (*recordingTraceStarter) StartTrace(ctx context.Context, _ string, _ ...obstracer.TraceOption) (context.Context, *obstracer.Span) {
	return ctx, &obstracer.Span{}
}

func (t *recordingTraceStarter) EndSpan(span *obstracer.Span, options ...obstracer.SpanOption) {
	for _, option := range options {
		option(span)
	}
	t.status = span.Status
}

func (*recordingTraceStarter) RecordError(*obstracer.Span, error, ...obstracer.ErrorOption) {}

func TestInterceptorsClassifyWrappedRPCStatus(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  string
		calls int
	}{
		{"wrapped retryable", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", codedError{code: "Unavailable"})), "Unavailable", 2},
		{"wrapped permanent", fmt.Errorf("operation: %w", codedError{code: "InvalidArgument"}), "InvalidArgument", 1},
		{"unclassified", errors.New("operation failed"), "Unknown", 1},
		{"success", nil, "OK", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := &recordingLogger{}
			tracer := &recordingTraceStarter{}
			calls := 0
			retry := RetryInterceptor(2, "Unavailable")
			logCall := LoggingInterceptor(logger)
			traceCall := TracingInterceptor(tracer)
			err := retry(t.Context(), "example", nil, nil, func(ctx context.Context, method string, req, reply any, opts ...CallOption) error {
				calls++
				return logCall(ctx, method, req, reply, func(ctx context.Context, method string, req, reply any, opts ...CallOption) error {
					return traceCall(ctx, method, req, reply, func(context.Context, string, any, any, ...CallOption) error {
						return tc.err
					}, opts...)
				}, opts...)
			})
			if err != tc.err || calls != tc.calls {
				t.Errorf("error=%v, calls=%d; want %v, %d", err, calls, tc.err, tc.calls)
			}
			if logger.status.Load() != tc.code || tracer.status.Description != tc.code {
				t.Errorf("logged status=%v trace status=%+v, want %s", logger.status.Load(), tracer.status, tc.code)
			}
		})
	}
}

type valuePoolConn struct{}

func (valuePoolConn) Close() error { return nil }

type slicePoolConn []int

func (slicePoolConn) Close() error { return nil }

func TestPoolDoesNotCacheTypedNilDial(t *testing.T) {
	for _, invalid := range []Conn{(*fakeConn)(nil), slicePoolConn(nil)} {
		calls := 0
		p := New(func(context.Context, string, ...DialOption) (Conn, error) {
			calls++
			if calls == 1 {
				return invalid, nil
			}
			return valuePoolConn{}, nil
		})
		defer p.Close()
		if conn, err := p.Dial(t.Context(), "fixture"); conn != nil || err == nil {
			t.Errorf("%T: first dial=%#v,%v", invalid, conn, err)
		}
		for range 2 {
			if conn, err := p.Dial(t.Context(), "fixture"); conn != (valuePoolConn{}) || err != nil {
				t.Errorf("%T: subsequent dial=%#v,%v", invalid, conn, err)
			}
		}
		if calls != 2 {
			t.Errorf("dialer calls=%d, want 2", calls)
		}
	}
}
