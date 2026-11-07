// WebSocket proxy support for the gateway reverse proxy.
//
// handleWebSocketProxy operates at the raw TCP proxy layer — it performs the
// HTTP upgrade handshake and then copies bytes bidirectionally between the
// client and the upstream backend. It does not own application-level WebSocket
// semantics (hub management, routing, broadcasting).
//
// For application-level WebSocket features use x/websocket, which provides
// the hub, route registration, and broadcast primitives.
package gateway

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spcent/plumego/internal/httputil"
)

// handleWebSocketProxy handles WebSocket proxying
// This is a raw TCP proxy that handles the WebSocket upgrade and bidirectional copying
func (p *Proxy) handleWebSocketProxy(w http.ResponseWriter, r *http.Request, backend *Backend) error {
	// Build backend WebSocket URL
	backendURL := &url.URL{
		Scheme: getWebSocketScheme(backend.ParsedURL.Scheme),
		Host:   backend.ParsedURL.Host,
		Path:   r.URL.Path,
	}

	// Apply path rewriting if configured
	if p.config.PathRewrite != nil {
		backendURL.Path = p.config.PathRewrite(backendURL.Path)
	}

	// Dial backend
	backendConn, err := p.dialBackend(backendURL, r)
	if err != nil {
		return err
	}
	defer func() { _ = backendConn.Close() }()

	// Hijack client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return ErrWebSocketUpgrade
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		return err
	}
	defer func() { _ = clientConn.Close() }()

	// Send upgrade response to client
	if err := p.sendUpgradeResponse(clientConn, r); err != nil {
		return err
	}

	// Bidirectional copy
	return p.bidirectionalCopy(clientConn, backendConn)
}

// dialBackend dials the backend WebSocket server
func (p *Proxy) dialBackend(backendURL *url.URL, r *http.Request) (net.Conn, error) {
	// Create dialer with timeout
	dialer := &net.Dialer{
		Timeout:   p.config.Timeout,
		KeepAlive: 30 * time.Second,
	}

	// Determine network address
	host := backendURL.Host
	if !strings.Contains(host, ":") {
		if backendURL.Scheme == "wss" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	// Dial backend
	ctx, cancel := context.WithTimeout(context.Background(), p.config.Timeout)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}

	// Send upgrade request to backend
	if err := p.sendBackendUpgrade(conn, backendURL, r); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// Read upgrade response from backend
	if err := p.readBackendUpgradeResponse(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return conn, nil
}

// sendBackendUpgrade sends the WebSocket upgrade request to the backend
func (p *Proxy) sendBackendUpgrade(conn net.Conn, backendURL *url.URL, r *http.Request) error {
	// Build upgrade request
	var sb strings.Builder
	sb.WriteString("GET ")
	sb.WriteString(backendURL.Path)
	if backendURL.RawQuery != "" {
		sb.WriteString("?")
		sb.WriteString(backendURL.RawQuery)
	}
	sb.WriteString(" HTTP/1.1\r\n")
	sb.WriteString("Host: ")
	sb.WriteString(backendURL.Host)
	sb.WriteString("\r\n")

	// Copy relevant headers from client request
	headers := []string{
		"Upgrade",
		"Connection",
		"Sec-WebSocket-Key",
		"Sec-WebSocket-Version",
		"Sec-WebSocket-Protocol",
		"Sec-WebSocket-Extensions",
	}

	for _, header := range headers {
		if value := r.Header.Get(header); value != "" {
			sb.WriteString(header)
			sb.WriteString(": ")
			sb.WriteString(value)
			sb.WriteString("\r\n")
		}
	}

	// Apply request modifications if configured
	if p.config.AddForwardedHeaders {
		// Add X-Forwarded headers
		clientIP := httputil.ClientIP(r)
		sb.WriteString("X-Forwarded-For: ")
		sb.WriteString(clientIP)
		sb.WriteString("\r\n")
		sb.WriteString("X-Real-IP: ")
		sb.WriteString(clientIP)
		sb.WriteString("\r\n")
	}

	sb.WriteString("\r\n")

	// Send request
	_, err := conn.Write([]byte(sb.String()))
	return err
}

// readBackendUpgradeResponse reads and validates the upgrade response from backend
func (p *Proxy) readBackendUpgradeResponse(conn net.Conn) error {
	// Set read deadline
	_ = conn.SetReadDeadline(time.Now().Add(p.config.Timeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	// Read response status line and headers
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}

	response := string(buf[:n])

	// Check for successful upgrade (101 Switching Protocols)
	if !strings.HasPrefix(response, "HTTP/1.1 101") {
		return ErrWebSocketUpgrade
	}

	return nil
}

// sendUpgradeResponse sends the WebSocket upgrade response to the client
func (p *Proxy) sendUpgradeResponse(conn net.Conn, r *http.Request) error {
	// Build upgrade response
	var sb strings.Builder
	sb.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	sb.WriteString("Upgrade: websocket\r\n")
	sb.WriteString("Connection: Upgrade\r\n")

	// Copy Sec-WebSocket-Accept from backend response
	// For simplicity, we'll generate it here
	if key := r.Header.Get("Sec-WebSocket-Key"); key != "" {
		accept := computeAcceptKey(key)
		sb.WriteString("Sec-WebSocket-Accept: ")
		sb.WriteString(accept)
		sb.WriteString("\r\n")
	}

	// Copy Sec-WebSocket-Protocol if present
	if protocol := r.Header.Get("Sec-WebSocket-Protocol"); protocol != "" {
		sb.WriteString("Sec-WebSocket-Protocol: ")
		sb.WriteString(protocol)
		sb.WriteString("\r\n")
	}

	sb.WriteString("\r\n")

	// Send response
	_, err := conn.Write([]byte(sb.String()))
	return err
}

// bidirectionalCopy copies data bidirectionally between client and backend
func (p *Proxy) bidirectionalCopy(client, backend net.Conn) error {
	errChan := make(chan error, 2)

	// Copy client -> backend
	go func() {
		_, err := io.Copy(backend, client)
		errChan <- err
	}()

	// Copy backend -> client
	go func() {
		_, err := io.Copy(client, backend)
		errChan <- err
	}()

	// Wait for one direction to complete
	err := <-errChan

	// Close both connections to terminate the other goroutine
	_ = client.Close()
	_ = backend.Close()

	return err
}

// getWebSocketScheme converts HTTP scheme to WebSocket scheme
func getWebSocketScheme(httpScheme string) string {
	if httpScheme == "https" {
		return "wss"
	}
	return "ws"
}

// computeAcceptKey computes the Sec-WebSocket-Accept value
// This implements the WebSocket protocol handshake computation
func computeAcceptKey(key string) string {
	// WebSocket protocol magic string
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte(magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
