package websocket

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	defaultReadLimit          int64 = 16 << 20
	maxReadLimit              int64 = 64 << 20
	maxPooledMessageBufferCap       = 64 << 10
)

// msgBufPool reuses bytes.Buffer instances across read operations
// to reduce allocator pressure from per-message buffer creation.
var msgBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

func putMessageBuffer(buf *bytes.Buffer) bool {
	if buf == nil {
		return false
	}
	if buf.Cap() > maxPooledMessageBufferCap {
		return false
	}
	buf.Reset()
	msgBufPool.Put(buf)
	return true
}

// SendBehavior determines behavior on enqueue timeout / full queue.
//
// SendBehavior controls how the connection handles message sending when the
// send queue is full or a timeout occurs.
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	// Block until space available (default)
//	conn, err := websocket.NewConn(netConn, 100, 5*time.Second, websocket.SendBlock)
//
//	// Drop message when queue full
//	conn, err := websocket.NewConn(netConn, 100, 5*time.Second, websocket.SendDrop)
//
//	// Close connection when queue full
//	conn, err := websocket.NewConn(netConn, 100, 5*time.Second, websocket.SendClose)
type SendBehavior int

const (
	// SendBlock blocks until space is available in the queue.
	// Can still timeout if context is used.
	SendBlock SendBehavior = iota

	// SendDrop drops the message when the queue is full.
	SendDrop

	// SendClose closes the connection when the queue is full.
	SendClose
)

// Outbound is an internal message for sending.
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	msg := websocket.Outbound{
//		Op:   websocket.OpcodeText,
//		Data: []byte("Hello, World!"),
//	}
type Outbound struct {
	Op   byte
	Data []byte
}

// UserInfo stores authenticated user information.
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	userInfo := websocket.UserInfo{
//		ID:    "user-123",
//		Name:  "John Doe",
//		Email: "john@example.com",
//		Roles: []string{"admin", "user"},
//	}
type UserInfo struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Email  string         `json:"email"`
	Roles  []string       `json:"roles"`
	Claims map[string]any `json:"claims"`
}

// Conn is a websocket connection wrapper with stream API and bounded queue send.
//
// Conn provides an experimental WebSocket connection with:
//   - Bounded send queue with configurable behavior
//   - Ping/pong heartbeat monitoring
//   - Read message size limits
//   - User authentication support
//   - Connection metadata storage
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	// Create connection with blocking send
//	conn, err := websocket.NewConn(netConn, 100, 5*time.Second, websocket.SendBlock)
//	defer conn.Close()
//
//	// Send a message
//	err := conn.WriteMessage(websocket.OpcodeText, []byte("Hello"))
//
//	// Read messages
//	for {
//		op, data, err := conn.ReadMessage()
//		if err != nil {
//			break
//		}
//		// Process message
//	}
//
//	// Set user info after authentication
//	conn.UserInfo = &websocket.UserInfo{
//		ID:    "user-123",
//		Name:  "John Doe",
//		Roles: []string{"admin"},
//	}
type Conn struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer

	writeMu sync.Mutex
	stateMu sync.RWMutex

	// client is true on connections returned by Dial. RFC 6455 §5.3 requires
	// client→server frames to be masked and forbids the server from masking its
	// frames; readFrame and writeFrame branch on this flag accordingly.
	client bool

	// send queue
	sendQueue chan Outbound
	// config
	sendQueueSize int
	sendTimeout   time.Duration
	sendBehavior  SendBehavior

	closeOnce sync.Once
	closed    atomic.Int32
	closeC    chan struct{}

	readLimit  atomic.Int64
	writeWait  atomic.Int64
	pingPeriod atomic.Int64 // stores time.Duration as int64 nanoseconds
	pongWait   atomic.Int64 // stores time.Duration as int64 nanoseconds
	lastPong   atomic.Int64

	// User information (set after authentication)
	UserInfo *UserInfo

	// Connection metadata (concurrent-safe)
	metadata sync.Map
}

// NewConn creates a Conn after handshake.
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	// Create connection with blocking send
//	conn, err := websocket.NewConn(netConn, 100, 5*time.Second, websocket.SendBlock)
//	defer conn.Close()
//
//	// Create connection with drop behavior
//	conn, err := websocket.NewConn(netConn, 100, 5*time.Second, websocket.SendDrop)
//
// For server-side connections obtained via http.Hijacker, prefer
// newConnFromHijack to reuse the bufio.ReadWriter that the HTTP server
// already created, avoiding a redundant allocation.
func NewConn(c net.Conn, queueSize int, sendTimeout time.Duration, behavior SendBehavior) (*Conn, error) {
	if err := validateConnConfig(c, queueSize, behavior); err != nil {
		return nil, err
	}
	return newConnFromHijack(
		c,
		bufio.NewReaderSize(c, defaultBufSize),
		bufio.NewWriterSize(c, defaultBufSize),
		queueSize, sendTimeout, behavior,
		false,
	), nil
}

func validateConnConfig(c net.Conn, queueSize int, behavior SendBehavior) error {
	if c == nil {
		return ErrNilNetConn
	}
	if queueSize < 0 {
		return ErrNegativeQueueSize
	}
	if behavior < SendBlock || behavior > SendClose {
		return ErrInvalidSendBehavior
	}
	return nil
}

// newConnFromHijack creates a Conn using buffers already allocated by the
// HTTP server's hijack operation, avoiding a redundant allocation pair.
//
// client toggles RFC 6455 framing direction. Server-side hijack constructions
// pass client=false; the Dial path passes client=true so outgoing frames are
// masked and incoming server frames are accepted unmasked.
func newConnFromHijack(c net.Conn, br *bufio.Reader, bw *bufio.Writer, queueSize int, sendTimeout time.Duration, behavior SendBehavior, client bool) *Conn {
	cc := &Conn{
		conn:          c,
		br:            br,
		bw:            bw,
		client:        client,
		sendQueue:     make(chan Outbound, queueSize),
		sendQueueSize: queueSize,
		sendTimeout:   sendTimeout,
		sendBehavior:  behavior,
		closeC:        make(chan struct{}),
	}
	cc.readLimit.Store(defaultReadLimit)
	cc.writeWait.Store(int64(defaultWriteWait))
	cc.pingPeriod.Store(int64(defaultPingPeriod))
	cc.pongWait.Store(int64(defaultPongWait))
	cc.lastPong.Store(time.Now().UnixNano())
	go cc.writerPump()
	go cc.pongMonitor()
	return cc
}

func (c *Conn) IsClosed() bool { return c.closed.Load() == 1 }

func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.stateMu.Lock()
		defer c.stateMu.Unlock()
		c.closed.Store(1)
		close(c.closeC)
		if c.conn != nil {
			err = c.conn.Close()
		}
	})
	return err
}

// SetReadLimit sets the maximum inbound message size in bytes.
func (c *Conn) SetReadLimit(limit int64) error {
	if limit == 0 {
		c.readLimit.Store(defaultReadLimit)
		return nil
	}
	if limit < 0 {
		return ErrInvalidReadLimit
	}
	if limit > maxReadLimit {
		return ErrPayloadTooLarge
	}
	c.readLimit.Store(limit)
	return nil
}

// SetPingPeriod sets the ping interval.
func (c *Conn) SetPingPeriod(d time.Duration) error {
	if d <= 0 {
		return ErrInvalidPingPeriod
	}
	if d >= positiveDurationOrDefault(c.pongWait.Load(), defaultPongWait) {
		return ErrInvalidPingPeriod
	}
	c.pingPeriod.Store(int64(d))
	return nil
}

// SetPongWait sets the pong wait time.
func (c *Conn) SetPongWait(d time.Duration) error {
	if d <= 0 {
		return ErrInvalidPongWait
	}
	if d <= positiveDurationOrDefault(c.pingPeriod.Load(), defaultPingPeriod) {
		return ErrInvalidPongWait
	}
	c.pongWait.Store(int64(d))
	return nil
}

// SetWriteTimeout sets the maximum duration for each network frame write.
func (c *Conn) SetWriteTimeout(d time.Duration) error {
	if d <= 0 {
		return ErrInvalidWriteTimeout
	}
	c.writeWait.Store(int64(d))
	return nil
}

// GetLastPong returns the last pong time
func (c *Conn) GetLastPong() time.Time {
	return time.Unix(0, c.lastPong.Load())
}

// WriteClose sends a best-effort WebSocket close frame with the given RFC 6455
// status code and human-readable reason, then closes the underlying connection.
//
// This does not wait for the peer's close frame; callers that need a full
// closing handshake must implement that higher-level coordination before
// calling Close. Calling Close() directly skips the close frame and tears down
// TCP immediately.
//
// Example:
//
//	conn.WriteClose(websocket.CloseNormalClosure, "goodbye")
//	conn.WriteClose(websocket.CloseGoingAway, "server shutting down")
func (c *Conn) WriteClose(code uint16, reason string) error {
	if c.IsClosed() {
		return ErrConnClosed
	}
	if !isValidCloseStatusCode(code) {
		return ErrInvalidCloseCode
	}
	if !utf8.ValidString(reason) {
		return ErrInvalidUTF8
	}
	if len(reason) > int(maxControlPayload)-2 {
		return ErrCloseReasonTooLong
	}
	// RFC 6455 §5.5.1: close payload is 2-byte big-endian status code followed
	// by a UTF-8 reason phrase (may be empty).
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload[:2], code)
	copy(payload[2:], reason)
	// Write directly, bypassing sendQueue so the frame is sent even when the
	// queue is full (e.g. during a slow-consumer shutdown).
	if err := c.writeFrame(opcodeClose, true, payload); err != nil {
		_ = c.Close()
		return err
	}
	return c.Close()
}

// ---------------- low-level frame IO ----------------

func (c *Conn) readFrame() (byte, bool, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(c.br, h[:]); err != nil {
		return 0, false, nil, err
	}
	fin := h[0]&finBit != 0
	op := h[0] & 0x0F
	mask := h[1]&0x80 != 0
	prefix := int64(h[1] & 0x7F)

	if h[0]&0x70 != 0 {
		return 0, false, nil, ErrProtocolError
	}
	if !isValidFrameOpcode(op) {
		return 0, false, nil, ErrProtocolError
	}
	// RFC 6455 §5.1/§5.3: client→server frames MUST be masked; server→client
	// frames MUST NOT be masked. Direction depends on which side this Conn is on.
	if c.client {
		if mask {
			return 0, false, nil, ErrUnexpectedMaskedFrame
		}
	} else {
		if !mask {
			return 0, false, nil, ErrUnmaskedFrame
		}
	}

	var payloadLen int64
	switch prefix {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, false, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint16(ext[:]))
		if payloadLen < 126 {
			return 0, false, nil, ErrProtocolError
		}
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, false, nil, err
		}
		// RFC 6455 §5.2: the MSB of a 64-bit payload length MUST be 0.
		// A negative int64 means the MSB is set; reject to prevent a panic in make().
		payloadLen = int64(binary.BigEndian.Uint64(ext[:]))
		if payloadLen < 0 {
			return 0, false, nil, ErrProtocolError
		}
		if payloadLen <= 0xFFFF {
			return 0, false, nil, ErrProtocolError
		}
	default:
		payloadLen = prefix
	}

	// Reject oversized frames before reading the mask key so we avoid
	// a 4-byte network round-trip for frames we will reject anyway.
	if payloadLen > c.readLimit.Load() {
		return 0, false, nil, ErrPayloadTooLarge
	}

	var maskKey [4]byte
	if mask {
		if _, err := io.ReadFull(c.br, maskKey[:]); err != nil {
			return 0, false, nil, err
		}
	}
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return 0, false, nil, err
		}
		if mask {
			applyMask(payload, maskKey)
		}
	}

	// control frame checks
	if op >= 0x8 {
		if !fin {
			return 0, false, nil, ErrFragmentedControl
		}
		if int64(len(payload)) > maxControlPayload {
			return 0, false, nil, ErrControlTooLarge
		}
		if op == opcodeClose && !isValidClosePayload(payload) {
			return 0, false, nil, ErrProtocolError
		}
	}
	return op, fin, payload, nil
}

func isValidFrameOpcode(op byte) bool {
	switch op {
	case opcodeContinuation, OpcodeText, OpcodeBinary, opcodeClose, opcodePing, opcodePong:
		return true
	default:
		return false
	}
}

func isValidClosePayload(payload []byte) bool {
	if len(payload) == 0 {
		return true
	}
	if len(payload) == 1 {
		return false
	}
	code := binary.BigEndian.Uint16(payload[:2])
	if !isValidCloseStatusCode(code) {
		return false
	}
	return utf8.Valid(payload[2:])
}

func isValidCloseStatusCode(code uint16) bool {
	switch {
	case code == CloseNormalClosure || code == CloseGoingAway || code == CloseProtocolError ||
		code == CloseUnsupportedData || code == CloseInvalidPayload || code == ClosePolicyViolation ||
		code == CloseMessageTooBig || code == CloseServerError || code == 1012 || code == 1013 || code == 1014:
		return true
	case code >= 3000 && code <= 4999:
		return true
	default:
		return false
	}
}

func (c *Conn) writeFrame(op byte, fin bool, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if writeWait := time.Duration(c.writeWait.Load()); writeWait > 0 {
		if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
			return err
		}
		defer func() { _ = c.conn.SetWriteDeadline(time.Time{}) }()
	}

	var header [14]byte
	hlen := 0
	b0 := byte(0)
	if fin {
		b0 |= finBit
	}
	b0 |= op & 0x0F
	header[0] = b0
	hlen = 1

	// RFC 6455 §5.3: client→server frames MUST be masked. Set the mask bit and
	// reserve room for the 4-byte mask key, written after the length prefix.
	maskBit := byte(0)
	if c.client {
		maskBit = 0x80
	}

	n := len(payload)
	switch {
	case n <= 125:
		header[hlen] = maskBit | byte(n)
		hlen++
	case n <= 0xFFFF:
		header[hlen] = maskBit | 126
		hlen++
		header[hlen] = byte(n >> 8)
		header[hlen+1] = byte(n)
		hlen += 2
	default:
		header[hlen] = maskBit | 127
		hlen++
		binary.BigEndian.PutUint64(header[hlen:hlen+8], uint64(n))
		hlen += 8
	}

	var maskKey [4]byte
	if c.client {
		if _, err := io.ReadFull(rand.Reader, maskKey[:]); err != nil {
			return err
		}
		copy(header[hlen:hlen+4], maskKey[:])
		hlen += 4
	}

	if _, err := c.bw.Write(header[:hlen]); err != nil {
		return err
	}
	if n > 0 {
		if c.client {
			// Mask in place on a private copy so the caller's slice is not
			// mutated; this also keeps message-pool buffers reusable.
			masked := make([]byte, n)
			copy(masked, payload)
			applyMask(masked, maskKey)
			if _, err := c.bw.Write(masked); err != nil {
				return err
			}
		} else {
			if _, err := c.bw.Write(payload); err != nil {
				return err
			}
		}
	}
	return c.bw.Flush()
}

// applyMask XORs payload in place with the 4-byte WebSocket mask key.
//
// Processes 4 bytes per iteration to let the compiler unroll the loop; avoids
// the modulo in the byte-by-byte tail. Used for both inbound unmasking
// (server-side) and outbound masking (client-side).
func applyMask(payload []byte, key [4]byte) {
	n := len(payload)
	i := 0
	for ; i+3 < n; i += 4 {
		payload[i] ^= key[0]
		payload[i+1] ^= key[1]
		payload[i+2] ^= key[2]
		payload[i+3] ^= key[3]
	}
	for ; i < n; i++ {
		payload[i] ^= key[i%4]
	}
}

// SetMetadata sets a metadata value for the connection (concurrent-safe).
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	conn, err := websocket.NewConn(...)
//	conn.SetMetadata("session_id", "abc123")
//	conn.SetMetadata("client_ip", "192.168.1.1")
func (c *Conn) SetMetadata(key string, value any) {
	c.metadata.Store(key, value)
}

// GetMetadata retrieves a metadata value for the connection (concurrent-safe).
//
// Returns the value and true if found, nil and false otherwise.
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	conn, err := websocket.NewConn(...)
//	conn.SetMetadata("session_id", "abc123")
//	if sessionID, ok := conn.GetMetadata("session_id"); ok {
//		fmt.Printf("Session ID: %v\n", sessionID)
//	}
func (c *Conn) GetMetadata(key string) (any, bool) {
	return c.metadata.Load(key)
}

// DeleteMetadata removes a metadata value from the connection (concurrent-safe).
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	conn, err := websocket.NewConn(...)
//	conn.SetMetadata("temp_data", "value")
//	conn.DeleteMetadata("temp_data")
func (c *Conn) DeleteMetadata(key string) {
	c.metadata.Delete(key)
}

// RangeMetadata iterates over all metadata key-value pairs (concurrent-safe).
//
// The iteration stops if the function returns false.
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	conn, err := websocket.NewConn(...)
//	conn.SetMetadata("key1", "value1")
//	conn.SetMetadata("key2", "value2")
//	conn.RangeMetadata(func(key, value any) bool {
//		fmt.Printf("%v: %v\n", key, value)
//		return true // continue iteration
//	})
func (c *Conn) RangeMetadata(f func(key, value any) bool) {
	c.metadata.Range(f)
}
