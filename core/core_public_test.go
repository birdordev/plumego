package core_test

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/internal/testutil/nettest"
	"github.com/birdordev/plumego/router"
)

func TestPublicServeHTTPWorkflow(t *testing.T) {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})

	if err := app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Core-Test", "middleware")
			next.ServeHTTP(w, r)
		})
	}); err != nil {
		t.Fatalf("Use returned error: %v", err)
	}
	if err := app.Get("/hello", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("hello"))
	})); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("ServeHTTP status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if got := rec.Header().Get("X-Core-Test"); got != "middleware" {
		t.Fatalf("middleware header = %q, want middleware", got)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "hello" {
		t.Fatalf("response body = %q, want hello", got)
	}
	if got := app.PreparationState(); got != core.PreparationStateHandlerPrepared {
		t.Fatalf("preparation state = %q, want %q", got, core.PreparationStateHandlerPrepared)
	}
	if _, err := app.Server(); err == nil {
		t.Fatal("Server succeeded before explicit Prepare")
	}
}

func TestPublicPrepareServerShutdownWorkflow(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()

	cfg := core.DefaultConfig()
	cfg.Addr = ln.Addr().String()
	app := core.New(cfg, core.AppDependencies{})
	if err := app.Get("/ready", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got := app.PreparationState(); got != core.PreparationStateServerPrepared {
		t.Fatalf("preparation state = %q, want %q", got, core.PreparationStateServerPrepared)
	}
	srv, err := app.Server()
	if err != nil {
		t.Fatalf("Server returned error: %v", err)
	}
	if srv.Addr != cfg.Addr {
		t.Fatalf("server addr = %q, want %q", srv.Addr, cfg.Addr)
	}
	if srv.Handler == nil {
		t.Fatal("prepared server handler is nil")
	}

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ln)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	nettest.WaitForHTTPStatus(t, client, "http://"+ln.Addr().String()+"/ready", http.StatusNoContent)

	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare after shutdown returned error: %v", err)
	}
	closedServer, err := app.Server()
	if err != nil {
		t.Fatalf("Server after shutdown returned error: %v", err)
	}
	if closedServer != srv {
		t.Fatal("Server after shutdown returned a different server")
	}
	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatalf("second Shutdown returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ServeHTTP after shutdown status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestPublicRouteRegistrationErrors(t *testing.T) {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})

	err := app.AddRoute(http.MethodGet, "/nil", nil)
	if !errors.Is(err, contract.ErrHandlerNil) {
		t.Fatalf("AddRoute nil error = %v, want ErrHandlerNil", err)
	}

	if err := app.Get("/ok", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if err := app.Get("/after-prepare", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})); err == nil {
		t.Fatal("route registration succeeded after Prepare")
	}
}

func TestPublicErrorContractWrapsExportedCauses(t *testing.T) {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})

	err := app.AddRoute(http.MethodGet, "/nil", nil)
	if !errors.Is(err, contract.ErrHandlerNil) {
		t.Fatalf("AddRoute nil error = %v, want ErrHandlerNil cause", err)
	}
	if err == nil || !strings.Contains(err.Error(), "core add_route") {
		t.Fatalf("AddRoute nil error = %v, want core add_route operation context", err)
	}

	_, err = app.Server()
	if err == nil || !strings.Contains(err.Error(), "core get_server:") {
		t.Fatalf("Server before Prepare error = %v, want core get_server operation context", err)
	}
}

func TestPublicServeHTTPThenPrepareFailureKeepsHandlerPrepared(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Addr = " "
	app := core.New(cfg, core.AppDependencies{})

	if err := app.Get("/handler-only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/handler-only", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ServeHTTP status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := app.PreparationState(); got != core.PreparationStateHandlerPrepared {
		t.Fatalf("preparation state after ServeHTTP = %q, want %q", got, core.PreparationStateHandlerPrepared)
	}

	if err := app.Prepare(); err == nil || !strings.Contains(err.Error(), "server address cannot be empty") {
		t.Fatalf("Prepare error = %v, want invalid server address error", err)
	}
	if got := app.PreparationState(); got != core.PreparationStateHandlerPrepared {
		t.Fatalf("preparation state after failed Prepare = %q, want %q", got, core.PreparationStateHandlerPrepared)
	}
	if _, err := app.Server(); err == nil {
		t.Fatal("Server succeeded after failed Prepare")
	}
	if err := app.Get("/after-handler-prepare-failure", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})); err == nil {
		t.Fatal("route registration succeeded after handler preparation")
	}
}

func TestPublicAdvancedTLSPolicyIsCallerOwned(t *testing.T) {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	if err := app.Get("/tls-policy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if srv.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("caller-owned TLS MinVersion = %d, want %d", srv.TLSConfig.MinVersion, tls.VersionTLS12)
	}
}

func TestPublicPreparedServerCanServeTLS(t *testing.T) {
	certFile, keyFile := nettest.WriteSelfSignedTLSCertFiles(t, "plumego-public-test-cert-*.pem", "plumego-public-test-key-*.pem")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()

	cfg := core.DefaultConfig()
	cfg.Addr = ln.Addr().String()
	cfg.TLS = core.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile}
	app := core.New(cfg, core.AppDependencies{})
	if err := app.Get("/secure", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secure"))
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
	if srv.TLSConfig == nil || len(srv.TLSConfig.Certificates) != 1 {
		t.Fatal("prepared server did not load TLS certificate material")
	}

	done := make(chan error, 1)
	go func() {
		done <- srv.ServeTLS(ln, "", "")
	}()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 2 * time.Second,
	}
	nettest.WaitForHTTPStatus(t, client, "https://"+ln.Addr().String()+"/secure", http.StatusOK)

	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestPublicHTTP2DisabledInstallsTLSNextProtoOverride(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.HTTP2Enabled = false
	app := core.New(cfg, core.AppDependencies{})
	if err := app.Get("/http2-policy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if srv.TLSNextProto == nil {
		t.Fatal("TLSNextProto override is nil when HTTP2Enabled is false")
	}
	if len(srv.TLSNextProto) != 0 {
		t.Fatalf("TLSNextProto override length = %d, want 0", len(srv.TLSNextProto))
	}
}

func TestPublicRouterPolicyAndNamedRouteURL(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Router.MethodNotAllowed = true
	app := core.New(cfg, core.AppDependencies{})

	if err := app.AddRoute(http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), router.WithRouteName("users.show")); err != nil {
		t.Fatalf("AddRoute returned error: %v", err)
	}
	if got, err := app.URL("users.show", "id", "42"); err != nil || got != "/users/42" {
		t.Fatalf("URL returned %q, err = %v, want /users/42", got, err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/users/42", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method mismatch status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); !strings.Contains(got, http.MethodGet) {
		t.Fatalf("Allow header = %q, want GET", got)
	}
}
