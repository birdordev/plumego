package websocket

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type failingWriteConn struct {
	writeErr error
}

func (c *failingWriteConn) Read(_ []byte) (int, error)  { return 0, io.EOF }
func (c *failingWriteConn) Write(_ []byte) (int, error) { return 0, c.writeErr }
func (c *failingWriteConn) Close() error                { return nil }
func (c *failingWriteConn) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (c *failingWriteConn) RemoteAddr() net.Addr        { return &net.TCPAddr{} }
func (c *failingWriteConn) SetDeadline(_ time.Time) error {
	return nil
}
func (c *failingWriteConn) SetReadDeadline(_ time.Time) error {
	return nil
}
func (c *failingWriteConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

type deadlineRecordingConn struct {
	writeDeadlines []time.Time
}

func (c *deadlineRecordingConn) Read(_ []byte) (int, error)  { return 0, io.EOF }
func (c *deadlineRecordingConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *deadlineRecordingConn) Close() error                { return nil }
func (c *deadlineRecordingConn) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (c *deadlineRecordingConn) RemoteAddr() net.Addr        { return &net.TCPAddr{} }
func (c *deadlineRecordingConn) SetDeadline(_ time.Time) error {
	return nil
}
func (c *deadlineRecordingConn) SetReadDeadline(_ time.Time) error {
	return nil
}
func (c *deadlineRecordingConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadlines = append(c.writeDeadlines, t)
	return nil
}

func TestWriterPumpClosesOnWriteError(t *testing.T) {
	rawConn := &failingWriteConn{writeErr: errors.New("write failed")}
	c := &Conn{
		conn:      rawConn,
		bw:        bufio.NewWriterSize(rawConn, defaultBufSize),
		sendQueue: make(chan Outbound, 1),
		closeC:    make(chan struct{}),
	}
	c.pingPeriod.Store(int64(time.Hour))

	done := make(chan struct{})
	go func() {
		c.writerPump()
		close(done)
	}()

	c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("x")}

	select {
	case <-c.closeC:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("connection was not closed on write error")
	}

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("writerPump did not exit after write error")
	}
}

func TestWriterPumpClosesOnPingWriteError(t *testing.T) {
	rawConn := &failingWriteConn{writeErr: errors.New("ping write failed")}
	c := &Conn{
		conn:      rawConn,
		bw:        bufio.NewWriterSize(rawConn, defaultBufSize),
		sendQueue: make(chan Outbound, 1),
		closeC:    make(chan struct{}),
	}
	c.pingPeriod.Store(int64(10 * time.Millisecond))

	done := make(chan struct{})
	go func() {
		c.writerPump()
		close(done)
	}()

	select {
	case <-c.closeC:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("connection was not closed after ping write error")
	}

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("writerPump did not exit after ping write error")
	}
}

func TestWriterPumpHandlesInvalidStoredPingPeriod(t *testing.T) {
	rawConn := &failingWriteConn{writeErr: errors.New("closed")}
	c := &Conn{
		conn:      rawConn,
		bw:        bufio.NewWriterSize(rawConn, defaultBufSize),
		sendQueue: make(chan Outbound, 1),
		closeC:    make(chan struct{}),
	}
	c.pingPeriod.Store(0)

	done := make(chan struct{})
	go func() {
		c.writerPump()
		close(done)
	}()

	c.Close()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("writerPump did not exit with invalid stored ping period")
	}
}

func TestPongMonitorHandlesInvalidStoredPongWait(t *testing.T) {
	c := &Conn{
		closeC: make(chan struct{}),
	}
	c.pongWait.Store(0)

	done := make(chan struct{})
	go func() {
		c.pongMonitor()
		close(done)
	}()

	c.Close()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("pongMonitor did not exit with invalid stored pong wait")
	}
}

func TestWriteMessageContextBlocksUntilQueueSpace(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendBlock,
	}
	c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- c.WriteMessageContext(ctx, OpcodeText, []byte("next"))
	}()

	select {
	case err := <-done:
		t.Fatalf("WriteMessageContext returned before queue space: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	<-c.sendQueue

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WriteMessageContext error: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("WriteMessageContext did not enqueue after queue space")
	}
}

func TestWriteMessageContextCopiesPayload(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendDrop,
	}
	payload := []byte("hello")
	if err := c.WriteMessageContext(context.Background(), OpcodeText, payload); err != nil {
		t.Fatalf("WriteMessageContext: %v", err)
	}
	payload[0] = 'j'

	out := <-c.sendQueue
	if string(out.Data) != "hello" {
		t.Fatalf("queued payload aliased caller buffer: %q", out.Data)
	}
}

func TestWriteMessageContextReturnsOnCloseWhileBlocked(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendBlock,
	}
	c.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}

	done := make(chan error, 1)
	go func() {
		done <- c.WriteMessageContext(context.Background(), OpcodeText, []byte("next"))
	}()

	select {
	case err := <-done:
		t.Fatalf("WriteMessageContext returned before close: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	_ = c.Close()

	select {
	case err := <-done:
		if !errors.Is(err, ErrConnClosed) {
			t.Fatalf("error = %v, want %v", err, ErrConnClosed)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("WriteMessageContext did not return after close")
	}
}

func TestWriteMessageContextDoesNotQueueAfterCancellation(t *testing.T) {
	c := &Conn{
		sendQueue:    make(chan Outbound, 1),
		closeC:       make(chan struct{}),
		sendBehavior: SendBlock,
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := c.WriteMessageContext(ctx, OpcodeText, []byte("canceled"))
	if !errors.Is(err, context.Canceled) || len(c.sendQueue) != 0 {
		t.Fatalf("write = %v, queue length = %d", err, len(c.sendQueue))
	}
	if err := c.WriteMessageContext(nil, OpcodeText, []byte("live")); err != nil {
		t.Fatal(err)
	}
	if got := string((<-c.sendQueue).Data); got != "live" {
		t.Fatalf("queued data = %q", got)
	}
}
