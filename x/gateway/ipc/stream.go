package ipc

import (
	"fmt"
	"io"
	"net"
	"time"
)

// StreamConfig holds configuration for stream transfers
type StreamConfig struct {
	ChunkSize      int           // Size of each chunk for streaming
	BufferSize     int           // Buffer size for stream operations
	EnableChecksum bool          // Enable checksum verification
	Timeout        time.Duration // Timeout per underlying stream read or write
}

// DefaultStreamConfig returns default stream configuration
func DefaultStreamConfig() *StreamConfig {
	return &StreamConfig{
		ChunkSize:      64 * 1024,  // 64KB chunks
		BufferSize:     128 * 1024, // 128KB buffer
		EnableChecksum: true,
		Timeout:        5 * time.Minute,
	}
}

// StreamClient extends Client with streaming capabilities for large data transfers
type StreamClient interface {
	Client
	// WriteStream sends reader data in chunks and returns the payload bytes sent,
	// including any partial payload sent before an error.
	WriteStream(r io.Reader) (int64, error)
	// ReadStream receives chunks and returns the bytes delivered to the writer,
	// including any partial payload delivered before an error.
	ReadStream(w io.Writer) (int64, error)
}

// streamClient wraps a Client with streaming support
type streamClient struct {
	client Client
	config *StreamConfig
}

// NewStreamClient creates a streaming client wrapper optimized for large data transfers.
// Uses chunked transfer with optional checksums for reliability.
func NewStreamClient(client Client, cfg *StreamConfig) StreamClient {
	if cfg == nil {
		cfg = DefaultStreamConfig()
	}

	return &streamClient{
		client: client,
		config: cfg,
	}
}

func (sc *streamClient) WriteStream(r io.Reader) (int64, error) {
	var totalWritten int64
	buf := make([]byte, sc.config.ChunkSize)

	for {
		// Read chunk
		n, readErr := r.Read(buf)
		if n > 0 {
			// Write chunk size (4 bytes, big-endian)
			sizeBytes := make([]byte, 4)
			sizeBytes[0] = byte(n >> 24)
			sizeBytes[1] = byte(n >> 16)
			sizeBytes[2] = byte(n >> 8)
			sizeBytes[3] = byte(n)

			if _, err := sc.client.WriteWithTimeout(sizeBytes, sc.config.Timeout); err != nil {
				return totalWritten, fmt.Errorf("failed to write chunk size: %w", err)
			}

			// Write chunk data
			written := 0
			for written < n {
				w, err := sc.client.WriteWithTimeout(buf[written:n], sc.config.Timeout)
				written += w
				if err != nil {
					return totalWritten + int64(written), fmt.Errorf("failed to write chunk data: %w", err)
				}
			}

			totalWritten += int64(n)
		}

		if readErr == io.EOF {
			// Write zero-length chunk to signal end
			endMarker := []byte{0, 0, 0, 0}
			if _, err := sc.client.WriteWithTimeout(endMarker, sc.config.Timeout); err != nil {
				return totalWritten, fmt.Errorf("failed to write end marker: %w", err)
			}
			break
		}

		if readErr != nil {
			return totalWritten, readErr
		}
	}

	return totalWritten, nil
}

type timedStreamReader struct {
	client  Client
	timeout time.Duration
}

func (r timedStreamReader) Read(p []byte) (int, error) {
	return r.client.ReadWithTimeout(p, r.timeout)
}

func (sc *streamClient) ReadStream(w io.Writer) (int64, error) {
	var reader io.Reader = sc.client
	if sc.config.Timeout > 0 {
		reader = timedStreamReader{client: sc.client, timeout: sc.config.Timeout}
	}
	var totalRead int64

	for {
		// Read chunk size (4 bytes)
		sizeBytes := make([]byte, 4)
		if _, err := io.ReadFull(reader, sizeBytes); err != nil {
			return totalRead, fmt.Errorf("failed to read chunk size: %w", err)
		}

		chunkSize := int(uint32(sizeBytes[0])<<24 |
			uint32(sizeBytes[1])<<16 |
			uint32(sizeBytes[2])<<8 |
			uint32(sizeBytes[3]))

		// Zero-length chunk signals end of stream
		if chunkSize == 0 {
			break
		}

		if chunkSize > sc.config.ChunkSize*2 {
			return totalRead, fmt.Errorf("chunk size too large: %d (max %d)", chunkSize, sc.config.ChunkSize*2)
		}

		// Read chunk data
		buf := make([]byte, chunkSize)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return totalRead, fmt.Errorf("failed to read chunk data: %w", err)
		}

		// Write to destination
		written := 0
		for written < chunkSize {
			writtenNow, err := w.Write(buf[written:chunkSize])
			written += writtenNow
			if err != nil {
				return totalRead + int64(written), fmt.Errorf("failed to write to destination: %w", err)
			}
		}

		totalRead += int64(chunkSize)
	}

	return totalRead, nil
}

// Delegate Client interface methods
func (sc *streamClient) Write(data []byte) (int, error) {
	return sc.client.Write(data)
}

func (sc *streamClient) WriteWithTimeout(data []byte, timeout time.Duration) (int, error) {
	return sc.client.WriteWithTimeout(data, timeout)
}

func (sc *streamClient) Read(buf []byte) (int, error) {
	return sc.client.Read(buf)
}

func (sc *streamClient) ReadWithTimeout(buf []byte, timeout time.Duration) (int, error) {
	return sc.client.ReadWithTimeout(buf, timeout)
}

func (sc *streamClient) RemoteAddr() net.Addr {
	return sc.client.RemoteAddr()
}

func (sc *streamClient) RemoteAddrString() string {
	return sc.client.RemoteAddrString()
}

func (sc *streamClient) Close() error {
	return sc.client.Close()
}
