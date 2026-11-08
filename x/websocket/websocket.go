package websocket

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	"github.com/birdordev/plumego/router"
)

type routeRegistrar interface {
	AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error
}

// BroadcastAuthorizer authorizes admin broadcast requests.
type BroadcastAuthorizer func(*http.Request) bool

// WebSocketConfig defines the configuration for WebSocket.
type WebSocketConfig struct {
	WorkerCount           int           // Number of worker goroutines
	JobQueueSize          int           // Size of the job queue
	SendQueueSize         int           // Size of the send queue per connection
	SendTimeout           time.Duration // Timeout for sending messages
	WriteTimeout          time.Duration // Timeout for network frame writes
	SendBehavior          SendBehavior  // Behavior when queue is full or timeout occurs
	Secret                []byte        // Secret key for JWT authentication
	TokenAuth             TokenAuthenticator
	RoomAuth              RoomAuthorizer
	OnMessage             MessageHandler
	WSRoutePath           string // Path for WebSocket connection
	BroadcastPath         string // Path for broadcasting messages
	BroadcastEnabled      bool   // Enable broadcast endpoint when true
	BroadcastSecret       []byte // Dedicated bearer secret for admin broadcast.
	BroadcastAuthorizer   BroadcastAuthorizer
	BroadcastMaxBytes     int64    // Maximum admin broadcast request body size. 0 means DefaultBroadcastMaxBytes.
	AllowedOrigins        []string // Browser origins allowed to connect. Empty rejects requests with Origin.
	AllowAllOrigins       bool     // Explicitly disable origin checks.
	AllowUnauthenticated  bool     // Explicitly allow websocket connections without JWT.
	AllowQueryToken       bool     // Explicitly allow ?token= JWT transport for trusted clients.
	RoomNameValidator     RoomNameValidator
	MaxRoomRegistrations  int  // Maximum room registrations (0 = unlimited)
	MaxRoomConnections    int  // Maximum connections per room (0 = unlimited)
	RejectOnQueueFull     bool // Count full broadcast queues as rejected work.
	MaxConnectionRate     int  // Maximum new room registrations per second (0 = unlimited).
	EnableDebugLogging    bool // Enable hub debug logs through Logger.
	EnableSecurityMetrics bool // Enable hub security event monitoring; metrics counters are always collected.
	Logger                *log.Logger
}

const (
	// DefaultSendQueueSize is the default buffer size for the WebSocket send queue.
	DefaultSendQueueSize = 256
	// DefaultBroadcastMaxBytes is the default max body size for the admin broadcast endpoint.
	DefaultBroadcastMaxBytes = 1 << 20
)

// DefaultWebSocketConfig returns default WebSocket configuration.
func DefaultWebSocketConfig() WebSocketConfig {
	return WebSocketConfig{
		WorkerCount:          16,
		JobQueueSize:         4096,
		SendQueueSize:        DefaultSendQueueSize,
		SendTimeout:          200 * time.Millisecond,
		SendBehavior:         SendBlock,
		WSRoutePath:          "/ws",
		BroadcastPath:        "/_admin/broadcast",
		BroadcastEnabled:     false,
		BroadcastMaxBytes:    DefaultBroadcastMaxBytes,
		MaxRoomRegistrations: 0,
		MaxRoomConnections:   0,
	}
}

type Server struct {
	config WebSocketConfig
	token  TokenAuthenticator
	room   RoomAuthorizer
	hub    *Hub
}

const minWebSocketSecretLen = 32

func New(cfg WebSocketConfig) (*Server, error) {
	cfg = normalizeWebSocketConfig(cfg)
	tokenAuth := cfg.TokenAuth
	if tokenAuth == nil && !cfg.AllowUnauthenticated {
		if len(cfg.Secret) < minWebSocketSecretLen {
			return nil, fmt.Errorf(
				"websocket secret must be at least %d bytes (pass Secret or TokenAuth via WebSocketConfig)",
				minWebSocketSecretLen,
			)
		}
		var err error
		tokenAuth, err = NewSimpleHS256TokenAuth(cfg.Secret)
		if err != nil {
			return nil, err
		}
	}
	if cfg.BroadcastMaxBytes < 0 {
		return nil, fmt.Errorf("websocket broadcast max bytes cannot be negative")
	}
	if cfg.WriteTimeout < 0 {
		return nil, ErrInvalidWriteTimeout
	}
	if cfg.BroadcastEnabled && cfg.BroadcastAuthorizer == nil {
		if len(cfg.BroadcastSecret) < minWebSocketSecretLen {
			return nil, fmt.Errorf("websocket broadcast secret must be at least %d bytes or BroadcastAuthorizer must be provided", minWebSocketSecretLen)
		}
		if secretsEqual(cfg.BroadcastSecret, cfg.Secret) {
			return nil, fmt.Errorf("websocket broadcast secret must be separate from websocket JWT secret")
		}
	}
	cfg.Secret = cloneBytes(cfg.Secret)
	cfg.BroadcastSecret = cloneBytes(cfg.BroadcastSecret)

	hub, err := NewHubWithConfig(HubConfig{
		WorkerCount:           cfg.WorkerCount,
		JobQueueSize:          cfg.JobQueueSize,
		MaxRoomRegistrations:  cfg.MaxRoomRegistrations,
		MaxRoomConnections:    cfg.MaxRoomConnections,
		RoomNameValidator:     cfg.RoomNameValidator,
		RejectOnQueueFull:     cfg.RejectOnQueueFull,
		MaxConnectionRate:     cfg.MaxConnectionRate,
		EnableDebugLogging:    cfg.EnableDebugLogging,
		EnableSecurityMetrics: cfg.EnableSecurityMetrics,
		Logger:                cfg.Logger,
	})
	if err != nil {
		return nil, err
	}

	return &Server{
		config: cfg,
		token:  tokenAuth,
		room:   cfg.RoomAuth,
		hub:    hub,
	}, nil
}

func normalizeWebSocketConfig(cfg WebSocketConfig) WebSocketConfig {
	defaults := DefaultWebSocketConfig()
	if cfg.WorkerCount == 0 {
		cfg.WorkerCount = defaults.WorkerCount
	}
	if cfg.JobQueueSize == 0 {
		cfg.JobQueueSize = defaults.JobQueueSize
	}
	if cfg.SendQueueSize == 0 {
		cfg.SendQueueSize = defaults.SendQueueSize
	}
	if cfg.SendTimeout == 0 {
		cfg.SendTimeout = defaults.SendTimeout
	}
	if strings.TrimSpace(cfg.WSRoutePath) == "" {
		cfg.WSRoutePath = defaults.WSRoutePath
	}
	if strings.TrimSpace(cfg.BroadcastPath) == "" {
		cfg.BroadcastPath = defaults.BroadcastPath
	}
	if cfg.BroadcastMaxBytes == 0 {
		cfg.BroadcastMaxBytes = defaults.BroadcastMaxBytes
	}
	return cfg
}

func (c *Server) RegisterRoutes(r routeRegistrar) error {
	if c == nil {
		return fmt.Errorf("%w: websocket server is nil", ErrInvalidConfig)
	}
	if r == nil {
		return fmt.Errorf("%w: websocket route registrar is nil", ErrInvalidConfig)
	}
	if c.hub == nil {
		return ErrNilHub
	}
	if strings.TrimSpace(c.config.WSRoutePath) == "" {
		return fmt.Errorf("%w: websocket route path is empty", ErrInvalidConfig)
	}
	if c.config.BroadcastEnabled && strings.TrimSpace(c.config.BroadcastPath) == "" {
		return fmt.Errorf("%w: websocket broadcast path is empty", ErrInvalidConfig)
	}

	if err := r.AddRoute(http.MethodGet, c.config.WSRoutePath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCfg := ServerConfig{
			Hub:                  c.hub,
			TokenAuth:            c.token,
			RoomAuth:             c.room,
			OnMessage:            c.config.OnMessage,
			QueueSize:            c.config.SendQueueSize,
			SendTimeout:          c.config.SendTimeout,
			WriteTimeout:         c.config.WriteTimeout,
			SendBehavior:         c.config.SendBehavior,
			AllowedOrigins:       c.config.AllowedOrigins,
			AllowAllOrigins:      c.config.AllowAllOrigins,
			AllowUnauthenticated: c.config.AllowUnauthenticated,
			AllowQueryToken:      c.config.AllowQueryToken,
			RoomNameValidator:    c.config.RoomNameValidator,
		}
		if c.config.OnMessage != nil {
			ServeWSWithConfig(w, r, serverCfg)
			return
		}
		ServeRoomFanoutWS(w, r, serverCfg)
	})); err != nil {
		return err
	}

	if c.config.BroadcastEnabled && c.config.BroadcastPath != "" {
		if err := r.AddRoute(http.MethodPost, c.config.BroadcastPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !c.authorizeBroadcast(r) {
				writeWebSocketHTTPError(w, r,
					http.StatusUnauthorized,
					contract.CodeUnauthorized,
					"unauthorized",
					contract.CategoryAuth,
				)
				return
			}

			b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, c.config.BroadcastMaxBytes))
			if err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					writeWebSocketHTTPError(w, r,
						http.StatusRequestEntityTooLarge,
						codeWebSocketRequestTooLarge,
						"request body too large",
						contract.CategoryClient,
					)
					return
				}
				writeWebSocketHTTPError(w, r,
					http.StatusInternalServerError,
					codeWebSocketRequestReadFailure,
					"error reading request body",
					contract.CategoryServer,
				)
				return
			}

			// Optional ?room= parameter targets a specific room; omit for all-room broadcast.
			var result BroadcastResult
			if room := r.URL.Query().Get("room"); room != "" {
				if err := validateRoomName(room, c.config.RoomNameValidator); err != nil {
					writeWebSocketHTTPError(w, r,
						http.StatusBadRequest,
						codeWebSocketInvalidRoom,
						"invalid websocket room",
						contract.CategoryValidation,
					)
					return
				}
				result = c.hub.tryBroadcastRoom(room, OpcodeText, b, c.config.RoomNameValidator)
			} else {
				result = c.hub.TryBroadcastAll(OpcodeText, b)
			}
			if result.Rejected() {
				writeWebSocketHTTPError(w, r,
					http.StatusServiceUnavailable,
					codeWebSocketBroadcastRejected,
					"websocket broadcast rejected",
					contract.CategoryServer,
				)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})); err != nil {
			return err
		}
	}

	return nil
}

func (c *Server) authorizeBroadcast(r *http.Request) bool {
	if c.config.BroadcastAuthorizer != nil {
		return c.config.BroadcastAuthorizer(r)
	}
	provided := bearerToken(r.Header.Get("Authorization"))
	if len(provided) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(provided, c.config.BroadcastSecret) == 1
}

func bearerToken(rawAuth string) []byte {
	const bearerPrefix = "bearer "
	if !strings.HasPrefix(strings.ToLower(rawAuth), bearerPrefix) {
		return nil
	}
	token := strings.TrimSpace(rawAuth[len(bearerPrefix):])
	if token == "" {
		return nil
	}
	return []byte(token)
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func secretsEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

func (c *Server) Shutdown(ctx context.Context) error {
	if c.hub != nil {
		err := c.hub.Shutdown(ctx)
		c.hub = nil
		return err
	}
	return nil
}

func (c *Server) Health() (string, health.HealthStatus) {
	status := health.HealthStatus{Status: health.StatusHealthy, Details: map[string]any{"broadcastEnabled": c.config.BroadcastEnabled}}

	if c.hub == nil {
		status.Status = health.StatusUnhealthy
		status.Message = "hub not initialized"
	}

	return "websocket", status
}

// Hub exposes the underlying WebSocket hub for advanced usage.
func (c *Server) Hub() *Hub { return c.hub }
