package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spcent/plumego/contract"
)

func TestNewDefaults(t *testing.T) {
	app := New(DefaultConfig(), AppDependencies{})

	if app.config.Addr != ":8080" {
		t.Fatalf("default addr should be :8080, got %s", app.config.Addr)
	}
	if app.config.TLS.Enabled {
		t.Fatalf("TLS should be disabled by default")
	}
}

func TestNilAppEntrypointsPanic(t *testing.T) {
	var app *App
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	req := httptest.NewRequest(http.MethodGet, "/nil", nil)
	rec := httptest.NewRecorder()

	tests := []struct {
		name string
		call func()
	}{
		{name: "Logger", call: func() { _ = app.Logger() }},
		{name: "Routes", call: func() { _ = app.Routes() }},
		{name: "URL", call: func() { _, _ = app.URL("missing") }},
		{name: "PreparationState", call: func() { _ = app.PreparationState() }},
		{name: "Use", call: func() { _ = app.Use(func(next http.Handler) http.Handler { return next }) }},
		{name: "Get", call: func() { _ = app.Get("/nil", handler) }},
		{name: "AddRoute", call: func() { _ = app.AddRoute(http.MethodPost, "/nil", handler) }},
		{name: "Prepare", call: func() { _ = app.Prepare() }},
		{name: "Server", call: func() { _, _ = app.Server() }},
		{name: "Shutdown", call: func() { _ = app.Shutdown(nil) }},
		{name: "ServeHTTP", call: func() { app.ServeHTTP(rec, req) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanics(t, tt.call)
		})
	}
}

func assertPanics(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	call()
}

func TestCoreRouteErrorParamsAreDeterministicAndWrapped(t *testing.T) {
	app := newTestApp()

	err := app.AddRoute(http.MethodGet, "/nil", nil)
	if err == nil {
		t.Fatal("expected nil handler error")
	}
	if !errors.Is(err, contract.ErrHandlerNil) {
		t.Fatalf("expected error to wrap ErrHandlerNil, got %v", err)
	}

	assertCoreError(t, err, operationAddRoute, "method=GET", "path=/nil", "handler cannot be nil")
}

func TestZeroValueAppEntrypoints(t *testing.T) {
	var app App
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	if logger := app.Logger(); logger == nil {
		t.Fatal("expected zero-value app Logger to return discard logger")
	}
	assertCoreError(t, app.Use(func(next http.Handler) http.Handler { return next }), operationUseMiddleware, "cannot add middleware after app has been prepared")
	if err := app.Get("/zero", handler); err == nil ||
		!strings.Contains(err.Error(), "core "+operationAddRoute) ||
		!strings.Contains(err.Error(), "method=GET") ||
		!strings.Contains(err.Error(), "path=/zero") ||
		!strings.Contains(err.Error(), "cannot register route after app has been prepared") {
		t.Fatalf("expected zero-value app route error, got %v", err)
	}
	if err := app.Get("/zero-nil", nil); err == nil ||
		!strings.Contains(err.Error(), "core "+operationAddRoute) ||
		!strings.Contains(err.Error(), "method=GET") ||
		!strings.Contains(err.Error(), "path=/zero-nil") ||
		!strings.Contains(err.Error(), "cannot register route after app has been prepared") {
		t.Fatalf("expected zero-value app state error before nil handler validation, got %v", err)
	}
	assertCoreError(t, app.Prepare(), operationPrepareServer, "app config not configured")
	if app.preparationState != "" || app.handler != nil {
		t.Fatalf("expected zero-value prepare to avoid handler preparation side effects, state=%q handler=%T", app.preparationState, app.handler)
	}
	_, err := app.Server()
	assertCoreError(t, err, operationGetServer, "server not prepared")
	assertCoreError(t, app.Shutdown(nil), operationShutdownApp, "server not prepared")
}

func TestZeroValueAppServeHTTPWritesUnavailable(t *testing.T) {
	var app App
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/zero", nil)

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rec.Code)
	}
	var response struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Message != "handler not configured" {
		t.Fatalf("expected handler not configured message, got %q", response.Error.Message)
	}
}

func TestPreparationStateEntrypoint(t *testing.T) {
	var zero App
	if got := zero.PreparationState(); got != "" {
		t.Fatalf("zero app preparation state = %q, want empty", got)
	}

	app := newTestApp()
	if got := app.PreparationState(); got != PreparationStateMutable {
		t.Fatalf("new app preparation state = %q, want %q", got, PreparationStateMutable)
	}

	mustRegisterRoute(t, app.Get("/state", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/state", nil))
	if got := app.PreparationState(); got != PreparationStateHandlerPrepared {
		t.Fatalf("handler-prepared state = %q, want %q", got, PreparationStateHandlerPrepared)
	}

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got := app.PreparationState(); got != PreparationStateServerPrepared {
		t.Fatalf("server-prepared state = %q, want %q", got, PreparationStateServerPrepared)
	}
}

func TestNewAppliesTypedConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = ":9090"
	cfg.TLS = TLSConfig{Enabled: true, CertFile: "cert", KeyFile: "key"}

	app := New(cfg, AppDependencies{})

	if app.config.Addr != ":9090" {
		t.Fatalf("addr should be :9090, got %s", app.config.Addr)
	}
	if !app.config.TLS.Enabled || app.config.TLS.CertFile != "cert" || app.config.TLS.KeyFile != "key" {
		t.Fatalf("TLS config should be populated when enabled")
	}
}

func TestUseMiddlewareAppliedAfterSetup(t *testing.T) {
	app := newTestApp()

	mustRegisterRoute(t, app.Get("/router", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("router"))
	})))
	mustRegisterRoute(t, app.Get("/mux", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mux"))
	})))

	_ = app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test", "applied")
			next.ServeHTTP(w, r)
		})
	})

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	tests := []struct {
		path     string
		expected string
	}{
		{path: "/router", expected: "router"},
		{path: "/mux", expected: "mux"},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		rr := httptest.NewRecorder()

		app.ServeHTTP(rr, req)

		if rr.Header().Get("X-Test") != "applied" {
			t.Fatalf("middleware header missing for path %s", tt.path)
		}
		if !strings.Contains(rr.Body.String(), tt.expected) {
			t.Fatalf("expected body to contain %q for path %s, got %q", tt.expected, tt.path, rr.Body.String())
		}
	}
}

func TestUseMiddlewareRunsInRegistrationOrder(t *testing.T) {
	app := newTestApp()
	order := []string{}

	if err := app.Use(
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "first-before")
				next.ServeHTTP(w, r)
				order = append(order, "first-after")
			})
		},
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "second-before")
				next.ServeHTTP(w, r)
				order = append(order, "second-after")
			})
		},
	); err != nil {
		t.Fatalf("Use returned error: %v", err)
	}

	mustRegisterRoute(t, app.Get("/ordered", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusNoContent)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ordered", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}
	got := strings.Join(order, ",")
	want := "first-before,second-before,handler,second-after,first-after"
	if got != want {
		t.Fatalf("middleware order = %q, want %q", got, want)
	}
}

func TestUseWithNoMiddlewareIsNoop(t *testing.T) {
	app := newTestApp()

	if err := app.Use(); err != nil {
		t.Fatalf("Use with no middleware returned error: %v", err)
	}
	if got := app.middlewareChain.Len(); got != 0 {
		t.Fatalf("middleware chain length = %d, want 0", got)
	}
	if got := app.PreparationState(); got != PreparationStateMutable {
		t.Fatalf("preparation state = %q, want %q", got, PreparationStateMutable)
	}

	mustRegisterRoute(t, app.Get("/noop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error after empty Use: %v", err)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/noop", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status after empty Use = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestServeHTTPLazilyBuildsHandler(t *testing.T) {
	app := newTestApp()

	_ = app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Lazy", "true")
			next.ServeHTTP(w, r)
		})
	})

	mustRegisterRoute(t, app.Get("/hello", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hi"))
	})))

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	rr := httptest.NewRecorder()

	app.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if rr.Header().Get("X-Lazy") != "true" {
		t.Fatalf("expected middleware header to be set")
	}
}

func TestUseAfterServeHTTPReturnsError(t *testing.T) {
	app := newTestApp()

	mustRegisterRoute(t, app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)

	if err := app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}); err == nil {
		t.Fatalf("expected error when adding middleware after handler is built")
	}
}

func TestUseRejectsNilMiddlewareWithoutMutatingChain(t *testing.T) {
	app := newTestApp()
	valid := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Valid", "true")
			next.ServeHTTP(w, r)
		})
	}

	if err := app.Use(nil, valid); err == nil {
		t.Fatal("expected nil middleware registration error")
	}
	if got := app.middlewareChain.Len(); got != 0 {
		t.Fatalf("expected chain to stay empty after rejected registration, got %d", got)
	}

	if err := app.Use(valid); err != nil {
		t.Fatalf("expected valid middleware registration to succeed, got %v", err)
	}
	mustRegisterRoute(t, app.Get("/middleware", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/middleware", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rr.Code)
	}
	if rr.Header().Get("X-Valid") != "true" {
		t.Fatal("expected valid middleware to run after nil registration was rejected")
	}
}

func TestPrepareBuildsHTTPServer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = ":5555"
	app := New(cfg, AppDependencies{})
	mustRegisterRoute(t, app.Get("/ready", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	server, err := app.Server()
	if err != nil {
		t.Fatalf("Server returned error: %v", err)
	}
	if server.Addr != ":5555" {
		t.Fatalf("httpServer addr should be :5555, got %s", server.Addr)
	}
	if server.Handler == nil {
		t.Fatalf("httpServer handler should not be nil")
	}
	if app.preparationState != PreparationStateServerPrepared {
		t.Fatalf("preparation_state = %q, want %q", app.preparationState, PreparationStateServerPrepared)
	}
}

func TestServeHTTPOnlyPreparesHandler(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = ":5555"
	app := New(cfg, AppDependencies{})

	_ = app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Prepared", "true")
			next.ServeHTTP(w, r)
		})
	})
	mustRegisterRoute(t, app.Get("/prepared", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/prepared", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Prepared") != "true" {
		t.Fatalf("expected prepared middleware to run")
	}

	if app.preparationState != PreparationStateHandlerPrepared {
		t.Fatalf("preparation_state = %q, want %q", app.preparationState, PreparationStateHandlerPrepared)
	}
	if _, err := app.Server(); err == nil {
		t.Fatalf("expected Server to fail before Prepare")
	}

	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error after ServeHTTP: %v", err)
	}
	server, err := app.Server()
	if err != nil {
		t.Fatalf("Server returned error after Prepare: %v", err)
	}
	if server.Addr != ":5555" {
		t.Fatalf("httpServer addr should be :5555, got %s", server.Addr)
	}
}

func TestServeHTTPSkipsServerConfigValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = ""
	app := New(cfg, AppDependencies{})

	mustRegisterRoute(t, app.Get("/handler-only", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/handler-only", nil)
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", rec.Code)
	}
	if app.preparationState != PreparationStateHandlerPrepared {
		t.Fatalf("preparation_state = %q, want %q", app.preparationState, PreparationStateHandlerPrepared)
	}
	if err := app.Get("/after-handler", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})); err == nil ||
		!strings.Contains(err.Error(), "cannot register route after app has been prepared") {
		t.Fatalf("expected route registration to be frozen after ServeHTTP, got %v", err)
	}

	assertCoreError(t, app.Prepare(), operationPrepareServer, "server address cannot be empty")
	if _, err := app.Server(); err == nil {
		t.Fatal("expected Server to fail after rejected Prepare")
	}
}

func TestUseAfterPreparedReturnsError(t *testing.T) {
	app := newTestApp()
	mustRegisterRoute(t, app.Get("/prepared", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	if err := app.Prepare(); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	err := app.Use(func(next http.Handler) http.Handler { return next })
	if err == nil {
		t.Fatalf("expected error when adding middleware after start")
	}
}

func TestConcurrentUseAndPrepareDoesNotRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		app := newTestApp()
		mustRegisterRoute(t, app.Get("/raced", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})))

		start := make(chan struct{})
		var wg sync.WaitGroup
		var useErr, prepareErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			useErr = app.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Raced", "true")
					next.ServeHTTP(w, r)
				})
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			prepareErr = app.Prepare()
		}()

		close(start)
		wg.Wait()

		if prepareErr != nil {
			t.Fatalf("Prepare returned error: %v", prepareErr)
		}
		if useErr != nil && !strings.Contains(useErr.Error(), "cannot add middleware after app has been prepared") {
			t.Fatalf("unexpected Use error: %v", useErr)
		}

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raced", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected status 204, got %d", rec.Code)
		}
	}
}
