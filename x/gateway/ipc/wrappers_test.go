package ipc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// ---- FramedClient tests ----

func TestFramedClientRoundTrip(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	sender := NewFramedClient(rawClient)
	receiver := NewFramedClient(serverConn)

	msg := []byte("hello framed world")
	if err := sender.WriteMessage(msg); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	got, err := receiver.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("got %q, want %q", got, msg)
	}
}

func TestFramedClientEmptyMessage(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	sender := NewFramedClient(rawClient)
	receiver := NewFramedClient(serverConn)

	if err := sender.WriteMessage([]byte{}); err != nil {
		t.Fatalf("WriteMessage empty: %v", err)
	}
	got, err := receiver.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty message, got %d bytes", len(got))
	}
}

func TestFramedClientMaxFrameSizeRejected(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	fc := NewFramedClient(rawClient)
	oversized := make([]byte, maxFrameSize+1)
	err := fc.WriteMessage(oversized)
	if err == nil {
		t.Fatal("expected error for oversized message, got nil")
	}
	if !strings.Contains(err.Error(), "message too large") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestFramedClientMultipleMessages(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	sender := NewFramedClient(rawClient)
	receiver := NewFramedClient(serverConn)

	messages := []string{"one", "two", "three", "four", "five"}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, m := range messages {
			if werr := sender.WriteMessage([]byte(m)); werr != nil {
				t.Errorf("WriteMessage %q: %v", m, werr)
				return
			}
		}
	}()

	var received []string
	for range messages {
		got, rerr := receiver.ReadMessage()
		if rerr != nil {
			t.Fatalf("ReadMessage: %v", rerr)
		}
		received = append(received, string(got))
	}
	wg.Wait()

	for i, want := range messages {
		if received[i] != want {
			t.Fatalf("message[%d] = %q, want %q", i, received[i], want)
		}
	}
}

func TestFramedClientWithTimeout(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	sender := NewFramedClient(rawClient)
	receiver := NewFramedClient(serverConn)

	msg := []byte("timed message")
	if err := sender.WriteMessageWithTimeout(msg, time.Second); err != nil {
		t.Fatalf("WriteMessageWithTimeout: %v", err)
	}
	got, err := receiver.ReadMessageWithTimeout(time.Second)
	if err != nil {
		t.Fatalf("ReadMessageWithTimeout: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("got %q, want %q", got, msg)
	}
}

func TestFramedClientDelegatesRawReadWrite(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	fc := NewFramedClient(rawClient)
	data := []byte("raw bypass")

	if _, err := fc.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, len(data))
	if _, err := io.ReadFull(serverConn, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(buf, data) {
		t.Fatalf("got %q, want %q", buf, data)
	}
}

func TestFramedClientConcurrentWrites(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	sender := NewFramedClient(rawClient)
	receiver := NewFramedClient(serverConn)

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if werr := sender.WriteMessage([]byte("concurrent")); werr != nil {
				t.Errorf("WriteMessage: %v", werr)
			}
		}()
	}

	// Drain all messages
	done := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			if _, rerr := receiver.ReadMessage(); rerr != nil {
				t.Errorf("ReadMessage: %v", rerr)
				return
			}
		}
		close(done)
	}()

	wg.Wait()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent messages")
	}
}

// ---- StreamClient tests ----

func TestStreamClientRoundTrip(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	sender := NewStreamClient(rawClient, nil)
	receiver := NewStreamClient(serverConn, nil)

	payload := []byte(strings.Repeat("x", 256*1024)) // 256 KB
	var totalSent int64
	var sendErr error

	done := make(chan struct{})
	go func() {
		defer close(done)
		totalSent, sendErr = sender.WriteStream(bytes.NewReader(payload))
	}()

	var buf bytes.Buffer
	totalRecv, err := receiver.ReadStream(&buf)
	<-done

	if sendErr != nil {
		t.Fatalf("WriteStream: %v", sendErr)
	}
	if err != nil {
		t.Fatalf("ReadStream: %v", err)
	}
	if totalSent != int64(len(payload)) {
		t.Fatalf("sent %d bytes, want %d", totalSent, len(payload))
	}
	if totalRecv != int64(len(payload)) {
		t.Fatalf("received %d bytes, want %d", totalRecv, len(payload))
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatal("stream data mismatch")
	}
}

func TestStreamClientEmptyStream(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	sender := NewStreamClient(rawClient, nil)
	receiver := NewStreamClient(serverConn, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, werr := sender.WriteStream(bytes.NewReader(nil)); werr != nil {
			t.Errorf("WriteStream empty: %v", werr)
		}
	}()

	var buf bytes.Buffer
	n, err := receiver.ReadStream(&buf)
	<-done
	if err != nil {
		t.Fatalf("ReadStream empty: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes received, got %d", n)
	}
}

func TestStreamClientCustomChunkSize(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	cfg := &StreamConfig{ChunkSize: 1024, BufferSize: 2048, EnableChecksum: false, Timeout: 5 * time.Second}
	sender := NewStreamClient(rawClient, cfg)
	receiver := NewStreamClient(serverConn, cfg)

	payload := []byte(strings.Repeat("y", 4096)) // exactly 4 chunks

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, werr := sender.WriteStream(bytes.NewReader(payload)); werr != nil {
			t.Errorf("WriteStream: %v", werr)
		}
	}()

	var buf bytes.Buffer
	if _, err := receiver.ReadStream(&buf); err != nil {
		t.Fatalf("ReadStream: %v", err)
	}
	<-done

	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatal("stream data mismatch with custom chunk size")
	}
}

func TestStreamClientDelegatesRawReadWrite(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	sc := NewStreamClient(rawClient, nil)
	data := []byte("raw stream bypass")
	if _, err := sc.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, len(data))
	if _, err := io.ReadFull(serverConn, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(buf, data) {
		t.Fatalf("got %q, want %q", buf, data)
	}
}

// ---- HeartbeatClient tests ----

func TestHeartbeatClientDisabledReturnsOriginal(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	cfg := &HeartbeatConfig{Enabled: false}
	hbClient, err := NewHeartbeatClient(rawClient, cfg)
	if err != nil {
		t.Fatalf("NewHeartbeatClient: %v", err)
	}

	// When disabled, the original client is returned directly (no wrapping).
	if hbClient != rawClient {
		t.Fatal("disabled heartbeat should return the original client unchanged")
	}
}

func TestHeartbeatClientNilConfigUsesDefaults(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	hbClient, err := NewHeartbeatClient(rawClient, nil)
	if err != nil {
		t.Fatalf("NewHeartbeatClient with nil config: %v", err)
	}
	// Should return a working (heartbeat-enabled) client.
	defer hbClient.Close()
}

func TestHeartbeatClientDelegatesWriteRead(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()
	defer rawClient.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	cfg := &HeartbeatConfig{Enabled: true, Interval: time.Hour, Timeout: 5 * time.Second}
	hbClient, err := NewHeartbeatClient(rawClient, cfg)
	if err != nil {
		t.Fatalf("NewHeartbeatClient: %v", err)
	}
	defer hbClient.Close()

	msg := []byte("heartbeat delegation test")
	if _, err := hbClient.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(serverConn, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Fatalf("got %q, want %q", buf, msg)
	}
}

func TestHeartbeatClientOnDeadCalledWhenConnectionClosed(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	deadCh := make(chan struct{}, 1)
	cfg := &HeartbeatConfig{
		Enabled:  true,
		Interval: 50 * time.Millisecond,
		Timeout:  100 * time.Millisecond,
		OnDead: func() {
			select {
			case deadCh <- struct{}{}:
			default:
			}
		},
	}

	hbClient, err := NewHeartbeatClient(rawClient, cfg)
	if err != nil {
		t.Fatalf("NewHeartbeatClient: %v", err)
	}

	// Close the server-side connection so the heartbeat ping cannot get a pong.
	serverConn.Close()

	select {
	case <-deadCh:
		// Good: OnDead was fired.
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for OnDead callback")
	}
	hbClient.Close()
}

func TestHeartbeatClientCloseStopsHeartbeat(t *testing.T) {
	server, rawClient := setupTestPair(t)
	defer server.Close()

	serverConn, err := server.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	deadCalled := false
	cfg := &HeartbeatConfig{
		Enabled:  true,
		Interval: 50 * time.Millisecond,
		Timeout:  100 * time.Millisecond,
		OnDead:   func() { deadCalled = true },
	}

	hbClient, err := NewHeartbeatClient(rawClient, cfg)
	if err != nil {
		t.Fatalf("NewHeartbeatClient: %v", err)
	}

	// Close before any heartbeat fires. The background goroutine must stop.
	if err := hbClient.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Give the goroutine a moment to observe the stop signal.
	time.Sleep(200 * time.Millisecond)

	if deadCalled {
		t.Fatal("OnDead should not be called after explicit Close")
	}
}

type failingHeartbeatClient struct {
	Client
	entered chan struct{}
	release chan struct{}
}

func (c *failingHeartbeatClient) WriteWithTimeout([]byte, time.Duration) (int, error) {
	close(c.entered)
	<-c.release
	return 0, io.ErrClosedPipe
}

func (*failingHeartbeatClient) Close() error { return nil }

func TestHeartbeatFailureCallbackLifecycle(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		name := "callback can close"
		if closeFirst {
			name = "close suppresses callback"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				raw := &failingHeartbeatClient{entered: make(chan struct{}), release: make(chan struct{})}
				var hb Client
				var calls int
				cfg := &HeartbeatConfig{
					Enabled:  true,
					Interval: time.Second,
					Timeout:  time.Second,
					OnDead: func() {
						calls++
						if hb.(*heartbeatClient).LastSeen().IsZero() {
							t.Error("LastSeen is zero")
						}
						if err := hb.Close(); err != nil {
							t.Error(err)
						}
					},
				}
				var err error
				hb, err = NewHeartbeatClient(raw, cfg)
				if err != nil {
					t.Fatal(err)
				}
				<-raw.entered
				if closeFirst {
					if err := hb.Close(); err != nil {
						t.Error(err)
					}
				}
				close(raw.release)
				synctest.Wait()
				want := 1
				if closeFirst {
					want = 0
				}
				if calls != want {
					t.Errorf("notification count = %d, want %d", calls, want)
				}
			})
		})
	}
}

// ---- Pool tests ----

func TestPoolGetAndReturn(t *testing.T) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	// Accept connections in background.
	go func() {
		for {
			c, aerr := server.Accept()
			if aerr != nil {
				return
			}
			go func(c Client) {
				buf := make([]byte, 64)
				_, _ = c.Read(buf)
				c.Close()
			}(c)
		}
	}()

	pool, err := NewPool(server.Addr(), nil)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	c, err := pool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	stats := pool.Stats()
	if stats.InUse != 1 {
		t.Fatalf("InUse = %d, want 1", stats.InUse)
	}

	// Returning the poolClient (via Close) should decrement InUse.
	c.Close()

	stats = pool.Stats()
	if stats.InUse != 0 {
		t.Fatalf("after return, InUse = %d, want 0", stats.InUse)
	}
	if stats.Idle != 1 {
		t.Fatalf("after return, Idle = %d, want 1", stats.Idle)
	}
}

func TestPoolReuseIdleConnection(t *testing.T) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	go func() {
		for {
			c, aerr := server.Accept()
			if aerr != nil {
				return
			}
			go func(c Client) { c.Close() }(c)
		}
	}()

	pool, err := NewPool(server.Addr(), DefaultPoolConfig())
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	c1, err := pool.Get()
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	c1.Close() // return to pool

	c2, err := pool.Get()
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	defer c2.Close()

	// Total connections should be 1 (reused, not a new dial).
	if stats := pool.Stats(); stats.TotalConns != 1 {
		t.Fatalf("TotalConns = %d, want 1 (should reuse)", stats.TotalConns)
	}
}

func TestPoolExhaustionContextCancelled(t *testing.T) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	go func() {
		for {
			c, aerr := server.Accept()
			if aerr != nil {
				return
			}
			go func(c Client) {
				time.Sleep(time.Second)
				c.Close()
			}(c)
		}
	}()

	cfg := &PoolConfig{MaxConns: 1, MaxIdleConns: 1, MaxIdleTime: 5 * time.Minute, DialTimeout: 5 * time.Second}
	pool, err := NewPool(server.Addr(), cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	// Acquire the only connection.
	c1, err := pool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer c1.Close()

	// Pool is full; a second Get with a cancelled context should fail quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = pool.GetWithContext(ctx)
	if err == nil {
		t.Fatal("expected error when pool is exhausted and context times out")
	}
}

func TestPoolCloseRejectsNewGet(t *testing.T) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	pool, err := NewPool(server.Addr(), nil)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	pool.Close()

	_, err = pool.Get()
	if err == nil {
		t.Fatal("expected error getting from closed pool")
	}
}

func TestPoolStats(t *testing.T) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	go func() {
		for {
			c, aerr := server.Accept()
			if aerr != nil {
				return
			}
			go func(c Client) { c.Close() }(c)
		}
	}()

	pool, err := NewPool(server.Addr(), nil)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	s := pool.Stats()
	if s.TotalConns != 0 || s.InUse != 0 || s.Idle != 0 {
		t.Fatalf("initial stats unexpected: %+v", s)
	}

	c, err := pool.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	s = pool.Stats()
	if s.TotalConns != 1 || s.InUse != 1 {
		t.Fatalf("after Get stats unexpected: %+v", s)
	}

	c.Close()

	s = pool.Stats()
	if s.Idle != 1 || s.InUse != 0 {
		t.Fatalf("after return stats unexpected: %+v", s)
	}
}

// ---- ReconnectClient tests ----

func TestReconnectClientDelegatesBasicOps(t *testing.T) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	serverConn, acceptErr := make(chan Client, 1), make(chan error, 1)
	go func() {
		c, err := server.Accept()
		serverConn <- c
		acceptErr <- err
	}()

	client, err := DialWithReconnect(server.Addr(), DefaultReconnectConfig())
	if err != nil {
		t.Fatalf("DialWithReconnect: %v", err)
	}
	defer client.Close()

	sc := <-serverConn
	if err := <-acceptErr; err != nil {
		t.Fatalf("accept error: %v", err)
	}
	defer sc.Close()

	msg := []byte("reconnect delegation")
	if _, err := client.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(sc, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Fatalf("got %q, want %q", buf, msg)
	}
}

func TestReconnectClientRemoteAddr(t *testing.T) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	go func() {
		for {
			c, aerr := server.Accept()
			if aerr != nil {
				return
			}
			go c.Close()
		}
	}()

	client, err := DialWithReconnect(server.Addr(), DefaultReconnectConfig())
	if err != nil {
		t.Fatalf("DialWithReconnect: %v", err)
	}
	defer client.Close()

	addr := client.RemoteAddr()
	if addr == nil {
		t.Fatal("RemoteAddr should not be nil")
	}
	addrStr := client.RemoteAddrString()
	if addrStr == "" {
		t.Fatal("RemoteAddrString should not be empty")
	}
}

func TestReconnectClientClosedWriteReturnsError(t *testing.T) {
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	go func() {
		for {
			c, aerr := server.Accept()
			if aerr != nil {
				return
			}
			go c.Close()
		}
	}()

	client, err := DialWithReconnect(server.Addr(), DefaultReconnectConfig())
	if err != nil {
		t.Fatalf("DialWithReconnect: %v", err)
	}
	client.Close()

	_, err = client.Write([]byte("after close"))
	if err == nil {
		t.Fatal("expected error writing to closed reconnect client")
	}
}

func TestReconnectClientMaxRetriesExceeded(t *testing.T) {
	// Create and immediately close the server so reconnects fail.
	server, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	addr := server.Addr()

	// Accept one connection so the initial Dial succeeds.
	acceptDone := make(chan Client, 1)
	go func() {
		c, _ := server.Accept()
		acceptDone <- c
	}()

	cfg := &ReconnectConfig{MaxRetries: 2, InitialDelay: 10 * time.Millisecond, MaxDelay: 20 * time.Millisecond, BackoffFactor: 1.5}
	client, err := DialWithReconnect(addr, cfg)
	if err != nil {
		server.Close()
		t.Fatalf("DialWithReconnect: %v", err)
	}

	sc := <-acceptDone
	// Close server and accepted connection to make reconnects fail.
	sc.Close()
	server.Close()

	// Force reconnect path by writing after connection is torn down.
	// The underlying connection is closed; reconnect will be attempted and fail.
	rc := client.(*reconnectClient)

	// Manually trigger reconnect; should exhaust retries.
	err = rc.reconnect()
	if err == nil {
		t.Fatal("expected error after max retries exhausted")
	}
	if !strings.Contains(err.Error(), "max reconnection attempts") {
		t.Fatalf("unexpected error: %v", err)
	}
	client.Close()
}

// ---- RateLimiter / RateLimitedServer tests ----

func TestRateLimiterAllowBurst(t *testing.T) {
	limiter := NewRateLimiter(10, 5) // 10 ops/sec, burst=5

	// Should allow up to burst immediately.
	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Fatalf("Allow() = false on attempt %d, expected true within burst", i+1)
		}
	}
}

func TestRateLimiterBlocksWhenExhausted(t *testing.T) {
	limiter := NewRateLimiter(100, 1) // burst=1

	// Drain the single burst token.
	limiter.Allow()

	// Next Allow should return false immediately (no waiting here).
	if limiter.Allow() {
		t.Fatal("Allow() should return false after burst is exhausted")
	}
}

func TestRateLimiterWaitSucceeds(t *testing.T) {
	limiter := NewRateLimiter(1000, 1) // fast refill, burst=1

	// Drain.
	limiter.Allow()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := limiter.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestRateLimiterWaitContextCancelled(t *testing.T) {
	limiter := NewRateLimiter(0.001, 1) // very slow refill

	// Drain.
	limiter.Allow()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := limiter.Wait(ctx)
	if err == nil {
		t.Fatal("Wait should return error when context is cancelled")
	}
}

func TestRateLimitedServerAcceptsWithinRate(t *testing.T) {
	inner, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer inner.Close()

	limiter := NewRateLimiter(100, 10) // generous rate for testing
	rlServer := NewRateLimitedServer(inner, limiter)
	defer rlServer.Close()

	if rlServer.Addr() != inner.Addr() {
		t.Fatalf("Addr mismatch: got %q, want %q", rlServer.Addr(), inner.Addr())
	}

	// Start a client and verify it can connect.
	go func() {
		c, dialErr := Dial(inner.Addr())
		if dialErr == nil {
			c.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := rlServer.AcceptWithContext(ctx)
	if err != nil {
		t.Fatalf("AcceptWithContext: %v", err)
	}
	conn.Close()
}

func TestRateLimitedServerShutdown(t *testing.T) {
	inner, err := NewServer(getTestAddr())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	limiter := NewRateLimiter(10, 5)
	rlServer := NewRateLimitedServer(inner, limiter)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := rlServer.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestNewAddr(t *testing.T) {
	addr := NewAddr("unix", "/tmp/test.sock")
	if addr == nil {
		t.Fatal("NewAddr should not return nil")
	}
	if addr.Network() != "unix" {
		t.Fatalf("Network = %q, want unix", addr.Network())
	}
	if addr.String() != "/tmp/test.sock" {
		t.Fatalf("String = %q, want /tmp/test.sock", addr.String())
	}
}

type poolTestConn struct {
	Client
	closes int
}

func (c *poolTestConn) Close() error {
	c.closes++
	return nil
}

type poolGetResult struct {
	client Client
	err    error
}

func TestPoolWaitingGetAfterReturnOrClose(t *testing.T) {
	for _, tt := range []struct {
		name  string
		close bool
	}{
		{name: "returned connection"},
		{name: "closed pool", close: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := &Pool{poolCfg: &PoolConfig{MaxConns: 1, MaxIdleTime: time.Hour}, stopCh: make(chan struct{})}
				raw := &poolTestConn{}
				conn := &poolConn{client: raw, pool: p, inUse: true}
				p.conns = []*poolConn{conn}
				lease := &poolClient{conn: conn}
				result := make(chan poolGetResult, 1)
				go func() {
					client, err := p.GetWithContext(t.Context())
					result <- poolGetResult{client, err}
				}()
				synctest.Wait()
				if p.Stats().Waiting != 1 {
					t.Fatal("GetWithContext did not wait for the full pool")
				}

				if tt.close {
					if err := p.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if err := lease.Close(); err != nil {
					t.Fatal(err)
				}
				got := <-result
				if tt.close {
					if got.client != nil || !errors.Is(got.err, ErrClientClosed) || raw.closes != 1 {
						t.Errorf("GetWithContext = %T, %v, underlying closes = %d", got.client, got.err, raw.closes)
					}
				} else if acquired, ok := got.client.(*poolClient); got.err != nil || !ok || acquired.conn != conn {
					t.Errorf("GetWithContext = %v, %v, want a lease on the returned connection", got.client, got.err)
				}
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestPoolGetRemovesExpiredIdleConnections(t *testing.T) {
	for _, tt := range []struct {
		name  string
		conns string
		want  PoolStats
	}{
		{name: "consecutive and trailing", conns: "EEAFEF", want: PoolStats{TotalConns: 3, InUse: 2, Idle: 1}},
		{name: "all expired", conns: "EEE", want: PoolStats{TotalConns: 1, InUse: 1}},
		{name: "none expired", conns: "AFF", want: PoolStats{TotalConns: 3, InUse: 2, Idle: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &Pool{poolCfg: &PoolConfig{MaxConns: len(tt.conns), MaxIdleTime: time.Hour}}
			now := time.Now()
			var expired []*poolTestConn
			var firstFresh *poolConn
			for _, kind := range tt.conns {
				raw := &poolTestConn{}
				pc := &poolConn{client: raw, pool: p, lastUsed: now, inUse: kind == 'A'}
				if kind == 'E' {
					pc.lastUsed = now.Add(-2 * time.Hour)
					expired = append(expired, raw)
				}
				if kind == 'F' && firstFresh == nil {
					firstFresh = pc
				}
				p.conns = append(p.conns, pc)
			}
			dials := 0
			client, err := p.getWithContext(t.Context(), func(context.Context, string, *Config) (Client, error) {
				dials++
				return &poolTestConn{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if firstFresh == nil {
				if dials != 1 {
					t.Errorf("dial count = %d, want 1", dials)
				}
			} else if dials != 0 || client.(*poolClient).conn != firstFresh {
				t.Error("Get did not reuse the first fresh connection")
			}
			if got := p.Stats(); got != tt.want {
				t.Errorf("stats = %+v, want %+v", got, tt.want)
			}
			for _, raw := range expired {
				if raw.closes != 1 {
					t.Errorf("expired connection closed %d times, want once", raw.closes)
				}
			}
		})
	}
}

func TestPoolDialReservationReleasesCapacity(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "canceled", "closed"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := &Pool{poolCfg: &PoolConfig{MaxConns: 1, MaxIdleTime: time.Hour}, stopCh: make(chan struct{})}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				started, release := make(chan struct{}), make(chan struct{})
				dialErr := errors.New("dial failed")
				raw := &poolTestConn{}
				first := make(chan poolGetResult, 1)
				go func() {
					client, err := p.getWithContext(ctx, func(context.Context, string, *Config) (Client, error) {
						close(started)
						<-release
						if outcome == "failure" {
							return nil, dialErr
						}
						return raw, nil
					})
					first <- poolGetResult{client, err}
				}()
				<-started

				secondRaw := &poolTestConn{}
				second := make(chan poolGetResult, 1)
				go func() {
					client, err := p.getWithContext(t.Context(), func(context.Context, string, *Config) (Client, error) {
						return secondRaw, nil
					})
					second <- poolGetResult{client, err}
				}()
				synctest.Wait()
				if got := p.Stats(); got.Waiting != 1 || got.TotalConns != 0 {
					t.Errorf("stats during first dial = %+v, want one waiter and zero established connections", got)
				}

				var firstErr error
				switch outcome {
				case "failure":
					firstErr = dialErr
				case "canceled":
					cancel()
					firstErr = context.Canceled
				case "closed":
					if err := p.Close(); err != nil {
						t.Fatal(err)
					}
					firstErr = ErrClientClosed
				}
				close(release)
				got := <-first
				if firstErr == nil {
					if got.err != nil || got.client == nil {
						t.Fatalf("first Get = %T, %v", got.client, got.err)
					}
					synctest.Wait()
					if p.Stats().Waiting != 1 {
						t.Error("second Get must wait until the established connection is returned")
					}
					if err := got.client.Close(); err != nil {
						t.Fatal(err)
					}
				} else if got.client != nil || !errors.Is(got.err, firstErr) {
					t.Errorf("first Get = %T, %v, want %v", got.client, got.err, firstErr)
				}
				if (outcome == "closed" || outcome == "canceled") && raw.closes != 1 {
					t.Errorf("discarded connection closed %d times, want once", raw.closes)
				}

				got = <-second
				if outcome == "closed" {
					if got.client != nil || !errors.Is(got.err, ErrClientClosed) || p.Stats() != (PoolStats{}) {
						t.Errorf("closed pool: second Get = %T, %v, stats = %+v", got.client, got.err, p.Stats())
					}
				} else {
					want := secondRaw
					if outcome == "success" {
						want = raw
					}
					if acquired, ok := got.client.(*poolClient); got.err != nil || !ok || acquired.conn.client != want {
						t.Errorf("second Get = %T, %v, want a lease on the expected connection", got.client, got.err)
					}
				}
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestPoolWaitersAcquireInOrderAfterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &Pool{poolCfg: &PoolConfig{MaxConns: 1, MaxIdleTime: time.Hour}, stopCh: make(chan struct{})}
		conn := &poolConn{client: &poolTestConn{}, pool: p, inUse: true}
		p.conns = []*poolConn{conn}
		lease := &poolClient{conn: conn}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		results := make([]chan poolGetResult, 3)
		for i := range results {
			waitCtx := t.Context()
			if i == 0 {
				waitCtx = ctx
			}
			results[i] = make(chan poolGetResult, 1)
			go func() {
				client, err := p.GetWithContext(waitCtx)
				results[i] <- poolGetResult{client, err}
			}()
			synctest.Wait()
			if got := p.Stats().Waiting; got != i+1 {
				t.Fatalf("waiting = %d, want %d", got, i+1)
			}
		}
		cancel()
		if got := <-results[0]; got.client != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled Get = %T, %v", got.client, got.err)
		}
		for _, result := range results[1:] {
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			select {
			case got := <-result:
				var ok bool
				lease, ok = got.client.(*poolClient)
				if got.err != nil || !ok || lease.conn != conn {
					t.Fatalf("Get = %T, %v, want the returned connection", got.client, got.err)
				}
			default:
				t.Fatal("oldest waiting caller did not acquire the returned connection")
			}
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

type heartbeatTimeoutClient struct {
	Client
	ping                      []byte
	writeTimeout, readTimeout time.Duration
}

func (c *heartbeatTimeoutClient) WriteWithTimeout(data []byte, timeout time.Duration) (int, error) {
	c.ping = bytes.Clone(data)
	c.writeTimeout = timeout
	return len(data), nil
}

func (c *heartbeatTimeoutClient) ReadWithTimeout(data []byte, timeout time.Duration) (int, error) {
	c.readTimeout = timeout
	return copy(data, c.ping), nil
}

func TestHeartbeatAppliesTimeoutToEachOperation(t *testing.T) {
	const timeout = 250 * time.Millisecond
	client := &heartbeatTimeoutClient{}
	hb := &heartbeatClient{client: client, config: &HeartbeatConfig{Timeout: timeout}}

	if err := hb.ping(); err != nil {
		t.Fatal(err)
	}
	if client.writeTimeout != timeout || client.readTimeout != timeout || hb.LastSeen().IsZero() {
		t.Errorf("write=%v, read=%v, last seen=%v", client.writeTimeout, client.readTimeout, hb.LastSeen())
	}
}

type directionalFrameTestClient struct {
	Client
	read  func([]byte) (int, error)
	write func([]byte) (int, error)
}

func (c directionalFrameTestClient) Read(data []byte) (int, error)  { return c.read(data) }
func (c directionalFrameTestClient) Write(data []byte) (int, error) { return c.write(data) }

func TestFramedClientReadAndWriteProgressIndependently(t *testing.T) {
	for _, blocked := range []string{"read", "write"} {
		t.Run(blocked, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			client := NewFramedClient(directionalFrameTestClient{
				read: func(data []byte) (int, error) {
					if blocked == "read" {
						close(entered)
						<-release
					}
					clear(data)
					return len(data), nil
				},
				write: func(data []byte) (int, error) {
					if blocked == "write" && len(data) == frameLenSize {
						close(entered)
						<-release
					}
					return len(data), nil
				},
			})
			read := func() error {
				msg, err := client.ReadMessage()
				if err == nil && len(msg) != 0 {
					t.Errorf("expected an empty frame, got %q", msg)
				}
				return err
			}
			write := func() error { return client.WriteMessage([]byte("sample")) }
			first, second := read, write
			if blocked == "write" {
				first, second = write, read
			}

			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			go func() { firstDone <- first() }()
			<-entered
			go func() { secondDone <- second() }()
			waitingForSecond := false
			select {
			case err := <-secondDone:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(time.Second):
				waitingForSecond = true
				t.Errorf("blocked %s prevented the other direction from completing", blocked)
			}
			close(release)
			if waitingForSecond {
				if err := <-secondDone; err != nil {
					t.Error(err)
				}
			}
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFramedClientConcurrentReadsPreserveBoundaries(t *testing.T) {
	var wire bytes.Buffer
	client := NewFramedClient(directionalFrameTestClient{read: wire.Read, write: wire.Write})
	for _, message := range []string{"first", "second message"} {
		if err := client.WriteMessage([]byte(message)); err != nil {
			t.Fatal(err)
		}
	}

	type result struct {
		message string
		err     error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			data, err := client.ReadMessage()
			results <- result{string(data), err}
		}()
	}
	seen := make(map[string]int)
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Error(got.err)
		}
		seen[got.message]++
	}
	if seen["first"] != 1 || seen["second message"] != 1 {
		t.Errorf("received messages = %v", seen)
	}
}

func TestNewPoolRejectsNegativeCapacity(t *testing.T) {
	cfg := DefaultPoolConfig()
	cfg.MaxConns = -1
	p, err := NewPool("unused", cfg)
	if p != nil {
		_ = p.Close()
	}
	if p != nil || err == nil {
		t.Fatalf("NewPool = %v, %v, want a capacity error", p, err)
	}
	for _, cfg := range []*PoolConfig{nil, {MaxConns: 1}} {
		p, err := NewPool("unused", cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

type poolCloseTestClient struct {
	Client
	err   error
	calls int
}

func (c *poolCloseTestClient) Close() error {
	c.calls++
	return c.err
}

func TestPoolCloseReportsConnectionErrors(t *testing.T) {
	firstErr := errors.New("first close error")
	secondErr := errors.New("second close error")
	p, err := NewPool("unused", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := &poolCloseTestClient{err: firstErr}
	second := &poolCloseTestClient{err: secondErr}
	success := &poolCloseTestClient{}
	p.mu.Lock()
	p.conns = []*poolConn{{client: first}, {client: second}, {client: success}}
	p.mu.Unlock()

	if err := p.Close(); !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Errorf("Close = %v, want both connection errors", err)
	}
	if err := p.Close(); err != nil || first.calls != 1 || second.calls != 1 || success.calls != 1 {
		t.Errorf("repeat close = %v, calls = %d, %d, %d", err, first.calls, second.calls, success.calls)
	}
	if conn, err := p.Get(); conn != nil || err == nil {
		t.Errorf("Get after close = %v, %v, want a closed-pool error", conn, err)
	}
}

type heartbeatCloseFixture struct {
	Client
	closed int
}

func (c *heartbeatCloseFixture) Close() error { c.closed++; return nil }

func TestHeartbeatClientValidatesIntervalBeforeStart(t *testing.T) {
	client := &heartbeatCloseFixture{}
	disabled, err := NewHeartbeatClient(client, &HeartbeatConfig{Interval: -time.Second})
	if err != nil || disabled != client {
		t.Fatalf("disabled = %v, %v", disabled, err)
	}
	if got, err := NewHeartbeatClient(client, &HeartbeatConfig{Enabled: true, Interval: -time.Second}); err == nil || got != nil {
		t.Fatalf("negative interval = %v, %v", got, err)
	}
	for _, interval := range []time.Duration{0, time.Hour} {
		cfg := &HeartbeatConfig{Enabled: true, Interval: interval}
		got, err := NewHeartbeatClient(client, cfg)
		if err != nil {
			t.Fatal(err)
		}
		hb := got.(*heartbeatClient)
		if err := hb.Close(); err != nil {
			t.Fatal(err)
		}
		want := interval
		if want == 0 {
			want = DefaultHeartbeatConfig().Interval
		}
		cfg.Interval = time.Second
		if hb.config.Interval != want {
			t.Errorf("owned interval = %v, want %v", hb.config.Interval, want)
		}
	}
}

type rawTimeoutFixture struct {
	Client
	write func([]byte, time.Duration) (int, error)
	read  func([]byte, time.Duration) (int, error)
}

func (c rawTimeoutFixture) WriteWithTimeout(p []byte, d time.Duration) (int, error) {
	return c.write(p, d)
}
func (c rawTimeoutFixture) ReadWithTimeout(p []byte, d time.Duration) (int, error) {
	return c.read(p, d)
}

func TestFramedClientDelegatesRawTimeouts(t *testing.T) {
	sentinel := errors.New("test deadline error")
	raw := rawTimeoutFixture{
		write: func(p []byte, d time.Duration) (int, error) {
			if string(p) != "raw" || d != time.Second {
				t.Errorf("raw write=%q, %v", p, d)
			}
			return 1, sentinel
		},
		read: func(p []byte, d time.Duration) (int, error) {
			if d != 2*time.Second {
				t.Errorf("read timeout=%v", d)
			}
			return copy(p, "ok"), sentinel
		},
	}
	w := NewFramedClient(raw)
	if n, err := w.WriteWithTimeout([]byte("raw"), time.Second); n != 1 || !errors.Is(err, sentinel) {
		t.Errorf("write=%d, %v", n, err)
	}
	var buffer [2]byte
	if n, err := w.ReadWithTimeout(buffer[:], 2*time.Second); n != 2 || string(buffer[:]) != "ok" || !errors.Is(err, sentinel) {
		t.Errorf("read=%d %q, %v", n, buffer, err)
	}
}

type heartbeatApplicationWriteFixture struct {
	Client
	write func([]byte, time.Duration) (int, error)
}

func (c heartbeatApplicationWriteFixture) WriteWithTimeout(p []byte, d time.Duration) (int, error) {
	return c.write(p, d)
}
func (c heartbeatApplicationWriteFixture) Close() error { return nil }

func TestHeartbeatClientDelegatesApplicationWriteTimeout(t *testing.T) {
	sentinel := errors.New("application write failure")
	calls := 0
	underlying := heartbeatApplicationWriteFixture{write: func(p []byte, d time.Duration) (int, error) {
		calls++
		if string(p) != "app" || d != 250*time.Millisecond {
			t.Errorf("write=%q, %v", p, d)
		}
		return 2, sentinel
	}}
	wrapped, err := NewHeartbeatClient(underlying, &HeartbeatConfig{Enabled: true, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wrapped.Close() })
	if n, err := wrapped.WriteWithTimeout([]byte("app"), 250*time.Millisecond); n != 2 || !errors.Is(err, sentinel) || calls != 1 {
		t.Errorf("write=%d, %v, calls=%d", n, err, calls)
	}
}

func TestFramedClientRawReadPreservesPartialResult(t *testing.T) {
	buffer := make([]byte, 8)
	sentinel := errors.New("partial raw read")
	calls := 0
	client := NewFramedClient(directionalFrameTestClient{read: func(got []byte) (int, error) {
		calls++
		if len(got) != len(buffer) || &got[0] != &buffer[0] {
			t.Fatal("raw buffer changed")
		}
		return copy(got, "abc"), sentinel
	}})
	if n, err := client.Read(buffer); n != 3 || !errors.Is(err, sentinel) || string(buffer[:3]) != "abc" || calls != 1 {
		t.Errorf("Read = %d, %v, buffer = %q, calls = %d", n, err, buffer, calls)
	}
}

func TestStreamClientCloseDelegatesError(t *testing.T) {
	sentinel := errors.New("test close failed")
	raw := &poolCloseTestClient{err: sentinel}
	stream := NewStreamClient(raw, nil)
	for call := 1; call <= 2; call++ {
		if err := stream.Close(); !errors.Is(err, sentinel) || raw.calls != call {
			t.Errorf("Close %d=%v, calls=%d", call, err, raw.calls)
		}
	}
}

type heartbeatReadFixture struct {
	Client
	read func([]byte, time.Duration) (int, error)
}

func (c heartbeatReadFixture) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	return c.read(buf, timeout)
}

func TestHeartbeatClientDelegatesApplicationReadTimeout(t *testing.T) {
	buffer := make([]byte, 8)
	deadline := 300 * time.Millisecond
	sentinel := errors.New("application read timed out")
	calls := 0
	hb := &heartbeatClient{client: heartbeatReadFixture{read: func(got []byte, timeout time.Duration) (int, error) {
		calls++
		if len(got) != len(buffer) || &got[0] != &buffer[0] || timeout != deadline {
			t.Errorf("read buffer length=%d, timeout=%v", len(got), timeout)
		}
		return copy(got, "abc"), sentinel
	}}}
	if n, err := hb.ReadWithTimeout(buffer, deadline); n != 3 || !errors.Is(err, sentinel) || string(buffer[:3]) != "abc" || calls != 1 {
		t.Errorf("ReadWithTimeout=%d, %v, buffer=%q, calls=%d", n, err, buffer, calls)
	}
}

type reconnectTimedReadClient struct {
	poolCloseTestClient
	timeout time.Duration
	reads   int
	readErr error
}

func (c *reconnectTimedReadClient) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	c.reads++
	c.timeout = timeout
	return copy(buf, "fixture"), c.readErr
}

func TestReconnectClientDelegatesReadWithTimeout(t *testing.T) {
	wantErr := errors.New("read deadline reached")
	underlying := &reconnectTimedReadClient{}
	wrapper := &reconnectClient{client: underlying}
	buf := make([]byte, 32)
	const timeout = 125 * time.Millisecond
	for _, readErr := range []error{nil, wantErr} {
		underlying.readErr = readErr
		n, err := wrapper.ReadWithTimeout(buf, timeout)
		if n != len("fixture") || string(buf[:n]) != "fixture" || !errors.Is(err, readErr) || underlying.timeout != timeout {
			t.Fatalf("read = %d, %v; delegated timeout = %v", n, err, underlying.timeout)
		}
	}
	if err := wrapper.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := wrapper.ReadWithTimeout(buf, timeout); n != 0 || !errors.Is(err, ErrClientClosed) || underlying.reads != 2 {
		t.Errorf("closed read = %d, %v; underlying calls = %d", n, err, underlying.reads)
	}
}

func TestFramedClientCloseDelegatesError(t *testing.T) {
	for _, want := range []error{nil, errors.New("fixture close failed")} {
		raw := &poolCloseTestClient{err: want}
		if err := NewFramedClient(raw).Close(); raw.calls != 1 || !errors.Is(err, want) {
			t.Errorf("Close returned %v after %d calls; want %v after one call", err, raw.calls, want)
		}
	}
}
