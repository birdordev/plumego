package core_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/internal/testutil/nettest"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
)

func TestPrepareRejectsNilFromIntermediateBuilder(t *testing.T) {
	t.Run("nil interface", func(t *testing.T) { testPrepareRejectsBuilderResult(t, nil) })
	t.Run("nil HandlerFunc", func(t *testing.T) { testPrepareRejectsBuilderResult(t, http.HandlerFunc(nil)) })
}

func testPrepareRejectsBuilderResult(t *testing.T, invalid http.Handler) {
	t.Helper()
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	var outerCalls, innerCalls int
	if err := app.Use(func(next http.Handler) http.Handler {
		outerCalls++
		return http.HandlerFunc(next.ServeHTTP)
	}, func(next http.Handler) http.Handler {
		innerCalls++
		if innerCalls == 1 {
			return invalid
		}
		return next
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err == nil || outerCalls != 0 || app.PreparationState() != core.PreparationStateMutable {
		t.Fatalf("prepare error=%v outer calls=%d phase=%s", err, outerCalls, app.PreparationState())
	}
	if _, err := app.Server(); err == nil {
		t.Fatal("failed preparation published a server")
	}
	if err := app.Get("/late", http.NotFoundHandler()); err == nil {
		t.Fatal("registration reopened after failed assembly")
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	dst := httptest.NewRecorder()
	app.ServeHTTP(dst, httptest.NewRequest(http.MethodGet, "/", nil))
	if outerCalls != 1 || innerCalls != 2 || dst.Code != http.StatusNoContent {
		t.Fatalf("outer calls=%d inner calls=%d status=%d", outerCalls, innerCalls, dst.Code)
	}
}

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
	if err := app.Get("/typed-nil", http.HandlerFunc(nil)); err == nil {
		t.Fatal("Get accepted a nil HandlerFunc")
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

func TestPublicPreparationRetriesMiddlewarePanic(t *testing.T) {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if err := app.Get("/", handler); err != nil {
		t.Fatal(err)
	}
	var builds atomic.Int32
	if err := app.Use(func(next http.Handler) http.Handler {
		if builds.Add(1) == 1 {
			panic("first build failed")
		}
		return next
	}); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if got := recover(); got != "first build failed" {
				t.Fatalf("panic = %v", got)
			}
		}()
		_ = app.Prepare()
	}()
	if state := app.PreparationState(); state != core.PreparationStateMutable {
		t.Fatalf("state after failed build = %q", state)
	}
	if _, err := app.Server(); err == nil {
		t.Fatal("server published after failed build")
	}
	if err := app.Get("/late", handler); err == nil {
		t.Fatal("accepted route after registration froze")
	}
	if err := app.Use(func(next http.Handler) http.Handler { return next }); err == nil {
		t.Fatal("accepted middleware after registration froze")
	}

	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range 12 {
		workers.Go(func() {
			<-start
			if i%2 == 0 {
				if err := app.Prepare(); err != nil && !strings.Contains(err.Error(), "preparation is already in progress") {
					t.Error(err)
				}
				return
			}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusNoContent && rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d", rec.Code)
			}
		})
	}
	close(start)
	workers.Wait()
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	if state := app.PreparationState(); state != core.PreparationStateServerPrepared || builds.Load() != 2 {
		t.Fatalf("state = %q, builds = %d", state, builds.Load())
	}
}

func TestPublicMiddlewareBuilderCanReenterPreparation(t *testing.T) {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	if err := app.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	finish := make(chan struct{})
	type attempt struct {
		err    error
		status int
	}
	nested := make(chan attempt, 1)
	if err := app.Use(func(next http.Handler) http.Handler {
		close(started)
		err := app.Prepare()
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		nested <- attempt{err: err, status: rec.Code}
		<-finish
		return next
	}); err != nil {
		t.Fatal(err)
	}

	prepared := make(chan error, 1)
	go func() { prepared <- app.Prepare() }()
	<-started
	defer close(finish)
	got := <-nested
	if got.err == nil || !strings.Contains(got.err.Error(), "preparation is already in progress") || got.status != http.StatusServiceUnavailable {
		t.Errorf("nested Prepare = %v, nested ServeHTTP status = %d", got.err, got.status)
	}
	if err := app.Prepare(); err == nil || !strings.Contains(err.Error(), "preparation is already in progress") {
		t.Errorf("concurrent Prepare = %v, want in-progress error", err)
	}
	finish <- struct{}{}
	if err := <-prepared; err != nil {
		t.Fatal(err)
	}
	server, err := app.Server()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	if got, err := app.Server(); err != nil || got != server {
		t.Fatalf("Server = %p, %v; want %p", got, err, server)
	}
}

func TestPublicPrepareRetriesAfterTLSFilesAppear(t *testing.T) {
	certFile, keyFile := nettest.WriteSelfSignedTLSCertFiles(t, "test-cert-*.pem", "test-key-*.pem")
	cert, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(certFile); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultConfig()
	cfg.TLS = core.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile}
	app := core.New(cfg, core.AppDependencies{})
	if err := app.Prepare(); !errors.Is(err, os.ErrNotExist) || app.PreparationState() != core.PreparationStateMutable {
		t.Fatalf("Prepare() = %v, state = %s", err, app.PreparationState())
	}
	if err := app.Get("/", http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	first, err := app.Server()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	if again, err := app.Server(); err != nil || first != again {
		t.Fatalf("Server() = %p, %v, want %p", again, err, first)
	}
	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPrepareRetriesAfterBuilderGoexit(t *testing.T) {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	if err := app.Get("/", http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
	builds := 0
	if err := app.Use(func(next http.Handler) http.Handler {
		builds++
		if builds == 1 {
			runtime.Goexit()
		}
		return next
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = app.Prepare()
		t.Error("Prepare returned after builder ended the goroutine")
	}()
	<-done
	if app.PreparationState() != core.PreparationStateMutable {
		t.Fatalf("published partial state %q", app.PreparationState())
	}
	if err := app.Get("/late", http.NotFoundHandler()); err == nil {
		t.Fatal("registration reopened")
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	if builds != 2 || app.PreparationState() != core.PreparationStateServerPrepared {
		t.Fatalf("builds=%d state=%q", builds, app.PreparationState())
	}
}

func TestConcurrentTLSPreparationPublishesOneServer(t *testing.T) {
	cert, key := nettest.WriteSelfSignedTLSCertFiles(t, "test-cert-*.pem", "test-key-*.pem")
	cfg := core.DefaultConfig()
	cfg.TLS = core.TLSConfig{Enabled: true, CertFile: cert, KeyFile: key}
	app := core.New(cfg, core.AppDependencies{})
	first := prepareConcurrentTLS(t, app, 4)
	if len(first.TLSConfig.Certificates) != 1 {
		t.Fatal("expected one loaded certificate")
	}
}

func BenchmarkConcurrentTLSPrepare(b *testing.B) {
	cert, key := nettest.WriteSelfSignedTLSCertFiles(b, "bench-cert-*.pem", "bench-key-*.pem")
	cfg := core.DefaultConfig()
	cfg.TLS = core.TLSConfig{Enabled: true, CertFile: cert, KeyFile: key}
	for _, workers := range []int{1, 4} {
		b.Run(fmt.Sprint(workers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				app := core.New(cfg, core.AppDependencies{})
				prepareConcurrentTLS(b, app, workers)
			}
		})
	}
}

func prepareConcurrentTLS(t testing.TB, app *core.App, workers int) *http.Server {
	t.Helper()
	start := make(chan struct{})
	type outcome struct {
		server *http.Server
		err    error
	}
	results := make(chan outcome, workers)
	for range workers {
		go func() {
			<-start
			if err := app.Prepare(); err != nil {
				results <- outcome{err: err}
				return
			}
			server, err := app.Server()
			results <- outcome{server: server, err: err}
		}()
	}
	close(start)
	first := <-results
	if first.err != nil || first.server == nil {
		t.Fatalf("first Prepare error=%v", first.err)
	}
	for range workers - 1 {
		if got := <-results; got.err != nil || got.server != first.server {
			t.Fatalf("published=%p first=%p error=%v", got.server, first.server, got.err)
		}
	}
	return first.server
}

func TestTLSPrepareRetriesAfterDirectHandlerPublication(t *testing.T) {
	cert, key := nettest.WriteSelfSignedTLSCertFiles(t, "cert-*.pem", "key-*.pem")
	cfg := core.DefaultConfig()
	cfg.TLS = core.TLSConfig{Enabled: true, CertFile: cert + ".pending", KeyFile: key}
	app := core.New(cfg, core.AppDependencies{})
	if err := app.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	checkHandler := func() {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("direct handler status=%d", w.Code)
		}
	}
	checkHandler()
	if err := app.Prepare(); !errors.Is(err, os.ErrNotExist) || app.PreparationState() != core.PreparationStateHandlerPrepared {
		t.Fatalf("Prepare=%v, state=%s", err, app.PreparationState())
	}
	checkHandler()
	if err := os.Rename(cert, cfg.TLS.CertFile); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(cfg.TLS.CertFile)
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	first, err := app.Server()
	if err != nil || first == nil {
		t.Fatalf("Server=%p: %v", first, err)
	}
	if again, err := app.Server(); err != nil || again != first {
		t.Fatalf("retry=%p, want %p: %v", again, first, err)
	}
}

func TestShutdownDuringHandlerConstructionAllowsPreparation(t *testing.T) {
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	if err := app.Get("/", http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	if err := app.Use(func(next http.Handler) http.Handler {
		close(entered)
		<-resume
		return next
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- app.Prepare() }()
	<-entered
	pendingServer, pendingServerErr := app.Server()
	pendingPrepareErr := app.Prepare()
	err := app.Shutdown(t.Context())
	close(resume)
	buildErr := <-done
	if pendingServer != nil || pendingServerErr == nil || pendingPrepareErr == nil {
		t.Fatalf("pending Server=%v err=%v Prepare=%v", pendingServer, pendingServerErr, pendingPrepareErr)
	}
	if err == nil || !strings.Contains(err.Error(), "server not prepared") || buildErr != nil {
		t.Fatalf("Shutdown=%v Prepare=%v", err, buildErr)
	}
	if srv, err := app.Server(); err != nil || srv == nil {
		t.Fatalf("Server=%v err=%v", srv, err)
	}
	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidTLSConfigKeepsDirectHandlerSeparateFromServer(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.TLS.Enabled = true
	app := core.New(cfg, core.AppDependencies{})
	if err := app.Prepare(); err == nil || app.PreparationState() != core.PreparationStateMutable {
		t.Fatalf("prepare=%v state=%s", err, app.PreparationState())
	}
	if err := app.Get("/fixture", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/fixture", nil))
		if rec.Code != http.StatusNoContent || app.PreparationState() != core.PreparationStateHandlerPrepared {
			t.Fatalf("direct status=%d state=%s", rec.Code, app.PreparationState())
		}
		if err := app.Prepare(); err == nil || !strings.Contains(err.Error(), "certificate or key") {
			t.Fatalf("prepare=%v", err)
		}
		if server, err := app.Server(); server != nil || err == nil {
			t.Fatalf("server=%v err=%v", server, err)
		}
		if err := app.Shutdown(t.Context()); err == nil {
			t.Fatal("shutdown succeeded without a server")
		}
	}
}

func TestDefaultConfigAndExplicitLoggersStayInstanceLocal(t *testing.T) {
	var firstOutput, secondOutput bytes.Buffer
	cfg, defaultCfg := core.DefaultConfig(), core.DefaultConfig()
	cfg.ReadTimeout = time.Second
	first := core.New(cfg, core.AppDependencies{Logger: log.NewLogger(log.LoggerConfig{Output: &firstOutput})})
	second := core.New(defaultCfg, core.AppDependencies{Logger: log.NewLogger(log.LoggerConfig{Output: &secondOutput})})
	for _, app := range []*core.App{first, second} {
		if err := app.Prepare(); err != nil {
			t.Fatal(err)
		}
	}
	firstServer, err := first.Server()
	if err != nil {
		t.Fatal(err)
	}
	secondServer, err := second.Server()
	if err != nil {
		t.Fatal(err)
	}
	if firstServer.ReadTimeout != time.Second || secondServer.ReadTimeout != core.DefaultConfig().ReadTimeout ||
		firstOutput.Len() == 0 || secondOutput.Len() == 0 {
		t.Fatalf("first=%s second=%s", firstServer.ReadTimeout, secondServer.ReadTimeout)
	}
	first.Logger().Info("first instance fixture")
	if !strings.Contains(firstOutput.String(), "first instance fixture") || strings.Contains(secondOutput.String(), "first instance fixture") {
		t.Fatal("logger output crossed instances")
	}
}

type warningBarrierLogger struct {
	log.StructuredLogger
	entered, release chan struct{}
	calls            atomic.Int32
}

func (l *warningBarrierLogger) Warn(string, ...log.Fields) {
	if l.calls.Add(1) == 1 {
		close(l.entered)
		<-l.release
	}
}
func TestEmptyAppWarningBelongsToFirstPublisher(t *testing.T) {
	logger := &warningBarrierLogger{StructuredLogger: log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard}), entered: make(chan struct{}), release: make(chan struct{})}
	finish := sync.OnceFunc(func() { close(logger.release) })
	defer finish()
	a := core.New(core.DefaultConfig(), core.AppDependencies{Logger: logger})
	first := make(chan error, 1)
	go func() { first <- a.Prepare() }()
	select {
	case <-logger.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("warning did not start")
	}
	rec := httptest.NewRecorder()
	done := make(chan error, 1)
	go func() { a.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil)); done <- a.Prepare() }()
	select {
	case err := <-done:
		if err != nil || rec.Code != 404 || a.PreparationState() != core.PreparationStateServerPrepared {
			t.Fatalf("status=%d state=%v err=%v", rec.Code, a.PreparationState(), err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first warning blocked independent caller")
	}
	finish()
	if err := <-first; err != nil || logger.calls.Load() != 1 {
		t.Fatalf("prepare=%v warnings=%d", err, logger.calls.Load())
	}
}

func TestPreparedServerPreservesMixedExplicitTimeoutFields(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.ReadTimeout = 0
	cfg.ReadHeaderTimeout = 0
	cfg.IdleTimeout = 0
	cfg.WriteTimeout = 3 * time.Second
	for _, config := range []core.AppConfig{cfg, core.DefaultConfig()} {
		app := core.New(config, core.AppDependencies{})
		if err := app.Prepare(); err != nil {
			t.Fatal(err)
		}
		server, err := app.Server()
		if err != nil {
			t.Fatal(err)
		}
		if server.ReadTimeout != config.ReadTimeout || server.ReadHeaderTimeout != config.ReadHeaderTimeout ||
			server.IdleTimeout != config.IdleTimeout || server.WriteTimeout != config.WriteTimeout {
			t.Fatalf("prepared timeouts=%v/%v/%v/%v", server.ReadTimeout, server.ReadHeaderTimeout, server.IdleTimeout, server.WriteTimeout)
		}
	}
}

func TestPreparedServerPreservesRequestContext(t *testing.T) {
	type key struct{}
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	if err := app.Get("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(key{}) != "fixture" || !errors.Is(r.Context().Err(), context.Canceled) {
			t.Errorf("received context=%v value=%v", r.Context().Err(), r.Context().Value(key{}))
		}
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	server, err := app.Server()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "fixture"))
	cancel()
	for _, h := range []http.Handler{server.Handler, app} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status=%d", rec.Code)
		}
	}
}
