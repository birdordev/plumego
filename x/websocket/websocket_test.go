package websocket

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/health"
	"github.com/birdordev/plumego/router"
)

// validSecret returns a secret that meets the minimum length requirement.
func validSecret() []byte {
	return []byte("this-is-a-secret-key-that-is-at-least-32-bytes-long!!")
}

func validBroadcastSecret() []byte {
	return []byte("this-is-a-distinct-broadcast-secret-32-bytes!!")
}

func mustSimpleRoomAuth(t *testing.T) *SimpleRoomAuth {
	t.Helper()
	return NewSimpleRoomAuth()
}

func mustSimpleHS256TokenAuth(t *testing.T, secret []byte) *SimpleHS256TokenAuth {
	t.Helper()
	auth, err := NewSimpleHS256TokenAuth(secret)
	if err != nil {
		t.Fatalf("NewSimpleHS256TokenAuth: %v", err)
	}
	return auth
}

func mustNewHubConfig(t *testing.T, cfg HubConfig) *Hub {
	t.Helper()
	hub, err := NewHubWithConfig(cfg)
	if err != nil {
		t.Fatalf("NewHubWithConfig: %v", err)
	}
	return hub
}

func mustTryJoin(t *testing.T, hub *Hub, room string, conn *Conn) {
	t.Helper()
	if err := hub.TryJoin(room, conn); err != nil {
		t.Fatalf("TryJoin(%q): %v", room, err)
	}
}

func newManualHub(jobQueueSize int) *Hub {
	hub := &Hub{
		rooms:    make(map[string]map[*Conn]struct{}),
		jobQueue: make(chan hubJob, jobQueueSize),
		quit:     make(chan struct{}),
	}
	hub.connListPool = sync.Pool{
		New: func() any {
			conns := make([]*Conn, 0, 64)
			return &conns
		},
	}
	return hub
}

type websocketErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestDefaultWebSocketConfig(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	if cfg.WorkerCount != 16 {
		t.Fatalf("expected WorkerCount 16, got %d", cfg.WorkerCount)
	}
	if cfg.JobQueueSize != 4096 {
		t.Fatalf("expected JobQueueSize 4096, got %d", cfg.JobQueueSize)
	}
	if cfg.SendQueueSize != DefaultSendQueueSize {
		t.Fatalf("expected SendQueueSize %d, got %d", DefaultSendQueueSize, cfg.SendQueueSize)
	}
	if cfg.SendTimeout != 200*time.Millisecond {
		t.Fatalf("expected SendTimeout 200ms, got %v", cfg.SendTimeout)
	}
	if cfg.SendBehavior != SendBlock {
		t.Fatalf("expected SendBehavior SendBlock, got %v", cfg.SendBehavior)
	}
	if cfg.WSRoutePath != "/ws" {
		t.Fatalf("expected WSRoutePath /ws, got %q", cfg.WSRoutePath)
	}
	if cfg.BroadcastPath != "/_admin/broadcast" {
		t.Fatalf("expected BroadcastPath /_admin/broadcast, got %q", cfg.BroadcastPath)
	}
	if cfg.Secret != nil {
		t.Fatal("expected Secret to be caller-provided")
	}
	if cfg.BroadcastEnabled {
		t.Fatal("expected BroadcastEnabled false")
	}
	if cfg.BroadcastMaxBytes != DefaultBroadcastMaxBytes {
		t.Fatalf("expected BroadcastMaxBytes %d, got %d", DefaultBroadcastMaxBytes, cfg.BroadcastMaxBytes)
	}
	if cfg.AllowUnauthenticated {
		t.Fatal("expected AllowUnauthenticated false")
	}
	if cfg.AllowAllOrigins {
		t.Fatal("expected AllowAllOrigins false")
	}
}

func TestNewSecretTooShort(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = []byte("short")

	_, err := New(cfg)
	assertErrorContains(t, err, "at least")
}

func TestNewEmptySecret(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = nil

	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for nil secret")
	}
}

func TestNewAllowUnauthenticatedDoesNotRequireSecret(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = nil
	cfg.AllowUnauthenticated = true

	comp, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer comp.Shutdown(t.Context())
}

func TestNewValidSecret(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comp == nil {
		t.Fatal("expected non-nil component")
	}
	if comp.Hub() == nil {
		t.Fatal("expected non-nil hub")
	}
}

func TestNewCopiesSecrets(t *testing.T) {
	secret := validSecret()
	broadcastSecret := validBroadcastSecret()
	cfg := DefaultWebSocketConfig()
	cfg.Secret = secret
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = broadcastSecret

	comp, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer comp.Shutdown(t.Context())

	for i := range secret {
		secret[i] = 'x'
	}
	for i := range broadcastSecret {
		broadcastSecret[i] = 'y'
	}

	if string(comp.config.Secret) == string(secret) {
		t.Fatal("server secret aliases caller-provided slice")
	}
	if string(comp.config.BroadcastSecret) == string(broadcastSecret) {
		t.Fatal("server broadcast secret aliases caller-provided slice")
	}
}

func TestNewMinimalConfigAppliesDefaults(t *testing.T) {
	comp, err := New(WebSocketConfig{Secret: validSecret()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer comp.Shutdown(t.Context())

	if comp.config.WorkerCount != 16 {
		t.Fatalf("WorkerCount = %d, want 16", comp.config.WorkerCount)
	}
	if comp.config.JobQueueSize != 4096 {
		t.Fatalf("JobQueueSize = %d, want 4096", comp.config.JobQueueSize)
	}
	if comp.config.SendQueueSize != DefaultSendQueueSize {
		t.Fatalf("SendQueueSize = %d, want %d", comp.config.SendQueueSize, DefaultSendQueueSize)
	}
	if comp.config.SendTimeout != 200*time.Millisecond {
		t.Fatalf("SendTimeout = %v, want 200ms", comp.config.SendTimeout)
	}
	if comp.config.WSRoutePath != "/ws" {
		t.Fatalf("WSRoutePath = %q, want /ws", comp.config.WSRoutePath)
	}
	if comp.config.BroadcastPath != "/_admin/broadcast" {
		t.Fatalf("BroadcastPath = %q, want /_admin/broadcast", comp.config.BroadcastPath)
	}
	if comp.config.BroadcastMaxBytes != DefaultBroadcastMaxBytes {
		t.Fatalf("BroadcastMaxBytes = %d, want %d", comp.config.BroadcastMaxBytes, DefaultBroadcastMaxBytes)
	}
}

func TestNewNegativeBroadcastMaxBytes(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastMaxBytes = -1

	_, err := New(cfg)
	assertErrorContains(t, err, "broadcast max bytes")
}

func TestNewBroadcastEnabledRequiresDedicatedAuth(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true

	_, err := New(cfg)
	assertErrorContains(t, err, "broadcast secret")
}

func TestNewBroadcastSecretMustBeSeparate(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = cfg.Secret

	_, err := New(cfg)
	assertErrorContains(t, err, "separate")
}

func TestNewBroadcastAuthorizerAllowsMissingSecret(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastAuthorizer = func(*http.Request) bool { return true }

	comp, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comp == nil {
		t.Fatal("expected component")
	}
}

func TestRegisterRoutesInvalidInputs(t *testing.T) {
	t.Run("nil registrar", func(t *testing.T) {
		cfg := DefaultWebSocketConfig()
		cfg.Secret = validSecret()
		comp, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := comp.RegisterRoutes(nil); err == nil {
			t.Fatal("expected nil registrar error")
		}
	})

	t.Run("nil hub", func(t *testing.T) {
		cfg := DefaultWebSocketConfig()
		cfg.Secret = validSecret()
		comp, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		comp.hub = nil
		if err := comp.RegisterRoutes(router.NewRouter()); err == nil {
			t.Fatal("expected nil hub error")
		}
	})

	t.Run("empty websocket path", func(t *testing.T) {
		cfg := DefaultWebSocketConfig()
		cfg.Secret = validSecret()
		cfg.WSRoutePath = ""
		comp, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		comp.config.WSRoutePath = ""
		if err := comp.RegisterRoutes(router.NewRouter()); err == nil {
			t.Fatal("expected empty websocket path error")
		}
	})

	t.Run("empty broadcast path", func(t *testing.T) {
		cfg := DefaultWebSocketConfig()
		cfg.Secret = validSecret()
		cfg.BroadcastEnabled = true
		cfg.BroadcastSecret = validBroadcastSecret()
		cfg.BroadcastPath = ""
		comp, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		comp.config.BroadcastPath = ""
		if err := comp.RegisterRoutes(router.NewRouter()); err == nil {
			t.Fatal("expected empty broadcast path error")
		}
	})
}

func TestHealthHealthy(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	name, status := comp.Health()
	if name != "websocket" {
		t.Fatalf("expected name websocket, got %q", name)
	}
	if status.Status != health.StatusHealthy {
		t.Fatalf("expected healthy, got %s", status.Status)
	}
	if status.Details == nil {
		t.Fatal("expected non-nil details")
	}
	if val, ok := status.Details["broadcastEnabled"]; !ok || val != false {
		t.Fatalf("expected broadcastEnabled=false in details, got %v", status.Details)
	}
}

func TestHealthUnhealthyAfterStop(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := comp.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	_, status := comp.Health()
	if status.Status != health.StatusUnhealthy {
		t.Fatalf("expected unhealthy after stop, got %s", status.Status)
	}
	if status.Message == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestStopNilHub(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()

	comp, _ := New(cfg)
	// Stop once to set hub to nil
	_ = comp.Shutdown(t.Context())
	// Stop again should not panic
	if err := comp.Shutdown(t.Context()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestBroadcastEndpointNoAuth(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = validBroadcastSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader(`{"msg":"hi"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestBroadcastEndpointWrongToken(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = validBroadcastSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader(`{"msg":"hi"}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestBroadcastEndpointRejectsJWTSecret(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = validBroadcastSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader(`{"msg":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+string(cfg.Secret))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestBroadcastEndpointValidToken(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = validBroadcastSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader(`{"msg":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+string(cfg.BroadcastSecret))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		body := rec.Body.String()
		t.Fatalf("expected 204, got %d; body: %s", rec.Code, body)
	}
}

func TestBroadcastEndpointRejectsInvalidRoomName(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = validBroadcastSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast?room=bad/room", strings.NewReader(`{"msg":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+string(cfg.BroadcastSecret))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		var body websocketErrorResponse
		_ = json.NewDecoder(rec.Body).Decode(&body)
		t.Fatalf("expected 400, got %d; body: %v", rec.Code, body)
	}
}

func TestBroadcastEndpointAuthorizer(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastAuthorizer = func(r *http.Request) bool {
		return r.Header.Get("X-Broadcast-Admin") == "yes"
	}

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader(`{"msg":"hi"}`))
	req.Header.Set("X-Broadcast-Admin", "yes")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestBroadcastEndpointTotalRejectionReturnsError(t *testing.T) {
	hub := newManualHub(1)
	conn := &Conn{closeC: make(chan struct{})}
	hub.rooms["room"] = map[*Conn]struct{}{conn: {}}
	hub.jobQueue <- hubJob{conn: conn, op: OpcodeText, data: []byte("full")}

	comp := &Server{
		config: WebSocketConfig{
			Secret:            validSecret(),
			WSRoutePath:       "/ws",
			BroadcastPath:     "/_admin/broadcast",
			BroadcastEnabled:  true,
			BroadcastSecret:   validBroadcastSecret(),
			BroadcastMaxBytes: DefaultBroadcastMaxBytes,
		},
		hub: hub,
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast?room=room", strings.NewReader(`{"msg":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+string(comp.config.BroadcastSecret))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		var body websocketErrorResponse
		_ = json.NewDecoder(rec.Body).Decode(&body)
		t.Fatalf("expected 503, got %d; body: %v", rec.Code, body)
	}
}

func TestBroadcastDisabled(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+string(validBroadcastSecret()))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Should get 404 or 405 since route is not registered
	if rec.Code == http.StatusNoContent {
		t.Fatal("broadcast should be disabled")
	}
}

func TestRegisterRoutesIdempotent(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	// Calling RegisterRoutes twice should not panic (sync.Once)
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("first RegisterRoutes: %v", err)
	}
	if err := comp.RegisterRoutes(r); err == nil {
		t.Fatal("expected duplicate RegisterRoutes to return an error")
	}
}

func TestHealthBroadcastEnabledInDetails(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()

	comp, _ := New(cfg)
	_, status := comp.Health()

	val, ok := status.Details["broadcastEnabled"]
	if !ok {
		t.Fatal("expected broadcastEnabled in details")
	}
	if val != false {
		t.Fatal("expected broadcastEnabled=false")
	}
}

func TestBroadcastEndpointEmptyBody(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = validBroadcastSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+string(cfg.BroadcastSecret))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for empty body, got %d", rec.Code)
	}
}

func TestBroadcastEndpointOversizedBody(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = validBroadcastSecret()
	cfg.BroadcastMaxBytes = 4

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader("12345"))
	req.Header.Set("Authorization", "Bearer "+string(cfg.BroadcastSecret))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		var body websocketErrorResponse
		_ = json.NewDecoder(rec.Body).Decode(&body)
		t.Fatalf("expected 413, got %d; body: %v", rec.Code, body)
	}
}

func TestBroadcastAuthCaseInsensitive(t *testing.T) {
	cfg := DefaultWebSocketConfig()
	cfg.Secret = validSecret()
	cfg.BroadcastEnabled = true
	cfg.BroadcastSecret = validBroadcastSecret()

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := router.NewRouter()
	if err := comp.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}

	// lowercase "bearer" should also work
	req := httptest.NewRequest(http.MethodPost, "/_admin/broadcast", strings.NewReader("test"))
	req.Header.Set("Authorization", "bearer "+string(cfg.BroadcastSecret))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		var body websocketErrorResponse
		_ = json.NewDecoder(rec.Body).Decode(&body)
		t.Fatalf("expected 204, got %d; body: %v", rec.Code, body)
	}
}

func TestNewCustomConfig(t *testing.T) {
	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)
	roomValidator := func(room string) error {
		if room == "team/blue" {
			return nil
		}
		return ErrInvalidRoomName
	}
	cfg := WebSocketConfig{
		WorkerCount:           4,
		JobQueueSize:          128,
		SendQueueSize:         64,
		SendTimeout:           100 * time.Millisecond,
		SendBehavior:          SendDrop,
		Secret:                validSecret(),
		WSRoutePath:           "/custom-ws",
		BroadcastPath:         "/custom-broadcast",
		BroadcastEnabled:      true,
		BroadcastSecret:       validBroadcastSecret(),
		RoomNameValidator:     roomValidator,
		MaxRoomRegistrations:  100,
		MaxRoomConnections:    10,
		RejectOnQueueFull:     true,
		MaxConnectionRate:     25,
		EnableDebugLogging:    true,
		EnableSecurityMetrics: true,
		Logger:                logger,
	}

	comp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if comp.Hub() == nil {
		t.Fatal("expected hub")
	}
	if got := comp.Hub().config; got.WorkerCount != 4 ||
		got.JobQueueSize != 128 ||
		got.MaxRoomRegistrations != 100 ||
		got.MaxRoomConnections != 10 ||
		got.RoomNameValidator == nil ||
		!got.RejectOnQueueFull ||
		got.MaxConnectionRate != 25 ||
		!got.EnableDebugLogging ||
		!got.EnableSecurityMetrics ||
		got.Logger != logger {
		t.Fatalf("hub config not aligned with websocket config: %+v", got)
	}
	if comp.Hub().rateLimiter == nil {
		t.Fatal("expected rate limiter from top-level MaxConnectionRate")
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want mention of %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want mention of %q", err, want)
	}
}

func assertErrorIsOrContains(t *testing.T, err error, target error, wants ...string) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want %v or mention of %v", target, wants)
	}
	if errors.Is(err, target) {
		return
	}
	for _, want := range wants {
		if strings.Contains(err.Error(), want) {
			return
		}
	}
	t.Fatalf("error = %v, want %v or mention of %v", err, target, wants)
}
