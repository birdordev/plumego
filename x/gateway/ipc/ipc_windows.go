// ipc_windows.go
//go:build windows

package ipc

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/birdordev/plumego/log"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procCreateNamedPipeW    = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe    = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe = kernel32.NewProc("DisconnectNamedPipe")
	procWaitNamedPipeW      = kernel32.NewProc("WaitNamedPipeW")
)

const (
	PIPE_ACCESS_DUPLEX       = 0x3
	PIPE_TYPE_BYTE           = 0x0
	PIPE_READMODE_BYTE       = 0x0
	PIPE_WAIT                = 0x0
	PIPE_UNLIMITED_INSTANCES = 255
	GENERIC_READ             = 0x80000000
	GENERIC_WRITE            = 0x40000000
	OPEN_EXISTING            = 3
	INVALID_HANDLE_VALUE     = ^uintptr(0)
	errorPipeConnected       = syscall.Errno(535)
	errorPipeClosing         = syscall.Errno(232)
)

// winServer implements Server for Windows using Named Pipes
type winServer struct {
	pipeName string
	config   *Config
	mu       sync.RWMutex
	closed   bool
	next     syscall.Handle
}

// winClient implements Client for Windows
type winClient struct {
	conn   net.Conn
	handle syscall.Handle
	config *Config
	mu     sync.RWMutex
	closed bool
}

func newPlatformServer(addr string, config *Config) (Server, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Try named pipe first for local communication
	if isNamedPipeAddr(addr) || !strings.Contains(addr, ":") {
		pipeName := normalizePipeAddr(addr)
		handle, err := createNamedPipe(pipeName, config.BufferSize)
		if err != nil {
			return nil, err
		}
		server := &winServer{
			pipeName: pipeName,
			config:   config,
			next:     handle,
		}
		return server, nil
	}

	// Fallback to TCP for network communication
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	return &tcpServer{
		listener: listener,
		config:   config,
	}, nil
}

// tcpServer for TCP fallback on Windows
type tcpServer struct {
	listener net.Listener
	config   *Config
	mu       sync.RWMutex
	closed   bool
}

func (s *tcpServer) Accept() (Client, error) {
	return s.AcceptWithContext(context.Background())
}

func (s *tcpServer) AcceptWithContext(ctx context.Context) (Client, error) {
	start := time.Now()
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrServerClosed
	}
	config := s.config
	s.mu.RUnlock()

	if deadline, ok := ctx.Deadline(); ok {
		if listener, ok := s.listener.(*net.TCPListener); ok {
			if err := listener.SetDeadline(deadline); err != nil {
				return nil, err
			}
			defer listener.SetDeadline(time.Time{})
		}
	}

	conn, err := s.listener.Accept()
	duration := time.Since(start)

	// Log and record metrics
	if config.Logger != nil {
		fields := log.Fields{
			"operation": "accept",
			"addr":      s.Addr(),
			"transport": "tcp",
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
		config.Metrics.ObserveIPC(ctx, "accept", s.Addr(), "tcp", 0, duration, err)
	}

	if err != nil {
		return nil, err
	}

	// Enable keepalive for TCP connections
	if tcpConn, ok := conn.(*net.TCPConn); ok && s.config.KeepAlive {
		if err := tcpConn.SetKeepAlive(true); err == nil {
			tcpConn.SetKeepAlivePeriod(s.config.KeepAlivePeriod)
		}
	}

	return &winClient{
		conn:   conn,
		config: s.config,
	}, nil
}

func (s *tcpServer) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

func (s *tcpServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *tcpServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}

	// Wait for context cancellation (graceful period)
	<-ctx.Done()

	return err
}

func (s *winServer) Accept() (Client, error) {
	return s.AcceptWithContext(context.Background())
}

func (s *winServer) AcceptWithContext(ctx context.Context) (Client, error) {
	start := time.Now()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrServerClosed
	}
	handle := s.next
	s.next = 0
	config := s.config
	s.mu.Unlock()

	if handle == 0 {
		var err error
		handle, err = createNamedPipe(s.pipeName, s.config.BufferSize)
		if err != nil {
			return nil, err
		}
	}

	var connectErr error
	for {
		if err := connectNamedPipeWithContext(ctx, handle); err != nil {
			if err == errorPipeClosing {
				go syscall.CloseHandle(handle)
				if ctx.Err() != nil {
					connectErr = ctx.Err()
					break
				}
				handle, err = createNamedPipe(s.pipeName, s.config.BufferSize)
				if err != nil {
					connectErr = err
					break
				}
				continue
			}
			go syscall.CloseHandle(handle)
			connectErr = err
			break
		}
		break
	}

	duration := time.Since(start)

	// Log and record metrics
	if config.Logger != nil {
		fields := log.Fields{
			"operation": "accept",
			"addr":      s.pipeName,
			"transport": "pipe",
			"duration":  duration,
		}
		if connectErr != nil {
			fields["error"] = connectErr.Error()
			config.Logger.Error("IPC accept failed", fields)
		} else {
			config.Logger.Debug("IPC accept succeeded", fields)
		}
	}

	if config.Metrics != nil {
		config.Metrics.ObserveIPC(ctx, "accept", s.pipeName, "pipe", 0, duration, connectErr)
	}

	if connectErr != nil {
		return nil, connectErr
	}

	// Asynchronously prepare the next handle to reduce Accept latency
	go s.prepareNextHandle()

	return &winClient{
		handle: handle,
		config: s.config,
	}, nil
}

func (s *winServer) Addr() string {
	return s.pipeName
}

func (s *winServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	if s.next != 0 {
		_ = syscall.CloseHandle(s.next)
		s.next = 0
	}
	return nil
}

func (s *winServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.next != 0 {
		_ = syscall.CloseHandle(s.next)
		s.next = 0
	}
	s.mu.Unlock()

	// Wait for context cancellation (graceful period)
	<-ctx.Done()

	return nil
}

func dialPlatform(addr string, config *Config) (Client, error) {
	return dialPlatformWithContext(context.Background(), addr, config)
}

func dialPlatformWithContext(ctx context.Context, addr string, config *Config) (Client, error) {
	start := time.Now()
	if config == nil {
		config = DefaultConfig()
	}

	// Try named pipe first for local communication
	if isNamedPipeAddr(addr) || !strings.Contains(addr, ":") {
		pipeName := normalizePipeAddr(addr)
		handle, err := dialNamedPipe(ctx, pipeName, config.ConnectTimeout)
		duration := time.Since(start)

		// Log and record metrics
		if config.Logger != nil {
			fields := log.Fields{
				"operation": "dial",
				"addr":      pipeName,
				"transport": "pipe",
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
			config.Metrics.ObserveIPC(ctx, "dial", pipeName, "pipe", 0, duration, err)
		}

		if err != nil {
			return nil, err
		}
		return &winClient{
			handle: handle,
			config: config,
		}, nil
	}

	// Fallback to TCP
	dialer := &net.Dialer{
		Timeout:   config.ConnectTimeout,
		KeepAlive: config.KeepAlivePeriod,
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	duration := time.Since(start)

	// Log and record metrics
	if config.Logger != nil {
		fields := log.Fields{
			"operation": "dial",
			"addr":      addr,
			"transport": "tcp",
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
		config.Metrics.ObserveIPC(ctx, "dial", addr, "tcp", 0, duration, err)
	}

	if err != nil {
		return nil, err
	}

	// Enable keepalive for TCP connections
	if tcpConn, ok := conn.(*net.TCPConn); ok && config.KeepAlive {
		if setErr := tcpConn.SetKeepAlive(true); setErr == nil {
			tcpConn.SetKeepAlivePeriod(config.KeepAlivePeriod)
		}
	}

	return &winClient{
		conn:   conn,
		config: config,
	}, nil
}

func createNamedPipe(pipeName string, bufSize int) (syscall.Handle, error) {
	pipeNamePtr, err := syscall.UTF16PtrFromString(pipeName)
	if err != nil {
		return 0, err
	}

	handle, _, _ := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(pipeNamePtr)),
		PIPE_ACCESS_DUPLEX,
		PIPE_TYPE_BYTE|PIPE_READMODE_BYTE|PIPE_WAIT,
		PIPE_UNLIMITED_INSTANCES,
		uintptr(bufSize),
		uintptr(bufSize),
		0,
		0,
	)

	if handle == INVALID_HANDLE_VALUE {
		return 0, fmt.Errorf("failed to create named pipe")
	}

	return syscall.Handle(handle), nil
}

// connectNamedPipeWithContext attempts to connect a named pipe with context cancellation.
// Note: On Windows, ConnectNamedPipe is a blocking system call that cannot be truly
// interrupted. If the context is cancelled, we close the handle which will eventually
// unblock the system call, but there may be a brief delay. The goroutine will complete
// once the system call returns.
func connectNamedPipeWithContext(ctx context.Context, handle syscall.Handle) error {
	type result struct {
		err    error
		handle syscall.Handle
	}
	done := make(chan result, 1)

	go func() {
		ret, _, err := procConnectNamedPipe.Call(uintptr(handle), 0)
		if ret == 0 {
			if err == errorPipeConnected {
				done <- result{err: nil, handle: handle}
				return
			}
			if err != nil {
				done <- result{err: err, handle: handle}
				return
			}
			done <- result{err: fmt.Errorf("failed to connect named pipe"), handle: handle}
			return
		}
		done <- result{err: nil, handle: handle}
	}()

	select {
	case <-ctx.Done():
		// Close the handle to unblock the ConnectNamedPipe call
		// The goroutine will complete shortly after the handle is closed
		syscall.CloseHandle(handle)
		// Wait for the goroutine to finish to avoid leaks
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-done:
			if !timer.Stop() {
				<-timer.C
			}
			// Goroutine completed
		case <-timer.C:
			// Timeout waiting for goroutine - it will complete eventually
			// This is acceptable as we've closed the handle
		}
		return ctx.Err()
	case res := <-done:
		return res.err
	}
}

func (s *winServer) prepareNextHandle() {
	s.mu.RLock()
	closed := s.closed
	hasNext := s.next != 0
	s.mu.RUnlock()
	if closed || hasNext {
		return
	}

	handle, err := createNamedPipe(s.pipeName, s.config.BufferSize)
	if err != nil {
		return
	}

	s.mu.Lock()
	if s.closed || s.next != 0 {
		s.mu.Unlock()
		_ = syscall.CloseHandle(handle)
		return
	}
	s.next = handle
	s.mu.Unlock()
}

func isNamedPipeAddr(addr string) bool {
	return strings.HasPrefix(addr, `\\.\pipe\`)
}

func normalizePipeAddr(addr string) string {
	if isNamedPipeAddr(addr) {
		return addr
	}
	return fmt.Sprintf(`\\.\pipe\%s`, addr)
}

func dialNamedPipe(ctx context.Context, pipeName string, timeout time.Duration) (syscall.Handle, error) {
	pipeNamePtr, err := syscall.UTF16PtrFromString(pipeName)
	if err != nil {
		return 0, err
	}

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, fmt.Errorf("named pipe connect timeout")
		}
		procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(pipeNamePtr)), uintptr(remaining/time.Millisecond))

		handle, err := syscall.CreateFile(
			pipeNamePtr,
			GENERIC_READ|GENERIC_WRITE,
			0,
			nil,
			OPEN_EXISTING,
			0,
			0,
		)
		if err == nil {
			return handle, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if time.Now().After(deadline) {
			return 0, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *winClient) Write(data []byte) (int, error) {
	return c.WriteWithTimeout(data, c.config.WriteTimeout)
}

func (c *winClient) WriteWithTimeout(data []byte, timeout time.Duration) (int, error) {
	start := time.Now()
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return 0, ErrClientClosed
	}
	conn := c.conn
	handle := c.handle
	config := c.config
	c.mu.RUnlock()

	var n int
	var err error
	var transport string

	if conn != nil {
		transport = "tcp"
		if timeout > 0 {
			conn.SetWriteDeadline(time.Now().Add(timeout))
			defer conn.SetWriteDeadline(time.Time{})
		}
		n, err = conn.Write(data)
	} else if handle != 0 {
		transport = "pipe"
		var written uint32
		err = syscall.WriteFile(handle, data, &written, nil)
		n = int(written)
	} else {
		return 0, fmt.Errorf("no valid connection")
	}

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
		config.Metrics.ObserveIPC(context.Background(), "write", "", transport, n, duration, err)
	}

	return n, err
}

func (c *winClient) Read(buf []byte) (int, error) {
	return c.ReadWithTimeout(buf, c.config.ReadTimeout)
}

func (c *winClient) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	start := time.Now()
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return 0, ErrClientClosed
	}
	conn := c.conn
	handle := c.handle
	config := c.config
	c.mu.RUnlock()

	var n int
	var err error
	var transport string

	if conn != nil {
		transport = "tcp"
		if timeout > 0 {
			conn.SetReadDeadline(time.Now().Add(timeout))
			defer conn.SetReadDeadline(time.Time{})
		}
		n, err = conn.Read(buf)
	} else if handle != 0 {
		transport = "pipe"
		var read uint32
		err = syscall.ReadFile(handle, buf, &read, nil)
		n = int(read)
	} else {
		return 0, fmt.Errorf("no valid connection")
	}

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
		config.Metrics.ObserveIPC(context.Background(), "read", "", transport, n, duration, err)
	}

	return n, err
}

func (c *winClient) RemoteAddr() net.Addr {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.conn != nil {
		addr := c.conn.RemoteAddr()
		if addr != nil {
			return addr
		}
	}
	// Return a default IPC address for named pipe
	return NewAddr("pipe", "named-pipe")
}

func (c *winClient) RemoteAddrString() string {
	addr := c.RemoteAddr()
	if addr != nil {
		return addr.String()
	}
	return ""
}

func (c *winClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	var err error
	if c.conn != nil {
		err = c.conn.Close()
	}

	if c.handle != 0 {
		procDisconnectNamedPipe.Call(uintptr(c.handle))
		if closeErr := syscall.CloseHandle(c.handle); closeErr != nil && err == nil {
			err = closeErr
		}
	}

	return err
}
