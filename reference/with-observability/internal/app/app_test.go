package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
	"with-observability/internal/config"
)

func TestRegisterRoutesCanonicalShape(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}

	got := a.Core.Routes()
	want := []router.RouteInfo{
		{Method: http.MethodGet, Path: "/"},
		{Method: http.MethodGet, Path: "/api/hello"},
		{Method: http.MethodGet, Path: "/api/v1/collector-stats"},
		{Method: http.MethodGet, Path: "/api/v1/spans"},
		{Method: http.MethodGet, Path: "/api/v1/stats"},
		{Method: http.MethodGet, Path: "/healthz"},
		{Method: http.MethodGet, Path: "/metrics"},
		{Method: http.MethodGet, Path: "/readyz"},
	}
	if len(got) != len(want) {
		t.Fatalf("routes count = %d, want %d\ngot:  %#v\nwant: %#v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i].Method != want[i].Method || got[i].Path != want[i].Path {
			t.Errorf("routes[%d] = {%s %s}, want {%s %s}", i, got[i].Method, got[i].Path, want[i].Method, want[i].Path)
		}
	}
}

func TestMetricsEndpointReturnsPrometheusText(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("GET /metrics: Content-Type = %q, want text/plain", ct)
	}
	// Uptime gauge is always present.
	body := rec.Body.String()
	if !strings.Contains(body, "_uptime_seconds") {
		t.Fatalf("GET /metrics: body missing uptime gauge\ngot: %s", body)
	}
}

func TestSpansEndpointReturnsJSON(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/spans", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/spans: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != contract.ContentTypeJSON {
		t.Fatalf("GET /api/v1/spans: Content-Type = %q, want %q", got, contract.ContentTypeJSON)
	}
}

func TestMetricsAccumulateAfterRequests(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	// Send two requests so the collector has something to report.
	for range 2 {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hello", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/hello: status = %d, want %d", rec.Code, http.StatusOK)
		}
	}

	stats := a.Collector.GetStats()
	if stats.TotalRecords == 0 {
		t.Fatal("PrometheusCollector.GetStats: TotalRecords = 0, want > 0 after requests")
	}
}

func TestTracingMiddlewareRecordsSpans(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	// One request through the tracing middleware should produce one span.
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hello", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/hello: status = %d, want %d", rec.Code, http.StatusOK)
	}

	spans := a.Tracer.Spans()
	if len(spans) == 0 {
		t.Fatal("OpenTelemetryTracer.Spans: got 0 spans, want > 0 after request")
	}
	// Each span must carry the standard HTTP attributes.
	for _, s := range spans {
		if s.TraceID == "" {
			t.Errorf("span %q: TraceID is empty", s.Name)
		}
		if s.SpanID == "" {
			t.Errorf("span %q: SpanID is empty", s.Name)
		}
		if s.Attributes["http.method"] == "" {
			t.Errorf("span %q: missing http.method attribute", s.Name)
		}
	}
}

func TestRequestIDPropagationThroughMiddlewareStack(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/hello", nil)
	req.Header.Set(contract.RequestIDHeader, "obs-test-1")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get(contract.RequestIDHeader); got != "obs-test-1" {
		t.Fatalf("%s = %q, want %q", contract.RequestIDHeader, got, "obs-test-1")
	}
}

func TestHealthEndpointLivenessAlwaysReturns200(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadinessEndpointReturnsOKWithNoCheckers(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /readyz: status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestSpansEndpointWithLimitParameter(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	// Generate multiple requests to create multiple spans.
	for range 5 {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hello", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/hello: status = %d, want %d", rec.Code, http.StatusOK)
		}
	}

	// Query with limit=2, should return only 2 spans.
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/spans?limit=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/spans?limit=2: status = %d, want %d", rec.Code, http.StatusOK)
	}

	var env struct {
		Data struct {
			Spans []map[string]interface{} `json:"spans"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(env.Data.Spans) != 2 {
		t.Fatalf("GET /api/v1/spans?limit=2: returned %d spans, want 2", len(env.Data.Spans))
	}
}

func TestRecoveryMiddlewareReturns500OnPanic(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	// Inject a panic handler after normal route registration to verify that the
	// recovery middleware catches panics and returns 500 instead of crashing.
	if err := a.Core.Get("/panic-test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("deliberate panic for recovery test")
	})); err != nil {
		t.Fatalf("register panic route: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic-test", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET /panic-test: status = %d, want 500 (recovery middleware should catch panic)", rec.Code)
	}

	// Server must continue serving normal requests after a recovered panic.
	rec2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /healthz after recovered panic: status = %d, want 200", rec2.Code)
	}
}

func TestStatsEndpointAccumulatesMetricsCorrectly(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if err := a.Core.Prepare(); err != nil {
		t.Fatalf("prepare app: %v", err)
	}
	srv, err := a.Core.Server()
	if err != nil {
		t.Fatalf("get server: %v", err)
	}

	// First check: no requests yet, stats should be empty.
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("initial GET /api/v1/stats: status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Make some requests.
	for range 3 {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hello", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/hello: status = %d, want %d", rec.Code, http.StatusOK)
		}
	}

	// Second check: stats should now show accumulated data.
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/stats: status = %d, want %d", rec.Code, http.StatusOK)
	}

	var env struct {
		Data struct {
			TotalRequests int64 `json:"total_requests"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// Should have at least the 3 /api/hello requests plus stats requests themselves.
	if env.Data.TotalRequests < 3 {
		t.Fatalf("GET /api/v1/stats: TotalRequests = %d, want >= 3", env.Data.TotalRequests)
	}
}
