package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestRegisterServeGracefulStop(t *testing.T) {
	runtime := &fakeRuntime{serveDone: make(chan struct{})}
	srv := New(runtime)

	if err := srv.RegisterService("desc", "impl"); err != nil {
		t.Fatalf("RegisterService() error = %v", err)
	}
	if runtime.desc != "desc" || runtime.impl != "impl" {
		t.Fatalf("registered service = %#v/%#v", runtime.desc, runtime.impl)
	}

	errs := make(chan error, 1)
	go func() {
		errs <- srv.Serve(nil)
	}()

	stopCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := srv.GracefulStop(stopCtx); err != nil {
		t.Fatalf("GracefulStop() error = %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if err := srv.GracefulStop(stopCtx); err != nil {
		t.Fatalf("repeated GracefulStop() error = %v", err)
	}
	if !runtime.graceful {
		t.Fatal("expected runtime.GracefulStop to be called")
	}
}

func TestGracefulStopCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := New(&fakeRuntime{}).GracefulStop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("GracefulStop() error = %v, want %v", err, context.Canceled)
	}
}

func TestNilRuntimeReturnsError(t *testing.T) {
	if err := New(nil).Serve(nil); !errors.Is(err, ErrRuntimeNil) {
		t.Fatalf("Serve() error = %v, want %v", err, ErrRuntimeNil)
	}
}

type fakeRuntime struct {
	desc      any
	impl      any
	serveDone chan struct{}
	graceful  bool
	stopped   bool
}

func (r *fakeRuntime) RegisterService(desc any, impl any) error {
	r.desc = desc
	r.impl = impl
	return nil
}

func (r *fakeRuntime) Serve(net.Listener) error {
	if r.serveDone == nil {
		return nil
	}
	<-r.serveDone
	return nil
}

func (r *fakeRuntime) GracefulStop() {
	r.graceful = true
	if r.serveDone != nil {
		close(r.serveDone)
	}
}

func (r *fakeRuntime) Stop() {
	r.stopped = true
	if r.serveDone != nil {
		close(r.serveDone)
	}
}

type blockedShutdownRuntime struct {
	fakeRuntime
	gracefulStarted chan struct{}
	forcedStarted   chan struct{}
	release         chan struct{}
}

func (r *blockedShutdownRuntime) GracefulStop() {
	close(r.gracefulStarted)
	<-r.release
}

func (r *blockedShutdownRuntime) Stop() {
	close(r.forcedStarted)
	<-r.release
}

func TestShutdownWaitersOwnTheirDeadline(t *testing.T) {
	runtime := &blockedShutdownRuntime{
		gracefulStarted: make(chan struct{}),
		forcedStarted:   make(chan struct{}),
		release:         make(chan struct{}),
	}
	defer close(runtime.release)
	srv := New(runtime)
	results := make(chan error, 2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for range 2 {
		go func() { results <- srv.GracefulStop(ctx) }()
	}
	<-runtime.gracefulStarted
	cancel()
	for range 2 {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("GracefulStop = %v, want canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("GracefulStop waited for the runtime callback")
		}
	}
	select {
	case <-runtime.forcedStarted:
	case <-time.After(time.Second):
		t.Fatal("Stop was not called")
	}
}
