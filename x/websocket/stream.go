package websocket

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// streamReader implements io.ReadCloser over one bounded WebSocket message.
// It reads continuation frames incrementally, while enforcing the parent
// connection's read limit across the total message payload.
type streamReader struct {
	parent    *Conn
	op        byte
	buf       bytes.Buffer
	done      bool
	readErr   error
	readBytes int64
	readMu    sync.Mutex
}

// streamReaderPool recycles streamReader instances to reduce per-message
// heap allocation. Because streamReader embeds a bytes.Buffer, pooling also
// reuses the buffer's internal backing array, saving an extra allocation for
// every received frame.
var streamReaderPool = sync.Pool{
	New: func() any { return new(streamReader) },
}

func (sr *streamReader) Read(p []byte) (int, error) {
	sr.readMu.Lock()
	defer sr.readMu.Unlock()

	// Capture parent once. Close() nils sr.parent, so capturing it here
	// into a local prevents a nil-dereference if Close() races with Read()
	// (which is a caller contract violation, but we defend against it anyway).
	parent := sr.parent
	if parent == nil {
		return 0, io.ErrClosedPipe
	}

	for {
		if sr.buf.Len() > 0 {
			return sr.buf.Read(p)
		}
		if sr.done {
			if sr.readErr != nil {
				return 0, sr.readErr
			}
			return 0, io.EOF
		}
		// need to pull next frame(s) from connection
		op, fin, payload, err := parent.readFrame()
		if err != nil {
			sr.readErr = err
			sr.done = true
			return 0, err
		}
		// control frames may appear in middle
		switch op {
		case opcodePing:
			_ = parent.writeFrame(opcodePong, true, payload)
			continue
		case opcodePong:
			parent.lastPong.Store(time.Now().UnixNano())
			continue
		case opcodeClose:
			_ = parent.writeFrame(opcodeClose, true, payload)
			sr.readErr = io.EOF
			sr.done = true
			return 0, io.EOF
		case opcodeContinuation:
			if sr.op == 0 {
				sr.readErr = ErrProtocolError
				sr.done = true
				return 0, sr.readErr
			}
			if err := sr.appendPayload(parent, payload); err != nil {
				sr.readErr = err
				sr.done = true
				return 0, err
			}
			if fin {
				sr.done = true
			}
			continue
		case OpcodeText, OpcodeBinary:
			// if new data opcode arrives while not started, treat as start
			// if already started assembling and new data opcode arrives -> protocol error
			if sr.op == 0 {
				sr.op = op
				if err := sr.appendPayload(parent, payload); err != nil {
					sr.readErr = err
					sr.done = true
					return 0, err
				}
				if fin {
					sr.done = true
				}
				continue
			} else {
				// shouldn't get new data opcode while assembling
				sr.readErr = ErrProtocolError
				sr.done = true
				return 0, sr.readErr
			}
		default:
			// ignore other opcodes
			continue
		}
	}
}

func (sr *streamReader) appendPayload(parent *Conn, payload []byte) error {
	next := sr.readBytes + int64(len(payload))
	if limit := parent.readLimit.Load(); limit >= 0 && next > limit {
		return ErrPayloadTooLarge
	}
	sr.readBytes = next
	_, _ = sr.buf.Write(payload)
	return nil
}

func (sr *streamReader) Close() error {
	sr.readMu.Lock()
	parent := sr.parent
	if parent == nil {
		resetStreamReader(sr)
		sr.readMu.Unlock()
		return nil
	}
	unfinished := !sr.done
	resetStreamReader(sr)
	sr.readMu.Unlock()

	if unfinished {
		_ = parent.Close()
	}
	if sr.buf.Cap() <= maxPooledMessageBufferCap {
		streamReaderPool.Put(sr)
	}
	return nil
}

func resetStreamReader(sr *streamReader) {
	sr.parent = nil
	sr.op = 0
	sr.done = true
	sr.readErr = nil
	sr.readBytes = 0
	if sr.buf.Cap() > maxPooledMessageBufferCap {
		sr.buf = bytes.Buffer{}
		return
	}
	sr.buf.Reset()
}

// ReadMessageReader returns an opcode and a bounded buffered reader for one
// message.
//
// The reader consumes continuation frames incrementally and enforces ReadLimit
// across the complete fragmented message, but it is not a zero-copy or
// unbounded streaming API: each frame payload is buffered before being returned
// through the reader. Read to EOF and then close the reader to consume the
// complete message. Close can discard unread buffered data once the final
// frame has arrived. If continuation frames are still pending, Close closes
// the parent connection. Release the reader with Close and do not use it again.
func (c *Conn) ReadMessageReader() (byte, io.ReadCloser, error) {
	if c.IsClosed() {
		return 0, nil, ErrConnClosed
	}
	// read first frame - must be text/binary or control
	for {
		op, fin, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case OpcodeText, OpcodeBinary:
			// Get a pooled reader and reset all fields from any prior use.
			sr := streamReaderPool.Get().(*streamReader)
			sr.parent = c
			sr.op = 0
			sr.done = false
			sr.readErr = nil
			sr.readBytes = 0
			sr.buf.Reset() // keeps the backing array; avoids re-allocation

			if err := sr.appendPayload(c, payload); err != nil {
				sr.parent = nil
				streamReaderPool.Put(sr)
				return 0, nil, err
			}
			if fin {
				sr.done = true
			} else {
				// not finished; set op marker and let subsequent continuation frames be read by Read
				sr.op = op
			}
			return op, sr, nil
		case opcodePing:
			_ = c.writeFrame(opcodePong, true, payload)
			continue
		case opcodePong:
			c.lastPong.Store(time.Now().UnixNano())
			continue
		case opcodeClose:
			_ = c.writeFrame(opcodeClose, true, payload)
			return 0, nil, io.EOF
		case opcodeContinuation:
			return 0, nil, ErrProtocolError
		default:
			return 0, nil, ErrProtocolError
		}
	}
}

// ReadMessage reads a complete message into memory and returns an owned copy of
// the message payload.
func (c *Conn) ReadMessage() (byte, []byte, error) {
	op, stream, err := c.ReadMessageReader()
	if err != nil {
		return 0, nil, err
	}

	buf := msgBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	_, err = io.Copy(buf, stream)
	_ = stream.Close()
	if err != nil {
		putMessageBuffer(buf)
		return 0, nil, err
	}
	// Copy data before returning buf to pool; callers expect to own the slice.
	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())
	putMessageBuffer(buf)
	return op, data, nil
}
