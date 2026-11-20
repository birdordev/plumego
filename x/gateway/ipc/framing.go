package ipc

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// FramedClient extends Client with message framing support using length-prefix protocol.
// Messages are encoded with a 4-byte length prefix (big-endian) followed by the payload.
type FramedClient interface {
	Client
	// WriteMessage writes a complete message with length prefix
	WriteMessage(msg []byte) error
	// WriteMessageWithTimeout writes a message with timeout
	WriteMessageWithTimeout(msg []byte, timeout time.Duration) error
	// ReadMessage reads a complete message
	ReadMessage() ([]byte, error)
	// ReadMessageWithTimeout reads a message with timeout
	ReadMessageWithTimeout(timeout time.Duration) ([]byte, error)
}

// framedClient wraps a Client with message framing support
type framedClient struct {
	client  Client
	readMu  sync.Mutex
	writeMu sync.Mutex
}

// NewFramedClient creates a framed client wrapper with length-prefix protocol.
// The protocol uses 4 bytes (uint32, big-endian) for message length, followed by the payload.
// Maximum message size is 16MB. Framed reads and writes serialize separately;
// the underlying client must support a read and a write in parallel.
func NewFramedClient(client Client) FramedClient {
	return &framedClient{
		client: client,
	}
}

const (
	maxFrameSize = 16 * 1024 * 1024 // 16MB max message size
	frameLenSize = 4                // 4 bytes for uint32 length
)

func (fc *framedClient) WriteMessage(msg []byte) error {
	return fc.WriteMessageWithTimeout(msg, 0)
}

func (fc *framedClient) WriteMessageWithTimeout(msg []byte, timeout time.Duration) error {
	if len(msg) > maxFrameSize {
		return fmt.Errorf("message too large: %d bytes (max %d)", len(msg), maxFrameSize)
	}

	fc.writeMu.Lock()
	defer fc.writeMu.Unlock()

	// Encode length prefix (4 bytes, big-endian)
	lengthPrefix := make([]byte, frameLenSize)
	lengthPrefix[0] = byte(len(msg) >> 24)
	lengthPrefix[1] = byte(len(msg) >> 16)
	lengthPrefix[2] = byte(len(msg) >> 8)
	lengthPrefix[3] = byte(len(msg))

	// Write length prefix
	var n int
	var err error
	if timeout > 0 {
		n, err = fc.client.WriteWithTimeout(lengthPrefix, timeout)
	} else {
		n, err = fc.client.Write(lengthPrefix)
	}
	if err != nil {
		return err
	}
	if n != frameLenSize {
		return fmt.Errorf("failed to write frame length: wrote %d bytes, expected %d", n, frameLenSize)
	}

	// Write message payload
	if timeout > 0 {
		n, err = fc.client.WriteWithTimeout(msg, timeout)
	} else {
		n, err = fc.client.Write(msg)
	}
	if err != nil {
		return err
	}
	if n != len(msg) {
		return fmt.Errorf("failed to write message: wrote %d bytes, expected %d", n, len(msg))
	}

	return nil
}

func (fc *framedClient) ReadMessage() ([]byte, error) {
	return fc.ReadMessageWithTimeout(0)
}

func (fc *framedClient) ReadMessageWithTimeout(timeout time.Duration) ([]byte, error) {
	fc.readMu.Lock()
	defer fc.readMu.Unlock()

	// Read length prefix (4 bytes)
	lengthPrefix := make([]byte, frameLenSize)
	var n int
	var err error
	if timeout > 0 {
		n, err = io.ReadFull(timeoutReader{fc.client, timeout}, lengthPrefix)
	} else {
		n, err = io.ReadFull(fc.client, lengthPrefix)
	}
	if err != nil {
		return nil, err
	}
	if n != frameLenSize {
		return nil, fmt.Errorf("failed to read frame length: read %d bytes, expected %d", n, frameLenSize)
	}

	// Decode message length (big-endian)
	msgLen := uint32(lengthPrefix[0])<<24 |
		uint32(lengthPrefix[1])<<16 |
		uint32(lengthPrefix[2])<<8 |
		uint32(lengthPrefix[3])

	if msgLen > maxFrameSize {
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", msgLen, maxFrameSize)
	}

	if msgLen == 0 {
		return []byte{}, nil
	}

	// Read message payload
	msg := make([]byte, msgLen)
	if timeout > 0 {
		n, err = io.ReadFull(timeoutReader{fc.client, timeout}, msg)
	} else {
		n, err = io.ReadFull(fc.client, msg)
	}
	if err != nil {
		return nil, err
	}
	if n != int(msgLen) {
		return nil, fmt.Errorf("failed to read message: read %d bytes, expected %d", n, msgLen)
	}

	return msg, nil
}

// Delegate Client interface methods to underlying client
func (fc *framedClient) Write(data []byte) (int, error) {
	return fc.client.Write(data)
}

func (fc *framedClient) WriteWithTimeout(data []byte, timeout time.Duration) (int, error) {
	return fc.client.WriteWithTimeout(data, timeout)
}

func (fc *framedClient) Read(buf []byte) (int, error) {
	return fc.client.Read(buf)
}

func (fc *framedClient) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	return fc.client.ReadWithTimeout(buf, timeout)
}

func (fc *framedClient) RemoteAddr() net.Addr {
	return fc.client.RemoteAddr()
}

func (fc *framedClient) RemoteAddrString() string {
	return fc.client.RemoteAddrString()
}

func (fc *framedClient) Close() error {
	return fc.client.Close()
}

// timeoutReader implements io.Reader with timeout support
type timeoutReader struct {
	client  Client
	timeout time.Duration
}

func (tr timeoutReader) Read(p []byte) (int, error) {
	return tr.client.ReadWithTimeout(p, tr.timeout)
}
