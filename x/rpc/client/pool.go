// Package client provides transport-neutral RPC client pooling and unary
// interceptor helpers.
package client

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrPoolClosed = errors.New("rpc client pool is closed")
	ErrNoDialer   = errors.New("rpc client dialer is nil")
)

// Conn is the minimal connection lifecycle required by Pool.
type Conn interface {
	Close() error
}

// DialOption is an opaque caller-owned option passed through to Dialer.
type DialOption any

// Dialer opens an RPC connection for a target. A successful result must hold
// a usable connection. Typed nil values are invalid.
type Dialer func(context.Context, string, ...DialOption) (Conn, error)

type Pool struct {
	mu         sync.Mutex
	dial       Dialer
	defaultOps []DialOption
	conns      map[string]Conn
	closed     bool
}

func New(dial Dialer, defaultOpts ...DialOption) *Pool {
	return &Pool{
		dial:       dial,
		defaultOps: append([]DialOption(nil), defaultOpts...),
		conns:      make(map[string]Conn),
	}
}

func (p *Pool) Dial(ctx context.Context, target string, opts ...DialOption) (Conn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	if p.dial == nil {
		p.mu.Unlock()
		return nil, ErrNoDialer
	}
	if conn, ok := p.conns[target]; ok {
		p.mu.Unlock()
		return conn, nil
	}
	dial := p.dial
	dialOpts := append([]DialOption(nil), p.defaultOps...)
	dialOpts = append(dialOpts, opts...)
	p.mu.Unlock()

	conn, err := dial(ctx, target, dialOpts...)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, errors.New("rpc client dialer returned a nil connection")
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = conn.Close()
		return nil, ErrPoolClosed
	}
	if existing, ok := p.conns[target]; ok {
		p.mu.Unlock()
		_ = conn.Close()
		return existing, nil
	}
	p.conns[target] = conn
	p.mu.Unlock()
	return conn, nil
}

func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	conns := make([]Conn, 0, len(p.conns))
	for target, conn := range p.conns {
		conns = append(conns, conn)
		delete(p.conns, target)
	}
	p.mu.Unlock()

	var errs []error
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
