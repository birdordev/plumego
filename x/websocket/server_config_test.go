package websocket

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

const validTestWSKey = "dGhlIHNhbXBsZSBub25jZQ=="

func TestServeWSWithConfig_HandshakeErrorContract(t *testing.T) {
	tests := []struct {
		name        string
		cfg         func(*testing.T) ServerConfig
		req         func() *http.Request
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name: "invalid config",
			cfg: func(t *testing.T) ServerConfig {
				return ServerConfig{TokenAuth: mustSimpleHS256TokenAuth(t, validSecret())}
			},
			req:         newValidHandshakeRequest,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    codeWebSocketInvalidConfig,
			wantMessage: "websocket server misconfigured",
		},
		{
			name: "method not allowed",
			cfg:  defaultHandshakeConfig,
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodPost, "/ws", nil)
			},
			wantStatus:  http.StatusMethodNotAllowed,
			wantCode:    contract.CodeMethodNotAllowed,
			wantMessage: "method not allowed",
		},
		{
			name: "bad upgrade",
			cfg:  defaultHandshakeConfig,
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/ws", nil)
			},
			wantStatus:  http.StatusBadRequest,
			wantCode:    codeWebSocketBadUpgrade,
			wantMessage: "websocket upgrade required",
		},
		{
			name: "missing websocket version",
			cfg:  defaultHandshakeConfig,
			req: func() *http.Request {
				r := newValidHandshakeRequest()
				r.Header.Del("Sec-WebSocket-Version")
				return r
			},
			wantStatus:  http.StatusBadRequest,
			wantCode:    codeWebSocketBadVersion,
			wantMessage: "websocket version 13 required",
		},
		{
			name: "unsupported websocket version",
			cfg:  defaultHandshakeConfig,
			req: func() *http.Request {
				r := newValidHandshakeRequest()
				r.Header.Set("Sec-WebSocket-Version", "12")
				return r
			},
			wantStatus:  http.StatusBadRequest,
			wantCode:    codeWebSocketBadVersion,
			wantMessage: "websocket version 13 required",
		},
		{
			name: "missing websocket key",
			cfg:  defaultHandshakeConfig,
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/ws", nil)
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
				r.Header.Set("Sec-WebSocket-Version", "13")
				return r
			},
			wantStatus:  http.StatusBadRequest,
			wantCode:    codeWebSocketKeyMissing,
			wantMessage: "websocket key required",
		},
		{
			name: "invalid websocket key",
			cfg:  defaultHandshakeConfig,
			req: func() *http.Request {
				r := newValidHandshakeRequest()
				r.Header.Set("Sec-WebSocket-Key", "bad-key")
				return r
			},
			wantStatus:  http.StatusBadRequest,
			wantCode:    codeWebSocketKeyInvalid,
			wantMessage: "invalid websocket key",
		},
		{
			name: "forbidden origin",
			cfg: func(t *testing.T) ServerConfig {
				cfg := defaultHandshakeConfig(t)
				cfg.AllowedOrigins = []string{"https://allowed.example"}
				return cfg
			},
			req: func() *http.Request {
				r := newValidHandshakeRequest()
				r.Header.Set("Origin", "https://blocked.example")
				return r
			},
			wantStatus:  http.StatusForbidden,
			wantCode:    codeWebSocketForbiddenOrigin,
			wantMessage: "forbidden origin",
		},
		{
			name: "room password denied",
			cfg: func(t *testing.T) ServerConfig {
				auth := mustSimpleRoomAuth(t)
				if err := auth.SetRoomPassword("private", "correct"); err != nil {
					t.Fatalf("SetRoomPassword: %v", err)
				}
				return ServerConfig{
					Hub:                  mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4}),
					TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
					RoomAuth:             auth,
					SendBehavior:         SendBlock,
					AllowUnauthenticated: true,
				}
			},
			req: func() *http.Request {
				r := newValidHandshakeRequest()
				r.URL.RawQuery = "room=private"
				r.Header.Set(roomPasswordHeader, "wrong")
				return r
			},
			wantStatus:  http.StatusForbidden,
			wantCode:    codeWebSocketRoomForbidden,
			wantMessage: "websocket room access denied",
		},
		{
			name: "invalid room name",
			cfg:  defaultHandshakeConfig,
			req: func() *http.Request {
				r := newValidHandshakeRequest()
				r.URL.RawQuery = "room=private/team"
				return r
			},
			wantStatus:  http.StatusBadRequest,
			wantCode:    codeWebSocketInvalidRoom,
			wantMessage: "invalid websocket room",
		},
		{
			name: "join denied",
			cfg: func(t *testing.T) ServerConfig {
				cfg := defaultHandshakeConfig(t)
				cfg.Hub.Stop()
				return cfg
			},
			req:         newValidHandshakeRequest,
			wantStatus:  http.StatusServiceUnavailable,
			wantCode:    codeWebSocketJoinDenied,
			wantMessage: "websocket room join denied",
		},
		{
			name: "missing token",
			cfg: func(t *testing.T) ServerConfig {
				cfg := defaultHandshakeConfig(t)
				cfg.AllowUnauthenticated = false
				return cfg
			},
			req:         newValidHandshakeRequest,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    codeWebSocketTokenRequired,
			wantMessage: "websocket token required",
		},
		{
			name: "invalid token",
			cfg:  defaultHandshakeConfig,
			req: func() *http.Request {
				r := newValidHandshakeRequest()
				r.Header.Set("Authorization", "Bearer not-a-token")
				return r
			},
			wantStatus:  http.StatusForbidden,
			wantCode:    codeWebSocketInvalidToken,
			wantMessage: "invalid websocket token",
		},
		{
			name: "query token disabled",
			cfg: func(t *testing.T) ServerConfig {
				cfg := defaultHandshakeConfig(t)
				cfg.AllowUnauthenticated = false
				cfg.AllowQueryToken = false
				return cfg
			},
			req: func() *http.Request {
				r := newValidHandshakeRequest()
				r.URL.RawQuery = "token=not-a-token"
				return r
			},
			wantStatus:  http.StatusUnauthorized,
			wantCode:    codeWebSocketTokenRequired,
			wantMessage: "websocket token required",
		},
		{
			name:        "hijack unsupported",
			cfg:         defaultHandshakeConfig,
			req:         newValidHandshakeRequest,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    codeWebSocketHijackUnsupported,
			wantMessage: "websocket hijack unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg(t)
			if cfg.Hub != nil {
				defer cfg.Hub.Stop()
			}

			w := httptest.NewRecorder()
			ServeWSWithConfig(w, tt.req(), cfg)

			assertWebSocketError(t, w, tt.wantStatus, tt.wantCode, tt.wantMessage)
		})
	}
}

func TestServeWSWithConfig_IgnoresQueryRoomPassword(t *testing.T) {
	auth := mustSimpleRoomAuth(t)
	if err := auth.SetRoomPassword("private", "correct"); err != nil {
		t.Fatalf("SetRoomPassword: %v", err)
	}
	cfg := ServerConfig{
		Hub:                  mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4}),
		TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
		RoomAuth:             auth,
		SendBehavior:         SendBlock,
		AllowUnauthenticated: true,
	}
	defer cfg.Hub.Stop()

	r := newValidHandshakeRequest()
	r.URL.RawQuery = "room=private&room_password=correct"
	w := httptest.NewRecorder()

	ServeWSWithConfig(w, r, cfg)

	assertWebSocketError(t, w, http.StatusForbidden, codeWebSocketRoomForbidden, "websocket room access denied")
}

func TestServeRoomFanoutWSRejectsConflictingHandler(t *testing.T) {
	cfg := defaultHandshakeConfig(t)
	cfg.OnMessage = func(*Conn, Message) error { return nil }
	defer cfg.Hub.Stop()

	w := httptest.NewRecorder()
	ServeRoomFanoutWS(w, newValidHandshakeRequest(), cfg)

	assertWebSocketError(t, w, http.StatusInternalServerError, codeWebSocketInvalidConfig, "websocket server misconfigured")
}

func TestServeWSWithConfig_VerifiesSuppliedTokenBeforeRoomAuth(t *testing.T) {
	auth := mustSimpleRoomAuth(t)
	if err := auth.SetRoomPassword("private", "correct"); err != nil {
		t.Fatalf("SetRoomPassword: %v", err)
	}
	cfg := ServerConfig{
		Hub:                  mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4}),
		TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
		RoomAuth:             auth,
		SendBehavior:         SendBlock,
		AllowUnauthenticated: true,
	}
	defer cfg.Hub.Stop()

	r := newValidHandshakeRequest()
	r.URL.RawQuery = "room=private"
	r.Header.Set("Authorization", "Bearer not-a-token")
	r.Header.Set(roomPasswordHeader, "wrong")
	w := httptest.NewRecorder()

	ServeWSWithConfig(w, r, cfg)

	assertWebSocketError(t, w, http.StatusForbidden, codeWebSocketInvalidToken, "invalid websocket token")
}

type claimsRoomAuthorizer struct {
	seen *UserInfo
}

func (a *claimsRoomAuthorizer) CheckRoomPassword(string, string) bool {
	return false
}

func (a *claimsRoomAuthorizer) AuthorizeRoom(decision RoomAuthorization) bool {
	a.seen = decision.User
	return decision.Room == "claims" &&
		decision.User != nil &&
		decision.User.ID == "test-user" &&
		decision.TokenClaims["sub"] == "test-user" &&
		!decision.Anonymous
}

func TestServeWSWithConfig_ContextualRoomAuthorizerSeesClaims(t *testing.T) {
	auth := &claimsRoomAuthorizer{}
	cfg := ServerConfig{
		Hub:                  mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4}),
		TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
		RoomAuth:             auth,
		SendBehavior:         SendBlock,
		AllowUnauthenticated: false,
	}
	defer cfg.Hub.Stop()

	r := newValidHandshakeRequest()
	r.URL.RawQuery = "room=claims"
	r.Header.Set("Authorization", "Bearer "+testJWTToken(t, validSecret()))
	w := httptest.NewRecorder()

	ServeWSWithConfig(w, r, cfg)

	if auth.seen == nil || auth.seen.ID != "test-user" {
		t.Fatalf("contextual room authorizer did not see authenticated user: %+v", auth.seen)
	}
	assertWebSocketError(t, w, http.StatusInternalServerError, codeWebSocketHijackUnsupported, "websocket hijack unsupported")
}

func TestServeWSWithConfig_CustomRoomNameValidator(t *testing.T) {
	cfg := defaultHandshakeConfig(t)
	cfg.RoomNameValidator = func(room string) error {
		if room == "team/blue" {
			return nil
		}
		return ErrInvalidRoomName
	}
	defer cfg.Hub.Stop()

	r := newValidHandshakeRequest()
	r.URL.RawQuery = "room=team/blue"
	w := httptest.NewRecorder()

	ServeWSWithConfig(w, r, cfg)

	assertWebSocketError(t, w, http.StatusInternalServerError, codeWebSocketHijackUnsupported, "websocket hijack unsupported")
}

func defaultHandshakeConfig(t *testing.T) ServerConfig {
	t.Helper()
	return ServerConfig{
		Hub:                  mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4}),
		TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
		RoomAuth:             mustSimpleRoomAuth(t),
		SendBehavior:         SendBlock,
		AllowUnauthenticated: true,
	}
}

func TestServeWSWithConfig_OriginRequiresExplicitAllow(t *testing.T) {
	cfg := defaultHandshakeConfig(t)
	cfg.AllowedOrigins = nil
	cfg.AllowAllOrigins = false
	defer cfg.Hub.Stop()

	r := newValidHandshakeRequest()
	r.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()

	ServeWSWithConfig(w, r, cfg)

	assertWebSocketError(t, w, http.StatusForbidden, codeWebSocketForbiddenOrigin, "forbidden origin")
}

func TestServeWSWithConfig_AllowAllOriginsIsExplicit(t *testing.T) {
	cfg := defaultHandshakeConfig(t)
	cfg.AllowedOrigins = nil
	cfg.AllowAllOrigins = true
	defer cfg.Hub.Stop()

	r := newValidHandshakeRequest()
	r.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()

	ServeWSWithConfig(w, r, cfg)

	assertWebSocketError(t, w, http.StatusInternalServerError, codeWebSocketHijackUnsupported, "websocket hijack unsupported")
}

func TestServeWSWithConfig_AllowedOriginsWildcardDoesNotAllowAll(t *testing.T) {
	cfg := defaultHandshakeConfig(t)
	cfg.AllowedOrigins = []string{"*"}
	cfg.AllowAllOrigins = false
	defer cfg.Hub.Stop()

	r := newValidHandshakeRequest()
	r.Header.Set("Origin", "https://app.example")
	w := httptest.NewRecorder()

	ServeWSWithConfig(w, r, cfg)

	assertWebSocketError(t, w, http.StatusForbidden, codeWebSocketForbiddenOrigin, "forbidden origin")
}

func TestServeWSWithConfig_PostHijackJoinDeniedReturnsHTTPError(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{
		MaxRoomRegistrations: 1,
		WorkerCount:          1,
		JobQueueSize:         4,
	})
	defer hub.Stop()

	cfg := ServerConfig{
		Hub:                  hub,
		TokenAuth:            mustSimpleHS256TokenAuth(t, validSecret()),
		RoomAuth:             mustSimpleRoomAuth(t),
		SendBehavior:         SendBlock,
		AllowAllOrigins:      true,
		AllowUnauthenticated: true,
	}

	clientReady := make(chan net.Conn, 1)
	done := make(chan struct{})
	w := &joinRaceHijackWriter{
		ResponseRecorder: httptest.NewRecorder(),
		onHijack: func() {
			existing := newMockConn()
			t.Cleanup(func() {
				hub.RemoveConn(existing)
				_ = existing.Close()
			})
			if err := hub.TryJoin("default", existing); err != nil {
				t.Errorf("fill hub during hijack: %v", err)
			}
		},
		clientReady: clientReady,
	}

	go func() {
		defer close(done)
		ServeWSWithConfig(w, newValidHandshakeRequest(), cfg)
	}()

	var client net.Conn
	select {
	case client = <-clientReady:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hijacked client connection")
	}
	defer client.Close()

	status, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read hijacked response status: %v", err)
	}
	if strings.Contains(status, "101") {
		t.Fatalf("unexpected websocket upgrade after join denial: %q", status)
	}
	if !strings.Contains(status, "503") {
		t.Fatalf("status = %q, want 503", status)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeWSWithConfig did not return after join denial")
	}
}

type joinRaceHijackWriter struct {
	*httptest.ResponseRecorder
	onHijack    func()
	clientReady chan<- net.Conn
}

func (w *joinRaceHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	server, client := net.Pipe()
	if w.onHijack != nil {
		w.onHijack()
	}
	w.clientReady <- client
	rw := bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server))
	return server, rw, nil
}

func newValidHandshakeRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", validTestWSKey)
	return r
}

func assertWebSocketError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode, wantMessage string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d", w.Code, wantStatus)
	}

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", resp.Error.Code, wantCode)
	}
	if resp.Error.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", resp.Error.Message, wantMessage)
	}
}

func TestServeWSWithConfig_InvalidConfig(t *testing.T) {
	t.Run("nil hub", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)

		ServeWSWithConfig(w, r, ServerConfig{
			TokenAuth: mustSimpleHS256TokenAuth(t, validSecret()),
		})

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", w.Code)
		}
	})

	t.Run("nil auth", func(t *testing.T) {
		hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
		defer hub.Stop()

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)

		ServeWSWithConfig(w, r, ServerConfig{
			Hub: hub,
		})

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", w.Code)
		}
	})

	t.Run("negative queue size", func(t *testing.T) {
		hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
		defer hub.Stop()

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)

		ServeWSWithConfig(w, r, ServerConfig{
			Hub:       hub,
			TokenAuth: mustSimpleHS256TokenAuth(t, validSecret()),
			QueueSize: -1,
		})

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", w.Code)
		}
	})
}

func TestResolveValidationConfig(t *testing.T) {
	cfg := ServerConfig{
		ReadLimit: 1024,
		MessageValidation: MessageValidationConfig{
			MaxLength:               4096,
			AllowEmpty:              true,
			RejectControlCharacters: true,
			RequireValidUTF8:        true,
		},
	}

	got := resolveValidationConfig(cfg)
	if got.MaxLength != 1024 {
		t.Fatalf("expected MaxLength=1024, got %d", got.MaxLength)
	}
	if !got.AllowEmpty {
		t.Fatal("expected AllowEmpty to be preserved")
	}
}

func TestNormalizeServerConfig_ReadLimitFromAuth(t *testing.T) {
	secret := bytes.Repeat([]byte("a"), 32)
	auth, err := NewSecureRoomAuth(secret, SecurityConfig{
		JWTSecret:          secret,
		MinJWTSecretLength: 32,
		MaxMessageSize:     2048,
	})
	if err != nil {
		t.Fatalf("NewSecureRoomAuth error: %v", err)
	}

	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	cfg, err := normalizeServerConfig(ServerConfig{
		Hub:          hub,
		TokenAuth:    auth,
		RoomAuth:     auth,
		SendBehavior: SendBlock,
	})
	if err != nil {
		t.Fatalf("normalizeServerConfig error: %v", err)
	}
	if cfg.ReadLimit != 2048 {
		t.Fatalf("expected read limit from auth (2048), got %d", cfg.ReadLimit)
	}
}

func TestNewSecureRoomAuth_SecretMismatch(t *testing.T) {
	secretA := bytes.Repeat([]byte("a"), 32)
	secretB := bytes.Repeat([]byte("b"), 32)

	_, err := NewSecureRoomAuth(secretA, SecurityConfig{
		JWTSecret:          secretB,
		MinJWTSecretLength: 32,
	})
	if err == nil {
		t.Fatal("expected error for mismatched secrets")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}
