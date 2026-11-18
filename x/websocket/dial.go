package websocket

// The client handshake implementation in this file is adapted from
// github.com/coder/websocket (ISC licensed, Copyright (c) 2025 Coder).
// The port covers the RFC 6455 Section 4 client opening handshake without the
// permessage-deflate extension, ping/pong callbacks, or subprotocol fallback
// recovery present in the upstream library. See dial_test.go for the
// behaviors that are validated.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// DialOptions configures the WebSocket client handshake performed by Dial.
//
// HTTPClient is used to send the upgrade request; its Transport must return a
// writable response body (net/http does this for HTTP/1.1 since Go 1.12, which
// is the only mode WebSocket can be tunneled through). HTTPHeader is cloned
// onto the request before the Connection/Upgrade/Sec-WebSocket-* headers are
// applied; callers may use it to send Authorization, Origin, or cookies.
//
// Subprotocols, when non-empty, is sent as a comma-separated
// Sec-WebSocket-Protocol request header; the server's selected subprotocol is
// returned in *http.Response and validated against this list.
type DialOptions struct {
	HTTPClient   *http.Client
	HTTPHeader   http.Header
	Host         string
	Subprotocols []string
}

// QueueSize and SendTimeout for client connections returned by Dial. Client
// callers in this package primarily use ReadMessage / WriteMessage directly,
// so the values are deliberately small to avoid retaining unused buffers.
const (
	defaultClientQueueSize   = 16
	defaultClientSendTimeout = 5 * time.Second
)

// Dial performs an RFC 6455 client handshake against the given URL.
//
// The URL scheme must be ws, wss, http, or https; ws/wss are rewritten to
// http/https before the upgrade request is sent. The returned Conn is wired
// for client-side framing (outgoing frames are masked, incoming server frames
// must be unmasked). The returned *http.Response is the handshake response
// with its Body field replaced by a non-nil placeholder; callers do not need
// to close it.
//
// On success the connection's reader and writer goroutines are started by the
// underlying Conn constructor; close it via Conn.Close or Conn.WriteClose.
// On handshake failure both Conn and the response body are released and a
// non-nil error is returned (Conn is nil; the response may still be inspected
// for status/headers).
func Dial(ctx context.Context, urlStr string, opts *DialOptions) (*Conn, *http.Response, error) {
	resolved, timeout := resolveDialOptions(opts)
	requestCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	stopParent := context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	defer stopParent()
	if err := ctx.Err(); err != nil {
		cancel(context.Cause(ctx))
	}
	connected := false
	defer func() {
		if !connected {
			cancel(nil)
		}
	}()

	secKey, err := generateSecWebSocketKey()
	if err != nil {
		return nil, nil, fmt.Errorf("websocket: generate Sec-WebSocket-Key: %w", err)
	}

	// Cancel the request only if the timer wins. A successful upgrade keeps
	// the request context alive until the connection is closed.
	var timer *time.Timer
	if timeout > 0 {
		timer = time.AfterFunc(timeout, func() { cancel(context.DeadlineExceeded) })
	}
	resp, err := sendHandshakeRequest(requestCtx, urlStr, resolved, secKey)
	if timer != nil && !timer.Stop() {
		<-requestCtx.Done()
	}
	if !stopParent() {
		<-requestCtx.Done()
	}
	if cause := context.Cause(requestCtx); cause != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, nil, fmt.Errorf("websocket: handshake request: %w", cause)
	}
	if err != nil {
		return nil, nil, err
	}

	if err := verifyHandshakeResponse(resp, secKey, resolved.Subprotocols); err != nil {
		_ = resp.Body.Close()
		return nil, resp, err
	}

	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		_ = resp.Body.Close()
		return nil, resp, fmt.Errorf("websocket: response body is not io.ReadWriteCloser: %T", resp.Body)
	}

	conn := newRWCConn(rwc)
	conn.onClose = func() { cancel(nil) }
	c := newConnFromHijack(
		conn,
		bufio.NewReaderSize(rwc, defaultBufSize),
		bufio.NewWriterSize(rwc, defaultBufSize),
		defaultClientQueueSize,
		defaultClientSendTimeout,
		SendBlock,
		true,
	)
	// Replace the body so the standard library does not log a warning about a
	// non-closed response body. The placeholder is empty and a no-op on Close.
	resp.Body = io.NopCloser(strings.NewReader(""))
	connected = true
	return c, resp, nil
}

// resolveDialOptions clones the HTTP client for an upgrade request.
func resolveDialOptions(in *DialOptions) (*DialOptions, time.Duration) {
	out := DialOptions{}
	if in != nil {
		out = *in
	}
	if out.HTTPClient == nil {
		out.HTTPClient = http.DefaultClient
	}
	timeout := out.HTTPClient.Timeout
	// Insert a CheckRedirect that rewrites ws/wss in the redirected URL so the
	// stdlib http client can follow Location headers without rejecting them.
	clone := *out.HTTPClient
	clone.Timeout = 0
	prevCheck := out.HTTPClient.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		switch req.URL.Scheme {
		case "ws":
			req.URL.Scheme = "http"
		case "wss":
			req.URL.Scheme = "https"
		}
		if prevCheck != nil {
			return prevCheck(req, via)
		}
		return nil
	}
	out.HTTPClient = &clone

	if out.HTTPHeader == nil {
		out.HTTPHeader = http.Header{}
	}
	return &out, timeout
}

func sendHandshakeRequest(ctx context.Context, urlStr string, opts *DialOptions, secKey string) (*http.Response, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("websocket: parse url: %w", err)
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return nil, fmt.Errorf("websocket: unexpected url scheme %q", u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("websocket: build request: %w", err)
	}
	if opts.Host != "" {
		req.Host = opts.Host
	}
	req.Header = opts.HTTPHeader.Clone()
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", secKey)
	if len(opts.Subprotocols) > 0 {
		req.Header.Set("Sec-WebSocket-Protocol", strings.Join(opts.Subprotocols, ", "))
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("websocket: handshake request: %w", err)
	}
	return resp, nil
}

func verifyHandshakeResponse(resp *http.Response, secKey string, subprotocols []string) error {
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("websocket: expected status %d, got %d", http.StatusSwitchingProtocols, resp.StatusCode)
	}
	if !headerContains(resp.Header, "Connection", "Upgrade") {
		return fmt.Errorf("websocket: missing or invalid Connection header: %q", resp.Header.Get("Connection"))
	}
	if !headerContains(resp.Header, "Upgrade", "websocket") {
		return fmt.Errorf("websocket: missing or invalid Upgrade header: %q", resp.Header.Get("Upgrade"))
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != computeAcceptKey(secKey) {
		return fmt.Errorf("websocket: invalid Sec-WebSocket-Accept %q", got)
	}
	if proto := resp.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		ok := false
		for _, want := range subprotocols {
			if strings.EqualFold(want, proto) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("websocket: unexpected Sec-WebSocket-Protocol %q", proto)
		}
	}
	return nil
}

func generateSecWebSocketKey() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// errClosedDialConn is returned by rwcConn methods after Close has been called.
var errClosedDialConn = errors.New("websocket: dial connection closed")

// rwcConn adapts the http.Response.Body returned by a successful WebSocket
// upgrade (an io.ReadWriteCloser) to the net.Conn interface that the existing
// Conn type uses for Close and SetWriteDeadline.
//
// The HTTP transport does not expose a hijacked socket directly, so deadline
// methods are no-ops here; per-call timeouts must be handled at the
// context/HTTPClient layer.
type rwcConn struct {
	rwc     io.ReadWriteCloser
	closed  atomic.Bool
	onClose func()
}

func newRWCConn(rwc io.ReadWriteCloser) *rwcConn {
	return &rwcConn{rwc: rwc}
}

func (c *rwcConn) Read(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, errClosedDialConn
	}
	return c.rwc.Read(p)
}

func (c *rwcConn) Write(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, errClosedDialConn
	}
	return c.rwc.Write(p)
}

func (c *rwcConn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.onClose != nil {
		c.onClose()
	}
	return c.rwc.Close()
}

func (c *rwcConn) LocalAddr() net.Addr  { return wsAddr{} }
func (c *rwcConn) RemoteAddr() net.Addr { return wsAddr{} }

// SetDeadline / SetReadDeadline / SetWriteDeadline are no-ops because the
// underlying transport is the HTTP client's body stream which does not honor
// per-call deadlines. Callers should use a request-scoped context.
func (c *rwcConn) SetDeadline(time.Time) error      { return nil }
func (c *rwcConn) SetReadDeadline(time.Time) error  { return nil }
func (c *rwcConn) SetWriteDeadline(time.Time) error { return nil }

// wsAddr is a placeholder net.Addr implementation for client connections;
// LocalAddr / RemoteAddr are not meaningful when the underlying transport is
// an HTTP response body rather than a hijacked TCP socket.
type wsAddr struct{}

func (wsAddr) Network() string { return "websocket" }
func (wsAddr) String() string  { return "websocket" }
