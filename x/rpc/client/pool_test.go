package client

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	plumelog "github.com/birdordev/plumego/log"
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
	l.storeMethod(fields...)
}

func (l *recordingLogger) InfoCtx(_ context.Context, _ string, fields ...plumelog.Fields) {
	l.storeMethod(fields...)
}

func (l *recordingLogger) storeMethod(fields ...plumelog.Fields) {
	for _, fieldSet := range fields {
		if method, ok := fieldSet["method"].(string); ok {
			l.method.Store(method)
			return
		}
	}
}
