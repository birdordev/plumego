package healthhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
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
	status     health.HealthStatus
	components map[string]*health.ComponentHealth
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
	return m.components
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

func TestRuntimeInfoHandlerResponseAndNoStore(t *testing.T) {
	out := httptest.NewRecorder()
	RuntimeInfoHandler().ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/runtime", nil))
	if out.Code != http.StatusOK || out.Header().Get("Cache-Control") != "no-cache, no-store, must-revalidate" {
		t.Fatalf("response status = %d, cache control = %q", out.Code, out.Header().Get("Cache-Control"))
	}
	info := decodeHealthHTTPData[RuntimeInfo](t, out)
	if info.GoVersion != runtime.Version() || info.NumCPU != runtime.NumCPU() ||
		info.GOARCH != runtime.GOARCH || info.GOOS != runtime.GOOS || info.NumGoroutine < 1 {
		t.Errorf("runtime info = %+v", info)
	}
	fields := decodeHealthHTTPData[map[string]json.RawMessage](t, out)
	for _, field := range []string{"go_version", "num_goroutine", "num_cpu", "goarch", "goos", "mem_alloc_bytes", "mem_sys_bytes", "num_gc"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("missing runtime field %q", field)
		}
	}
	for _, field := range []string{"config", "readiness", "health", "components", "build_info", "timestamp", "runtime"} {
		if _, ok := fields[field]; ok {
			t.Errorf("unexpected diagnostics field %q", field)
		}
	}
}

func TestBuildInfoHandlerEnvelope(t *testing.T) {
	out := httptest.NewRecorder()
	BuildInfoHandler().ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/build", nil))
	if out.Code != http.StatusOK {
		t.Fatalf("status = %d", out.Code)
	}
	if got, want := decodeHealthHTTPData[BuildInfo](t, out), GetBuildInfo(); got != want {
		t.Errorf("build info = %+v, want %+v", got, want)
	}
	fields := decodeHealthHTTPData[map[string]json.RawMessage](t, out)
	for _, field := range []string{"version", "commit", "buildTime"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("missing build field %q", field)
		}
	}
}

func TestHealthEncodingFailureKeepsUnhealthyStatusAndSafeBody(t *testing.T) {
	m := readinessManager{status: health.HealthStatus{
		Status: health.StatusUnhealthy, Details: map[string]any{"fixture": make(chan int)},
	}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(contract.WithRequestID(req.Context(), "request-id"))
	w := httptest.NewRecorder()
	SummaryHandler(m).ServeHTTP(w, req)
	var payload struct {
		RequestID string `json:"request_id"`
		Error     struct{ Code string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil ||
		w.Code != http.StatusServiceUnavailable || payload.Error.Code != contract.CodeUnavailable ||
		payload.RequestID != "request-id" {
		t.Fatalf("status=%d body=%q error=%v", w.Code, w.Body.String(), err)
	}
	w = httptest.NewRecorder()
	ReadinessHandlerWithManager(m).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status=%d", w.Code)
	}
}

func TestAcceptanceHealthStateHTTPMapping(t *testing.T) {
	for _, tt := range []struct {
		state          health.HealthState
		summary, ready int
	}{
		{health.StatusHealthy, 200, 200},
		{health.StatusDegraded, 206, 200},
		{health.StatusUnhealthy, 503, 503},
		{"", 503, 503},
		{"unknown", 503, 503},
	} {
		t.Run(string(tt.state), func(t *testing.T) {
			manager := readinessManager{status: health.HealthStatus{Status: tt.state}}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, tc := range []struct {
				handler http.Handler
				want    int
			}{
				{SummaryHandler(manager), tt.summary},
				{DetailedHandler(manager), tt.summary},
				{ReadinessHandlerWithManager(manager), tt.ready},
			} {
				w := httptest.NewRecorder()
				tc.handler.ServeHTTP(w, req)
				if w.Code != tc.want {
					t.Fatalf("status=%d, want %d; body=%q", w.Code, tc.want, w.Body.String())
				}
			}
		})
	}
}

func TestAcceptanceAllComponentsEncodingAndWriteFailures(t *testing.T) {
	manager := readinessManager{components: map[string]*health.ComponentHealth{
		"db": {HealthStatus: health.HealthStatus{Status: health.StatusHealthy}},
	}}
	handler := AllComponentsHealthHandler(manager)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(contract.WithRequestID(req.Context(), "request-id"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	components := decodeHealthHTTPData[map[string]*health.ComponentHealth](t, w)
	if w.Code != http.StatusOK || components["db"].Status != health.StatusHealthy {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}

	short := &componentWriteFailure{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(short, req)
	if short.headers != 1 || short.writes != 1 || short.Code != http.StatusOK {
		t.Fatalf("post-commit failure: %+v", short)
	}

	for _, invalid := range []any{make(chan int), panickingHealthDetail{}} {
		manager.components["db"].Details = map[string]any{"unencodable": invalid}
		failure := &componentWriteFailure{ResponseRecorder: httptest.NewRecorder(), accept: true}
		handler.ServeHTTP(failure, req)
		var payload struct {
			RequestID string `json:"request_id"`
			Error     struct{ Code string }
		}
		if err := json.Unmarshal(failure.Body.Bytes(), &payload); err != nil ||
			failure.headers != 1 || failure.writes != 1 || failure.Code != http.StatusInternalServerError ||
			payload.RequestID != "request-id" || payload.Error.Code != contract.CodeInternalError {
			t.Fatalf("status=%d headers=%d writes=%d body=%q error=%v", failure.Code, failure.headers, failure.writes, failure.Body, err)
		}
	}
}

type panickingHealthDetail struct{}

func (panickingHealthDetail) MarshalJSON() ([]byte, error) {
	panic("health detail encoder failed")
}

type componentWriteFailure struct {
	*httptest.ResponseRecorder
	headers, writes int
	accept          bool
}

func (w *componentWriteFailure) WriteHeader(status int) {
	w.headers++
	w.ResponseRecorder.WriteHeader(status)
}

func (w *componentWriteFailure) Write(body []byte) (int, error) {
	w.writes++
	if w.accept {
		return w.ResponseRecorder.Write(body)
	}
	return 0, io.ErrClosedPipe
}

type componentSnapshotManager struct {
	Manager
	status   *health.ComponentHealth
	exists   bool
	checkErr error
}

func (m componentSnapshotManager) CheckComponent(context.Context, string) error { return m.checkErr }

func (m componentSnapshotManager) GetComponentHealth(string) (*health.ComponentHealth, bool) {
	return m.status, m.exists
}

func TestAcceptanceComponentFailureLeavesBorrowedSnapshotIntact(t *testing.T) {
	snapshot := &health.ComponentHealth{HealthStatus: health.HealthStatus{Status: health.StatusHealthy, Message: "previous"}}
	manager := componentSnapshotManager{status: snapshot, exists: true, checkErr: mockError("backend fixture")}
	w := httptest.NewRecorder()
	ComponentHealthHandler(manager, "db").ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	response := decodeHealthHTTPData[health.ComponentHealth](t, w)
	if w.Code != http.StatusServiceUnavailable || response.Status != health.StatusUnhealthy || response.Message != "component check failed" ||
		snapshot.Status != health.StatusHealthy || snapshot.Message != "previous" {
		t.Fatalf("code=%d response=%+v snapshot=%+v", w.Code, response, snapshot)
	}
}

func TestAcceptanceComponentSnapshotAvailability(t *testing.T) {
	for _, tc := range []struct {
		name       string
		manager    componentSnapshotManager
		wantStatus int
		wantCode   string
	}{
		{"missing", componentSnapshotManager{}, http.StatusNotFound, "COMPONENT_NOT_FOUND"},
		{"empty", componentSnapshotManager{exists: true}, http.StatusServiceUnavailable, contract.CodeUnavailable},
		{"empty after failure", componentSnapshotManager{exists: true, checkErr: mockError("backend fixture")}, http.StatusServiceUnavailable, contract.CodeUnavailable},
		{"healthy", componentSnapshotManager{exists: true, status: &health.ComponentHealth{HealthStatus: health.HealthStatus{Status: health.StatusHealthy}}}, http.StatusOK, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r = r.WithContext(contract.WithRequestID(r.Context(), "fixture"))
			w := &componentWriteFailure{ResponseRecorder: httptest.NewRecorder(), accept: true}
			ComponentHealthHandler(tc.manager, "db").ServeHTTP(w, r)
			var payload struct {
				RequestID string `json:"request_id"`
				Error     struct{ Code string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || w.Code != tc.wantStatus ||
				payload.Error.Code != tc.wantCode || payload.RequestID != "fixture" || w.headers != 1 || w.writes != 1 {
				t.Fatalf("status=%d commits=%d writes=%d body=%q error=%v", w.Code, w.headers, w.writes, w.Body, err)
			}
		})
	}
}

func TestAcceptanceComponentNamesSortedAndEmptyArrayPreserved(t *testing.T) {
	for _, components := range []map[string]*health.ComponentHealth{nil, {"z": {}, "a": {}, "m": {}}} {
		handler := ComponentsListHandler(readinessManager{components: components})
		want := []string{}
		if len(components) > 0 {
			want = []string{"a", "m", "z"}
		}
		for range 12 {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			got := decodeHealthHTTPData[ComponentsListResponse](t, w)
			if w.Code != http.StatusOK || got.Components == nil || !slices.Equal(got.Components, want) || got.Count != len(want) {
				t.Fatalf("status=%d list=%+v", w.Code, got)
			}
		}
	}
}

type nilSafeReadinessManager struct {
	Manager
}

func (*nilSafeReadinessManager) Readiness() health.ReadinessStatus {
	return health.ReadinessStatus{Ready: false, Reason: "fixture"}
}

func TestReadinessDistinguishesNilInterfaceAndNilSafeManager(t *testing.T) {
	var nilSafe *nilSafeReadinessManager
	for _, tc := range []struct {
		manager Manager
		field   string
	}{
		{nil, "error"},
		{nilSafe, "data"},
	} {
		w := httptest.NewRecorder()
		ReadinessHandler(tc.manager).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusServiceUnavailable ||
			body[tc.field] == nil || len(body) != 1 {
			t.Fatalf("status=%d body=%s error=%v", w.Code, w.Body, err)
		}
	}
}
