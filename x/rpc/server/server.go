// Package server wraps caller-owned RPC runtimes with explicit lifecycle
// helpers.
package server

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
)

var ErrRuntimeNil = errors.New("rpc server runtime is nil")

// Runtime is the dependency-free lifecycle surface required by Server.
// Stop must be safe to call concurrently with GracefulStop and must make the
// graceful shutdown return.
type Runtime interface {
	RegisterService(desc any, impl any) error
	Serve(net.Listener) error
	GracefulStop()
	Stop()
}

type Server struct {
	runtime  Runtime
	shutdown *shutdownState
}

type shutdownState struct {
	start sync.Once
	force sync.Once
	done  chan struct{}
}

// New wraps a runtime. Nil and typed nil runtimes return ErrRuntimeNil from Server operations.
func New(runtime Runtime) *Server {
	value := reflect.ValueOf(runtime)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			runtime = nil
		}
	}
	return &Server{
		runtime:  runtime,
		shutdown: &shutdownState{done: make(chan struct{})},
	}
}

func (s *Server) RegisterService(desc any, impl any) error {
	if s == nil || s.runtime == nil {
		return ErrRuntimeNil
	}
	return s.runtime.RegisterService(desc, impl)
}

func (s *Server) Serve(lis net.Listener) error {
	if s == nil || s.runtime == nil {
		return ErrRuntimeNil
	}
	return s.runtime.Serve(lis)
}

// GracefulStop starts shutdown once and waits for it to finish or for ctx to
// be canceled. Cancellation while waiting initiates one background Stop.
func (s *Server) GracefulStop(ctx context.Context) error {
	if s == nil || s.runtime == nil {
		return ErrRuntimeNil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	shutdown := s.shutdown
	shutdown.start.Do(func() {
		go func() {
			s.runtime.GracefulStop()
			close(shutdown.done)
		}()
	})

	select {
	case <-shutdown.done:
		return nil
	case <-ctx.Done():
		shutdown.force.Do(func() {
			select {
			case <-shutdown.done:
			default:
				go s.runtime.Stop()
			}
		})
		return ctx.Err()
	}
}
