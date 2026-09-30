package ipc

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
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
