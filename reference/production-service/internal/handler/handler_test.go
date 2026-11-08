package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/metrics"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	"production-service/internal/domain/tenant"
)

// discardLogger returns a StructuredLogger that silently discards all output.
// Use it in tests that construct handlers to satisfy the non-nil Logger requirement.
func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

// componentChecker adapts a name and check function to health.ComponentChecker.
type componentChecker struct {
	name  string
	check func(context.Context) error
}

func (c componentChecker) Name() string                    { return c.name }
func (c componentChecker) Check(ctx context.Context) error { return c.check(ctx) }

// stubMetrics satisfies MetricsCollector for OpsHandler tests.
type stubMetrics struct {
	stats metrics.CollectorStats
}

func (s *stubMetrics) GetStats() metrics.CollectorStats { return s.stats }

// stubProfiles satisfies ProfileStore for ProfileHandler tests.
type stubProfiles struct {
	profiles map[string]tenant.Profile
}

func (s *stubProfiles) Get(_ context.Context, tenantID string) (tenant.Profile, bool) {
	p, ok := s.profiles[tenantID]
	return p, ok
}

// responseEnvelope holds the success response structure for test decoding.
type responseEnvelope struct {
	Data      json.RawMessage `json:"data"`
	Meta      map[string]any  `json:"meta"`
	RequestID string          `json:"request_id"`
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) responseEnvelope {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}
	var env responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode response envelope: %v", err)
	}
	return env
}

func decodeData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	env := decodeEnvelope(t, rec)
	if len(env.Data) == 0 {
		t.Fatal("success envelope missing data")
	}
	var body T
	if err := json.Unmarshal(env.Data, &body); err != nil {
		t.Fatalf("decode data field: %v", err)
	}
	return body
}

func decodeErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return env.Error.Code
}

func decodeErrorDetails(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return env.Error.Details
}

func TestServiceHandlerRoot(t *testing.T) {
	h := ServiceHandler{
		ServiceName: "test-service",
		Logger:      discardLogger(),
		Features:    []string{"feature_a", "feature_b"},
	}
	rec := httptest.NewRecorder()
	h.Root(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeData[serviceResponse](t, rec)
	if got.Service != "test-service" {
		t.Fatalf("service = %q, want %q", got.Service, "test-service")
	}
	if got.Timestamp == "" {
		t.Fatal("timestamp must not be empty")
	}
	if len(got.Features) != 2 || got.Features[0] != "feature_a" {
		t.Fatalf("features = %v, want [feature_a feature_b]", got.Features)
	}
}

func TestServiceHandlerLive(t *testing.T) {
	h := ServiceHandler{ServiceName: "test-service", Logger: discardLogger()}
	rec := httptest.NewRecorder()
	h.Live(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeData[livenessResponse](t, rec)
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Service != "test-service" {
		t.Fatalf("service = %q, want test-service", got.Service)
	}
	if got.Check != "liveness" {
		t.Fatalf("check = %q, want liveness", got.Check)
	}
	if got.Timestamp == "" {
		t.Fatal("timestamp must not be empty")
	}
}

func TestServiceHandlerReadyNoCheckers(t *testing.T) {
	h := ServiceHandler{ServiceName: "test-service", Logger: discardLogger()}
	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeData[health.ReadinessStatus](t, rec)
	if !got.Ready {
		t.Fatal("Ready = false, want true (no checkers)")
	}
	if got.Timestamp.IsZero() {
		t.Fatal("Timestamp must not be zero")
	}
	if len(got.Components) != 0 {
		t.Fatalf("Components = %v, want empty (no checkers)", got.Components)
	}
}

func TestServiceHandlerReadyWithCheckers(t *testing.T) {
	t.Run("passing checker returns 200 with component map", func(t *testing.T) {
		h := ServiceHandler{
			ServiceName: "test-service",
			Logger:      discardLogger(),
			Checkers: []health.ComponentChecker{
				componentChecker{name: "database", check: func(_ context.Context) error { return nil }},
			},
		}
		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		got := decodeData[health.ReadinessStatus](t, rec)
		if !got.Ready {
			t.Fatal("Ready = false, want true")
		}
		if !got.Components["database"] {
			t.Fatalf("Components[database] = false, want true; got %v", got.Components)
		}
	})

	t.Run("failing checker returns 503 with component name as detail key", func(t *testing.T) {
		h := ServiceHandler{
			ServiceName: "test-service",
			Logger:      discardLogger(),
			Checkers: []health.ComponentChecker{
				componentChecker{name: "database", check: func(_ context.Context) error {
					return errors.New("connection refused")
				}},
			},
		}
		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		details := decodeErrorDetails(t, rec)
		if details["database"] != "connection refused" {
			t.Fatalf("error detail database = %v, want connection refused; all details: %v", details["database"], details)
		}
	})

	t.Run("first checker passes second fails returns 503 for failing component", func(t *testing.T) {
		h := ServiceHandler{
			ServiceName: "test-service",
			Logger:      discardLogger(),
			Checkers: []health.ComponentChecker{
				componentChecker{name: "database", check: func(_ context.Context) error { return nil }},
				componentChecker{name: "cache", check: func(_ context.Context) error {
					return errors.New("cache offline")
				}},
			},
		}
		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		details := decodeErrorDetails(t, rec)
		if details["cache"] != "cache offline" {
			t.Fatalf("error detail cache = %v, want cache offline; all details: %v", details["cache"], details)
		}
	})

	t.Run("both checkers failing returns 503 with all failing component details", func(t *testing.T) {
		h := ServiceHandler{
			ServiceName: "test-service",
			Logger:      discardLogger(),
			Checkers: []health.ComponentChecker{
				componentChecker{name: "database", check: func(_ context.Context) error {
					return errors.New("connection refused")
				}},
				componentChecker{name: "cache", check: func(_ context.Context) error {
					return errors.New("cache offline")
				}},
			},
		}
		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		details := decodeErrorDetails(t, rec)
		if details["database"] != "connection refused" {
			t.Fatalf("error detail database = %v, want connection refused; all details: %v", details["database"], details)
		}
		if details["cache"] != "cache offline" {
			t.Fatalf("error detail cache = %v, want cache offline; all details: %v", details["cache"], details)
		}
	})

	t.Run("all checkers passing returns component map with all true", func(t *testing.T) {
		h := ServiceHandler{
			ServiceName: "test-service",
			Logger:      discardLogger(),
			Checkers: []health.ComponentChecker{
				componentChecker{name: "database", check: func(_ context.Context) error { return nil }},
				componentChecker{name: "cache", check: func(_ context.Context) error { return nil }},
			},
		}
		rec := httptest.NewRecorder()
		h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		got := decodeData[health.ReadinessStatus](t, rec)
		if !got.Ready {
			t.Fatal("Ready = false, want true")
		}
		for _, name := range []string{"database", "cache"} {
			if !got.Components[name] {
				t.Fatalf("Components[%s] = false, want true; full map: %v", name, got.Components)
			}
		}
	})
}

func TestProfileHandlerGetProfile(t *testing.T) {
	profiles := &stubProfiles{
		profiles: map[string]tenant.Profile{
			"tenant-a": {
				TenantID: "tenant-a",
				Name:     "Tenant A",
				Plan:     "production",
				Features: []string{"api", "ops"},
			},
		},
	}
	h := ProfileHandler{Profiles: profiles, Logger: discardLogger()}

	t.Run("known tenant returns 200 with profile", func(t *testing.T) {
		req := tenantcore.RequestWithTenantID(
			httptest.NewRequest(http.MethodGet, "/api/profile", nil), "tenant-a")
		rec := httptest.NewRecorder()
		h.GetProfile(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		got := decodeData[tenant.Profile](t, rec)
		if got.TenantID != "tenant-a" || got.Name != "Tenant A" || got.Plan != "production" {
			t.Fatalf("unexpected profile: %+v", got)
		}
	})

	t.Run("unknown tenant returns 404 TypeNotFound", func(t *testing.T) {
		req := tenantcore.RequestWithTenantID(
			httptest.NewRequest(http.MethodGet, "/api/profile", nil), "tenant-unknown")
		rec := httptest.NewRecorder()
		h.GetProfile(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
		if code := decodeErrorCode(t, rec); code != codeProfileNotFound {
			t.Fatalf("error code = %q, want %q", code, codeProfileNotFound)
		}
	})
}

func TestOpsHandlerMetricStats(t *testing.T) {
	h := OpsHandler{
		Metrics: &stubMetrics{
			stats: metrics.CollectorStats{
				TotalRecords: 42,
			},
		},
		Logger: discardLogger(),
	}
	rec := httptest.NewRecorder()
	h.MetricStats(rec, httptest.NewRequest(http.MethodGet, "/ops/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeData[metrics.CollectorStats](t, rec)
	if got.TotalRecords != 42 {
		t.Fatalf("TotalRecords = %d, want 42", got.TotalRecords)
	}
}

func TestStatusHandlerStatus(t *testing.T) {
	h := StatusHandler{
		Logger: discardLogger(),
		Cfg: StatusConfig{
			ServiceName:    "test-service",
			Environment:    "test",
			BodyLimitBytes: 1 << 20,
			RequestTimeout: 5 * time.Second,
			RateLimit:      20,
			RateBurst:      40,
			Profiles: ProfilesSummary{
				Kind:        "app_local_in_memory_reference",
				Replacement: "replace Store",
			},
		},
	}
	rec := httptest.NewRecorder()
	h.Status(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	got := decodeData[StatusResponse](t, rec)
	if got.Status != "healthy" {
		t.Fatalf("status = %q, want healthy", got.Status)
	}
	if got.Service != "test-service" {
		t.Fatalf("service = %q, want test-service", got.Service)
	}
	if got.Deployment.Environment != "test" {
		t.Fatalf("environment = %q, want test", got.Deployment.Environment)
	}
	if got.Limits.BodyLimitBytes != 1<<20 {
		t.Fatalf("body_limit_bytes = %d, want %d", got.Limits.BodyLimitBytes, 1<<20)
	}
	if got.Limits.RequestTimeout != "5s" {
		t.Fatalf("request_timeout = %q, want 5s", got.Limits.RequestTimeout)
	}
	if got.Limits.RateLimit != 20 {
		t.Fatalf("rate_limit = %v, want 20", got.Limits.RateLimit)
	}
	if got.Limits.RateBurst != 40 {
		t.Fatalf("rate_burst = %d, want 40", got.Limits.RateBurst)
	}
	if got.Storage.ProfileStore != "app_local_in_memory_reference" {
		t.Fatalf("storage.profile_store = %q", got.Storage.ProfileStore)
	}
	if got.Timestamp == "" {
		t.Fatal("timestamp must not be empty")
	}
}
