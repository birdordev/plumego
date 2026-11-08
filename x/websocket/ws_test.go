package websocket

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/router"
)

// ---------------- Tests & Benchmark ----------------
// To run unit tests and benchmark, run: go test -v -bench . -run ^$
// There is a simple benchmark that spawns many simulated clients and broadcasts messages.

func TestJWTAndRoomAuth(t *testing.T) {
	secret := validSecret()
	auth := mustSimpleRoomAuth(t)
	tokenAuth := mustSimpleHS256TokenAuth(t, secret)
	if err := auth.SetRoomPassword("a", "p"); err != nil {
		t.Fatalf("SetRoomPassword: %v", err)
	}
	if !auth.CheckRoomPassword("a", "p") {
		t.Fatal("password check failed")
	}
	// create a token
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user1","exp":` + fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()) + `}`))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(header + "." + payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	token := header + "." + payload + "." + sig
	if _, err := tokenAuth.VerifyJWT(token); err != nil {
		t.Fatal("verify jwt failed:", err)
	}
}

func startTestServer(t *testing.T) (*http.Server, *Hub, string) {
	workerCount := 4
	jobQueueSize := 1024
	sendQueueSize := 64
	sendTimeout := 50 * time.Millisecond
	sendBehavior := SendBlock
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: workerCount, JobQueueSize: jobQueueSize})
	secret := validSecret()
	auth := mustSimpleRoomAuth(t)
	if err := auth.SetRoomPassword("room1", "pwd1"); err != nil {
		t.Fatalf("SetRoomPassword: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ServeRoomFanoutWS(w, r, ServerConfig{
			Hub:                  hub,
			TokenAuth:            mustSimpleHS256TokenAuth(t, secret),
			RoomAuth:             auth,
			QueueSize:            sendQueueSize,
			SendTimeout:          sendTimeout,
			SendBehavior:         sendBehavior,
			AllowAllOrigins:      true,
			AllowUnauthenticated: false,
		})
	})
	server := &http.Server{Addr: "127.0.0.1:0", Handler: mux}
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(ln) }()
	return server, hub, "http://" + ln.Addr().String()
}

func startCustomHandlerServer(t *testing.T, handled chan Message) (*http.Server, *Hub, string) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 4, JobQueueSize: 1024})
	auth := mustSimpleRoomAuth(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ServeWSWithConfig(w, r, ServerConfig{
			Hub:                  hub,
			TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
			RoomAuth:             auth,
			QueueSize:            64,
			SendTimeout:          50 * time.Millisecond,
			SendBehavior:         SendBlock,
			AllowAllOrigins:      true,
			AllowUnauthenticated: false,
			OnMessage: func(_ *Conn, msg Message) error {
				handled <- msg
				return nil
			},
		})
	})
	server := &http.Server{Addr: "127.0.0.1:0", Handler: mux}
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(ln) }()
	return server, hub, "http://" + ln.Addr().String()
}

// minimal WS client helper that performs handshake and basic send/receive frames
type testWSClient struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
}

func newTestWSClient(t *testing.T, url string, room, pwd, token string) *testWSClient {
	// parse host
	// url like http://host:port/ws
	parts := strings.Split(url, "://")
	if len(parts) != 2 {
		t.Fatal("bad url")
	}
	hostPart := strings.TrimPrefix(parts[1], "http://")
	host := hostPart
	conn, err := net.Dial("tcp", host)
	if err != nil {
		t.Fatal(err)
	}
	// build request
	keyBytes := make([]byte, 16)
	_, err = rand.Read(keyBytes)
	if err != nil {
		t.Fatal(err)
	}

	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := "/ws"
	if room != "" {
		path += "?room=" + room
	}
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n", path, host, key)
	if token != "" {
		req += "Authorization: Bearer " + token + "\r\n"
	}
	if pwd != "" {
		req += roomPasswordHeader + ": " + pwd + "\r\n"
	}
	req += "\r\n"
	bw := bufio.NewWriter(conn)
	_, _ = bw.WriteString(req)
	_ = bw.Flush()
	br := bufio.NewReader(conn)
	// read response lines
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake failed: %s", status)
	}
	// consume headers until blank line
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	return &testWSClient{conn: conn, br: br, bw: bufio.NewWriter(conn)}
}

func (c *testWSClient) sendFrame(op byte, fin bool, payload []byte) error {
	var header [14]byte
	hlen := 0
	b0 := byte(0)
	if fin {
		b0 |= finBit
	}
	b0 |= op & 0x0F
	header[0] = b0
	hlen = 1
	n := len(payload)
	switch {
	case n <= 125:
		header[hlen] = byte(n) | 0x80 // masked
		hlen++
	case n <= 0xFFFF:
		header[hlen] = 126 | 0x80
		hlen++
		binary.BigEndian.PutUint16(header[hlen:hlen+2], uint16(n))
		hlen += 2
	default:
		header[hlen] = 127 | 0x80
		hlen++
		binary.BigEndian.PutUint64(header[hlen:hlen+8], uint64(n))
		hlen += 8
	}
	// mask key
	maskKey := make([]byte, 4)
	rand.Read(maskKey)
	if _, err := c.bw.Write(header[:hlen]); err != nil {
		return err
	}
	if _, err := c.bw.Write(maskKey); err != nil {
		return err
	}
	// masked payload
	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ maskKey[i%4]
	}
	if _, err := c.bw.Write(masked); err != nil {
		return err
	}
	return c.bw.Flush()
}

func (c *testWSClient) sendText(s string) error {
	return c.sendFrame(OpcodeText, true, []byte(s))
}

func (c *testWSClient) readFrame() (byte, bool, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(c.br, h[:]); err != nil {
		return 0, false, nil, err
	}
	fin := h[0]&finBit != 0
	op := h[0] & 0x0F
	mask := h[1]&0x80 != 0
	len7 := int64(h[1] & 0x7F)
	var payloadLen int64
	switch len7 {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, false, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, false, nil, err
		}
		payloadLen = int64(binary.BigEndian.Uint64(ext[:]))
	default:
		payloadLen = len7
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
	}
	if mask {
		for i := int64(0); i < payloadLen; i++ {
			payload[i] ^= maskKey[i%4]
		}
	}
	return op, fin, payload, nil
}

func closeFrameCode(payload []byte) uint16 {
	if len(payload) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(payload[:2])
}

func TestSimpleEchoAndRoom(t *testing.T) {
	server, hub, base := startTestServer(t)
	defer server.Close()
	defer hub.Stop()

	secret := validSecret()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u","exp":` + fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()) + `}`))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(header + "." + payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	token := header + "." + payload + "." + sig

	cli := newTestWSClient(t, base, "room1", "pwd1", token)
	defer cli.conn.Close()
	// send a message
	if err := cli.sendText("hello"); err != nil {
		t.Fatal(err)
	}
	// read broadcast (server will broadcast back)
	op, _, payloadb, err := cli.readFrame()
	if err != nil {
		t.Fatal(err)
	}
	if op != OpcodeText {
		t.Fatalf("expected text op, got %d", op)
	}
	if string(payloadb) != "hello" {
		t.Fatalf("expected hello, got %s", string(payloadb))
	}
}

func TestServeWSWithConfigDelegatesMessages(t *testing.T) {
	handled := make(chan Message, 1)
	server, hub, base := startCustomHandlerServer(t, handled)
	defer server.Close()
	defer hub.Stop()

	cli := newTestWSClient(t, base, "custom", "", testJWTToken(t, validSecret()))
	defer cli.conn.Close()

	if err := cli.sendText("custom-payload"); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-handled:
		if msg.Room != "custom" {
			t.Fatalf("Room = %q, want custom", msg.Room)
		}
		if msg.Op != OpcodeText {
			t.Fatalf("Op = %d, want %d", msg.Op, OpcodeText)
		}
		if string(msg.Data) != "custom-payload" {
			t.Fatalf("Data = %q, want custom-payload", string(msg.Data))
		}
	case <-time.After(time.Second):
		t.Fatal("message handler was not called")
	}
}

func TestServerRegisterRoutesUsesConfiguredMessageHandler(t *testing.T) {
	handled := make(chan Message, 1)
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.AllowAllOrigins = true
	cfg.OnMessage = func(_ *Conn, msg Message) error {
		handled <- msg
		return nil
	}
	server, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer server.Shutdown(context.Background())

	r := router.NewRouter()
	if err := server.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	httpServer := &http.Server{Addr: "127.0.0.1:0", Handler: r}
	ln, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = httpServer.Serve(ln) }()
	defer httpServer.Close()

	cli := newTestWSClient(t, "http://"+ln.Addr().String(), "custom", "", testJWTToken(t, validSecret()))
	defer cli.conn.Close()

	if err := cli.sendText("custom-payload"); err != nil {
		t.Fatalf("send text: %v", err)
	}

	select {
	case msg := <-handled:
		if msg.Room != "custom" || msg.Op != OpcodeText || string(msg.Data) != "custom-payload" {
			t.Fatalf("unexpected handled message: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("configured server message handler was not called")
	}
}

func TestServeWSWithConfigHandlerCloseError(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ServeWSWithConfig(w, r, ServerConfig{
			Hub:                  hub,
			TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
			QueueSize:            8,
			SendTimeout:          50 * time.Millisecond,
			SendBehavior:         SendBlock,
			AllowAllOrigins:      true,
			AllowUnauthenticated: false,
			OnMessage: func(*Conn, Message) error {
				return NewCloseError(ClosePolicyViolation, "blocked", errors.New("blocked"))
			},
		})
	})
	server := &http.Server{Addr: "127.0.0.1:0", Handler: mux}
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(ln) }()
	defer server.Close()

	cli := newTestWSClient(t, "http://"+ln.Addr().String(), "custom", "", testJWTToken(t, validSecret()))
	defer cli.conn.Close()

	if err := cli.sendText("blocked"); err != nil {
		t.Fatalf("send text: %v", err)
	}
	op, _, payload, err := cli.readFrame()
	if err != nil {
		t.Fatalf("read close frame: %v", err)
	}
	if op != opcodeClose {
		t.Fatalf("expected close frame, got opcode %d", op)
	}
	if got := closeFrameCode(payload); got != ClosePolicyViolation {
		t.Fatalf("close code = %d, want %d", got, ClosePolicyViolation)
	}
	if got := string(payload[2:]); got != "blocked" {
		t.Fatalf("close reason = %q, want blocked", got)
	}
}

func TestServeWSWithConfigHandlerDefaultCloseError(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ServeWSWithConfig(w, r, ServerConfig{
			Hub:                  hub,
			TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
			QueueSize:            8,
			SendTimeout:          50 * time.Millisecond,
			SendBehavior:         SendBlock,
			AllowAllOrigins:      true,
			AllowUnauthenticated: false,
			OnMessage: func(*Conn, Message) error {
				return errors.New("handler failed")
			},
		})
	})
	server := &http.Server{Addr: "127.0.0.1:0", Handler: mux}
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(ln) }()
	defer server.Close()

	cli := newTestWSClient(t, "http://"+ln.Addr().String(), "custom", "", testJWTToken(t, validSecret()))
	defer cli.conn.Close()

	if err := cli.sendText("fail"); err != nil {
		t.Fatalf("send text: %v", err)
	}
	op, _, payload, err := cli.readFrame()
	if err != nil {
		t.Fatalf("read close frame: %v", err)
	}
	if op != opcodeClose {
		t.Fatalf("expected close frame, got opcode %d", op)
	}
	if got := closeFrameCode(payload); got != CloseServerError {
		t.Fatalf("close code = %d, want %d", got, CloseServerError)
	}
}

func TestServerClosesInvalidTextPayload(t *testing.T) {
	server, hub, base := startTestServer(t)
	defer server.Close()
	defer hub.Stop()

	secret := validSecret()
	token := testJWTToken(t, secret)
	cli := newTestWSClient(t, base, "room1", "pwd1", token)
	defer cli.conn.Close()

	if err := cli.sendFrame(OpcodeText, true, []byte{0xff}); err != nil {
		t.Fatalf("send invalid text: %v", err)
	}

	op, _, payload, err := cli.readFrame()
	if err != nil {
		t.Fatalf("read close frame: %v", err)
	}
	if op != opcodeClose {
		t.Fatalf("expected close frame, got opcode %d", op)
	}
	if got := closeFrameCode(payload); got != CloseInvalidPayload {
		t.Fatalf("expected close code %d, got %d", CloseInvalidPayload, got)
	}
}
