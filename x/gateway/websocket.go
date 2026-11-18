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
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/birdordev/plumego/internal/httputil"
)

// handleWebSocketProxy handles WebSocket proxying
// This is a raw TCP proxy that handles the WebSocket upgrade and bidirectional copying
func (p *Proxy) handleWebSocketProxy(w http.ResponseWriter, r *http.Request, backend *Backend) (hijacked bool, err error) {
	// Build backend WebSocket URL
	backendURL := &url.URL{
		Scheme:   getWebSocketScheme(backend.ParsedURL.Scheme),
		Host:     backend.ParsedURL.Host,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	// Apply path rewriting if configured
	if p.config.PathRewrite != nil {
		backendURL.Path = p.config.PathRewrite(backendURL.Path)
	}

	backendConn, backendReader, response, err := p.dialBackend(backendURL, r)
	if err != nil {
		return false, err
	}
	defer func() { _ = backendConn.Close() }()

	clientConn, clientBuffer, err := httputil.HijackIfSupported(w)
	if err != nil {
		return false, err
	}
	defer func() { _ = clientConn.Close() }()

	if err := sendUpgradeResponse(clientBuffer.Writer, response); err != nil {
		return true, err
	}
	return true, p.bidirectionalCopy(clientConn, backendConn, clientBuffer.Reader, backendReader)
}

// dialBackend opens the upstream connection and reads the upgrade response.
func (p *Proxy) dialBackend(backendURL *url.URL, r *http.Request) (net.Conn, io.Reader, *http.Response, error) {
	dialer := &net.Dialer{Timeout: p.config.Timeout, KeepAlive: 30 * time.Second}

	host := backendURL.Host
	if backendURL.Port() == "" {
		port := "80"
		if backendURL.Scheme == "wss" {
			port = "443"
		}
		host = net.JoinHostPort(backendURL.Hostname(), port)
	}

	ctx, cancel := context.WithTimeout(r.Context(), p.config.Timeout)
	defer cancel()
	dial := dialer.DialContext
	if backendURL.Scheme == "wss" {
		tlsConfig := &tls.Config{}
		if p.config.Transport != nil && p.config.Transport.TLSClientConfig != nil {
			tlsConfig = p.config.Transport.TLSClientConfig.Clone()
		}
		tlsConfig.NextProtos = []string{"http/1.1"}
		dial = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext
	}
	conn, err := dial(ctx, "tcp", host)
	if err != nil {
		return nil, nil, nil, err
	}

	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, nil, nil, err
	}
	if err := p.sendBackendUpgrade(conn, backendURL, r); err != nil {
		_ = conn.Close()
		return nil, nil, nil, err
	}

	// Keep one reader across the upgrade so any buffered frame bytes survive.
	const maxUpgradeResponseBytes = 64 << 10
	bounded := &io.LimitedReader{R: conn, N: maxUpgradeResponseBytes}
	reader := bufio.NewReader(bounded)
	response, err := http.ReadResponse(reader, r)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols ||
		!strings.EqualFold(response.Header.Get("Upgrade"), "websocket") ||
		response.Header.Get("Sec-WebSocket-Accept") != computeAcceptKey(r.Header.Get("Sec-WebSocket-Key")) {
		_ = conn.Close()
		return nil, nil, nil, ErrWebSocketUpgrade
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, nil, nil, err
	}
	return conn, io.MultiReader(io.LimitReader(reader, int64(reader.Buffered())), conn), response, nil
}

// sendBackendUpgrade sends the WebSocket upgrade request to the backend
func (p *Proxy) sendBackendUpgrade(conn net.Conn, backendURL *url.URL, r *http.Request) error {
	// Build upgrade request
	var sb strings.Builder
	sb.WriteString("GET ")
	sb.WriteString(backendURL.RequestURI())
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

// sendUpgradeResponse forwards the negotiated WebSocket response headers.
func sendUpgradeResponse(w *bufio.Writer, upstream *http.Response) error {
	response := &http.Response{
		StatusCode: http.StatusSwitchingProtocols,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}},
	}
	for _, name := range []string{"Sec-WebSocket-Accept", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions"} {
		for _, value := range upstream.Header.Values(name) {
			response.Header.Add(name, value)
		}
	}
	if err := response.Write(w); err != nil {
		return err
	}
	return w.Flush()
}

// bidirectionalCopy uses the readers left by both handshakes.
func (p *Proxy) bidirectionalCopy(client, backend net.Conn, clientReader, backendReader io.Reader) error {
	errChan := make(chan error, 2)

	// Copy client -> backend
	go func() {
		_, err := io.Copy(backend, clientReader)
		errChan <- err
	}()

	// Copy backend -> client
	go func() {
		_, err := io.Copy(client, backendReader)
		errChan <- err
	}()

	// Wait for one direction to complete
	err := <-errChan

	// Close both connections to terminate the other goroutine
	_ = client.Close()
	_ = backend.Close()
	<-errChan

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
