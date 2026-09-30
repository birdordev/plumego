package websocket

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testJWTToken(t *testing.T, secret []byte) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"test-user","exp":4070908800}`))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(header + "." + payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return header + "." + payload + "." + sig
}

func TestComputeAcceptKey(t *testing.T) {
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	expected := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	result := computeAcceptKey(key)
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestHeaderContains(t *testing.T) {
	tests := []struct {
		name     string
		header   http.Header
		key      string
		val      string
		expected bool
	}{
		{
			name:     "exact match",
			header:   http.Header{"Connection": []string{"Upgrade"}},
			key:      "Connection",
			val:      "Upgrade",
			expected: true,
		},
		{
			name:     "case insensitive",
			header:   http.Header{"Connection": []string{"upgrade"}},
			key:      "Connection",
			val:      "Upgrade",
			expected: true,
		},
		{
			name:     "multiple values",
			header:   http.Header{"Connection": []string{"keep-alive, Upgrade"}},
			key:      "Connection",
			val:      "Upgrade",
			expected: true,
		},
		{
			name:     "not found",
			header:   http.Header{"Connection": []string{"keep-alive"}},
			key:      "Connection",
			val:      "Upgrade",
			expected: false,
		},
		{
			name:     "empty header",
			header:   http.Header{},
			key:      "Connection",
			val:      "Upgrade",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := headerContains(tt.header, tt.key, tt.val)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestServeWSWithConfig_MethodNotAllowed(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 10})
	defer hub.Stop()
	auth := mustSimpleRoomAuth(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/ws", nil)

	ServeWSWithConfig(w, r, ServerConfig{
		Hub:                  hub,
		RoomAuth:             auth,
		QueueSize:            10,
		SendTimeout:          5 * time.Second,
		SendBehavior:         SendDrop,
		AllowAllOrigins:      true,
		AllowUnauthenticated: true,
	})

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestServeWSWithConfig_BadRequest(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*http.Request)
		expect int
	}{
		{
			name: "missing connection header",
			setup: func(r *http.Request) {
				r.Header.Set("Upgrade", "websocket")
				r.Header.Set("Sec-WebSocket-Version", "13")
				r.Header.Set("Sec-WebSocket-Key", "test-key")
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "missing upgrade header",
			setup: func(r *http.Request) {
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Sec-WebSocket-Version", "13")
				r.Header.Set("Sec-WebSocket-Key", "test-key")
			},
			expect: http.StatusBadRequest,
		},
		{
			name: "missing sec-websocket-key",
			setup: func(r *http.Request) {
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
				r.Header.Set("Sec-WebSocket-Version", "13")
			},
			expect: http.StatusBadRequest,
		},
	}

	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 10})
	defer hub.Stop()
	auth := mustSimpleRoomAuth(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/ws", nil)
			tt.setup(r)

			ServeWSWithConfig(w, r, ServerConfig{
				Hub:                  hub,
				RoomAuth:             auth,
				QueueSize:            10,
				SendTimeout:          5 * time.Second,
				SendBehavior:         SendDrop,
				AllowAllOrigins:      true,
				AllowUnauthenticated: true,
			})

			if w.Code != tt.expect {
				t.Errorf("expected status %d, got %d", tt.expect, w.Code)
			}
		})
	}
}

func TestServeWSWithConfig_BadRoomPassword(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 10})
	defer hub.Stop()
	auth := mustSimpleRoomAuth(t)
	// Set a room password first
	if err := auth.SetRoomPassword("test", "correct"); err != nil {
		t.Fatalf("SetRoomPassword: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ws?room=test", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==") // Valid WebSocket Key
	r.Header.Set(roomPasswordHeader, "wrong")

	ServeWSWithConfig(w, r, ServerConfig{
		Hub:                  hub,
		RoomAuth:             auth,
		QueueSize:            10,
		SendTimeout:          5 * time.Second,
		SendBehavior:         SendDrop,
		AllowAllOrigins:      true,
		AllowUnauthenticated: true,
	})

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
	}
}

func TestStreamReaderRead(t *testing.T) {
	sr := &streamReader{
		parent: &Conn{},
		op:     0,
	}

	sr.done = true
	buf := make([]byte, 10)
	n, err := sr.Read(buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes, got %d", n)
	}
}

func TestStreamReaderClose(t *testing.T) {
	sr := &streamReader{}

	err := sr.Close()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err = sr.Close()
	if err != nil {
		t.Errorf("unexpected error on second close: %v", err)
	}
}

func TestStreamReaderCloseBeforeEOFClosesParent(t *testing.T) {
	conn := &Conn{closeC: make(chan struct{})}
	sr := &streamReader{
		parent: conn,
		done:   false,
	}

	if err := sr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !conn.IsClosed() {
		t.Fatal("closing an unfinished reader must close the parent connection")
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestWriteMessageWithTimeout(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendTimeout:  10 * time.Millisecond,
		sendBehavior: SendBlock,
	}

	c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}

	err := c.WriteMessage(OpcodeText, []byte("test"))
	assertErrorIsOrContains(t, err, context.DeadlineExceeded, "timeout", "deadline")
}

func TestWriteMessageDrop(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendDrop,
	}

	c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}

	err := c.WriteMessage(OpcodeText, []byte("test"))
	assertErrorIsOrContains(t, err, ErrQueueFull, "queue full")
}

func TestWriteMessageClose(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendClose,
	}

	c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}

	err := c.WriteMessage(OpcodeText, []byte("test"))
	assertErrorContains(t, err, "closed")
}

func TestWriteText(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendDrop,
	}

	err := c.WriteText("hello")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	select {
	case out := <-c.sendQueue:
		if out.Op != OpcodeText {
			t.Errorf("expected OpcodeText, got %d", out.Op)
		}
		if string(out.Data) != "hello" {
			t.Errorf("expected 'hello', got %s", string(out.Data))
		}
	default:
		t.Error("message not queued")
	}
}

func TestWriteBinary(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendDrop,
	}

	data := []byte{0x01, 0x02, 0x03}
	err := c.WriteBinary(data)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	select {
	case out := <-c.sendQueue:
		if out.Op != OpcodeBinary {
			t.Errorf("expected OpcodeBinary, got %d", out.Op)
		}
		if !bytes.Equal(out.Data, data) {
			t.Errorf("expected %v, got %v", data, out.Data)
		}
	default:
		t.Error("message not queued")
	}
}

func TestWriteJSONMarshalError(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendDrop,
	}

	ch := make(chan int)
	err := c.WriteJSON(ch)
	if err == nil {
		t.Error("expected marshal error")
	}
}

func TestWriteMessageClosed(t *testing.T) {
	c := &Conn{
		closeC: make(chan struct{}),
	}
	c.Close()

	err := c.WriteMessage(OpcodeText, []byte("test"))
	assertErrorIsOrContains(t, err, ErrConnClosed, "closed")
}

func TestWriteMessageRejectsInvalidOpcode(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendDrop,
	}

	err := c.WriteMessage(opcodePing, []byte("ping"))
	if !errors.Is(err, ErrInvalidOpcode) {
		t.Fatalf("expected ErrInvalidOpcode, got %v", err)
	}
	select {
	case out := <-c.sendQueue:
		t.Fatalf("invalid opcode was enqueued: %+v", out)
	default:
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestWriteCloseReturnsFrameWriteError(t *testing.T) {
	conn, err := NewConn(&simpleMockConn{
		reader: bytes.NewReader(nil),
		writer: failingWriter{},
	}, 1, time.Second, SendDrop)
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}

	err = conn.WriteClose(CloseNormalClosure, "bye")
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected frame write error, got %v", err)
	}
	if !conn.IsClosed() {
		t.Fatal("WriteClose should close after frame write failure")
	}
}

func TestWriteCloseSetsWriteDeadline(t *testing.T) {
	rawConn := &deadlineRecordingConn{}
	conn, err := NewConn(rawConn, 1, time.Second, SendDrop)
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	if err := conn.SetWriteTimeout(25 * time.Millisecond); err != nil {
		t.Fatalf("SetWriteTimeout: %v", err)
	}

	if err := conn.WriteClose(CloseNormalClosure, "bye"); err != nil {
		t.Fatalf("WriteClose: %v", err)
	}
	if len(rawConn.writeDeadlines) < 2 {
		t.Fatalf("expected set and clear write deadlines, got %d", len(rawConn.writeDeadlines))
	}
	if rawConn.writeDeadlines[0].IsZero() {
		t.Fatal("expected first write deadline to be non-zero")
	}
	if !rawConn.writeDeadlines[len(rawConn.writeDeadlines)-1].IsZero() {
		t.Fatal("expected final write deadline to be cleared")
	}
}

func TestWriteMessageClosedDoesNotEnqueue(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendBlock,
	}
	c.Close()

	err := c.WriteMessage(OpcodeText, []byte("test"))
	assertErrorIsOrContains(t, err, ErrConnClosed, "closed")

	select {
	case out := <-c.sendQueue:
		t.Fatalf("closed connection enqueued message: %+v", out)
	default:
	}
}

func TestWriteMessageUnknownBehavior(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: 99,
	}

	_ = c.WriteMessage(OpcodeText, []byte("test1"))
	err := c.WriteMessage(OpcodeText, []byte("test2"))
	assertErrorContains(t, err, "unknown")
}

func TestNewConnRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name      string
		conn      net.Conn
		queueSize int
		behavior  SendBehavior
		wantErr   error
	}{
		{
			name:      "nil net conn",
			queueSize: 1,
			behavior:  SendDrop,
			wantErr:   ErrNilNetConn,
		},
		{
			name:      "negative queue size",
			conn:      newTestNetConn(t),
			queueSize: -1,
			behavior:  SendDrop,
			wantErr:   ErrNegativeQueueSize,
		},
		{
			name:      "invalid send behavior",
			conn:      newTestNetConn(t),
			queueSize: 1,
			behavior:  SendBehavior(99),
			wantErr:   ErrInvalidSendBehavior,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := NewConn(tt.conn, tt.queueSize, time.Second, tt.behavior)
			if conn != nil {
				_ = conn.Close()
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewConnInvalidConfigReturnsError(t *testing.T) {
	if _, err := NewConn(nil, 1, time.Second, SendDrop); !errors.Is(err, ErrNilNetConn) {
		t.Fatalf("NewConn invalid config error = %v, want %v", err, ErrNilNetConn)
	}
}

func TestSetPingPeriodAndPongWaitRejectInvalidDurations(t *testing.T) {
	c := &Conn{}

	if err := c.SetPingPeriod(0); !errors.Is(err, ErrInvalidPingPeriod) {
		t.Fatalf("SetPingPeriod(0) error = %v, want %v", err, ErrInvalidPingPeriod)
	}
	if err := c.SetPingPeriod(time.Second); err != nil {
		t.Fatalf("SetPingPeriod valid error: %v", err)
	}
	if got := time.Duration(c.pingPeriod.Load()); got != time.Second {
		t.Fatalf("pingPeriod = %v, want 1s", got)
	}
	if err := c.SetPingPeriod(defaultPongWait); !errors.Is(err, ErrInvalidPingPeriod) {
		t.Fatalf("SetPingPeriod >= pong wait error = %v, want %v", err, ErrInvalidPingPeriod)
	}

	if err := c.SetPongWait(-time.Second); !errors.Is(err, ErrInvalidPongWait) {
		t.Fatalf("SetPongWait(-1s) error = %v, want %v", err, ErrInvalidPongWait)
	}
	if err := c.SetPongWait(2 * time.Second); err != nil {
		t.Fatalf("SetPongWait valid error: %v", err)
	}
	if got := time.Duration(c.pongWait.Load()); got != 2*time.Second {
		t.Fatalf("pongWait = %v, want 2s", got)
	}
	if err := c.SetPongWait(time.Second); !errors.Is(err, ErrInvalidPongWait) {
		t.Fatalf("SetPongWait <= ping period error = %v, want %v", err, ErrInvalidPongWait)
	}
}

func newTestNetConn(t *testing.T) net.Conn {
	t.Helper()

	server, client := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	return server
}

func TestWriterPumpFragmentation(t *testing.T) {
	c := &Conn{
		sendQueue: make(chan Outbound, 1),
		closeC:    make(chan struct{}),
	}
	c.pingPeriod.Store(int64(1 * time.Second))

	largeData := bytes.Repeat([]byte("x"), maxFragmentSize*2+100)
	c.sendQueue <- Outbound{Op: OpcodeBinary, Data: largeData}
	c.Close()

	select {
	case out := <-c.sendQueue:
		if len(out.Data) != len(largeData) {
			t.Errorf("expected data length %d, got %d", len(largeData), len(out.Data))
		}
	default:
		t.Error("message not found in queue")
	}
}

func TestPongMonitor(t *testing.T) {
	c := &Conn{
		closeC: make(chan struct{}),
	}
	c.lastPong.Store(time.Now().Add(-2 * time.Second).UnixNano())
	c.pingPeriod.Store(int64(10 * time.Millisecond))
	c.pongWait.Store(int64(5 * time.Millisecond))

	done := make(chan bool)
	go func() {
		ticker := time.NewTicker(time.Duration(c.pongWait.Load()) / 3)
		defer ticker.Stop()

		for {
			select {
			case <-c.closeC:
				done <- true
				return
			case <-ticker.C:
				last := time.Unix(0, c.lastPong.Load())
				if time.Since(last) > time.Duration(c.pongWait.Load()) {
					c.Close()
				}
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("pongMonitor did not close connection")
	}
}

func TestReadMessageReaderClosed(t *testing.T) {
	c := &Conn{
		closeC: make(chan struct{}),
	}
	c.Close()

	_, _, err := c.ReadMessageReader()
	assertErrorIsOrContains(t, err, ErrConnClosed, "closed")
}

func TestStreamReaderWithBuffer(t *testing.T) {
	sr := &streamReader{
		parent: &Conn{},
		op:     0,
	}

	sr.buf.WriteString("testdata")
	sr.done = true

	buf := make([]byte, 4)
	n, err := sr.Read(buf)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 bytes, got %d", n)
	}
	if string(buf) != "test" {
		t.Errorf("expected 'test', got %s", string(buf))
	}

	n, err = sr.Read(buf)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 bytes, got %d", n)
	}
	if string(buf[:n]) != "data" {
		t.Errorf("expected 'data', got %s", string(buf[:n]))
	}

	n, err = sr.Read(buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes, got %d", n)
	}
}

func TestStreamReaderReadError(t *testing.T) {
	sr := &streamReader{
		parent:  &Conn{},
		op:      0,
		readErr: errors.New("test error"),
		done:    true,
	}

	buf := make([]byte, 10)
	_, err := sr.Read(buf)
	if err == nil || err.Error() != "test error" {
		t.Errorf("expected 'test error', got %v", err)
	}
}

func TestServeWSWithConfig_HijackFailure(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 10})
	defer hub.Stop()
	secret := validSecret()
	auth := mustSimpleRoomAuth(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==") // Valid WebSocket Key
	r.Header.Set("Authorization", "Bearer "+testJWTToken(t, secret))

	ServeWSWithConfig(w, r, ServerConfig{
		Hub:             hub,
		TokenAuth:       mustSimpleHS256TokenAuth(t, secret),
		RoomAuth:        auth,
		QueueSize:       10,
		SendTimeout:     5 * time.Second,
		SendBehavior:    SendDrop,
		AllowAllOrigins: true,
	})

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, w.Code)
	}
	assertWebSocketError(t, w, http.StatusInternalServerError, codeWebSocketHijackUnsupported, "websocket hijack unsupported")
}

func TestConnWriteMessageWithBehavior(t *testing.T) {
	tests := []struct {
		name      string
		behavior  SendBehavior
		timeout   time.Duration
		setupFunc func(*Conn)
		expectErr bool
		errMsg    string
	}{
		{
			name:     "block with timeout success",
			behavior: SendBlock,
			timeout:  100 * time.Millisecond,
			setupFunc: func(c *Conn) {
			},
			expectErr: false,
		},
		{
			name:     "block with timeout failure",
			behavior: SendBlock,
			timeout:  10 * time.Millisecond,
			setupFunc: func(c *Conn) {
				c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}
			},
			expectErr: true,
			errMsg:    "deadline", // context.DeadlineExceeded
		},
		{
			name:     "drop when full",
			behavior: SendDrop,
			timeout:  0,
			setupFunc: func(c *Conn) {
				c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}
			},
			expectErr: true,
			errMsg:    "queue full", // ErrQueueFull
		},
		{
			name:     "close when full",
			behavior: SendClose,
			timeout:  0,
			setupFunc: func(c *Conn) {
				c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}
			},
			expectErr: true,
			errMsg:    "closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Conn{
				sendQueue:    make(chan Outbound, 1),
				closeC:       make(chan struct{}),
				sendTimeout:  tt.timeout,
				sendBehavior: tt.behavior,
			}

			tt.setupFunc(c)

			err := c.WriteMessage(OpcodeText, []byte("test"))

			if tt.expectErr {
				assertErrorContains(t, err, tt.errMsg)
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestServeWSWithConfig_QueryTokenDisabled(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 10})
	defer hub.Stop()
	secret := validSecret()
	auth := mustSimpleRoomAuth(t)
	w := &testHijackWriter{
		httptest.NewRecorder(),
	}

	r := httptest.NewRequest("GET", "/ws?room=test&token="+testJWTToken(t, secret), nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	ServeWSWithConfig(w, r, ServerConfig{
		Hub:             hub,
		TokenAuth:       mustSimpleHS256TokenAuth(t, secret),
		RoomAuth:        auth,
		QueueSize:       10,
		SendTimeout:     5 * time.Second,
		SendBehavior:    SendDrop,
		AllowAllOrigins: true,
	})

	assertWebSocketError(t, w.ResponseRecorder, http.StatusUnauthorized, codeWebSocketTokenRequired, "websocket token required")
}

type testHijackWriter struct {
	*httptest.ResponseRecorder
}

func (w *testHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack not supported")
}

func TestConnWriteMessageBlockWithZeroTimeout(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendTimeout:  0,
		sendBehavior: SendBlock,
	}

	c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}

	go func() {
		time.Sleep(50 * time.Millisecond)
		<-c.sendQueue
	}()

	done := make(chan error, 1)
	go func() {
		err := c.WriteMessage(OpcodeText, []byte("test"))
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("write blocked indefinitely")
	}
}

func TestConnWriteMessageBlockWithClose(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendTimeout:  100 * time.Millisecond,
		sendBehavior: SendBlock,
	}

	c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}

	go func() {
		time.Sleep(20 * time.Millisecond)
		c.Close()
	}()

	err := c.WriteMessage(OpcodeText, []byte("test"))
	assertErrorContains(t, err, "closed")
}
