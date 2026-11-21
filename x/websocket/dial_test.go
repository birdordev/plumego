package websocket

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// startUnauthDialServer starts an in-process WebSocket server that accepts
// unauthenticated connections and echoes received messages back to the same
// room. It returns a base http URL the test can pass to Dial.
func startUnauthDialServer(t *testing.T) (*httptest.Server, *Hub) {
	t.Helper()
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 32})
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ServeRoomFanoutWS(w, r, ServerConfig{
			Hub:                  hub,
			QueueSize:            8,
			SendTimeout:          200 * time.Millisecond,
			SendBehavior:         SendBlock,
			AllowAllOrigins:      true,
			AllowUnauthenticated: true,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		hub.Stop()
	})
	return srv, hub
}

func TestDialRoundtrip(t *testing.T) {
	srv, _ := startUnauthDialServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := Dial(ctx, srv.URL+"/ws?room=alpha", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(OpcodeText, []byte("hello-from-client")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	op, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != OpcodeText {
		t.Fatalf("op = %d, want OpcodeText (%d)", op, OpcodeText)
	}
	if string(data) != "hello-from-client" {
		t.Fatalf("payload = %q, want hello-from-client", string(data))
	}
}

func TestDialBinaryRoundtrip(t *testing.T) {
	srv, _ := startUnauthDialServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := Dial(ctx, srv.URL+"/ws?room=binary", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	payload := []byte{0x01, 0x02, 0xFF, 0x00, 0xAB}
	if err := conn.WriteMessage(OpcodeBinary, payload); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	op, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != OpcodeBinary {
		t.Fatalf("op = %d, want OpcodeBinary", op)
	}
	if string(data) != string(payload) {
		t.Fatalf("payload mismatch: got %v, want %v", data, payload)
	}
}

func TestDialPropagatesHTTPHeader(t *testing.T) {
	srv, _ := startUnauthDialServer(t)

	got := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("X-Test-Header")
		// Reject so we exercise the error path; we only want to verify headers
		// propagate through Dial's request.
		http.Error(w, "probe", http.StatusBadRequest)
	})
	probe := httptest.NewServer(mux)
	defer probe.Close()
	_ = srv

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	headers := http.Header{}
	headers.Set("X-Test-Header", "propagated")
	_, _, err := Dial(ctx, probe.URL+"/probe", &DialOptions{HTTPHeader: headers})
	if err == nil {
		t.Fatalf("expected handshake to fail against /probe (status 400)")
	}
	select {
	case h := <-got:
		if h != "propagated" {
			t.Fatalf("X-Test-Header = %q, want propagated", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("probe handler was not invoked")
	}
}

func TestDialRejectsBadScheme(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err := Dial(ctx, "ftp://example.com/ws", nil)
	if err == nil {
		t.Fatal("expected error for ftp scheme")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("error %q does not mention scheme", err.Error())
	}
}

func TestDialRejectsNon101Status(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no upgrade", http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, _, err := Dial(ctx, srv.URL+"/ws", nil)
	if err == nil {
		t.Fatal("expected handshake error against non-101 server")
	}
	if !strings.Contains(err.Error(), "101") {
		t.Fatalf("error %q does not mention status code", err.Error())
	}
}

func TestDialRejectsInvalidAccept(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Respond with the right status but a wrong Sec-WebSocket-Accept.
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: bogusvalue\r\n\r\n")
		_ = buf.Flush()
		_ = conn.Close()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, _, err := Dial(ctx, srv.URL+"/ws", nil)
	if err == nil {
		t.Fatal("expected error against bad Sec-WebSocket-Accept")
	}
	if !strings.Contains(err.Error(), "Accept") {
		t.Fatalf("error %q does not mention Sec-WebSocket-Accept", err.Error())
	}
}

func TestDialContextDeadline(t *testing.T) {
	// Listener that accepts and never responds; client should time out via ctx.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Just hold the connection open; do not respond.
			go func(c net.Conn) {
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err = Dial(ctx, "ws://"+ln.Addr().String()+"/ws", nil)
	if err == nil {
		t.Fatal("expected error from context deadline")
	}
	// Either net err or context.DeadlineExceeded; both are acceptable.
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context") {
		t.Logf("note: error from blocked listener was %v", err)
	}
}

// rwcStub implements io.ReadWriteCloser over an in-memory pipe for tests; the
// stdlib's io.NopCloser only adapts a Reader, so we need a 3-method stub.
type rwcStub struct{ closed bool }

func (s *rwcStub) Read(p []byte) (int, error)  { return 0, io.EOF }
func (s *rwcStub) Write(p []byte) (int, error) { return len(p), nil }
func (s *rwcStub) Close() error                { s.closed = true; return nil }

func TestRWCConnDeadlineNoOps(t *testing.T) {
	// Defensive check: deadline methods on rwcConn must never propagate errors,
	// because Conn.writeFrame relies on them being callable.
	c := newRWCConn(&rwcStub{})
	if err := c.SetWriteDeadline(time.Now()); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if err := c.SetReadDeadline(time.Now()); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := c.SetDeadline(time.Now()); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	if c.LocalAddr().Network() != "websocket" {
		t.Fatalf("LocalAddr.Network = %q", c.LocalAddr().Network())
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, errClosedDialConn) {
		t.Fatalf("Write after Close = %v, want errClosedDialConn", err)
	}
}

func TestDialClientTimeoutDuringHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, _, err := Dial(ctx, server.URL, &DialOptions{HTTPClient: &http.Client{Timeout: 30 * time.Millisecond}})
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("handshake err = %v, parent err = %v", err, ctx.Err())
	}
}

func TestDialClientTimeoutEndsAtUpgrade(t *testing.T) {
	server, _ := startUnauthDialServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	timeout := 200 * time.Millisecond
	client, _, err := Dial(ctx, server.URL+"/ws?room=timeout", &DialOptions{HTTPClient: &http.Client{Timeout: timeout}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// The caller can also cancel its dial context after the upgrade.
	cancel()
	// Wait beyond the handshake budget, then exercise both directions.
	time.Sleep(timeout)
	if err := client.WriteMessage(OpcodeText, []byte("after upgrade")); err != nil {
		t.Fatal(err)
	}
	_, payload, err := client.ReadMessage()
	if err != nil || string(payload) != "after upgrade" {
		t.Fatalf("read = %q, %v", payload, err)
	}
}

type partialRWCStub struct {
	rwcStub
	reads int
	err   error
}

func (s *partialRWCStub) Read(p []byte) (int, error) {
	s.reads++
	return copy(p, "data"), s.err
}

func TestRWCConnReadAfterCloseDoesNotDelegate(t *testing.T) {
	sentinel := errors.New("partial read")
	stream := &partialRWCStub{err: sentinel}
	conn := newRWCConn(stream)
	buffer := make([]byte, 8)
	if n, err := conn.Read(buffer); n != 4 || string(buffer[:4]) != "data" || !errors.Is(err, sentinel) {
		t.Errorf("Read = %d, %v, buffer=%q", n, err, buffer)
	}
	if err := conn.Close(); err != nil || !stream.closed {
		t.Fatalf("Close = %v, underlying closed = %t", err, stream.closed)
	}
	if n, err := conn.Read(buffer); n != 0 || !errors.Is(err, errClosedDialConn) || stream.reads != 1 {
		t.Errorf("closed Read = %d, %v, underlying reads=%d", n, err, stream.reads)
	}
}
