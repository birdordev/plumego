package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/birdordev/plumego/metrics"
	"production-service/internal/config"
	"production-service/internal/domain/tenant"
	"production-service/internal/handler"
)

type responseEnvelope[T any] struct {
	Data T `json:"data"`
}

func TestProductionServiceSmoke(t *testing.T) {
	t.Setenv("OPS_TOKEN", "ops-token")

	cfg := config.Defaults()
	cfg.App.APIToken = "api-token"
	cfg.App.OpsToken = "ops-token"
	cfg.App.ProfileStorePath = filepath.Join(t.TempDir(), "profiles.json")
	cfg.App.RateLimit = 1000
	cfg.App.RateBurst = 1000

	production, err := New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if err := production.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	if _, err := os.Stat(cfg.App.ProfileStorePath); err != nil {
		t.Fatalf("profile store was not materialized: %v", err)
	}

	assertStatus(t, production, http.MethodGet, "/healthz", nil, http.StatusOK)
	assertStatus(t, production, http.MethodGet, "/readyz", nil, http.StatusOK)

	statusRec := assertStatus(t, production, http.MethodGet, "/api/status", nil, http.StatusOK)
	status := decodeData[handler.StatusResponse](t, statusRec)
	if status.Storage.ProfileStore != "app_local_json_file_reference" {
		t.Fatalf("storage profile store = %q", status.Storage.ProfileStore)
	}
	if status.Ops.AdminRoutes != "not_mounted_by_default" {
		t.Fatalf("admin route policy = %q", status.Ops.AdminRoutes)
	}
	if status.Ops.Devtools != "not_mounted_by_default" {
		t.Fatalf("devtools policy = %q", status.Ops.Devtools)
	}

	assertStatus(t, production, http.MethodGet, "/api/profile", nil, http.StatusUnauthorized)
	profileRec := assertStatus(t, production, http.MethodGet, "/api/profile", map[string]string{
		"Authorization": "Bearer api-token",
		"X-Tenant-ID":   "tenant-a",
	}, http.StatusOK)
	profile := decodeData[tenant.Profile](t, profileRec)
	if profile.TenantID != "tenant-a" {
		t.Fatalf("tenant id = %q", profile.TenantID)
	}

	assertStatus(t, production, http.MethodGet, "/ops/metrics", nil, http.StatusUnauthorized)
	assertStatus(t, production, http.MethodGet, "/ops/metrics", map[string]string{
		"Authorization": "Bearer ops-token",
	}, http.StatusOK)
}

func TestProductionObservabilityWiringRecordsHTTPMetricsOnce(t *testing.T) {
	cfg := config.Defaults()
	cfg.App.APIToken = "api-token"
	cfg.App.ProfileStorePath = filepath.Join(t.TempDir(), "profiles.json")
	cfg.App.RateLimit = 1000
	cfg.App.RateBurst = 1000

	production, err := New(cfg)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	if production.RateLimit != nil {
		defer production.RateLimit.Stop()
	}
	if err := production.RegisterRoutes(); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	production.Metrics.Clear()

	assertStatus(t, production, http.MethodGet, "/healthz", nil, http.StatusOK)

	stats := production.Metrics.GetStats()
	if got := stats.NameBreakdown[metrics.MetricHTTPRequest]; got != 1 {
		t.Fatalf("HTTP metric records = %d, want 1; stats=%+v", got, stats)
	}
	if stats.TotalRecords != 1 {
		t.Fatalf("total metric records = %d, want 1; stats=%+v", stats.TotalRecords, stats)
	}
}

func assertStatus(t *testing.T, production *App, method, path string, headers map[string]string, want int) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	production.Core.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, rec.Code, want, rec.Body.String())
	}
	return rec
}

func decodeData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var envelope responseEnvelope[T]
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope.Data
}
