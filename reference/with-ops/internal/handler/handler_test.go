package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/metrics"
	"with-ops/internal/config"
)

func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return New(cfg, metrics.NewBaseMetricsCollector(), discardLogger())
}

func TestRootResponseShape(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.Root(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Data struct {
			Service string `json:"service"`
			Ops     string `json:"ops"`
			Metrics string `json:"metrics"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Service != "with-ops" {
		t.Errorf("service = %q, want with-ops", resp.Data.Service)
	}
	if resp.Data.Ops == "" {
		t.Error("ops field should not be empty")
	}
	if resp.Data.Metrics != "/metrics" {
		t.Errorf("metrics = %q, want /metrics", resp.Data.Metrics)
	}
}

func TestMetricsResponseShape(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.Metrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
