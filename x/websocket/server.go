package websocket

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/birdordev/plumego/contract"
)

const (
	roomPasswordHeader       = "X-WebSocket-Room-Password"
	defaultMaxRoomNameLength = 128
	MaxRoomNameLength        = defaultMaxRoomNameLength
)

// RoomNameValidator validates application room identifiers before hub registration.
type RoomNameValidator func(string) error

func validateRoomName(room string, validator RoomNameValidator) error {
	if validator == nil {
		validator = defaultRoomNameValidator
	}
	return validator(room)
}

// ValidateRoomName validates a room name with the default room-name policy.
func ValidateRoomName(room string) error {
	return defaultRoomNameValidator(room)
}

func defaultRoomNameValidator(room string) error {
	if room == "" || len(room) > defaultMaxRoomNameLength {
		return ErrInvalidRoomName
	}
	for i := 0; i < len(room); i++ {
		c := room[i]
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '.', '_', ':', '-':
			continue
		default:
			return ErrInvalidRoomName
		}
	}
	return nil
}

// computeAcceptKey computes the WebSocket accept key
func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + guid))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// headerContains reports whether the comma-separated header value contains val
// (case-insensitive). Iterates without allocating a slice.
func headerContains(h http.Header, key, val string) bool {
	v := h.Get(key)
	for v != "" {
		var token string
		if i := strings.IndexByte(v, ','); i >= 0 {
			token, v = strings.TrimSpace(v[:i]), v[i+1:]
		} else {
			token, v = strings.TrimSpace(v), ""
		}
		if strings.EqualFold(token, val) {
			return true
		}
	}
	return false
}

// isOriginAllowed checks if the request origin is explicitly allowed.
func isOriginAllowed(origin string, allowedOrigins []string, allowAll bool) bool {
	if origin == "" {
		return true
	}
	if allowAll {
		return true
	}
	for _, allowed := range allowedOrigins {
		if allowed == origin {
			return true
		}
	}
	return false
}

// ServerConfig configures WebSocket server options.
//
// TokenAuth verifies tokens when AllowUnauthenticated is false or a token is
// supplied. RoomAuth authorizes room access; nil RoomAuth allows rooms without
// passwords.
type ServerConfig struct {
	Hub                  *Hub
	TokenAuth            TokenAuthenticator
	RoomAuth             RoomAuthorizer
	OnMessage            MessageHandler
	QueueSize            int
	SendTimeout          time.Duration
	WriteTimeout         time.Duration
	SendBehavior         SendBehavior
	ReadLimit            int64 // Optional max inbound frame payload size. 0 means use defaults.
	MessageValidation    MessageValidationConfig
	AllowedOrigins       []string // Browser origins allowed to connect. Use AllowAllOrigins for allow-all behavior.
	AllowAllOrigins      bool     // Explicitly disable origin checks for development or trusted non-browser clients.
	AllowUnauthenticated bool     // Explicitly allow connections without JWT; room passwords still apply.
	AllowQueryToken      bool     // Explicitly allow ?token= JWT transport for trusted clients.
	RoomNameValidator    RoomNameValidator
}

// Message describes one validated client message delivered by ServeWSWithConfig.
type Message struct {
	Room string
	Op   byte
	Data []byte
}

// MessageHandler handles validated client messages for ServeWSWithConfig.
type MessageHandler func(*Conn, Message) error

type messageSizeProvider interface {
	MaxMessageSize() int64
}

func maxIntValue() int64 {
	return int64(^uint(0) >> 1)
}

func readLimitToInt(limit int64) int {
	if limit <= 0 {
		return 0
	}
	maxInt := maxIntValue()
	if limit > maxInt {
		return int(maxInt)
	}
	return int(limit)
}

func normalizeServerConfig(cfg ServerConfig) (ServerConfig, error) {
	if cfg.Hub == nil {
		return cfg, ErrNilHub
	}
	if cfg.TokenAuth == nil && !cfg.AllowUnauthenticated {
		return cfg, ErrNilAuthenticator
	}
	if cfg.QueueSize < 0 {
		return cfg, ErrNegativeQueueSize
	}
	if cfg.SendBehavior < SendBlock || cfg.SendBehavior > SendClose {
		return cfg, ErrInvalidSendBehavior
	}
	if cfg.ReadLimit < 0 {
		return cfg, ErrNegativeReadLimit
	}
	if cfg.WriteTimeout < 0 {
		return cfg, ErrInvalidWriteTimeout
	}
	if cfg.RoomNameValidator == nil {
		cfg.RoomNameValidator = defaultRoomNameValidator
	}
	if cfg.ReadLimit == 0 {
		if p, ok := cfg.TokenAuth.(messageSizeProvider); ok {
			if lim := p.MaxMessageSize(); lim > 0 {
				cfg.ReadLimit = lim
			}
		}
	}
	return cfg, nil
}

func resolveValidationConfig(cfg ServerConfig) MessageValidationConfig {
	validationCfg := cfg.MessageValidation
	if validationCfg == (MessageValidationConfig{}) {
		validationCfg = DefaultMessageValidationConfig()
	}
	if cfg.ReadLimit > 0 {
		maxLen := readLimitToInt(cfg.ReadLimit)
		if maxLen > 0 && (validationCfg.MaxLength == 0 || validationCfg.MaxLength > maxLen) {
			validationCfg.MaxLength = maxLen
		}
	}
	return validationCfg
}

// ServeWSWithConfig performs the WebSocket handshake with full configuration
// options including origin validation (CSRF protection).
func ServeWSWithConfig(w http.ResponseWriter, r *http.Request, cfg ServerConfig) {
	normalized, err := normalizeServerConfig(cfg)
	if err != nil {
		writeWebSocketHandshakeError(w, r, http.StatusInternalServerError, codeWebSocketInvalidConfig, "websocket server misconfigured", contract.CategoryServer)
		return
	}
	cfg = normalized

	if !handshakeValidateOrigin(w, r, cfg) {
		return
	}
	if !handshakeValidateMethod(w, r, cfg) {
		return
	}
	key, ok := handshakeValidateKey(w, r, cfg)
	if !ok {
		return
	}
	room, ok := handshakeResolveRoom(w, r, cfg)
	if !ok {
		return
	}
	token, userInfo, tokenClaims, ok := handshakeResolveUser(w, r, cfg)
	if !ok {
		return
	}
	if !handshakeCheckRoomAccess(w, r, cfg, room, userInfo, tokenClaims, token) {
		return
	}
	c, bufWriter, ok := handshakeUpgradeConn(w, r, cfg, userInfo)
	if !ok {
		return
	}
	if !handshakeRegisterAndRespond(c, bufWriter, cfg, room, key) {
		return
	}
	startReadLoopAndCleanup(c, room, cfg)
}

func authorizeRoomAccess(auth RoomAuthorizer, decision RoomAuthorization) bool {
	if auth == nil {
		return true
	}
	if requestAuth, ok := auth.(RoomRequestAuthorizer); ok {
		return requestAuth.AuthorizeRoom(decision)
	}
	return auth.CheckRoomPassword(decision.Room, decision.Password)
}

// ServeRoomFanoutWS serves a room-fanout websocket endpoint.
//
// It uses the same handshake, auth, origin, and validation behavior as
// ServeWSWithConfig, then broadcasts each validated client message to the room
// selected by the request.
func ServeRoomFanoutWS(w http.ResponseWriter, r *http.Request, cfg ServerConfig) {
	if cfg.OnMessage != nil {
		writeWebSocketHandshakeError(w, r, http.StatusInternalServerError, codeWebSocketInvalidConfig, "websocket server misconfigured", contract.CategoryServer)
		return
	}
	cfg.OnMessage = func(_ *Conn, msg Message) error {
		cfg.Hub.BroadcastRoom(msg.Room, msg.Op, msg.Data)
		return nil
	}
	ServeWSWithConfig(w, r, cfg)
}

func writeCloseForReadError(c *Conn, err error) {
	switch {
	case errors.Is(err, ErrPayloadTooLarge):
		_ = c.WriteClose(CloseMessageTooBig, "message too large")
	case errors.Is(err, ErrInvalidUTF8):
		_ = c.WriteClose(CloseInvalidPayload, "invalid payload")
	case errors.Is(err, ErrProtocolError),
		errors.Is(err, ErrUnmaskedFrame),
		errors.Is(err, ErrFragmentedControl),
		errors.Is(err, ErrControlTooLarge):
		_ = c.WriteClose(CloseProtocolError, "protocol error")
	default:
		_ = c.WriteClose(CloseServerError, "read failed")
	}
}

func writeCloseForValidationError(c *Conn, err error) {
	switch {
	case errors.Is(err, ErrMessageTooLong):
		_ = c.WriteClose(CloseMessageTooBig, "message too large")
	case errors.Is(err, ErrInvalidUTF8):
		_ = c.WriteClose(CloseInvalidPayload, "invalid text")
	default:
		_ = c.WriteClose(ClosePolicyViolation, "message rejected")
	}
}

func writeCloseForHandlerError(c *Conn, err error) {
	const defaultReason = "message handler failed"
	var closeErr *CloseError
	if !errors.As(err, &closeErr) || closeErr == nil {
		_ = c.WriteClose(CloseServerError, defaultReason)
		return
	}

	code := closeErr.Code
	if !isValidCloseStatusCode(code) {
		code = CloseServerError
	}
	reason := closeErr.Reason
	if reason == "" || !utf8.ValidString(reason) || len(reason) > int(maxControlPayload)-2 {
		reason = defaultReason
	}
	_ = c.WriteClose(code, reason)
}

func writeWebSocketHandshakeError(w http.ResponseWriter, r *http.Request, status int, code, message string, category contract.ErrorCategory) {
	writeWebSocketHTTPError(w, r, status, code, message, category)
}

func writeWebSocketHTTPError(w http.ResponseWriter, r *http.Request, status int, code, message string, category contract.ErrorCategory) {
	_ = category
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(webSocketErrorType(status)).
		Code(code).
		Message(message).
		Build())
}

func writeHijackedWebSocketHandshakeError(bw *bufio.Writer, status int, code, message string, category contract.ErrorCategory) {
	body, err := json.Marshal(struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
		} `json:"error"`
	}{
		Error: struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
		}{
			Code:     code,
			Message:  message,
			Category: category,
		},
	})
	if err != nil {
		body = []byte(`{"error":{"code":"WEBSOCKET_HANDSHAKE_FAILED","message":"websocket handshake failed"}}`)
	}

	_, _ = bw.WriteString("HTTP/1.1 " + strconv.Itoa(status) + " " + http.StatusText(status) + "\r\n")
	_, _ = bw.WriteString("Content-Type: application/json\r\n")
	_, _ = bw.WriteString("Connection: close\r\n")
	_, _ = bw.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	_, _ = bw.WriteString("\r\n")
	_, _ = bw.Write(body)
	_ = bw.Flush()
}

func webSocketErrorType(status int) contract.ErrorType {
	switch status {
	case http.StatusUnauthorized:
		return contract.TypeUnauthorized
	case http.StatusForbidden:
		return contract.TypeForbidden
	case http.StatusMethodNotAllowed:
		return contract.TypeMethodNotAllowed
	case http.StatusRequestEntityTooLarge:
		return contract.TypePayloadTooLarge
	case http.StatusTooManyRequests:
		return contract.TypeRateLimited
	case http.StatusServiceUnavailable:
		return contract.TypeUnavailable
	default:
		if status >= http.StatusInternalServerError {
			return contract.TypeInternal
		}
		return contract.TypeValidation
	}
}

func websocketJoinDeniedStatus(err error) int {
	if errors.Is(err, ErrRoomFull) {
		return http.StatusTooManyRequests
	}
	return http.StatusServiceUnavailable
}

// ============================================================================
// Refactored helpers: each validates one handshake phase and returns true on success.
// On failure they write the appropriate HTTP error and return false.
// ============================================================================

func handshakeValidateOrigin(w http.ResponseWriter, r *http.Request, cfg ServerConfig) bool {
	origin := r.Header.Get("Origin")
	if !isOriginAllowed(origin, cfg.AllowedOrigins, cfg.AllowAllOrigins) {
		cfg.Hub.securityRejections.Add(1)
		writeWebSocketHandshakeError(w, r, http.StatusForbidden, codeWebSocketForbiddenOrigin, "forbidden origin", contract.CategoryClient)
		return false
	}
	return true
}

func handshakeValidateMethod(w http.ResponseWriter, r *http.Request, cfg ServerConfig) bool {
	if r.Method != http.MethodGet {
		writeWebSocketHandshakeError(w, r, http.StatusMethodNotAllowed, contract.CodeMethodNotAllowed, "method not allowed", contract.CategoryClient)
		return false
	}
	if !headerContains(r.Header, "Connection", "Upgrade") || !headerContains(r.Header, "Upgrade", "websocket") {
		writeWebSocketHandshakeError(w, r, http.StatusBadRequest, codeWebSocketBadUpgrade, "websocket upgrade required", contract.CategoryClient)
		return false
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		writeWebSocketHandshakeError(w, r, http.StatusBadRequest, codeWebSocketBadVersion, "websocket version 13 required", contract.CategoryClient)
		return false
	}
	return true
}

func handshakeValidateKey(w http.ResponseWriter, r *http.Request, cfg ServerConfig) (string, bool) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		writeWebSocketHandshakeError(w, r, http.StatusBadRequest, codeWebSocketKeyMissing, "websocket key required", contract.CategoryClient)
		return "", false
	}
	if err := ValidateWebSocketKey(key); err != nil {
		cfg.Hub.invalidWSKeys.Add(1)
		writeWebSocketHandshakeError(w, r, http.StatusBadRequest, codeWebSocketKeyInvalid, "invalid websocket key", contract.CategoryClient)
		return "", false
	}
	return key, true
}

func handshakeResolveRoom(w http.ResponseWriter, r *http.Request, cfg ServerConfig) (string, bool) {
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "default"
	}
	if err := cfg.RoomNameValidator(room); err != nil {
		cfg.Hub.securityRejections.Add(1)
		writeWebSocketHandshakeError(w, r, http.StatusBadRequest, codeWebSocketInvalidRoom, "invalid websocket room", contract.CategoryClient)
		return "", false
	}
	return room, true
}

func handshakeResolveUser(w http.ResponseWriter, r *http.Request, cfg ServerConfig) (token string, userInfo *UserInfo, tokenClaims map[string]any, ok bool) {
	if ah := r.Header.Get("Authorization"); ah != "" && strings.HasPrefix(strings.ToLower(ah), "bearer ") {
		token = strings.TrimSpace(ah[len("bearer "):])
	} else if cfg.AllowQueryToken {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		if !cfg.AllowUnauthenticated {
			cfg.Hub.securityRejections.Add(1)
			writeWebSocketHandshakeError(w, r, http.StatusUnauthorized, codeWebSocketTokenRequired, "websocket token required", contract.CategoryClient)
			return "", nil, nil, false
		}
		return "", nil, nil, true // anonymous allowed
	}
	if cfg.TokenAuth == nil {
		cfg.Hub.securityRejections.Add(1)
		writeWebSocketHandshakeError(w, r, http.StatusForbidden, codeWebSocketInvalidToken, "invalid websocket token", contract.CategoryClient)
		return "", nil, nil, false
	}
	payload, err := cfg.TokenAuth.VerifyJWT(token)
	if err != nil {
		cfg.Hub.securityRejections.Add(1)
		writeWebSocketHandshakeError(w, r, http.StatusForbidden, codeWebSocketInvalidToken, "invalid websocket token", contract.CategoryClient)
		return "", nil, nil, false
	}
	cfg.Hub.successfulAuths.Add(1)
	return token, ExtractUserInfo(payload), payload, true
}

func handshakeCheckRoomAccess(w http.ResponseWriter, r *http.Request, cfg ServerConfig, room string, userInfo *UserInfo, tokenClaims map[string]any, token string) bool {
	roomPwd := r.Header.Get(roomPasswordHeader)
	if !authorizeRoomAccess(cfg.RoomAuth, RoomAuthorization{
		Request:      r,
		Room:         room,
		Password:     roomPwd,
		User:         userInfo,
		TokenClaims:  tokenClaims,
		Anonymous:    token == "",
		QueryTokenOK: cfg.AllowQueryToken,
	}) {
		cfg.Hub.securityRejections.Add(1)
		writeWebSocketHandshakeError(w, r, http.StatusForbidden, codeWebSocketRoomForbidden, "websocket room access denied", contract.CategoryClient)
		return false
	}
	if err := cfg.Hub.canJoin(room, cfg.RoomNameValidator); err != nil {
		status := websocketJoinDeniedStatus(err)
		writeWebSocketHandshakeError(w, r, status, codeWebSocketJoinDenied, "websocket room join denied", contract.CategoryClient)
		return false
	}
	return true
}

func handshakeUpgradeConn(w http.ResponseWriter, r *http.Request, cfg ServerConfig, userInfo *UserInfo) (*Conn, *bufio.Writer, bool) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeWebSocketHandshakeError(w, r, http.StatusInternalServerError, codeWebSocketHijackUnsupported, "websocket hijack unsupported", contract.CategoryServer)
		return nil, nil, false
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		writeWebSocketHandshakeError(w, r, http.StatusInternalServerError, codeWebSocketHandshakeFailed, "websocket handshake failed", contract.CategoryServer)
		return nil, nil, false
	}

	c := newConnFromHijack(conn, buf.Reader, buf.Writer, cfg.QueueSize, cfg.SendTimeout, cfg.SendBehavior, false)
	if cfg.WriteTimeout > 0 {
		if err := c.SetWriteTimeout(cfg.WriteTimeout); err != nil {
			writeHijackedWebSocketHandshakeError(buf.Writer, http.StatusInternalServerError, codeWebSocketInvalidConfig, "websocket server misconfigured", contract.CategoryServer)
			_ = c.Close()
			return nil, nil, false
		}
	}
	if cfg.ReadLimit > 0 {
		if err := c.SetReadLimit(cfg.ReadLimit); err != nil {
			writeHijackedWebSocketHandshakeError(buf.Writer, http.StatusInternalServerError, codeWebSocketInvalidConfig, "websocket server misconfigured", contract.CategoryServer)
			_ = c.Close()
			return nil, nil, false
		}
	}
	c.UserInfo = userInfo
	return c, buf.Writer, true
}

func handshakeRegisterAndRespond(c *Conn, bufWriter *bufio.Writer, cfg ServerConfig, room, key string) bool {
	if err := cfg.Hub.tryJoin(room, c, cfg.RoomNameValidator); err != nil {
		writeHijackedWebSocketHandshakeError(bufWriter, websocketJoinDeniedStatus(err), codeWebSocketJoinDenied, "websocket room join denied", contract.CategoryClient)
		_ = c.Close()
		return false
	}

	accept := computeAcceptKey(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n" +
		"\r\n"
	if _, err := bufWriter.WriteString(resp); err != nil {
		cfg.Hub.RemoveConn(c)
		_ = c.Close()
		return false
	}
	if err := bufWriter.Flush(); err != nil {
		cfg.Hub.RemoveConn(c)
		_ = c.Close()
		return false
	}
	return true
}

func startReadLoopAndCleanup(c *Conn, room string, cfg ServerConfig) {
	go func() {
		<-c.closeC
		cfg.Hub.RemoveConn(c)
	}()

	go func() {
		validationCfg := resolveValidationConfig(cfg)
		for {
			op, rstream, err := c.ReadMessageReader()
			if err != nil {
				if err != io.EOF {
					cfg.Hub.logger.Printf("ReadMessageReader error: %v", err)
					writeCloseForReadError(c, err)
				}
				_ = c.Close()
				return
			}
			buf := msgBufPool.Get().(*bytes.Buffer)
			buf.Reset()
			if _, err := io.Copy(buf, rstream); err != nil {
				_ = rstream.Close()
				putMessageBuffer(buf)
				cfg.Hub.logger.Printf("ReadMessageReader copy error: %v", err)
				writeCloseForReadError(c, err)
				_ = c.Close()
				return
			}
			if err := rstream.Close(); err != nil {
				putMessageBuffer(buf)
				cfg.Hub.logger.Printf("ReadMessageReader close error: %v", err)
				_ = c.WriteClose(CloseServerError, "read close failed")
				_ = c.Close()
				return
			}
			// Validate text messages before broadcasting.
			if op == OpcodeText {
				if err := ValidateTextMessage(buf.Bytes(), validationCfg); err != nil {
					cfg.Hub.logger.Printf("closing invalid text message: %v", err)
					putMessageBuffer(buf)
					writeCloseForValidationError(c, err)
					_ = c.Close()
					return
				}
			}
			// Copy data before returning buf to pool; callbacks may retain.
			data := make([]byte, buf.Len())
			copy(data, buf.Bytes())
			putMessageBuffer(buf)
			if cfg.OnMessage != nil {
				if err := cfg.OnMessage(c, Message{Room: room, Op: op, Data: data}); err != nil {
					cfg.Hub.logger.Printf("OnMessage error: %v", err)
					writeCloseForHandlerError(c, err)
					_ = c.Close()
					return
				}
			}
		}
	}()
}
