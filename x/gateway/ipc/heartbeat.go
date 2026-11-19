package ipc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// HeartbeatConfig holds configuration for connection heartbeat
type HeartbeatConfig struct {
	Enabled  bool          // Enable heartbeat mechanism
	Interval time.Duration // Interval between heartbeat checks
	Timeout  time.Duration // Timeout for heartbeat response
	// OnDead is called once on heartbeat failure, outside the client lock.
	// Close suppresses a notification that has not been selected yet.
	OnDead func()
}

// DefaultHeartbeatConfig returns default heartbeat configuration
func DefaultHeartbeatConfig() *HeartbeatConfig {
	return &HeartbeatConfig{
		Enabled:  true,
		Interval: 30 * time.Second,
		Timeout:  10 * time.Second,
		OnDead:   nil,
	}
}

// heartbeatClient wraps a Client with heartbeat functionality
type heartbeatClient struct {
	client   Client
	config   *HeartbeatConfig
	mu       sync.RWMutex
	stopCh   chan struct{}
	stopped  bool
	lastSeen time.Time
}

// NewHeartbeatClient creates a client wrapper with heartbeat monitoring.
// The heartbeat uses a simple ping-pong protocol over the framed client.
func NewHeartbeatClient(client Client, cfg *HeartbeatConfig) (Client, error) {
	if cfg == nil {
		cfg = DefaultHeartbeatConfig()
	}

	if !cfg.Enabled {
		return client, nil
	}

	hb := &heartbeatClient{
		client:   client,
		config:   cfg,
		stopCh:   make(chan struct{}),
		stopped:  false,
		lastSeen: time.Now(),
	}

	// Start heartbeat goroutine
	go hb.run()

	return hb, nil
}

func (hb *heartbeatClient) run() {
	ticker := time.NewTicker(hb.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := hb.ping(); err != nil {
				hb.mu.RLock()
				onDead, stopped := hb.config.OnDead, hb.stopped
				hb.mu.RUnlock()
				if !stopped && onDead != nil {
					onDead()
				}
				return
			}
		case <-hb.stopCh:
			return
		}
	}
}

func (hb *heartbeatClient) ping() error {
	hb.mu.Lock()
	if hb.stopped {
		hb.mu.Unlock()
		return ErrClientClosed
	}
	hb.mu.Unlock()

	// Simple ping: write a special byte sequence
	pingMsg := []byte{0xFF, 0xFF, 0x00, 0x00} // Magic ping bytes
	deadline := time.Now().Add(hb.config.Timeout)

	// Set deadline for ping
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	// Write ping
	n, err := hb.client.WriteWithTimeout(pingMsg, hb.config.Timeout)
	if err != nil || n != len(pingMsg) {
		return fmt.Errorf("heartbeat ping failed: %w", err)
	}

	// Expect pong (same bytes echoed back)
	pongBuf := make([]byte, len(pingMsg))
	n, err = hb.client.ReadWithTimeout(pongBuf, hb.config.Timeout)
	if err != nil || n != len(pingMsg) {
		return fmt.Errorf("heartbeat pong failed: %w", err)
	}

	// Update last seen time
	hb.mu.Lock()
	hb.lastSeen = time.Now()
	hb.mu.Unlock()

	_ = ctx // Use ctx to satisfy linter

	return nil
}

// LastSeen returns the last time a successful heartbeat was received
func (hb *heartbeatClient) LastSeen() time.Time {
	hb.mu.RLock()
	defer hb.mu.RUnlock()
	return hb.lastSeen
}

// Delegate Client interface methods
func (hb *heartbeatClient) Write(data []byte) (int, error) {
	return hb.client.Write(data)
}

func (hb *heartbeatClient) WriteWithTimeout(data []byte, timeout time.Duration) (int, error) {
	return hb.client.WriteWithTimeout(data, timeout)
}

func (hb *heartbeatClient) Read(buf []byte) (int, error) {
	return hb.client.Read(buf)
}

func (hb *heartbeatClient) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	return hb.client.ReadWithTimeout(buf, timeout)
}

func (hb *heartbeatClient) RemoteAddr() net.Addr {
	return hb.client.RemoteAddr()
}

func (hb *heartbeatClient) RemoteAddrString() string {
	return hb.client.RemoteAddrString()
}

func (hb *heartbeatClient) Close() error {
	hb.mu.Lock()
	if !hb.stopped {
		hb.stopped = true
		close(hb.stopCh)
	}
	hb.mu.Unlock()

	return hb.client.Close()
}
