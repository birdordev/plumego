//go:build !windows

package ipc

import (
	"io"

	"context"
	"github.com/birdordev/plumego/log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// unixServer implements Server for Unix-like systems
type unixServer struct {
	listener   net.Listener // The underlying listener
	socketPath string       // The path to the Unix domain socket file (if applicable)
	config     *Config      // The configuration for the server
	mu         sync.RWMutex // Mutex for thread-safe access to server state
	closed     bool         // Indicates if the server is closed
}

// unixClient implements Client for Unix-like systems
type unixClient struct {
	conn   net.Conn     // The underlying connection
	config *Config      // The configuration for the client
	mu     sync.RWMutex // Mutex for thread-safe access to client state
	closed bool         // Indicates if the client is closed
}

func newPlatformServer(addr string, config *Config) (Server, error) {
	if config == nil {
		config = DefaultConfig()
	}

	var listener net.Listener
	var socketPath string
	var err error

	// Try Unix domain socket first
	if !strings.Contains(addr, ":") {
		// Ensure directory exists with configured permissions
		dir := filepath.Dir(addr)
		dirPerm := os.FileMode(config.UnixSocketDirPerm)
		if err = os.MkdirAll(dir, dirPerm); err != nil {
			return nil, err
		}

		// Remove existing socket file
		if err = os.Remove(addr); err != nil && !os.IsNotExist(err) {
			return nil, err
		}

		listener, err = net.Listen("unix", addr)
		if err == nil {
			socketPath = addr
			// Set socket file permissions
			socketPerm := os.FileMode(config.UnixSocketPerm)
			if chmodErr := os.Chmod(addr, socketPerm); chmodErr != nil {
				_ = listener.Close()
				_ = os.Remove(addr)
				return nil, chmodErr
			}
		}
	}

	// Fallback to TCP if Unix socket fails or addr contains port
	if listener == nil {
		tcpAddr := addr
		if !strings.Contains(addr, ":") {
			tcpAddr = "127.0.0.1:0" // Auto-assign port
		}
		listener, err = net.Listen("tcp", tcpAddr)
		if err != nil {
			return nil, err
		}
	}

	server := &unixServer{
		listener:   listener,
		socketPath: socketPath,
		config:     config,
	}

	return server, nil
}

func (s *unixServer) Accept() (Client, error) {
	return s.AcceptWithContext(context.Background())
}

func (s *unixServer) AcceptWithContext(ctx context.Context) (Client, error) {
	start := time.Now()
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrServerClosed
	}
	config := s.config
	s.mu.RUnlock()

	// Set deadline if context has timeout
	if deadline, ok := ctx.Deadline(); ok {
		// Safe type assertion - handle both Unix and TCP listeners
		switch l := s.listener.(type) {
		case *net.UnixListener:
			if err := l.SetDeadline(deadline); err != nil {
				return nil, err
			}
		case *net.TCPListener:
			if err := l.SetDeadline(deadline); err != nil {
				return nil, err
			}
		}
		// Clear deadline after accept completes
		defer func() {
			switch l := s.listener.(type) {
			case *net.UnixListener:
				_ = l.SetDeadline(time.Time{})
			case *net.TCPListener:
				_ = l.SetDeadline(time.Time{})
			}
		}()
	}

	conn, err := s.listener.Accept()
	duration := time.Since(start)

	// Get transport type
	transport := "unix"
	if _, ok := conn.(*net.TCPConn); ok {
		transport = "tcp"
	}

	// Log and record metrics
	if config.Logger != nil {
		fields := log.Fields{
			"operation": "accept",
			"addr":      s.Addr(),
			"transport": transport,
			"duration":  duration,
		}
		if err != nil {
			fields["error"] = err.Error()
			config.Logger.Error("IPC accept failed", fields)
		} else {
			config.Logger.Debug("IPC accept succeeded", fields)
		}
	}

	if config.Metrics != nil {
		config.Metrics.ObserveIPC(ctx, "accept", s.Addr(), transport, 0, duration, err)
	}

	if err != nil {
		return nil, err
	}

	// Enable keepalive for TCP connections
	if tcpConn, ok := conn.(*net.TCPConn); ok && s.config.KeepAlive {
		if err := tcpConn.SetKeepAlive(true); err == nil {
			_ = tcpConn.SetKeepAlivePeriod(s.config.KeepAlivePeriod)
		}
	}

	client := &unixClient{
		conn:   conn,
		config: s.config,
	}

	return client, nil
}

func (s *unixServer) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

func (s *unixServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}

	// Clean up socket file
	if s.socketPath != "" {
		if removeErr := os.Remove(s.socketPath); removeErr != nil && !os.IsNotExist(removeErr) {
			if err == nil {
				err = removeErr
			}
		}
	}

	return err
}

func (s *unixServer) Shutdown(ctx context.Context) error {
	// Mark as closed to stop accepting new connections
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// Close the listener to unblock any pending Accept calls
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}

	// Wait for context cancellation (graceful period)
	<-ctx.Done()

	// Clean up socket file
	if s.socketPath != "" {
		if removeErr := os.Remove(s.socketPath); removeErr != nil && !os.IsNotExist(removeErr) {
			if err == nil {
				err = removeErr
			}
		}
	}

	return err
}

func dialPlatform(addr string, config *Config) (Client, error) {
	return dialPlatformWithContext(context.Background(), addr, config)
}

func dialPlatformWithContext(ctx context.Context, addr string, config *Config) (Client, error) {
	start := time.Now()
	if config == nil {
		config = DefaultConfig()
	}

	var conn net.Conn
	var err error
	var transport string

	dialer := &net.Dialer{
		Timeout:   config.ConnectTimeout,
		KeepAlive: config.KeepAlivePeriod,
	}

	// Try Unix domain socket first if no port specified
	if !strings.Contains(addr, ":") {
		conn, err = dialer.DialContext(ctx, "unix", addr)
		transport = "unix"
	}

	// Fallback to TCP if Unix socket fails
	if conn == nil {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
		transport = "tcp"
		// Enable keepalive for TCP connections
		if err == nil {
			if tcpConn, ok := conn.(*net.TCPConn); ok && config.KeepAlive {
				if setErr := tcpConn.SetKeepAlive(true); setErr == nil {
					_ = tcpConn.SetKeepAlivePeriod(config.KeepAlivePeriod)
				}
			}
		}
	}

	duration := time.Since(start)

	// Log and record metrics
	if config.Logger != nil {
		fields := log.Fields{
			"operation": "dial",
			"addr":      addr,
			"transport": transport,
			"duration":  duration,
		}
		if err != nil {
			fields["error"] = err.Error()
			config.Logger.Error("IPC dial failed", fields)
		} else {
			config.Logger.Debug("IPC dial succeeded", fields)
		}
	}

	if config.Metrics != nil {
		config.Metrics.ObserveIPC(ctx, "dial", addr, transport, 0, duration, err)
	}

	if err != nil {
		return nil, err
	}

	client := &unixClient{
		conn:   conn,
		config: config,
	}

	return client, nil
}

func (c *unixClient) Write(data []byte) (int, error) {
	return c.WriteWithTimeout(data, 0)
}

func (c *unixClient) WriteWithTimeout(data []byte, timeout time.Duration) (int, error) {
	start := time.Now()
	c.mu.RLock()
	if c.closed || c.conn == nil {
		c.mu.RUnlock()
		return 0, ErrClientClosed
	}
	conn := c.conn
	config := c.config
	c.mu.RUnlock()

	if timeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(timeout))
		defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	}

	n, err := conn.Write(data)
	duration := time.Since(start)

	// Log and record metrics
	if config.Logger != nil && err != nil {
		config.Logger.Error("IPC write failed", log.Fields{
			"operation": "write",
			"bytes":     n,
			"duration":  duration,
			"error":     err.Error(),
		})
	}

	if config.Metrics != nil {
		transport := "unix"
		if _, ok := conn.(*net.TCPConn); ok {
			transport = "tcp"
		}
		config.Metrics.ObserveIPC(context.Background(), "write", "", transport, n, duration, err)
	}

	return n, err
}

func (c *unixClient) Read(buf []byte) (int, error) {
	return c.ReadWithTimeout(buf, 0)
}

func (c *unixClient) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	start := time.Now()
	c.mu.RLock()
	if c.closed || c.conn == nil {
		c.mu.RUnlock()
		return 0, ErrClientClosed
	}
	conn := c.conn
	config := c.config
	c.mu.RUnlock()

	if timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	}

	n, err := conn.Read(buf)
	duration := time.Since(start)

	// Log and record metrics (only log errors to avoid excessive logging)
	if config.Logger != nil && err != nil && err != io.EOF {
		config.Logger.Error("IPC read failed", log.Fields{
			"operation": "read",
			"bytes":     n,
			"duration":  duration,
			"error":     err.Error(),
		})
	}

	if config.Metrics != nil {
		transport := "unix"
		if _, ok := conn.(*net.TCPConn); ok {
			transport = "tcp"
		}
		config.Metrics.ObserveIPC(context.Background(), "read", "", transport, n, duration, err)
	}

	return n, err
}

func (c *unixClient) RemoteAddr() net.Addr {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.conn != nil {
		addr := c.conn.RemoteAddr()
		if addr != nil {
			return addr
		}
	}
	// Return a default IPC address if no connection
	return NewAddr("ipc", "")
}

func (c *unixClient) RemoteAddrString() string {
	addr := c.RemoteAddr()
	if addr != nil {
		return addr.String()
	}
	return ""
}

func (c *unixClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
