package ipc

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// PoolConfig holds configuration for connection pool
type PoolConfig struct {
	MaxConns     int           // Maximum number of established connections and pending dials
	MaxIdleConns int           // Maximum number of idle connections to retain
	MaxIdleTime  time.Duration // Maximum time a connection can be idle before being closed
	DialTimeout  time.Duration // Timeout for creating new connections
}

// DefaultPoolConfig returns default pool configuration
func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		MaxConns:     10,
		MaxIdleConns: 5,
		MaxIdleTime:  5 * time.Minute,
		DialTimeout:  10 * time.Second,
	}
}

// poolConn wraps a connection with pool metadata
type poolConn struct {
	client    Client
	pool      *Pool
	createdAt time.Time
	lastUsed  time.Time
	inUse     bool
}

// Pool manages a pool of reusable IPC connections
type Pool struct {
	addr    string
	config  *Config
	poolCfg *PoolConfig

	mu           sync.Mutex
	conns        []*poolConn
	pendingDials int
	waiting      list.List // of chan struct{}, in arrival order
	closed       bool

	stopCh chan struct{}
}

// NewPool creates a connection pool. A negative MaxConns is invalid.
func NewPool(addr string, poolCfg *PoolConfig, opts ...Option) (*Pool, error) {
	if poolCfg == nil {
		poolCfg = DefaultPoolConfig()
	}
	if poolCfg.MaxConns < 0 {
		return nil, fmt.Errorf("ipc pool: MaxConns must not be negative: %d", poolCfg.MaxConns)
	}

	config := DefaultConfig()
	for _, opt := range opts {
		opt(config)
	}

	pool := &Pool{
		addr:    addr,
		config:  config,
		poolCfg: poolCfg,
		conns:   make([]*poolConn, 0, poolCfg.MaxConns),
		stopCh:  make(chan struct{}),
	}

	// Start background cleanup goroutine
	go pool.cleanup()

	return pool, nil
}

// Get acquires a connection from the pool
func (p *Pool) Get() (Client, error) {
	return p.GetWithContext(context.Background())
}

// GetWithContext acquires a connection until one is available or ctx is done.
// A closed pool returns ErrClientClosed. Expired idle connections are detached
// before being closed, and dials that finish after closure are discarded.
func (p *Pool) GetWithContext(ctx context.Context) (Client, error) {
	return p.getWithContext(ctx, dialPlatformWithContext)
}

func (p *Pool) getWithContext(ctx context.Context, dial func(context.Context, string, *Config) (Client, error)) (Client, error) {
	var waiter *list.Element
	var ready chan struct{}
	for {
		p.mu.Lock()
		if p.closed {
			p.removeWaiterLocked(waiter)
			p.mu.Unlock()
			return nil, ErrClientClosed
		}
		if err := ctx.Err(); err != nil {
			p.removeWaiterLocked(waiter)
			p.mu.Unlock()
			return nil, err
		}

		var expired []*poolConn
		// Only the oldest waiter may claim a connection or start a dial.
		if first := p.waiting.Front(); first == nil || first == waiter {
			var idle *poolConn
			idle, expired = p.takeIdleLocked(time.Now())
			if idle != nil {
				p.removeWaiterLocked(waiter)
				p.mu.Unlock()
				closePoolConnections(expired)
				return &poolClient{conn: idle}, nil
			}
			if len(p.conns)+p.pendingDials < p.poolCfg.MaxConns {
				p.pendingDials++
				p.removeWaiterLocked(waiter)
				p.mu.Unlock()
				closePoolConnections(expired)
				return p.dialReserved(ctx, dial)
			}
		}

		if waiter == nil {
			ready = make(chan struct{}, 1)
			waiter = p.waiting.PushBack(ready)
		}
		p.mu.Unlock()
		closePoolConnections(expired)

		select {
		case <-ready:
		case <-ctx.Done():
		case <-p.stopCh:
		}
	}
}

// takeIdleLocked selects the first reusable connection and detaches all expired idle connections.
func (p *Pool) takeIdleLocked(now time.Time) (idle *poolConn, expired []*poolConn) {
	kept := p.conns[:0]
	for _, pc := range p.conns {
		if !pc.inUse {
			if now.Sub(pc.lastUsed) > p.poolCfg.MaxIdleTime {
				expired = append(expired, pc)
				continue
			}
			if idle == nil {
				idle = pc
				idle.inUse = true
				idle.lastUsed = now
			}
		}
		kept = append(kept, pc)
	}
	clear(p.conns[len(kept):])
	p.conns = kept
	return idle, expired
}

func (p *Pool) dialReserved(ctx context.Context, dial func(context.Context, string, *Config) (Client, error)) (Client, error) {
	client, err := dial(ctx, p.addr, p.config)

	p.mu.Lock()
	p.pendingDials--
	if p.closed {
		err = ErrClientClosed
	} else if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		p.notifyLocked()
		p.mu.Unlock()
		if client != nil {
			if closeErr := client.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
		return nil, err
	}

	now := time.Now()
	pc := &poolConn{client: client, pool: p, createdAt: now, lastUsed: now, inUse: true}
	p.conns = append(p.conns, pc)
	p.mu.Unlock()
	return &poolClient{conn: pc}, nil
}

// removeWaiterLocked advances the queue when the oldest caller leaves.
func (p *Pool) removeWaiterLocked(waiter *list.Element) {
	if waiter == nil {
		return
	}
	wasFirst := p.waiting.Front() == waiter
	p.waiting.Remove(waiter)
	if wasFirst {
		p.notifyLocked()
	}
}

// notifyLocked tells the oldest waiter to recheck state without transferring a lease.
func (p *Pool) notifyLocked() {
	if next := p.waiting.Front(); next != nil {
		select {
		case next.Value.(chan struct{}) <- struct{}{}:
		default:
		}
	}
}

// put returns a connection to the pool.
func (p *Pool) put(pc *poolConn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}

	pc.lastUsed = time.Now()
	pc.inUse = false
	p.notifyLocked()
}

// Close closes the pool and all established connections. Any pending dial
// that returns a connection after closure is responsible for closing it. Close
// reports all errors from closing established connections.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.stopCh)
	conns := p.conns
	p.conns = nil
	p.mu.Unlock()

	var failures []error
	for _, pc := range conns {
		if err := pc.client.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// closePoolConnections performs best-effort cleanup for background eviction.
func closePoolConnections(conns []*poolConn) {
	for _, pc := range conns {
		_ = pc.client.Close()
	}
}

// cleanup periodically removes idle connections
func (p *Pool) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			now := time.Now()

			// Keep track of connections to close
			var toClose []*poolConn

			// Filter out expired idle connections
			filtered := make([]*poolConn, 0, len(p.conns))
			idleCount := 0

			for _, pc := range p.conns {
				if pc.inUse {
					filtered = append(filtered, pc)
					continue
				}

				// Check if connection is too old
				if now.Sub(pc.lastUsed) > p.poolCfg.MaxIdleTime {
					toClose = append(toClose, pc)
					continue
				}

				// Check if we have too many idle connections
				idleCount++
				if idleCount > p.poolCfg.MaxIdleConns {
					toClose = append(toClose, pc)
					continue
				}

				filtered = append(filtered, pc)
			}

			p.conns = filtered
			if len(toClose) > 0 {
				p.notifyLocked()
			}
			p.mu.Unlock()

			// Close connections outside lock
			closePoolConnections(toClose)

		case <-p.stopCh:
			return
		}
	}
}

// Stats returns pool statistics
func (p *Pool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := PoolStats{
		TotalConns: len(p.conns),
		Waiting:    p.waiting.Len(),
	}

	for _, pc := range p.conns {
		if pc.inUse {
			stats.InUse++
		} else {
			stats.Idle++
		}
	}

	return stats
}

// PoolStats contains pool statistics
type PoolStats struct {
	TotalConns int // Total connections in pool
	InUse      int // Connections currently in use
	Idle       int // Idle connections
	Waiting    int // Goroutines waiting for connection
}

// poolClient wraps a poolConn and returns it to pool on close
type poolClient struct {
	conn *poolConn
	mu   sync.Mutex
}

func (pc *poolClient) Write(data []byte) (int, error) {
	return pc.conn.client.Write(data)
}

func (pc *poolClient) WriteWithTimeout(data []byte, timeout time.Duration) (int, error) {
	return pc.conn.client.WriteWithTimeout(data, timeout)
}

func (pc *poolClient) Read(buf []byte) (int, error) {
	return pc.conn.client.Read(buf)
}

func (pc *poolClient) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	return pc.conn.client.ReadWithTimeout(buf, timeout)
}

func (pc *poolClient) RemoteAddr() net.Addr {
	return pc.conn.client.RemoteAddr()
}

func (pc *poolClient) RemoteAddrString() string {
	return pc.conn.client.RemoteAddrString()
}

func (pc *poolClient) Close() error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.conn == nil {
		return nil
	}

	pool := pc.conn.pool
	conn := pc.conn
	pc.conn = nil

	// Return to pool instead of closing
	pool.put(conn)
	return nil
}
