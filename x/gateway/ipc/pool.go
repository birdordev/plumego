package ipc

import (
	"context"
	"net"
	"sync"
	"time"
)

// PoolConfig holds configuration for connection pool
type PoolConfig struct {
	MaxConns     int           // Maximum number of connections in pool
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

	mu      sync.Mutex
	conns   []*poolConn
	waiting []chan *poolConn
	closed  bool

	stopCh chan struct{}
}

// NewPool creates a new connection pool
func NewPool(addr string, poolCfg *PoolConfig, opts ...Option) (*Pool, error) {
	if poolCfg == nil {
		poolCfg = DefaultPoolConfig()
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
		waiting: make([]chan *poolConn, 0),
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

// GetWithContext acquires a connection from the pool with context
func (p *Pool) GetWithContext(ctx context.Context) (Client, error) {
	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()
		return nil, ErrClientClosed
	}

	// Try to find an idle connection
	for i, pc := range p.conns {
		if !pc.inUse {
			// Check if connection is still valid
			if time.Since(pc.lastUsed) > p.poolCfg.MaxIdleTime {
				// Connection too old, remove it
				_ = pc.client.Close()
				p.conns = append(p.conns[:i], p.conns[i+1:]...)
				continue
			}

			pc.inUse = true
			pc.lastUsed = time.Now()
			p.mu.Unlock()
			return &poolClient{conn: pc}, nil
		}
	}

	// No idle connection, try to create new one
	if len(p.conns) < p.poolCfg.MaxConns {
		p.mu.Unlock()
		client, err := dialPlatformWithContext(ctx, p.addr, p.config)
		if err != nil {
			return nil, err
		}

		pc := &poolConn{
			client:    client,
			pool:      p,
			createdAt: time.Now(),
			lastUsed:  time.Now(),
			inUse:     true,
		}

		p.mu.Lock()
		p.conns = append(p.conns, pc)
		p.mu.Unlock()

		return &poolClient{conn: pc}, nil
	}

	// Pool is full, wait for a connection to be released
	waiter := make(chan *poolConn, 1)
	p.waiting = append(p.waiting, waiter)
	p.mu.Unlock()

	select {
	case pc := <-waiter:
		return &poolClient{conn: pc}, nil
	case <-ctx.Done():
		// Remove from waiting list
		p.mu.Lock()
		for i, w := range p.waiting {
			if w == waiter {
				p.waiting = append(p.waiting[:i], p.waiting[i+1:]...)
				break
			}
		}
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}

// put returns a connection to the pool
func (p *Pool) put(pc *poolConn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		_ = pc.client.Close()
		return
	}

	pc.lastUsed = time.Now()
	pc.inUse = false

	// Wake up waiting goroutine if any
	if len(p.waiting) > 0 {
		waiter := p.waiting[0]
		p.waiting = p.waiting[1:]
		pc.inUse = true
		waiter <- pc
	}
}

// Close closes the pool and all connections
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true
	close(p.stopCh)

	// Close all connections
	for _, pc := range p.conns {
		_ = pc.client.Close()
	}
	p.conns = nil

	// Wake up all waiting goroutines
	for _, waiter := range p.waiting {
		close(waiter)
	}
	p.waiting = nil

	return nil
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
			p.mu.Unlock()

			// Close connections outside lock
			for _, pc := range toClose {
				_ = pc.client.Close()
			}

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
		Waiting:    len(p.waiting),
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
