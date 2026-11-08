// Package ipc provides cross-platform inter-process communication (IPC) primitives.
//
// # Version 2 Breaking Changes
//
// This version includes the following breaking changes:
//
// 1. Client.RemoteAddr() now returns net.Addr instead of string
//   - Migration: Use RemoteAddrString() for backward compatibility
//   - Or: Use addr.String() on the returned net.Addr
//
// 2. Client interface has been refactored into composable sub-interfaces
//   - Reader: io.Reader + ReadWithTimeout
//   - Writer: io.Writer + WriteWithTimeout
//   - AddrProvider: RemoteAddr() + RemoteAddrString()
//   - Client now embeds all these interfaces
//
// These changes improve type safety and enable more flexible composition.
//
// # Supported Transports
//
// - Unix Domain Sockets (Linux, macOS, BSD)
// - Windows Named Pipes (Windows)
// - TCP sockets (fallback for all platforms)
//
// # Performance Characteristics
//
// Unix Domain Sockets offer approximately 50% lower latency than TCP and 30% higher
// throughput for local communication. Windows Named Pipes provide similar performance
// benefits on Windows. TCP is universal but slower, suitable for network IPC.
//
// # Basic Usage
//
//	// Server
//	server, err := ipc.NewServer("/tmp/myapp.sock")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer server.Close()
//
//	// Accept connections
//	client, err := server.Accept()
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer client.Close()
//
//	// Client
//	client, err := ipc.Dial("/tmp/myapp.sock")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer client.Close()
//
//	// Get remote address (v2 API)
//	addr := client.RemoteAddr()
//	fmt.Printf("Connected to: %s (%s)\n", addr.String(), addr.Network())
//
//	// Or use backward-compatible string method
//	addrStr := client.RemoteAddrString()
//	fmt.Printf("Address: %s\n", addrStr)
//
// # Functional Options
//
// Configure timeouts, permissions, and buffer sizes using functional options:
//
//	server, err := ipc.NewServer("/tmp/myapp.sock",
//		ipc.WithUnixSocketPerm(0700),  // Owner only
//		ipc.WithTimeouts(5*time.Second, 30*time.Second, 30*time.Second),
//	)
//
//	client, err := ipc.Dial("/tmp/myapp.sock",
//		ipc.WithConnectTimeout(5*time.Second),
//		ipc.WithReadTimeout(10*time.Second),
//	)
//
// # Auto-Reconnection
//
// For resilient connections that automatically reconnect on failure:
//
//	reconnCfg := &ipc.ReconnectConfig{
//		MaxRetries:    5,
//		InitialDelay:  100 * time.Millisecond,
//		MaxDelay:      30 * time.Second,
//		BackoffFactor: 2.0,
//	}
//
//	client, err := ipc.DialWithReconnect("/tmp/myapp.sock", reconnCfg)
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer client.Close()
//
//	// Write will automatically reconnect on connection failure
//	n, err := client.Write(data)
//
// # Thread Safety
//
// All types are safe for concurrent use from multiple goroutines.
//
// # Error Handling
//
// The package provides structured errors for better error handling:
//
//	if errors.Is(err, ipc.ErrClientClosed) {
//		// Handle closed client
//	}
//
//	var ipcErr *ipc.Error
//	if errors.As(err, &ipcErr) {
//		fmt.Printf("IPC %s error: %v\n", ipcErr.Op, ipcErr.Err)
//	}
package ipc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/birdordev/plumego/log"
)

// Error represents an IPC error with context
type Error struct {
	Op   string // Operation: "accept", "dial", "read", "write", "close"
	Addr string // Address
	Err  error  // Underlying error
}

// Error implements the error interface
func (e *Error) Error() string {
	if e.Addr != "" {
		return fmt.Sprintf("ipc %s %s: %v", e.Op, e.Addr, e.Err)
	}
	return fmt.Sprintf("ipc %s: %v", e.Op, e.Err)
}

// Unwrap returns the underlying error
func (e *Error) Unwrap() error {
	return e.Err
}

// Config holds configuration for IPC connections
type Config struct {
	ConnectTimeout      time.Duration        // Timeout for connection establishment
	ReadTimeout         time.Duration        // Timeout for read operations
	WriteTimeout        time.Duration        // Timeout for write operations
	BufferSize          int                  // Buffer size for read/write operations (Windows Named Pipe only)
	UnixSocketPerm      uint32               // Unix socket file permissions (e.g., 0700). Default: 0700 (owner only). Unix/Linux only.
	UnixSocketDirPerm   uint32               // Unix socket directory permissions (e.g., 0755). Default: 0755. Unix/Linux only.
	KeepAlive           bool                 // Enable TCP keepalive for TCP connections. Default: true.
	KeepAlivePeriod     time.Duration        // TCP keepalive period. Default: 30s. Only applies to TCP connections.
	WindowsSecuritySDDL string               // Windows security descriptor (SDDL string). Windows only. Empty = default security.
	Metrics             MetricsObserver      // Optional metrics collector
	Logger              log.StructuredLogger // Optional structured logger
}

// MetricsObserver captures IPC-specific observations without routing the
// feature-owned observer contract through stable metrics.
type MetricsObserver interface {
	ObserveIPC(ctx context.Context, operation, addr, transport string, bytes int, duration time.Duration, err error)
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		ConnectTimeout:    10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		BufferSize:        4096,
		UnixSocketPerm:    0700, // Owner only (rwx------)
		UnixSocketDirPerm: 0755, // Owner rwx, others rx (rwxr-xr-x)
		KeepAlive:         true,
		KeepAlivePeriod:   30 * time.Second,
	}
}

// Option is a functional option for configuring IPC connections
type Option func(*Config)

// WithConnectTimeout sets the connection timeout
func WithConnectTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.ConnectTimeout = d
	}
}

// WithReadTimeout sets the read timeout
func WithReadTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.ReadTimeout = d
	}
}

// WithWriteTimeout sets the write timeout
func WithWriteTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.WriteTimeout = d
	}
}

// WithBufferSize sets the buffer size (Windows Named Pipe only)
func WithBufferSize(size int) Option {
	return func(c *Config) {
		c.BufferSize = size
	}
}

// WithUnixSocketPerm sets Unix socket file permissions (Unix/Linux only)
func WithUnixSocketPerm(perm uint32) Option {
	return func(c *Config) {
		c.UnixSocketPerm = perm
	}
}

// WithUnixSocketDirPerm sets Unix socket directory permissions (Unix/Linux only)
func WithUnixSocketDirPerm(perm uint32) Option {
	return func(c *Config) {
		c.UnixSocketDirPerm = perm
	}
}

// WithTimeouts sets all timeout values at once
func WithTimeouts(connect, read, write time.Duration) Option {
	return func(c *Config) {
		c.ConnectTimeout = connect
		c.ReadTimeout = read
		c.WriteTimeout = write
	}
}

// WithKeepAlive enables or disables TCP keepalive
func WithKeepAlive(enable bool) Option {
	return func(c *Config) {
		c.KeepAlive = enable
	}
}

// WithKeepAlivePeriod sets the TCP keepalive period
func WithKeepAlivePeriod(period time.Duration) Option {
	return func(c *Config) {
		c.KeepAlivePeriod = period
	}
}

// WithMetrics sets the metrics collector
func WithMetrics(m MetricsObserver) Option {
	return func(c *Config) {
		c.Metrics = m
	}
}

// WithLogger sets the structured logger
func WithLogger(l log.StructuredLogger) Option {
	return func(c *Config) {
		c.Logger = l
	}
}

// WithWindowsSecuritySDDL sets Windows security descriptor (SDDL string) for Named Pipes.
// Only applicable on Windows. Example SDDL strings:
//   - "D:P(A;;GA;;;WD)" - Allow all access to Everyone
//   - "D:P(A;;GA;;;SY)(A;;GA;;;BA)" - Allow SYSTEM and Administrators
//   - "D:P(A;;GRGW;;;AU)" - Allow Authenticated Users read/write
//
// See https://docs.microsoft.com/en-us/windows/win32/secauthz/security-descriptor-string-format
func WithWindowsSecuritySDDL(sddl string) Option {
	return func(c *Config) {
		c.WindowsSecuritySDDL = sddl
	}
}

// Addr represents an IPC address, implementing net.Addr interface
type Addr struct {
	network string // "unix", "pipe", "tcp"
	address string // actual address string
}

// Network returns the network type
func (a *Addr) Network() string {
	return a.network
}

// String returns the address string
func (a *Addr) String() string {
	return a.address
}

// NewAddr creates a new IPC address
func NewAddr(network, address string) net.Addr {
	return &Addr{
		network: network,
		address: address,
	}
}

// RateLimiter defines the interface for rate limiting
type RateLimiter interface {
	// Allow returns true if an operation is allowed
	Allow() bool
	// Wait blocks until an operation is allowed or context is cancelled
	Wait(ctx context.Context) error
}

// tokenBucketLimiter implements a token bucket rate limiter
type tokenBucketLimiter struct {
	rate      float64   // Tokens per second
	capacity  int       // Maximum burst size
	tokens    float64   // Current tokens
	lastCheck time.Time // Last time tokens were added
	mu        sync.Mutex
}

// NewRateLimiter creates a new token bucket rate limiter
// rate: operations per second
// burst: maximum burst size (number of operations that can be performed at once)
func NewRateLimiter(rate float64, burst int) RateLimiter {
	return &tokenBucketLimiter{
		rate:      rate,
		capacity:  burst,
		tokens:    float64(burst),
		lastCheck: time.Now(),
	}
}

func (tb *tokenBucketLimiter) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}

	return false
}

func (tb *tokenBucketLimiter) Wait(ctx context.Context) error {
	for {
		if tb.Allow() {
			return nil
		}

		// Calculate how long to wait for next token
		tb.mu.Lock()
		tokensNeeded := 1.0 - tb.tokens
		waitTime := time.Duration(float64(time.Second) * tokensNeeded / tb.rate)
		tb.mu.Unlock()

		if waitTime > 0 {
			timer := time.NewTimer(waitTime)
			select {
			case <-timer.C:
				// Try again
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
}

func (tb *tokenBucketLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastCheck)
	tb.lastCheck = now

	// Add tokens based on elapsed time
	tokensToAdd := elapsed.Seconds() * tb.rate
	tb.tokens += tokensToAdd

	if tb.tokens > float64(tb.capacity) {
		tb.tokens = float64(tb.capacity)
	}
}

// rateLimitedServer wraps a Server with rate limiting
type rateLimitedServer struct {
	server  Server
	limiter RateLimiter
}

// NewRateLimitedServer creates a server wrapper with connection rate limiting
func NewRateLimitedServer(server Server, limiter RateLimiter) Server {
	return &rateLimitedServer{
		server:  server,
		limiter: limiter,
	}
}

func (rs *rateLimitedServer) Accept() (Client, error) {
	return rs.AcceptWithContext(context.Background())
}

func (rs *rateLimitedServer) AcceptWithContext(ctx context.Context) (Client, error) {
	// Wait for rate limiter
	if err := rs.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	return rs.server.AcceptWithContext(ctx)
}

func (rs *rateLimitedServer) Addr() string {
	return rs.server.Addr()
}

func (rs *rateLimitedServer) Close() error {
	return rs.server.Close()
}

func (rs *rateLimitedServer) Shutdown(ctx context.Context) error {
	return rs.server.Shutdown(ctx)
}
