package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/spcent/plumego/log"
	"github.com/spcent/plumego/metrics"
	"time"
)

func optionsFromConfig(cfg *Config) []Option {
	if cfg == nil {
		return nil
	}

	opts := []Option{
		WithConnectTimeout(cfg.ConnectTimeout),
		WithReadTimeout(cfg.ReadTimeout),
		WithWriteTimeout(cfg.WriteTimeout),
		WithBufferSize(cfg.BufferSize),
		WithKeepAlive(cfg.KeepAlive),
		WithKeepAlivePeriod(cfg.KeepAlivePeriod),
	}

	if cfg.UnixSocketPerm != 0 {
		opts = append(opts, WithUnixSocketPerm(cfg.UnixSocketPerm))
	}
	if cfg.UnixSocketDirPerm != 0 {
		opts = append(opts, WithUnixSocketDirPerm(cfg.UnixSocketDirPerm))
	}
	if cfg.Metrics != nil {
		opts = append(opts, WithMetrics(cfg.Metrics))
	}
	if cfg.Logger != nil {
		opts = append(opts, WithLogger(cfg.Logger))
	}
	if cfg.WindowsSecuritySDDL != "" {
		opts = append(opts, WithWindowsSecuritySDDL(cfg.WindowsSecuritySDDL))
	}

	return opts
}

func ExampleServer() {
	// Create server with custom config
	config := &Config{
		ConnectTimeout: 5 * time.Second,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		BufferSize:     8192,
	}

	// Use a unique temporary socket path so the example is runnable and leaves
	// no residue in the package directory.
	addr := filepath.Join(os.TempDir(), fmt.Sprintf("plumego-ipc-example-%d.sock", time.Now().UnixNano()))
	server, err := NewServer(addr, optionsFromConfig(config)...)
	if err != nil {
		stdlog.Fatal(err)
	}
	defer server.Close()

	// Accept connections and echo each client's messages back.
	go func() {
		for {
			client, acceptErr := server.Accept()
			if acceptErr != nil {
				return
			}
			go handleClient(client)
		}
	}()

	// Client connection example
	client, err := Dial(server.Addr(), optionsFromConfig(config)...)
	if err != nil {
		stdlog.Fatal(err)
	}
	defer client.Close()

	// Send data with timeout
	data := []byte("Hello, IPC!")
	n, err := client.WriteWithTimeout(data, 5*time.Second)
	if err != nil {
		stdlog.Fatal(err)
	}
	fmt.Printf("Sent %d bytes\n", n)

	// Read the echoed response
	buf := make([]byte, 1024)
	n, err = client.ReadWithTimeout(buf, 5*time.Second)
	if err != nil {
		stdlog.Fatal(err)
	}
	fmt.Printf("Received: %s\n", string(buf[:n]))

	// Output:
	// Sent 11 bytes
	// Received: Hello, IPC!
}

func handleClient(client Client) {
	defer client.Close()

	buf := make([]byte, 1024)
	for {
		n, err := client.Read(buf)
		if err != nil {
			return
		}

		// Echo back the data
		client.Write(buf[:n])
	}
}

// TestConfig tests the configuration system
func TestConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := DefaultConfig()
		if config == nil {
			t.Fatal("DefaultConfig returned nil")
		}
		if config.ConnectTimeout != 10*time.Second {
			t.Errorf("Expected ConnectTimeout 10s, got %v", config.ConnectTimeout)
		}
		if config.BufferSize != 4096 {
			t.Errorf("Expected BufferSize 4096, got %d", config.BufferSize)
		}
	})
}

// TestServerBasic tests basic server functionality
func TestServerBasic(t *testing.T) {
	t.Run("CreateAndClose", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}

		addr := server.Addr()
		if addr == "" {
			t.Error("Server address is empty")
		}

		if err := server.Close(); err != nil {
			t.Errorf("Failed to close server: %v", err)
		}
	})

	t.Run("WithCustomConfig", func(t *testing.T) {
		config := &Config{
			ConnectTimeout: 5 * time.Second,
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			BufferSize:     8192,
		}

		server, err := NewServer(getTestAddr(), optionsFromConfig(config)...)
		if err != nil {
			t.Fatalf("Failed to create server with config: %v", err)
		}
		defer server.Close()

		if server.Addr() == "" {
			t.Error("Server address is empty")
		}
	})

	t.Run("MultipleClose", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}

		// Close multiple times should not error
		if err := server.Close(); err != nil {
			t.Errorf("First close failed: %v", err)
		}
		if err := server.Close(); err != nil {
			t.Errorf("Second close failed: %v", err)
		}
	})
}

// TestClientServer tests client-server communication
func TestClientServer(t *testing.T) {
	t.Run("BasicCommunication", func(t *testing.T) {
		server, client := setupTestPair(t)
		defer server.Close()
		defer client.Close()

		testData := []byte("Hello, IPC!")

		// Write from client
		n, err := client.Write(testData)
		if err != nil {
			t.Fatalf("Client write failed: %v", err)
		}
		if n != len(testData) {
			t.Errorf("Expected to write %d bytes, wrote %d", len(testData), n)
		}

		// Accept and read from server
		serverClient, err := server.Accept()
		if err != nil {
			t.Fatalf("Server accept failed: %v", err)
		}
		defer serverClient.Close()

		buf := make([]byte, 1024)
		n, err = serverClient.Read(buf)
		if err != nil {
			t.Fatalf("Server read failed: %v", err)
		}

		received := buf[:n]
		if string(received) != string(testData) {
			t.Errorf("Expected %q, got %q", testData, received)
		}
	})

	t.Run("BidirectionalCommunication", func(t *testing.T) {
		server, client := setupTestPair(t)
		defer server.Close()
		defer client.Close()

		var wg sync.WaitGroup
		wg.Add(2)

		// Server goroutine
		go func() {
			defer wg.Done()
			serverClient, err := server.Accept()
			if err != nil {
				t.Errorf("Server accept failed: %v", err)
				return
			}
			defer serverClient.Close()

			// Read from client
			buf := make([]byte, 1024)
			n, err := serverClient.Read(buf)
			if err != nil {
				t.Errorf("Server read failed: %v", err)
				return
			}

			// Echo back with prefix
			response := append([]byte("Echo: "), buf[:n]...)
			if _, err := serverClient.Write(response); err != nil {
				t.Errorf("Server write failed: %v", err)
			}
		}()

		// Client goroutine
		go func() {
			defer wg.Done()

			// Wait a bit for server to be ready
			time.Sleep(100 * time.Millisecond)

			testData := []byte("Hello from client")
			if _, err := client.Write(testData); err != nil {
				t.Errorf("Client write failed: %v", err)
				return
			}

			// Read response
			buf := make([]byte, 1024)
			n, err := client.Read(buf)
			if err != nil {
				t.Errorf("Client read failed: %v", err)
				return
			}

			expected := "Echo: Hello from client"
			if string(buf[:n]) != expected {
				t.Errorf("Expected %q, got %q", expected, string(buf[:n]))
			}
		}()

		wg.Wait()
	})

	t.Run("MultipleClients", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		numClients := 5
		var wg sync.WaitGroup
		wg.Add(numClients)
		var acceptWG sync.WaitGroup
		acceptWG.Add(numClients)

		// Start clients
		for i := 0; i < numClients; i++ {
			go func(clientID int) {
				defer wg.Done()

				client, err := Dial(server.Addr())
				if err != nil {
					t.Errorf("Client %d dial failed: %v", clientID, err)
					return
				}
				defer client.Close()

				data := fmt.Sprintf("Client %d data", clientID)
				if _, err := client.Write([]byte(data)); err != nil {
					t.Errorf("Client %d write failed: %v", clientID, err)
				}
			}(i)
		}

		// Accept clients on server
		for i := 0; i < numClients; i++ {
			go func() {
				defer acceptWG.Done()
				serverClient, err := server.Accept()
				if err != nil {
					// Ignore closed server during shutdown
					if errors.Is(err, net.ErrClosed) {
						return
					}
					if strings.Contains(err.Error(), "use of closed network connection") {
						return
					}
					t.Errorf("Server accept failed: %v", err)
					return
				}
				defer serverClient.Close()

				buf := make([]byte, 1024)
				if _, err := serverClient.Read(buf); err != nil {
					if !errors.Is(err, io.EOF) {
						t.Errorf("Server read failed: %v", err)
					}
				}
			}()
		}

		wg.Wait()
		acceptWG.Wait()
	})
}

// TestWithTimeout tests timeout functionality
func TestWithTimeout(t *testing.T) {
	t.Run("WriteTimeout", func(t *testing.T) {
		server, client := setupTestPair(t)
		defer server.Close()
		defer client.Close()

		// This should succeed quickly
		data := []byte("test data")
		n, err := client.WriteWithTimeout(data, 1*time.Second)
		if err != nil {
			t.Errorf("WriteWithTimeout failed: %v", err)
		}
		if n != len(data) {
			t.Errorf("Expected to write %d bytes, wrote %d", len(data), n)
		}
	})

	t.Run("ReadTimeout", func(t *testing.T) {
		server, client := setupTestPair(t)
		defer server.Close()
		defer client.Close()

		// Start a server that delays response
		go func() {
			serverClient, err := server.Accept()
			if err != nil {
				return
			}
			defer serverClient.Close()

			// Read but don't respond immediately
			buf := make([]byte, 1024)
			_, _ = serverClient.Read(buf)

			// Delay response
			time.Sleep(200 * time.Millisecond)
			serverClient.Write([]byte("delayed response"))
		}()

		// Send data
		client.Write([]byte("test"))

		// Try to read with short timeout (should work since we delay only 200ms)
		buf := make([]byte, 1024)
		_, err := client.ReadWithTimeout(buf, 500*time.Millisecond)
		if err != nil {
			t.Errorf("ReadWithTimeout should have succeeded: %v", err)
		}
	})

	t.Run("ConnectTimeout", func(t *testing.T) {
		config := &Config{
			ConnectTimeout: 100 * time.Millisecond,
			ReadTimeout:    1 * time.Second,
			WriteTimeout:   1 * time.Second,
			BufferSize:     4096,
		}

		// Try to connect to non-existent address
		_, err := Dial("127.0.0.1:0", optionsFromConfig(config)...)
		if err == nil {
			t.Error("Expected connect timeout error")
		}
	})
}

// TestWithContext tests context functionality
func TestWithContext(t *testing.T) {
	t.Run("AcceptWithContext", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()

		// This should timeout since no client connects
		_, err = server.AcceptWithContext(ctx)
		if err == nil {
			t.Error("Expected context timeout error")
		}
	})

	t.Run("DialWithContext", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
		defer cancel()

		client, err := DialWithContext(ctx, server.Addr())
		if err != nil {
			t.Fatalf("DialWithContext failed: %v", err)
		}
		defer client.Close()

		addr := client.RemoteAddr()
		if addr == nil || addr.String() == "" {
			t.Error("Client RemoteAddr is empty")
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		ctx, cancel := context.WithCancel(t.Context())

		// Cancel immediately
		cancel()

		_, err = DialWithContext(ctx, server.Addr())
		if err == nil {
			t.Error("Expected context cancellation error")
		}
	})
}

// TestErrorConditions tests various error conditions
func TestErrorConditions(t *testing.T) {
	t.Run("WriteToClosedClient", func(t *testing.T) {
		server, client := setupTestPair(t)
		defer server.Close()

		client.Close()

		_, err := client.Write([]byte("test"))
		if err == nil {
			t.Error("Expected error writing to closed client")
		}
	})

	t.Run("ReadFromClosedClient", func(t *testing.T) {
		server, client := setupTestPair(t)
		defer server.Close()

		client.Close()

		buf := make([]byte, 1024)
		_, err := client.Read(buf)
		if err == nil {
			t.Error("Expected error reading from closed client")
		}
	})

	t.Run("AcceptFromClosedServer", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}

		server.Close()

		_, err = server.Accept()
		if err == nil {
			t.Error("Expected error accepting from closed server")
		}
	})

	t.Run("DialNonExistentServer", func(t *testing.T) {
		_, err := Dial("127.0.0.1:1") // Port 1 should be unavailable
		if err == nil {
			t.Error("Expected error dialing non-existent server")
		}
	})
}

// TestConcurrency tests concurrent operations
func TestConcurrency(t *testing.T) {
	t.Run("ConcurrentReadsWrites", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		numRoutines := 5
		var clientWG sync.WaitGroup
		var serverWG sync.WaitGroup
		clientWG.Add(numRoutines)
		serverWG.Add(numRoutines)

		for i := 0; i < numRoutines; i++ {
			go func() {
				defer serverWG.Done()
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()

				serverClient, err := server.AcceptWithContext(ctx)
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						return
					}
					t.Errorf("AcceptWithContext failed: %v", err)
					return
				}
				defer serverClient.Close()

				buf := make([]byte, 64)
				if _, err := serverClient.ReadWithTimeout(buf, time.Second); err != nil {
					if nerr, ok := err.(net.Error); !ok || !nerr.Timeout() {
						t.Errorf("ReadWithTimeout failed: %v", err)
					}
				}
			}()
		}

		for i := 0; i < numRoutines; i++ {
			go func(id int) {
				defer clientWG.Done()
				client, err := Dial(server.Addr())
				if err != nil {
					t.Errorf("Dial failed: %v", err)
					return
				}
				defer client.Close()

				data := fmt.Sprintf("client-%d", id)
				if _, err := client.WriteWithTimeout([]byte(data), time.Second); err != nil {
					t.Errorf("WriteWithTimeout failed: %v", err)
				}
			}(i)
		}

		clientWG.Wait()
		serverWG.Wait()
	})

	t.Run("ConcurrentAccepts", func(t *testing.T) {
		server, err := NewServer(getTestAddr())
		if err != nil {
			t.Fatalf("Failed to create server: %v", err)
		}
		defer server.Close()

		numClients := 5
		var wg sync.WaitGroup
		wg.Add(numClients * 2) // clients + server accepts

		// Start accept goroutines
		for i := 0; i < numClients; i++ {
			go func() {
				defer wg.Done()
				serverClient, err := server.Accept()
				if err != nil {
					t.Errorf("Accept failed: %v", err)
					return
				}
				serverClient.Close()
			}()
		}

		// Start client connections
		for i := 0; i < numClients; i++ {
			go func() {
				defer wg.Done()
				client, err := Dial(server.Addr())
				if err != nil {
					t.Errorf("Dial failed: %v", err)
					return
				}
				client.Close()
			}()
		}

		wg.Wait()
	})
}

// TestLargeData tests handling of large data transfers
func TestLargeData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large data test in short mode")
	}

	server, client := setupTestPair(t)
	defer server.Close()
	defer client.Close()

	serverClient, err := server.Accept()
	if err != nil {
		t.Fatalf("Server accept failed: %v", err)
	}
	defer serverClient.Close()

	// Test with 1MB of data
	dataSize := 1024 * 1024
	testData := make([]byte, dataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	var readData []byte
	var readErr error

	// Server reads all data
	go func() {
		defer wg.Done()
		readData = make([]byte, 0, dataSize)
		buf := make([]byte, 4096)

		for len(readData) < dataSize {
			n, err := serverClient.Read(buf)
			if err != nil {
				if err != io.EOF {
					readErr = err
				}
				break
			}
			readData = append(readData, buf[:n]...)
		}
	}()

	// Client sends all data
	go func() {
		defer wg.Done()

		sent := 0
		for sent < len(testData) {
			n, err := client.Write(testData[sent:])
			if err != nil {
				t.Errorf("Write failed: %v", err)
				break
			}
			sent += n
		}
	}()

	wg.Wait()

	if readErr != nil {
		t.Fatalf("Read error: %v", readErr)
	}

	if len(readData) != len(testData) {
		t.Errorf("Expected %d bytes, got %d", len(testData), len(readData))
	}

	// Verify data integrity
	for i, b := range testData {
		if i >= len(readData) || readData[i] != b {
			t.Errorf("Data mismatch at position %d: expected %d, got %d", i, b, readData[i])
			break
		}
	}
}

// Benchmark tests
func BenchmarkClientServer(b *testing.B) {
	server, client := setupTestPairB(b)
	defer server.Close()
	defer client.Close()

	serverClient, err := server.Accept()
	if err != nil {
		b.Fatalf("Server accept failed: %v", err)
	}
	defer serverClient.Close()

	data := []byte("benchmark data")
	buf := make([]byte, 1024)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			client.Write(data)
			_, _ = serverClient.Read(buf)
		}
	})
}

func BenchmarkLargeWrite(b *testing.B) {
	server, client := setupTestPairB(b)
	defer server.Close()
	defer client.Close()

	serverClient, err := server.Accept()
	if err != nil {
		b.Fatalf("Server accept failed: %v", err)
	}
	defer serverClient.Close()

	data := make([]byte, 64*1024) // 64KB
	buf := make([]byte, 64*1024)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))

	for i := 0; i < b.N; i++ {
		go func() { _, _ = serverClient.Read(buf) }()
		client.Write(data)
	}
}

// Helper functions

func getTestAddr() string {
	if runtime.GOOS == "windows" {
		return "127.0.0.1:0"
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("test_socket_%d_%d", os.Getpid(), time.Now().UnixNano()))
}

func setupTestPair(t *testing.T) (Server, Client) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	client, err := Dial(server.Addr())
	if err != nil {
		server.Close()
		t.Fatalf("Failed to create client: %v", err)
	}

	return server, client
}

func setupTestPairB(b *testing.B) (Server, Client) {
	addr := getTestAddrB()
	server, err := NewServer(addr)
	if err != nil {
		b.Fatalf("Failed to create server: %v", err)
	}

	client, err := Dial(server.Addr())
	if err != nil {
		server.Close()
		b.Fatalf("Failed to create client: %v", err)
	}

	return server, client
}

func getTestAddrB() string {
	if runtime.GOOS == "windows" {
		return "127.0.0.1:0"
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("bench_socket_%d_%d", os.Getpid(), time.Now().UnixNano()))
}

// TestMetricsIntegration tests metrics collection
func TestMetricsIntegration(t *testing.T) {
	collector := &testMetricsCollector{
		NoopCollector: metrics.NewNoopCollector(),
		records:       make(map[string]int),
	}

	addr := getTestAddr()
	server, err := NewServer(addr,
		WithMetrics(collector),
	)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Accept connection in background
	var acceptedClient Client
	acceptDone := make(chan error, 1)
	go func() {
		var acceptErr error
		acceptedClient, acceptErr = server.Accept()
		acceptDone <- acceptErr
	}()

	// Connect client
	client, err := Dial(server.Addr(),
		WithMetrics(collector),
	)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer client.Close()

	// Wait for accept to complete
	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	defer acceptedClient.Close()

	// Write some data
	data := []byte("test data")
	if _, err := client.Write(data); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read the data
	buf := make([]byte, len(data))
	if _, err := acceptedClient.Read(buf); err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// Verify metrics were collected
	collector.mu.Lock()
	defer collector.mu.Unlock()

	if collector.records["accept"] < 1 {
		t.Errorf("Expected at least 1 accept metric, got %d", collector.records["accept"])
	}
	if collector.records["dial"] < 1 {
		t.Errorf("Expected at least 1 dial metric, got %d", collector.records["dial"])
	}
	if collector.records["write"] < 1 {
		t.Errorf("Expected at least 1 write metric, got %d", collector.records["write"])
	}
	if collector.records["read"] < 1 {
		t.Errorf("Expected at least 1 read metric, got %d", collector.records["read"])
	}
}

// TestLoggingIntegration tests structured logging
func TestLoggingIntegration(t *testing.T) {
	logger := &testLogger{
		logs: make([]logEntry, 0),
	}

	addr := getTestAddr()
	server, err := NewServer(addr,
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Accept connection in background
	var acceptedClient Client
	acceptDone := make(chan error, 1)
	go func() {
		var acceptErr error
		acceptedClient, acceptErr = server.Accept()
		acceptDone <- acceptErr
	}()

	// Connect client with logger
	client, err := Dial(server.Addr(),
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer client.Close()

	// Wait for accept to complete
	if err := <-acceptDone; err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	defer acceptedClient.Close()

	// Write some data to trigger logging
	data := []byte("test data")
	if _, err := client.Write(data); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Verify logs were collected
	logger.mu.Lock()
	defer logger.mu.Unlock()

	if len(logger.logs) < 2 {
		t.Errorf("Expected at least 2 log entries, got %d", len(logger.logs))
	}

	// Verify log entries have required fields
	for i, entry := range logger.logs {
		if entry.fields["operation"] == nil {
			t.Errorf("Log entry %d missing operation field", i)
		}
		if entry.fields["duration"] == nil {
			t.Errorf("Log entry %d missing duration field", i)
		}
	}
}

// testMetricsCollector is a simple test implementation of MetricsCollector.
// It embeds NoopCollector for default implementations and only overrides ObserveIPC
// to track IPC operation counts.
type testMetricsCollector struct {
	*metrics.NoopCollector
	mu      sync.Mutex
	records map[string]int
}

func (c *testMetricsCollector) ObserveIPC(ctx context.Context, operation, addr, transport string, bytes int, duration time.Duration, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records[operation]++
}

// testLogger is a simple test implementation of StructuredLogger
type testLogger struct {
	mu   sync.Mutex
	logs []logEntry
}

type logEntry struct {
	level  string
	msg    string
	fields log.Fields
}

func (l *testLogger) WithFields(fields log.Fields) log.StructuredLogger {
	return l
}

func (l *testLogger) With(_ string, _ any) log.StructuredLogger {
	return l
}

func firstGlogField(fields []log.Fields) log.Fields {
	if len(fields) > 0 {
		return fields[0]
	}
	return nil
}

func (l *testLogger) Debug(msg string, fields ...log.Fields) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, logEntry{level: "debug", msg: msg, fields: copyGlogFields(firstGlogField(fields))})
}

func (l *testLogger) Info(msg string, fields ...log.Fields) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, logEntry{level: "info", msg: msg, fields: copyGlogFields(firstGlogField(fields))})
}

func (l *testLogger) Warn(msg string, fields ...log.Fields) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, logEntry{level: "warn", msg: msg, fields: copyGlogFields(firstGlogField(fields))})
}

func (l *testLogger) Error(msg string, fields ...log.Fields) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, logEntry{level: "error", msg: msg, fields: copyGlogFields(firstGlogField(fields))})
}

func (l *testLogger) DebugCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.Debug(msg, firstGlogField(fields))
}

func (l *testLogger) InfoCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.Info(msg, firstGlogField(fields))
}

func (l *testLogger) WarnCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.Warn(msg, firstGlogField(fields))
}

func (l *testLogger) ErrorCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.Error(msg, firstGlogField(fields))
}

func (l *testLogger) Fatal(msg string, fields ...log.Fields) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, logEntry{level: "fatal", msg: msg, fields: copyGlogFields(firstGlogField(fields))})
}

func (l *testLogger) FatalCtx(ctx context.Context, msg string, fields ...log.Fields) {
	l.Fatal(msg, firstGlogField(fields))
}

func copyGlogFields(fields log.Fields) log.Fields {
	result := make(log.Fields)
	for k, v := range fields {
		result[k] = v
	}
	return result
}
