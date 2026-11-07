package ipc

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// ReconnectConfig holds configuration for auto-reconnection
type ReconnectConfig struct {
	MaxRetries    int           // Maximum number of retry attempts (0 = infinite)
	InitialDelay  time.Duration // Initial delay before first retry
	MaxDelay      time.Duration // Maximum delay between retries
	BackoffFactor float64       // Multiplier for exponential backoff (e.g., 2.0)
}

// DefaultReconnectConfig returns default reconnection configuration
func DefaultReconnectConfig() *ReconnectConfig {
	return &ReconnectConfig{
		MaxRetries:    5,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
	}
}

// reconnectClient wraps a Client with auto-reconnection capability
type reconnectClient struct {
	addr         string
	config       *Config
	reconn       *ReconnectConfig
	mu           sync.RWMutex
	client       Client
	closed       bool
	reconnecting bool
}

// DialWithReconnect creates a client with automatic reconnection on failure
func DialWithReconnect(addr string, reconnCfg *ReconnectConfig, opts ...Option) (Client, error) {
	config := DefaultConfig()
	for _, opt := range opts {
		opt(config)
	}

	if reconnCfg == nil {
		reconnCfg = DefaultReconnectConfig()
	}

	client, err := dialPlatform(addr, config)
	if err != nil {
		return nil, err
	}

	return &reconnectClient{
		addr:   addr,
		config: config,
		reconn: reconnCfg,
		client: client,
	}, nil
}

func (rc *reconnectClient) reconnect() error {
	rc.mu.Lock()
	if rc.closed || rc.reconnecting {
		rc.mu.Unlock()
		return ErrClientClosed
	}
	rc.reconnecting = true
	rc.mu.Unlock()

	defer func() {
		rc.mu.Lock()
		rc.reconnecting = false
		rc.mu.Unlock()
	}()

	delay := rc.reconn.InitialDelay
	retries := 0

	for {
		// Check if max retries reached
		if rc.reconn.MaxRetries > 0 && retries >= rc.reconn.MaxRetries {
			return fmt.Errorf("max reconnection attempts (%d) reached", rc.reconn.MaxRetries)
		}

		// Wait before retry
		if retries > 0 {
			time.Sleep(delay)
			// Exponential backoff
			delay = time.Duration(float64(delay) * rc.reconn.BackoffFactor)
			if delay > rc.reconn.MaxDelay {
				delay = rc.reconn.MaxDelay
			}
		}

		// Attempt reconnection
		client, err := dialPlatform(rc.addr, rc.config)
		if err == nil {
			rc.mu.Lock()
			if rc.client != nil {
				_ = rc.client.Close()
			}
			rc.client = client
			rc.mu.Unlock()
			return nil
		}

		retries++
	}
}

func (rc *reconnectClient) Write(data []byte) (int, error) {
	rc.mu.RLock()
	client := rc.client
	closed := rc.closed
	rc.mu.RUnlock()

	if closed {
		return 0, ErrClientClosed
	}

	n, err := client.Write(data)
	if err != nil && rc.shouldReconnect(err) {
		if reconnErr := rc.reconnect(); reconnErr == nil {
			// Retry after successful reconnection
			rc.mu.RLock()
			client = rc.client
			rc.mu.RUnlock()
			return client.Write(data)
		}
	}
	return n, err
}

func (rc *reconnectClient) WriteWithTimeout(data []byte, timeout time.Duration) (int, error) {
	rc.mu.RLock()
	client := rc.client
	closed := rc.closed
	rc.mu.RUnlock()

	if closed {
		return 0, ErrClientClosed
	}

	n, err := client.WriteWithTimeout(data, timeout)
	if err != nil && rc.shouldReconnect(err) {
		if reconnErr := rc.reconnect(); reconnErr == nil {
			// Retry after successful reconnection
			rc.mu.RLock()
			client = rc.client
			rc.mu.RUnlock()
			return client.WriteWithTimeout(data, timeout)
		}
	}
	return n, err
}

func (rc *reconnectClient) Read(buf []byte) (int, error) {
	rc.mu.RLock()
	client := rc.client
	closed := rc.closed
	rc.mu.RUnlock()

	if closed {
		return 0, ErrClientClosed
	}

	n, err := client.Read(buf)
	if err != nil && rc.shouldReconnect(err) {
		if reconnErr := rc.reconnect(); reconnErr == nil {
			// Retry after successful reconnection
			rc.mu.RLock()
			client = rc.client
			rc.mu.RUnlock()
			return client.Read(buf)
		}
	}
	return n, err
}

func (rc *reconnectClient) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	rc.mu.RLock()
	client := rc.client
	closed := rc.closed
	rc.mu.RUnlock()

	if closed {
		return 0, ErrClientClosed
	}

	n, err := client.ReadWithTimeout(buf, timeout)
	if err != nil && rc.shouldReconnect(err) {
		if reconnErr := rc.reconnect(); reconnErr == nil {
			// Retry after successful reconnection
			rc.mu.RLock()
			client = rc.client
			rc.mu.RUnlock()
			return client.ReadWithTimeout(buf, timeout)
		}
	}
	return n, err
}

func (rc *reconnectClient) RemoteAddr() net.Addr {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if rc.client != nil {
		return rc.client.RemoteAddr()
	}
	return NewAddr("ipc", rc.addr)
}

func (rc *reconnectClient) RemoteAddrString() string {
	addr := rc.RemoteAddr()
	if addr != nil {
		return addr.String()
	}
	return ""
}

func (rc *reconnectClient) Close() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if rc.closed {
		return nil
	}
	rc.closed = true

	if rc.client != nil {
		return rc.client.Close()
	}
	return nil
}

// shouldReconnect determines if an error should trigger reconnection
func (rc *reconnectClient) shouldReconnect(err error) bool {
	if err == nil {
		return false
	}
	// Check for common disconnection errors
	if errors.Is(err, io.EOF) || errors.Is(err, ErrClientClosed) {
		return true
	}
	// Check error message for common patterns
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "use of closed")
}
