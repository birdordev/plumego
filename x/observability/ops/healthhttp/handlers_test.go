package healthhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
)

type mockChecker struct {
	name    string
	healthy bool
	delay   time.Duration
}

func (mc *mockChecker) Name() string {
	return mc.name
}

func (mc *mockChecker) Check(ctx context.Context) error {
	if mc.delay > 0 {
		select {
		case <-time.After(mc.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if mc.healthy {
		return nil
	}
	return mockError("mock component failure")
}

type mockError string

func (e mockError) Error() string {
	return string(e)
}

type readinessManager struct {
	status health.HealthStatus
}

func (m readinessManager) RegisterComponent(health.ComponentChecker) error {
	return nil
}

func (m readinessManager) UnregisterComponent(string) error {
	return nil
}

func (m readinessManager) CheckComponent(context.Context, string) error {
	return nil
}

func (m readinessManager) CheckAllComponents(context.Context) health.HealthStatus {
	return m.status
}

func (m readinessManager) GetComponentHealth(string) (*health.ComponentHealth, bool) {
	return nil, false
}

func (m readinessManager) GetAllHealth() map[string]*health.ComponentHealth {
	return nil
}

func (m readinessManager) GetOverallHealth() health.HealthStatus {
	return m.status
}

func (m readinessManager) Readiness() health.ReadinessStatus {
	return health.ReadinessStatus{}
}

func (m readinessManager) MarkReady() {}

func (m readinessManager) MarkNotReady(string) {}

func (m readinessManager) SetConfig(Config) error {
	return nil
}

func (m readinessManager) GetConfig() Config {
	return Config{}
}

func (m readinessManager) Close() error {
	return nil
}

func TestSummaryHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	_ = manager.RegisterComponent(&mockChecker{name: "healthy", healthy: true})
	_ = manager.RegisterComponent(&mockChecker{name: "unhealthy", healthy: false})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	SummaryHandler(manager).ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when components are unhealthy, got %d", rr.Code)
	}

	status := decodeHealthHTTPData[health.HealthStatus](t, rr)
	if status.Status != health.StatusUnhealthy {
		t.Fatalf("expected unhealthy status, got %v", status.Status)
	}
}

func TestDetailedHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	_ = manager.RegisterComponent(&mockChecker{name: "healthy", healthy: true})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	DetailedHandler(manager).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	response := decodeHealthHTTPData[HealthResponse](t, rr)
	if response.BuildInfo.Version == "" {
		t.Fatal("expected build info in detailed response")
	}
	if response.Runtime != nil {
		t.Fatal("expected runtime to be omitted in detailed response")
	}
}

func TestComponentHealthHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	_ = manager.RegisterComponent(&mockChecker{name: "healthy", healthy: true})

	req := httptest.NewRequest(http.MethodGet, "/health/component/healthy", nil)
	rr := httptest.NewRecorder()
	ComponentHealthHandler(manager, "healthy").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for healthy component, got %d", rr.Code)
	}

	componentHealth := decodeHealthHTTPData[health.ComponentHealth](t, rr)
	if componentHealth.Status != health.StatusHealthy {
		t.Fatalf("expected healthy status, got %v", componentHealth.Status)
	}

	req = httptest.NewRequest(http.MethodGet, "/health/component/nonexistent", nil)
	rr = httptest.NewRecorder()
	ComponentHealthHandler(manager, "nonexistent").ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent component, got %d", rr.Code)
	}
}

func TestAllComponentsHealthHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	_ = manager.RegisterComponent(&mockChecker{name: "component1", healthy: true})
	_ = manager.RegisterComponent(&mockChecker{name: "component2", healthy: false})

	req := httptest.NewRequest(http.MethodGet, "/health/all", nil)
	rr := httptest.NewRecorder()
	AllComponentsHealthHandler(manager).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	allHealth := decodeHealthHTTPData[map[string]*health.ComponentHealth](t, rr)
	if len(allHealth) != 2 {
		t.Fatalf("expected 2 components, got %d", len(allHealth))
	}
}

func TestHealthHistoryHandler(t *testing.T) {
	coreManager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	manager := NewTracker(coreManager)
	_ = manager.RegisterComponent(&mockChecker{name: "test", healthy: true})
	manager.CheckAllComponents(t.Context())

	req := httptest.NewRequest(http.MethodGet, "/health/history", nil)
	rr := httptest.NewRecorder()
	HealthHistoryHandler(manager).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	historyEntries := decodeHealthHTTPData[[]HealthHistoryEntry](t, rr)
	if len(historyEntries) == 0 {
		t.Fatalf("expected history entries, got none")
	}
}

func TestLiveHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	rr := httptest.NewRecorder()
	LiveHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "alive" {
		t.Fatalf("expected 'alive', got %q", body)
	}
}

func TestComponentsListHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	_ = manager.RegisterComponent(&mockChecker{name: "one", healthy: true})
	_ = manager.RegisterComponent(&mockChecker{name: "two", healthy: true})

	req := httptest.NewRequest(http.MethodGet, "/health/components", nil)
	rr := httptest.NewRecorder()
	ComponentsListHandler(manager).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	response := decodeHealthHTTPData[ComponentsListResponse](t, rr)
	if response.Count != 2 {
		t.Fatalf("expected count 2, got %d", response.Count)
	}
}

func TestReadinessHandlerWithManager(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	_ = manager.RegisterComponent(&mockChecker{name: "healthy", healthy: true})

	req := httptest.NewRequest(http.MethodGet, "/health/ready-checks", nil)
	rr := httptest.NewRecorder()
	ReadinessHandlerWithManager(manager).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	response := decodeHealthHTTPData[ReadinessResponse](t, rr)
	if !response.Ready {
		t.Fatalf("expected ready response")
	}
}

func TestReadinessHandlerWithManagerTreatsDegradedAsReady(t *testing.T) {
	manager := readinessManager{
		status: health.HealthStatus{
			Status:    health.StatusDegraded,
			Message:   "optional component disabled",
			Timestamp: time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/health/ready-checks", nil)
	rr := httptest.NewRecorder()
	ReadinessHandlerWithManager(manager).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for degraded readiness, got %d", rr.Code)
	}

	response := decodeHealthHTTPData[ReadinessResponse](t, rr)
	if !response.Ready {
		t.Fatalf("expected degraded aggregate health to be ready")
	}
	if response.Status != health.StatusDegraded {
		t.Fatalf("status = %v, want %v", response.Status, health.StatusDegraded)
	}
}

func TestReadinessHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rr := httptest.NewRecorder()
	ReadinessHandler(manager).ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before ready, got %d", rr.Code)
	}
	notReady := decodeHealthHTTPData[health.ReadinessStatus](t, rr)
	if notReady.Ready {
		t.Fatal("expected not-ready response before MarkReady")
	}

	manager.MarkReady()
	rr = httptest.NewRecorder()
	ReadinessHandler(manager).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 after ready, got %d", rr.Code)
	}
	ready := decodeHealthHTTPData[health.ReadinessStatus](t, rr)
	if !ready.Ready {
		t.Fatal("expected ready response after MarkReady")
	}
}

func TestHealthHandlerIncludesRuntime(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	_ = manager.RegisterComponent(&mockChecker{name: "healthy", healthy: true})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	rr := httptest.NewRecorder()
	HealthHandler(manager, true).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	debugResponse := decodeHealthHTTPData[HealthResponse](t, rr)
	if debugResponse.Runtime == nil {
		t.Fatalf("expected runtime info in debug response")
	}

	rr = httptest.NewRecorder()
	HealthHandler(manager, false).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	nonDebugResponse := decodeHealthHTTPData[HealthResponse](t, rr)
	if nonDebugResponse.Runtime != nil {
		t.Fatalf("expected runtime to be omitted in non-debug response")
	}
}

func TestDiagnosticsHandler(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	_ = manager.RegisterComponent(&mockChecker{name: "healthy", healthy: true})

	req := httptest.NewRequest(http.MethodGet, "/health/debug", nil)

	rr := httptest.NewRecorder()
	DiagnosticsHandler(manager, false).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when debug disabled, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	DiagnosticsHandler(manager, true).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when debug enabled, got %d", rr.Code)
	}

	response := decodeHealthHTTPData[diagnosticsResponse](t, rr)
	if response.Runtime == nil {
		t.Fatalf("expected runtime diagnostics in debug response")
	}
	if response.Health == nil {
		t.Fatalf("expected health diagnostics in debug response")
	}
	if response.Readiness == nil || response.Components == nil || response.Config == nil {
		t.Fatalf("expected manager diagnostics in debug response: %+v", response)
	}

	rr = httptest.NewRecorder()
	DiagnosticsHandler(nil, true).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil manager, got %d", rr.Code)
	}
	response = decodeHealthHTTPData[diagnosticsResponse](t, rr)
	if response.Runtime == nil || response.Health != nil || response.Config != nil {
		t.Fatalf("unexpected nil-manager diagnostics response: %+v", response)
	}
}

func decodeHealthHTTPData[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	if got := rr.Header().Get(contract.HeaderContentType); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode success envelope: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("success envelope missing data")
	}

	var body T
	if err := json.Unmarshal(env.Data, &body); err != nil {
		t.Fatalf("failed to decode success data: %v", err)
	}
	return body
}

func TestGetRuntimeInfo(t *testing.T) {
	info := getRuntimeInfo()
	if info.GoVersion == "" {
		t.Fatal("expected Go version")
	}
	if info.NumCPU != runtime.NumCPU() {
		t.Fatalf("expected NumCPU %d, got %d", runtime.NumCPU(), info.NumCPU)
	}
	if info.NumGoroutine <= 0 {
		t.Fatalf("expected positive goroutine count, got %d", info.NumGoroutine)
	}
}
