package pubsubdebug_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/messaging/pubsub"
	"github.com/birdordev/plumego/x/observability/devtools/pubsubdebug"
)

// stubRegistrar captures registered routes for inspection.
type stubRegistrar struct {
	method  string
	path    string
	handler http.Handler
}

func (s *stubRegistrar) AddRoute(method, path string, handler http.Handler, _ ...router.RouteOption) error {
	s.method = method
	s.path = path
	s.handler = handler
	return nil
}

type hookRegistrar struct {
	method  string
	path    string
	handler http.Handler
}

func (s *hookRegistrar) RegisterRoute(method, path string, handler http.Handler) error {
	s.method = method
	s.path = path
	s.handler = handler
	return nil
}

type brokerWithoutSnapshot struct{}

func (brokerWithoutSnapshot) Publish(string, pubsub.Message) error { return nil }

func (brokerWithoutSnapshot) Subscribe(context.Context, string, pubsub.SubOptions) (pubsub.Subscription, error) {
	return nil, errors.New("not implemented")
}

func (brokerWithoutSnapshot) Close() error { return nil }

func TestNew_ReturnsHandler(t *testing.T) {
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: true}, nil)
	if h == nil {
		t.Fatal("New returned nil")
	}
}

func TestRegisterRoutes_Disabled(t *testing.T) {
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: false}, nil)
	reg := &stubRegistrar{}
	if err := h.RegisterRoutes(reg); err != nil {
		t.Fatalf("RegisterRoutes error: %v", err)
	}
	if reg.handler != nil {
		t.Error("expected no route registered when disabled")
	}
}

func TestRegisterRoutes_DefaultPath(t *testing.T) {
	broker := pubsub.New()
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: true, Pub: broker}, nil)
	reg := &stubRegistrar{}
	if err := h.RegisterRoutes(reg); err != nil {
		t.Fatalf("RegisterRoutes error: %v", err)
	}
	if reg.path != "/_debug/pubsub" {
		t.Errorf("expected default path /_debug/pubsub, got %q", reg.path)
	}
}

func TestRegisterRoutes_CustomPath(t *testing.T) {
	broker := pubsub.New()
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: true, Path: "/custom/debug", Pub: broker}, nil)
	reg := &stubRegistrar{}
	if err := h.RegisterRoutes(reg); err != nil {
		t.Fatalf("RegisterRoutes error: %v", err)
	}
	if reg.path != "/custom/debug" {
		t.Errorf("expected /custom/debug, got %q", reg.path)
	}
}

func TestHandler_Snapshot_WithBroker(t *testing.T) {
	broker := pubsub.New()
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: true, Pub: broker}, nil)
	reg := &stubRegistrar{}
	if err := h.RegisterRoutes(reg); err != nil {
		t.Fatalf("RegisterRoutes error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/_debug/pubsub", nil)
	w := httptest.NewRecorder()
	reg.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_NilPub_ReturnsServiceUnavailable(t *testing.T) {
	// No pub in config and no fallback; the debug endpoint is unavailable.
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: true, Pub: nil}, nil)
	reg := &stubRegistrar{}
	if err := h.RegisterRoutes(reg); err != nil {
		t.Fatalf("RegisterRoutes error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/_debug/pubsub", nil)
	w := httptest.NewRecorder()
	reg.handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d: %s", w.Code, w.Body.String())
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
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Error.Code != contract.CodeUnavailable {
		t.Fatalf("expected code %s, got %s", contract.CodeUnavailable, resp.Error.Code)
	}
}

func TestHandler_UnsupportedBroker_ReturnsStableCode(t *testing.T) {
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: true, Pub: brokerWithoutSnapshot{}}, nil)
	reg := &stubRegistrar{}
	if err := h.RegisterRoutes(reg); err != nil {
		t.Fatalf("RegisterRoutes error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/_debug/pubsub", nil)
	w := httptest.NewRecorder()
	reg.handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
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
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Error.Code != contract.CodeNotImplemented {
		t.Fatalf("expected code %s, got %s", contract.CodeNotImplemented, resp.Error.Code)
	}
}

func TestConfigure_UnsupportedBroker_ReturnsStableCode(t *testing.T) {
	reg := &hookRegistrar{}
	pubsubdebug.Configure(pubsubdebug.Hooks{
		EnsureMutable: func(_, _ string) error { return nil },
		ConfigSnapshot: func() pubsubdebug.PubSubConfig {
			return pubsubdebug.PubSubConfig{Enabled: true, Pub: brokerWithoutSnapshot{}}
		},
		RegisterRoute: reg.RegisterRoute,
	})

	if reg.handler == nil {
		t.Fatal("expected route to be registered")
	}

	req := httptest.NewRequest(http.MethodGet, "/_debug/pubsub", nil)
	w := httptest.NewRecorder()
	reg.handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
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
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Error.Code != contract.CodeNotImplemented {
		t.Fatalf("expected code %s, got %s", contract.CodeNotImplemented, resp.Error.Code)
	}
}

func TestHealth_Enabled(t *testing.T) {
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: true}, nil)
	name, status := h.Health()
	if name != "pubsub_debug" {
		t.Errorf("unexpected health name: %q", name)
	}
	if status.Status != health.StatusHealthy {
		t.Errorf("expected healthy, got %v", status.Status)
	}
}

func TestHealth_Disabled(t *testing.T) {
	h := pubsubdebug.New(pubsubdebug.PubSubConfig{Enabled: false}, nil)
	_, status := h.Health()
	if status.Status != health.StatusDegraded {
		t.Errorf("expected degraded when disabled, got %v", status.Status)
	}
}
