package plumego_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego"
	"github.com/birdordev/plumego/core"
)

// TestNewReturnsWorkingApp verifies the minimal quick-start path: New()
// returns a *core.App that can register routes and serve requests.
func TestNewReturnsWorkingApp(t *testing.T) {
	app := plumego.New()
	if app == nil {
		t.Fatal("New() returned nil")
	}
	if err := app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ServeHTTP status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestNewImplementsHTTPHandler confirms New() satisfies http.Handler, which is
// the contract shown in the README quick start (http.ListenAndServe(":8080", app)).
func TestNewImplementsHTTPHandler(t *testing.T) {
	var _ http.Handler = plumego.New()
}

// TestNewWithConfigAppliesConfig verifies that NewWithConfig propagates the
// caller-supplied config into the prepared http.Server.
func TestNewWithConfigAppliesConfig(t *testing.T) {
	cfg := plumego.DefaultConfig()
	cfg.Addr = ":9191"
	cfg.ReadTimeout = 7 * time.Second
	cfg.HTTP2Enabled = false

	app := plumego.NewWithConfig(cfg)
	if app == nil {
		t.Fatal("NewWithConfig returned nil")
	}
	if err := app.Get("/ready", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	srv, err := app.Server()
	if err != nil {
		t.Fatalf("Server returned error: %v", err)
	}
	if srv.Addr != ":9191" {
		t.Errorf("server Addr = %q, want :9191", srv.Addr)
	}
	if srv.ReadTimeout != 7*time.Second {
		t.Errorf("server ReadTimeout = %v, want 7s", srv.ReadTimeout)
	}
	if srv.TLSNextProto == nil {
		t.Error("TLSNextProto override is nil when HTTP2Enabled is false")
	}
}

// TestNewWithConfigIsIndependentFromCaller verifies that mutating the caller's
// cfg after passing it to NewWithConfig does not affect the prepared server,
// matching the same value-copy contract as core.New.
func TestNewWithConfigIsIndependentFromCaller(t *testing.T) {
	cfg := plumego.DefaultConfig()
	cfg.Addr = ":9292"

	app := plumego.NewWithConfig(cfg)
	cfg.Addr = ":9999" // mutate after construction

	if err := app.Get("/ready", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	srv, err := app.Server()
	if err != nil {
		t.Fatalf("Server returned error: %v", err)
	}
	if srv.Addr != ":9292" {
		t.Errorf("server Addr = %q, want :9292 (post-construction mutation should not affect prepared server)", srv.Addr)
	}
}

// TestDefaultConfigMatchesCoreDefaultConfig verifies the root-package shim
// returns the same canonical baseline as core.DefaultConfig.
func TestDefaultConfigMatchesCoreDefaultConfig(t *testing.T) {
	got := plumego.DefaultConfig()
	want := core.DefaultConfig()

	if got.Addr != want.Addr {
		t.Errorf("Addr = %q, want %q", got.Addr, want.Addr)
	}
	if got.ReadTimeout != want.ReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", got.ReadTimeout, want.ReadTimeout)
	}
	if got.ReadHeaderTimeout != want.ReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", got.ReadHeaderTimeout, want.ReadHeaderTimeout)
	}
	if got.WriteTimeout != want.WriteTimeout {
		t.Errorf("WriteTimeout = %v, want %v", got.WriteTimeout, want.WriteTimeout)
	}
	if got.IdleTimeout != want.IdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", got.IdleTimeout, want.IdleTimeout)
	}
	if got.MaxHeaderBytes != want.MaxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", got.MaxHeaderBytes, want.MaxHeaderBytes)
	}
	if got.HTTP2Enabled != want.HTTP2Enabled {
		t.Errorf("HTTP2Enabled = %v, want %v", got.HTTP2Enabled, want.HTTP2Enabled)
	}
	if got.TLS.Enabled != want.TLS.Enabled {
		t.Errorf("TLS.Enabled = %v, want %v", got.TLS.Enabled, want.TLS.Enabled)
	}
	if got.Router.MethodNotAllowed != want.Router.MethodNotAllowed {
		t.Errorf("Router.MethodNotAllowed = %v, want %v", got.Router.MethodNotAllowed, want.Router.MethodNotAllowed)
	}
}

// TestNewUsesDefaultAddr checks that New() creates an app that uses the
// canonical default address after Prepare, without requiring explicit config.
func TestNewUsesDefaultAddr(t *testing.T) {
	app := plumego.New()
	if err := app.Get("/addr", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	srv, err := app.Server()
	if err != nil {
		t.Fatalf("Server returned error: %v", err)
	}
	if srv.Addr != ":8080" {
		t.Errorf("default server Addr = %q, want :8080", srv.Addr)
	}
}
