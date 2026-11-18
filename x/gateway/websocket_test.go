package gateway

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestComputeAcceptKey(t *testing.T) {
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="

	if got := computeAcceptKey(key); got != want {
		t.Fatalf("computeAcceptKey mismatch: got %q want %q", got, want)
	}
}

func TestWebSocketProxyPreservesNegotiationAndBufferedFrames(t *testing.T) {
	const key = "dGhlIHNhbXBsZSBub25jZQ=="
	serverFrame := []byte{0x81, 0x02, 'o', 'k'}
	clientFrame := []byte{0x81, 0x81, 1, 2, 3, 4, 'x' ^ 1}
	backendErr := make(chan error, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendErr <- func() error {
			conn, rw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return err
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				return err
			}
			if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n"); err != nil {
				return err
			}
			if err := rw.Flush(); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(rw, "Connection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: chat\r\n\r\n", computeAcceptKey(key)); err != nil {
				return err
			}
			if _, err := rw.Write(serverFrame); err != nil {
				return err
			}
			if err := rw.Flush(); err != nil {
				return err
			}
			received := make([]byte, len(clientFrame))
			if _, err := io.ReadFull(rw, received); err != nil {
				return err
			}
			if !bytes.Equal(received, clientFrame) {
				return fmt.Errorf("client frame = %x, want %x", received, clientFrame)
			}
			return nil
		}()
	}))
	defer backend.Close()
	proxy, err := New(Config{Targets: []string{backend.URL}, WebSocketEnabled: true, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	frontend := httptest.NewServer(proxy)
	defer frontend.Close()
	client, err := net.DialTimeout("tcp", frontend.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf("GET /chat HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Protocol: other, chat\r\n\r\n", frontend.Listener.Addr(), key)
	if _, err := client.Write(append([]byte(request), clientFrame...)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols || response.Header.Get("Sec-WebSocket-Protocol") != "chat" {
		t.Fatalf("upgrade response = %d, protocol = %q", response.StatusCode, response.Header.Get("Sec-WebSocket-Protocol"))
	}
	received := make([]byte, len(serverFrame))
	if _, err := io.ReadFull(reader, received); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, serverFrame) {
		t.Fatalf("backend frame = %x, want %x", received, serverFrame)
	}
	if err := <-backendErr; err != nil {
		t.Fatal(err)
	}
}
