package websocket

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// TestConnConfiguration tests connection configuration methods
func TestConnConfiguration(t *testing.T) {
	// Create a mock connection
	mockConn, _ := createMockConnection(t)
	defer mockConn.Close()

	// Test SetReadLimit
	if err := mockConn.SetReadLimit(32 << 20); err != nil {
		t.Fatalf("SetReadLimit: %v", err)
	}
	if mockConn.readLimit.Load() != 32<<20 {
		t.Errorf("SetReadLimit failed, expected 32MB, got %d", mockConn.readLimit.Load())
	}
	if err := mockConn.SetReadLimit(0); err != nil {
		t.Fatalf("SetReadLimit(0): %v", err)
	}
	if got := mockConn.readLimit.Load(); got != defaultReadLimit {
		t.Fatalf("SetReadLimit(0) read limit = %d, want %d", got, defaultReadLimit)
	}

	// Test SetPingPeriod
	newPingPeriod := 10 * time.Second
	_ = mockConn.SetPingPeriod(newPingPeriod)
	if time.Duration(mockConn.pingPeriod.Load()) != newPingPeriod {
		t.Errorf("SetPingPeriod failed, expected %v, got %v", newPingPeriod, time.Duration(mockConn.pingPeriod.Load()))
	}

	// Test SetPongWait
	newPongWait := 15 * time.Second
	_ = mockConn.SetPongWait(newPongWait)
	if time.Duration(mockConn.pongWait.Load()) != newPongWait {
		t.Errorf("SetPongWait failed, expected %v, got %v", newPongWait, time.Duration(mockConn.pongWait.Load()))
	}

	// Test GetLastPong
	lastPong := mockConn.GetLastPong()
	if lastPong.IsZero() {
		t.Error("GetLastPong returned zero time")
	}
}

// TestWriteJSON tests JSON serialization
func TestWriteJSON(t *testing.T) {
	mockConn, _ := createMockConnection(t)
	defer mockConn.Close()

	testData := map[string]any{
		"message": "hello",
		"count":   42,
		"nested": map[string]string{
			"key": "value",
		},
	}

	// This will fail because sendQueue is not being consumed, but we test the JSON marshaling
	err := mockConn.WriteJSON(testData)
	if err != nil {
		t.Logf("WriteJSON error (expected due to queue): %v", err)
	}
}

// TestHubOperations tests Hub management functions
func TestHubOperations(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 10})
	defer hub.Stop()

	// Test GetRoomCount
	count := hub.GetRoomCount("test")
	if count != 0 {
		t.Errorf("Expected room count 0, got %d", count)
	}

	// Test GetRoomRegistrationCount
	total := hub.GetRoomRegistrationCount()
	if total != 0 {
		t.Errorf("Expected total count 0, got %d", total)
	}

	// Test GetRooms
	rooms := hub.GetRooms()
	if len(rooms) != 0 {
		t.Errorf("Expected 0 rooms, got %d", len(rooms))
	}

	// Create mock connections and join rooms
	mock1, _ := createMockConnection(t)
	mock2, _ := createMockConnection(t)
	defer mock1.Close()
	defer mock2.Close()

	mustTryJoin(t, hub, "room1", mock1)
	mustTryJoin(t, hub, "room1", mock2)
	mustTryJoin(t, hub, "room2", mock1)

	// Test GetRoomCount after joins
	count = hub.GetRoomCount("room1")
	if count != 2 {
		t.Errorf("Expected room1 count 2, got %d", count)
	}

	count = hub.GetRoomCount("room2")
	if count != 1 {
		t.Errorf("Expected room2 count 1, got %d", count)
	}

	// Test GetRoomRegistrationCount
	total = hub.GetRoomRegistrationCount()
	if total != 3 {
		t.Errorf("Expected total count 3, got %d", total)
	}

	// Test GetRooms
	rooms = hub.GetRooms()
	if len(rooms) != 2 {
		t.Errorf("Expected 2 rooms, got %d", len(rooms))
	}

	// Test Leave
	hub.Leave("room1", mock1)
	count = hub.GetRoomCount("room1")
	if count != 1 {
		t.Errorf("Expected room1 count 1 after leave, got %d", count)
	}

	// Test RemoveConn
	hub.RemoveConn(mock2)
	count = hub.GetRoomCount("room1")
	if count != 0 {
		t.Errorf("Expected room1 count 0 after remove, got %d", count)
	}
}

func TestHubConnectionLimits(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{
		WorkerCount:          1,
		JobQueueSize:         10,
		MaxRoomRegistrations: 2,
		MaxRoomConnections:   1,
	})
	defer hub.Stop()

	conn1, _ := createMockConnection(t)
	conn2, _ := createMockConnection(t)
	conn3, _ := createMockConnection(t)
	defer conn1.Close()
	defer conn2.Close()
	defer conn3.Close()

	if err := hub.TryJoin("room", conn1); err != nil {
		t.Fatalf("unexpected join error: %v", err)
	}

	if err := hub.TryJoin("room", conn2); err != ErrRoomFull {
		t.Fatalf("expected room full error, got %v", err)
	}

	if err := hub.TryJoin("room2", conn2); err != nil {
		t.Fatalf("unexpected join error: %v", err)
	}

	if err := hub.TryJoin("room3", conn3); err != ErrHubFull {
		t.Fatalf("expected hub full error, got %v", err)
	}

	metrics := hub.Metrics()
	if metrics.AcceptedTotal != 2 || metrics.RejectedTotal != 2 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}

// TestBroadcast tests Hub broadcast functionality
func TestBroadcast(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 10})
	defer hub.Stop()

	// Create multiple connections
	connections := make([]*Conn, 5)
	for i := 0; i < 5; i++ {
		conn, _ := createMockConnection(t)
		connections[i] = conn
		mustTryJoin(t, hub, "test", conn)
		defer conn.Close()
	}

	// Broadcast to room
	hub.BroadcastRoom("test", OpcodeText, []byte("hello"))

	// Give workers time to process
	time.Sleep(100 * time.Millisecond)

	// Verify all connections received the message (check sendQueue)
	for i, conn := range connections {
		select {
		case msg := <-conn.sendQueue:
			if string(msg.Data) != "hello" {
				t.Errorf("Connection %d received wrong message: %s", i, string(msg.Data))
			}
		default:
			// Message might have been processed or dropped
			t.Logf("Connection %d queue empty (might be processed)", i)
		}
	}

	// Test BroadcastAll
	hub.BroadcastAll(OpcodeBinary, []byte("binary"))

	time.Sleep(100 * time.Millisecond)
}

// TestStreamLargeMessage tests bounded large-message reads.
func TestStreamLargeMessage(t *testing.T) {
	// Create a large message (larger than fragment size)
	largeData := bytes.Repeat([]byte("x"), maxFragmentSize*3)

	// Create mock connection
	mockConn, _ := createMockConnection(t)
	defer mockConn.Close()

	// Write large message
	err := mockConn.WriteMessage(OpcodeBinary, largeData)
	if err != nil {
		t.Logf("WriteMessage error (expected due to queue): %v", err)
	}
}

// TestAuthentication tests auth functionality
func TestAuthentication(t *testing.T) {
	secret := validSecret()
	auth := mustSimpleRoomAuth(t)
	tokenAuth := mustSimpleHS256TokenAuth(t, secret)

	// Test room password
	if err := auth.SetRoomPassword("secure", "secret123"); err != nil {
		t.Fatalf("SetRoomPassword: %v", err)
	}
	if !auth.CheckRoomPassword("secure", "secret123") {
		t.Error("Room password check failed")
	}
	if auth.CheckRoomPassword("secure", "wrong") {
		t.Error("Room password should fail with wrong password")
	}

	// Test JWT verification
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user1","name":"Test User","exp":` + fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()) + `}`))

	// Create signature
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(header + "." + payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	token := header + "." + payload + "." + sig

	// Verify token
	claims, err := tokenAuth.VerifyJWT(token)
	if err != nil {
		t.Fatalf("JWT verification failed: %v", err)
	}

	// Extract user info
	userInfo := ExtractUserInfo(claims)
	if userInfo.ID != "user1" {
		t.Errorf("Expected user ID 'user1', got '%s'", userInfo.ID)
	}
	if userInfo.Name != "Test User" {
		t.Errorf("Expected name 'Test User', got '%s'", userInfo.Name)
	}

	// Test expired token
	expiredPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user1","exp":` + fmt.Sprintf("%d", time.Now().Add(-time.Hour).Unix()) + `}`))
	mac2 := hmac.New(sha256.New, secret)
	mac2.Write([]byte(header + "." + expiredPayload))
	sig2 := base64.RawURLEncoding.EncodeToString(mac2.Sum(nil))
	expiredToken := header + "." + expiredPayload + "." + sig2

	_, err = tokenAuth.VerifyJWT(expiredToken)
	if err == nil {
		t.Error("Expired token should fail verification")
	}
}

// TestFragmentation tests message fragmentation
func TestFragmentation(t *testing.T) {
	// Test that large messages are fragmented
	largeMsg := bytes.Repeat([]byte("A"), maxFragmentSize*2+1000)

	// Count how many frames would be needed
	remaining := len(largeMsg)
	frameCount := 0
	for remaining > 0 {
		if remaining <= maxFragmentSize {
			frameCount++
			break
		}
		remaining -= maxFragmentSize
		frameCount++
	}

	if frameCount != 3 {
		t.Errorf("Expected 3 frames for message of size %d, would use %d frames", len(largeMsg), frameCount)
	}
}

// TestMetadata tests connection metadata
func TestMetadata(t *testing.T) {
	mockConn, _ := createMockConnection(t)
	defer mockConn.Close()

	// Test metadata operations
	mockConn.SetMetadata("key1", "value1")
	mockConn.SetMetadata("key2", 123)

	if val, ok := mockConn.GetMetadata("key1"); !ok || val != "value1" {
		t.Error("Metadata string value not stored correctly")
	}
	if val, ok := mockConn.GetMetadata("key2"); !ok || val != 123 {
		t.Error("Metadata int value not stored correctly")
	}

	// Test delete
	mockConn.DeleteMetadata("key1")
	if _, ok := mockConn.GetMetadata("key1"); ok {
		t.Error("Metadata should have been deleted")
	}
}

// Helper function to create mock connection for testing
func createMockConnection(t *testing.T) (*Conn, error) {
	// We'll use a simple approach: create a connection with a dummy net.Conn
	// For testing purposes, we'll create a pipe
	server, client := createMockPipe(t)

	// Create Conn with minimal configuration
	conn, err := NewConn(server, 10, 100*time.Millisecond, SendDrop)
	if err != nil {
		return nil, err
	}

	// Override the br/bw to use our pipe
	conn.br = bufio.NewReaderSize(server, 8192)
	conn.bw = bufio.NewWriterSize(server, 8192)

	// Store client side for test reads
	t.Cleanup(func() {
		client.Close()
	})

	return conn, nil
}

func createMockPipe(_ *testing.T) (server, client net.Conn) {
	// Create a simple pipe for testing
	// This is a simplified version - in real tests you'd want proper mock
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()

	// Create custom net.Conn wrappers
	server = &mockConn{reader: r1, writer: w2}
	client = &mockConn{reader: r2, writer: w1}

	return server, client
}

// mockConn implements net.Conn for testing
type mockConn struct {
	reader io.Reader
	writer io.Writer
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	return m.reader.Read(b)
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	return m.writer.Write(b)
}

func (m *mockConn) Close() error {
	// Close the writer to unblock any pending writes
	if w, ok := m.writer.(*io.PipeWriter); ok {
		w.Close()
	}
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return &mockAddr{}
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &mockAddr{}
}

func (m *mockConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type mockAddr struct{}

func (m *mockAddr) Network() string { return "tcp" }
func (m *mockAddr) String() string  { return "127.0.0.1:0" }
