package devtools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/health"
	"github.com/birdordev/plumego/router"
)

func TestNewComponentDefaults(t *testing.T) {
	c := New(Options{})
	if c == nil {
		t.Fatal("expected non-nil component")
	}
	if c.logger == nil {
		t.Fatal("expected default logger")
	}
	if c.devMetrics == nil {
		t.Fatal("expected devMetrics to be initialized")
	}
	if c.debug {
		t.Fatal("expected debug false by default")
	}
}

func TestNewComponentWithDebug(t *testing.T) {
	c := New(Options{Debug: true})
	if !c.debug {
		t.Fatal("expected debug true")
	}
}

func TestHealthDebugEnabled(t *testing.T) {
	c := New(Options{Debug: true})
	name, status := c.Health()
	if name != "devtools" {
		t.Fatalf("expected name devtools, got %q", name)
	}
	if status.Status != health.StatusHealthy {
		t.Fatalf("expected healthy, got %s", status.Status)
	}
	if status.Details == nil {
		t.Fatal("expected details")
	}
	if val, ok := status.Details["enabled"]; !ok || val != true {
		t.Fatalf("expected enabled=true in details, got %v", status.Details)
	}
}

func TestHealthDebugDisabled(t *testing.T) {
	c := New(Options{Debug: false})
	name, status := c.Health()
	if name != "devtools" {
		t.Fatalf("expected name devtools, got %q", name)
	}
	if status.Status != health.StatusDegraded {
		t.Fatalf("expected degraded, got %s", status.Status)
	}
	if status.Message == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestRegisterRoutesDebugFalse(t *testing.T) {
	c := New(Options{Debug: false})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	// Debug routes should not be registered
	req := httptest.NewRequest(http.MethodGet, DevToolsRoutesPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Should get 404 since routes weren't registered
	if rec.Code == http.StatusOK {
		t.Fatal("debug routes should not be registered when debug=false")
	}
}

func TestRegisterRoutesDebugTrue(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, DevToolsRoutesPath},
		{http.MethodGet, DevToolsRoutesJSONPath},
		{http.MethodGet, DevToolsMiddlewarePath},
		{http.MethodGet, DevToolsConfigPath},
		{http.MethodGet, DevToolsInfoPath},
		{http.MethodGet, DevToolsMetricsPath},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Errorf("expected route %s %s to be registered, got 404", tt.method, tt.path)
		}
	}
}

func TestLegacyTopLevelDebugAliasesRemoved(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	for _, path := range []string{
		"/_" + "routes",
		"/_" + "config",
		"/_" + "info",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected legacy alias %s to be removed, got status %d", path, rec.Code)
		}
	}
}

func TestRoutesJSONEndpoint(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsRoutesJSONPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	data := decodeDevToolsData[routesResponse](t, rec)
	if data.Routes == nil {
		t.Fatal("expected routes in response")
	}
}

func TestConfigEndpointNoHook(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsConfigPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	data := decodeDevToolsData[ConfigSnapshot](t, rec)
	if !data.Debug {
		t.Fatalf("expected debug=true in config snapshot, got %v", data)
	}
}

func TestConfigEndpointWithHook(t *testing.T) {
	c := New(Options{
		Debug:   true,
		EnvFile: ".env.test",
		Hooks: Hooks{
			RuntimeSnapshot: func() RuntimeSnapshot {
				return RuntimeSnapshot{
					Addr:             ":9090",
					DrainInterval:    250 * time.Millisecond,
					PreparationState: core.PreparationStateServerPrepared,
				}
			},
		},
	})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsConfigPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	data := decodeDevToolsData[ConfigSnapshot](t, rec)
	if data.Addr != ":9090" {
		t.Fatalf("expected addr=:9090, got %v", data.Addr)
	}
	if data.EnvFile != ".env.test" {
		t.Fatalf("expected env_file=.env.test, got %v", data.EnvFile)
	}
	if data.PreparationState != core.PreparationStateServerPrepared {
		t.Fatalf("expected preparation_state=%q, got %v", core.PreparationStateServerPrepared, data.PreparationState)
	}
}

func TestMiddlewareEndpointNoHook(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsMiddlewarePath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	data := decodeDevToolsData[middlewareResponse](t, rec)
	if data.Middlewares != nil {
		t.Fatalf("expected nil middlewares, got %v", data.Middlewares)
	}
}

func TestMiddlewareEndpointWithHook(t *testing.T) {
	c := New(Options{
		Debug: true,
		Hooks: Hooks{
			MiddlewareList: func() []string {
				return []string{"logging", "recovery"}
			},
		},
	})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsMiddlewarePath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	data := decodeDevToolsData[middlewareResponse](t, rec)
	if len(data.Middlewares) != 2 {
		t.Fatalf("expected 2 middlewares, got %v", data.Middlewares)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsMetricsPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	data := decodeDevToolsData[metricsResponse](t, rec)
	if !data.Enabled || data.HTTP == nil || data.DB == nil {
		t.Fatalf("expected enabled metrics response, got %+v", data)
	}
}

func TestMetricsClearEndpoint(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, DevToolsMetricsClear, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := decodeDevToolsData[actionResponse](t, rec)
	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %+v", resp)
	}
}

func TestReloadEndpointNoEnvFile(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, DevToolsReloadPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Should fail because envFile is empty
	if rec.Code == http.StatusOK {
		t.Fatal("expected error when envFile is empty")
	}

	var body struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != contract.CodeInternalError {
		t.Fatalf("expected code %s, got %s", contract.CodeInternalError, body.Error.Code)
	}
	if body.Error.Message != "env reload failed" {
		t.Fatalf("expected safe reload message, got %q", body.Error.Message)
	}
	if strings.Contains(body.Error.Message, "env file") {
		t.Fatalf("reload message exposes raw error text: %q", body.Error.Message)
	}
}

func TestReloadEndpointEnvFileNotFound(t *testing.T) {
	c := New(Options{
		Debug:   true,
		EnvFile: "/nonexistent/.env",
	})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, DevToolsReloadPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("expected error when env file doesn't exist")
	}
}

func TestReloadEndpointSuccess(t *testing.T) {
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, ".env")
	if err := os.WriteFile(envFile, []byte("TEST_KEY=test_value\n"), 0644); err != nil {
		t.Fatal(err)
	}

	c := New(Options{
		Debug:   true,
		EnvFile: envFile,
	})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, DevToolsReloadPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	resp := decodeDevToolsData[actionResponse](t, rec)
	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %+v", resp)
	}
}

func TestStartNoDebug(t *testing.T) {
	c := New(Options{Debug: false})
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestStartNoEnvFile(t *testing.T) {
	c := New(Options{Debug: true})
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestStartEnvFileNotExist(t *testing.T) {
	c := New(Options{
		Debug:   true,
		EnvFile: "/nonexistent/.env",
	})
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("expected nil (graceful skip), got %v", err)
	}
}

func TestStartAndStop(t *testing.T) {
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, ".env")
	if err := os.WriteFile(envFile, []byte("K=V\n"), 0644); err != nil {
		t.Fatal(err)
	}

	c := New(Options{
		Debug:   true,
		EnvFile: envFile,
	})

	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	if err := c.Stop(t.Context()); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
}

func TestStopWithoutStart(t *testing.T) {
	c := New(Options{})
	if err := c.Stop(t.Context()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestAttachMetricsAttachesDevMetrics(t *testing.T) {
	var attached *DevCollector
	c := New(Options{
		Hooks: Hooks{
			AttachDevMetrics: func(dc *DevCollector) {
				attached = dc
			},
		},
	})
	c.AttachMetrics()
	if attached == nil {
		t.Fatal("expected dev metrics to be attached via hook")
	}
}

func TestAttachMetricsNoHook(t *testing.T) {
	c := New(Options{})
	// Should not panic even without the hook
	c.AttachMetrics()
}

func TestInfoEndpoint(t *testing.T) {
	c := New(Options{
		Debug:   true,
		EnvFile: ".env.test",
		Hooks: Hooks{
			RuntimeSnapshot: func() RuntimeSnapshot {
				return RuntimeSnapshot{
					Addr: ":8088",
				}
			},
		},
	})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsInfoPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	data := decodeDevToolsData[infoResponse](t, rec)
	if data.Config.EnvFile != ".env.test" {
		t.Fatalf("expected env_file=.env.test, got %v", data.Config.EnvFile)
	}
	if data.Build.Version == "" {
		t.Fatal("expected build version in info response")
	}
}

func TestMetricsEndpointDisabledCollectorShape(t *testing.T) {
	c := New(Options{Debug: true})
	c.devMetrics = nil
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsMetricsPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	data := decodeDevToolsData[metricsResponse](t, rec)
	if data.Enabled || data.HTTP != nil || data.DB != nil {
		t.Fatalf("expected disabled metrics response, got %+v", data)
	}
}

func TestRoutesTextEndpoint(t *testing.T) {
	c := New(Options{Debug: true})
	r := router.NewRouter()
	_ = c.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, DevToolsRoutesPath, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
	if body := rec.Body.String(); !strings.HasPrefix(body, "Registered Routes:\n") {
		t.Fatalf("expected rendered routes text, got %q", body)
	}
}

func decodeDevToolsData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if got := rec.Header().Get(contract.HeaderContentType); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("success envelope missing data")
	}

	var body T
	if err := json.Unmarshal(env.Data, &body); err != nil {
		t.Fatalf("decode success data: %v", err)
	}
	return body
}
