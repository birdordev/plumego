package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/websocket"
	"with-websocket/internal/config"
	"with-websocket/internal/message"
)

// makeEchoApp builds an App with echo OnMessage and registers routes.
func makeEchoApp(t *testing.T) *App {
	t.Helper()
	cfg := config.Defaults()
	cfg.WSSecret = strings.Repeat("x", 32)
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	return a
}

// dialEcho dials the /ws endpoint on srv and returns an open connection.
func dialEcho(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return conn
}

// TestEchoTextRoundtrip verifies that a text message is echoed back verbatim.
func TestEchoTextRoundtrip(t *testing.T) {
	srv := httptest.NewServer(makeEchoApp(t).Core)
	defer srv.Close()

	conn := dialEcho(t, srv)
	defer conn.Close()

	const want = "hello echo"
	if err := conn.WriteText(want); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	op, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != websocket.OpcodeText {
		t.Fatalf("opcode = %d, want OpcodeText (%d)", op, websocket.OpcodeText)
	}
	if string(got) != want {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}

// TestEchoBinaryRoundtrip verifies that a binary frame is echoed back verbatim.
func TestEchoBinaryRoundtrip(t *testing.T) {
	srv := httptest.NewServer(makeEchoApp(t).Core)
	defer srv.Close()

	conn := dialEcho(t, srv)
	defer conn.Close()

	want := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if err := conn.WriteBinary(want); err != nil {
		t.Fatalf("WriteBinary: %v", err)
	}

	op, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != websocket.OpcodeBinary {
		t.Fatalf("opcode = %d, want OpcodeBinary (%d)", op, websocket.OpcodeBinary)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("echo = %v, want %v", got, want)
	}
}

// TestEchoMultipleMessages verifies that multiple messages are echoed in order.
func TestEchoMultipleMessages(t *testing.T) {
	srv := httptest.NewServer(makeEchoApp(t).Core)
	defer srv.Close()

	conn := dialEcho(t, srv)
	defer conn.Close()

	messages := []string{"first", "second", "third"}
	for _, msg := range messages {
		if err := conn.WriteText(msg); err != nil {
			t.Fatalf("WriteText(%q): %v", msg, err)
		}
		_, got, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if string(got) != msg {
			t.Fatalf("echo = %q, want %q", got, msg)
		}
	}
}

// TestEchoHubTracksConnection verifies the hub registers the connection.
func TestEchoHubTracksConnection(t *testing.T) {
	a := makeEchoApp(t)
	srv := httptest.NewServer(a.Core)
	defer srv.Close()

	conn := dialEcho(t, srv)
	defer conn.Close()

	// Complete one round-trip to confirm the connection is fully established
	// before checking hub metrics.
	if err := conn.WriteText("probe"); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	m := a.WS.Hub().Metrics()
	if m.ActiveConnections == 0 {
		t.Fatalf("hub.Metrics().ActiveConnections = 0, want > 0")
	}
}

// TestEchoGracefulClientClose verifies that a client-initiated close does not error.
func TestEchoGracefulClientClose(t *testing.T) {
	srv := httptest.NewServer(makeEchoApp(t).Core)
	defer srv.Close()

	conn := dialEcho(t, srv)

	if err := conn.WriteClose(websocket.CloseNormalClosure, "done"); err != nil {
		t.Fatalf("WriteClose: %v", err)
	}
	if !conn.IsClosed() {
		t.Fatalf("conn.IsClosed() = false after WriteClose")
	}
}

// TestEchoJSONPingRespondsWithPong verifies that {"type":"ping"} elicits {"type":"pong"}.
// This demonstrates the JSON message encode/decode path in echoHandler.
func TestEchoJSONPingRespondsWithPong(t *testing.T) {
	srv := httptest.NewServer(makeEchoApp(t).Core)
	defer srv.Close()

	conn := dialEcho(t, srv)
	defer conn.Close()

	ping, err := message.Encode(message.Event{Type: "ping"})
	if err != nil {
		t.Fatalf("Encode ping: %v", err)
	}
	if err := conn.WriteText(string(ping)); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	op, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != websocket.OpcodeText {
		t.Fatalf("opcode = %d, want OpcodeText (%d)", op, websocket.OpcodeText)
	}

	var ev message.Event
	if err := json.Unmarshal(got, &ev); err != nil {
		t.Fatalf("unmarshal pong: %v (raw = %s)", err, got)
	}
	if ev.Type != "pong" {
		t.Fatalf("pong type = %q, want %q", ev.Type, "pong")
	}
}

// TestEchoNonJSONTextFallsBackToRaw verifies that non-JSON text is echoed verbatim.
// The handler tries JSON decode first; if it fails it reflects raw bytes.
func TestEchoNonJSONTextFallsBackToRaw(t *testing.T) {
	srv := httptest.NewServer(makeEchoApp(t).Core)
	defer srv.Close()

	conn := dialEcho(t, srv)
	defer conn.Close()

	const want = "hello world"
	if err := conn.WriteText(want); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != want {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}

// TestEchoUnknownJSONTypeFallsBackToRaw verifies that a JSON text message whose
// type field is not "ping" is echoed back verbatim rather than silently consumed.
func TestEchoUnknownJSONTypeFallsBackToRaw(t *testing.T) {
	srv := httptest.NewServer(makeEchoApp(t).Core)
	defer srv.Close()

	conn := dialEcho(t, srv)
	defer conn.Close()

	const want = `{"type":"custom","data":{"key":"value"}}`
	if err := conn.WriteText(want); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != want {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}
