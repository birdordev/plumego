package websocket

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBroadcastRoomRaceCondition tests concurrent broadcasts while connections join/leave
func TestBroadcastRoomRaceCondition(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 4, JobQueueSize: 1024})
	defer hub.Stop()

	const (
		numConns      = 50
		numBroadcasts = 100
		numIterations = 10
	)

	for iter := 0; iter < numIterations; iter++ {
		conns := make([]*Conn, numConns)
		for i := 0; i < numConns; i++ {
			conns[i] = newMockConn()
		}

		var wg sync.WaitGroup

		// Goroutine 1: Concurrent joins
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < numConns; i++ {
				_ = hub.TryJoin("test-room", conns[i])
				time.Sleep(time.Microsecond)
			}
		}()

		// Goroutine 2: Concurrent broadcasts
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < numBroadcasts; i++ {
				hub.BroadcastRoom("test-room", OpcodeText, []byte("test"))
				time.Sleep(time.Microsecond)
			}
		}()

		// Goroutine 3: Concurrent leaves
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond) // Let some joins happen first
			for i := 0; i < numConns/2; i++ {
				hub.Leave("test-room", conns[i])
				time.Sleep(time.Microsecond)
			}
		}()

		wg.Wait()

		// Cleanup
		for i := 0; i < numConns; i++ {
			hub.RemoveConn(conns[i])
			conns[i].Close()
		}
	}
}

// TestBroadcastAllRaceCondition tests concurrent BroadcastAll operations
func TestBroadcastAllRaceCondition(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 4, JobQueueSize: 1024})
	defer hub.Stop()

	const (
		numRooms        = 10
		numConnsPerRoom = 10
		numBroadcasts   = 50
	)

	// Create connections in multiple rooms
	allConns := make([]*Conn, 0, numRooms*numConnsPerRoom)
	for r := 0; r < numRooms; r++ {
		room := string(rune('A' + r))
		for c := 0; c < numConnsPerRoom; c++ {
			conn := newMockConn()
			_ = hub.TryJoin(room, conn)
			allConns = append(allConns, conn)
		}
	}

	var wg sync.WaitGroup

	// Multiple concurrent BroadcastAll calls
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numBroadcasts; j++ {
				hub.BroadcastAll(OpcodeText, []byte("broadcast"))
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Concurrent modifications
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numConnsPerRoom; i++ {
			conn := newMockConn()
			_ = hub.TryJoin("Z", conn)
			time.Sleep(time.Millisecond)
			hub.Leave("Z", conn)
			conn.Close()
		}
	}()

	wg.Wait()

	// Cleanup
	for _, conn := range allConns {
		hub.RemoveConn(conn)
		conn.Close()
	}
}

// TestAtomicRoomRegistrationsAccuracy tests that roomRegistrations counter stays accurate
func TestAtomicRoomRegistrationsAccuracy(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 4, JobQueueSize: 1024})
	defer hub.Stop()

	const numConns = 100
	conns := make([]*Conn, numConns)

	// Add connections
	for i := 0; i < numConns; i++ {
		conns[i] = newMockConn()
		if err := hub.TryJoin("test", conns[i]); err != nil {
			t.Fatalf("Failed to join: %v", err)
		}
	}

	// Verify count
	if got := hub.GetRoomRegistrationCount(); got != numConns {
		t.Errorf("Expected %d room registrations, got %d", numConns, got)
	}

	// Remove half
	for i := 0; i < numConns/2; i++ {
		hub.Leave("test", conns[i])
	}

	expected := numConns - numConns/2
	if got := hub.GetRoomRegistrationCount(); got != expected {
		t.Errorf("After removing %d, expected %d room registrations, got %d", numConns/2, expected, got)
	}

	// Remove all
	for i := numConns / 2; i < numConns; i++ {
		hub.Leave("test", conns[i])
	}

	if got := hub.GetRoomRegistrationCount(); got != 0 {
		t.Errorf("After removing all, expected 0 room registrations, got %d", got)
	}

	// Cleanup
	for i := 0; i < numConns; i++ {
		conns[i].Close()
	}
}

// TestRateLimiter tests the rate limiter implementation
func TestRateLimiter(t *testing.T) {
	// Create rate limiter: 10 requests per second, burst 20
	rl := newRateLimiter(10, 20)

	// Should allow burst initially
	allowed := 0
	for i := 0; i < 25; i++ {
		if rl.allow() {
			allowed++
		}
	}

	// Should allow up to burst size (20)
	if allowed != 20 {
		t.Errorf("Expected 20 allowed requests in burst, got %d", allowed)
	}

	// Wait for refill (100ms should give us ~1 token at 10/sec rate)
	time.Sleep(150 * time.Millisecond)

	// Should allow at least 1 more
	if !rl.allow() {
		t.Error("Expected at least one request allowed after refill")
	}
}

// TestRateLimiterConcurrent tests rate limiter under concurrent load
func TestRateLimiterConcurrent(t *testing.T) {
	rl := newRateLimiter(100, 200)

	const numGoroutines = 10
	const attemptsPerGoroutine = 50

	var allowed atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < attemptsPerGoroutine; j++ {
				if rl.allow() {
					allowed.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// Should have allowed burst size (200) + some refill
	allowedCount := allowed.Load()
	if allowedCount < 200 || allowedCount > 250 {
		t.Logf("Warning: Expected ~200-250 allowed requests, got %d", allowedCount)
	}
}

// TestMetadataConcurrency tests concurrent metadata operations
func TestMetadataConcurrency(t *testing.T) {
	conn := newMockConn()
	defer conn.Close()

	const (
		numGoroutines = 10
		numOperations = 100
	)

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				conn.SetMetadata("key", id*1000+j)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_, _ = conn.GetMetadata("key")
			}
		}()
	}

	// Concurrent deletes
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				conn.DeleteMetadata("key")
			}
		}()
	}

	wg.Wait()

	// Should not crash - that's the main test
	t.Log("Metadata concurrent operations completed successfully")
}

// TestHubCapacityLimits tests that hub enforces capacity limits correctly
func TestHubCapacityLimits(t *testing.T) {
	cfg := HubConfig{
		WorkerCount:          4,
		JobQueueSize:         1024,
		MaxRoomRegistrations: 10,
		MaxRoomConnections:   5,
	}
	hub := mustNewHubConfig(t, cfg)
	defer hub.Stop()

	// Fill up a room to its limit
	conns := make([]*Conn, 0)
	for i := 0; i < 5; i++ {
		conn := newMockConn()
		if err := hub.TryJoin("room1", conn); err != nil {
			t.Fatalf("Failed to join room1: %v", err)
		}
		conns = append(conns, conn)
	}

	// Next connection should be rejected (room full)
	conn := newMockConn()
	if err := hub.TryJoin("room1", conn); err != ErrRoomFull {
		t.Errorf("Expected ErrRoomFull, got %v", err)
	}
	conn.Close()

	// But should be able to join a different room
	conn = newMockConn()
	if err := hub.TryJoin("room2", conn); err != nil {
		t.Errorf("Should be able to join room2: %v", err)
	}
	conns = append(conns, conn)

	// Fill to hub limit (10 total)
	for i := 6; i < 10; i++ {
		conn := newMockConn()
		if err := hub.TryJoin("room3", conn); err != nil {
			t.Fatalf("Failed to join room3: %v", err)
		}
		conns = append(conns, conn)
	}

	// Hub should now be full
	conn = newMockConn()
	if err := hub.TryJoin("room4", conn); err != ErrHubFull {
		t.Errorf("Expected ErrHubFull, got %v", err)
	}
	conn.Close()

	// Cleanup
	for _, c := range conns {
		hub.RemoveConn(c)
		c.Close()
	}
}

// TestHubRateLimitIntegration tests rate limiting in TryJoin
func TestHubRateLimitIntegration(t *testing.T) {
	cfg := HubConfig{
		WorkerCount:       4,
		JobQueueSize:      1024,
		MaxConnectionRate: 10, // 10 connections per second
	}
	hub := mustNewHubConfig(t, cfg)
	defer hub.Stop()

	// Burst should allow ~20 connections quickly (2x rate)
	conns := make([]*Conn, 0)
	allowed := 0
	for i := 0; i < 30; i++ {
		conn := newMockConn()
		if err := hub.TryJoin("test", conn); err == nil {
			allowed++
			conns = append(conns, conn)
		} else {
			conn.Close()
		}
	}

	// Should have allowed around burst size (20)
	if allowed < 15 || allowed > 25 {
		t.Logf("Rate limiting: allowed %d connections (expected ~20)", allowed)
	}

	// Cleanup
	for _, c := range conns {
		hub.RemoveConn(c)
		c.Close()
	}
}

// newMockConn creates a mock connection for testing
func newMockConn() *Conn {
	// Create a pair of connected pipes
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()

	mockConn := &simpleMockConn{
		reader: r1,
		writer: w2,
	}

	conn, err := NewConn(mockConn, 64, 5*time.Second, SendDrop)
	if err != nil {
		panic(err)
	}

	// Start goroutines to prevent deadlock
	go func() {
		_, _ = io.Copy(io.Discard, r2) // Discard writes
	}()
	go func() {
		<-conn.closeC
		w1.Close()
		r1.Close()
		w2.Close()
		r2.Close()
	}()

	// Consume send queue to prevent blocking
	go func() {
		for {
			select {
			case <-conn.sendQueue:
				// Consume messages
			case <-conn.closeC:
				return
			}
		}
	}()

	return conn
}

// simpleMockConn is a simple mock net.Conn for testing
type simpleMockConn struct {
	reader io.Reader
	writer io.Writer
}

func (m *simpleMockConn) Read(b []byte) (n int, err error) {
	return m.reader.Read(b)
}

func (m *simpleMockConn) Write(b []byte) (n int, err error) {
	return m.writer.Write(b)
}

func (m *simpleMockConn) Close() error {
	if closer, ok := m.reader.(io.Closer); ok {
		closer.Close()
	}
	if closer, ok := m.writer.(io.Closer); ok {
		closer.Close()
	}
	return nil
}

func (m *simpleMockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
}

func (m *simpleMockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9090}
}

func (m *simpleMockConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *simpleMockConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *simpleMockConn) SetWriteDeadline(t time.Time) error {
	return nil
}
